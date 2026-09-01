package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// Process-event source (ADR-0021 Decision 2, P1).
//
// A domain-neutral, durable way to wait on a blocking external condition
// without holding an agent turn. The source knows nothing about PRs, tasks or
// any domain: resolution is decided by a caller-injected ProcessEventResolver,
// and registration carries only an opaque event id, an opaque payload and a
// generation.
//
// Durable order per resolution (must not be reordered):
//  1. capture the resolver's result into the event's own record
//     (temp + fsync + rename, the WriteRetirementRecord pattern);
//  2. enqueue exactly one wake through home.EnqueueWake, the sole delivery
//     substrate (ADR-0015/0019) -- announceProcessEvent refuses to enqueue for
//     a record whose durable copy does not carry a captured result;
//  3. only an ack of the exact current generation stops re-announcement.
//
// Re-announcement across restarts is suppressed ONLY when
// Generation == AckedGeneration. Registration bumps Generation, so an ack of a
// prior generation never suppresses a new registration. Within one process
// the Announced flag keeps a steady tick from spending a second wake on an
// already-announced resolution; RecoverProcessEvents ignores that flag and
// re-delivers every captured-but-unacked event once per home per process, so
// a crash anywhere before ack re-delivers and a crash after ack does not.
//
// P1 is additive: nothing in the binary evaluates or recovers these records
// yet. P2 re-expresses merged-PR retirement as an instance and C1 builds
// condition-action on top; the deadcode waivers name that condition.

// ProcessEventSchema is the schema version for ProcessEventRecord.
const ProcessEventSchema = 1

// ProcessEventWakeKind is the wake-queue kind under which a resolved event is
// announced. The wake key is the event id and the payload is a JSON
// ProcessEventWake carrying the generation the consumer must ack.
const ProcessEventWakeKind = "process-event"

// processEventDir is the private state directory holding one durable record
// per registered event, relative to home.StateDir.
const processEventDir = ".process-events"

// ProcessEventResolver decides whether the external condition an event waits
// on has resolved. It returns the captured result when resolved is true. The
// source never interprets the result.
type ProcessEventResolver func(ctx context.Context) (resolved bool, result []byte, err error)

// ProcessEventRecord is the durable record for one registered event. It holds
// both the announcement state and the captured result, so the wake queue
// carries only the announcement and no second delivery substrate exists.
type ProcessEventRecord struct {
	SchemaVersion int `json:"schemaVersion"`

	EventID string `json:"eventId"`
	Payload string `json:"payload"`

	// Generation is bumped by every registration of the same event id.
	// AckedGeneration is the last generation a consumer acknowledged; the
	// event is quiet across restarts only while the two are equal.
	Generation      uint64 `json:"generation"`
	AckedGeneration uint64 `json:"ackedGeneration"`

	// Resolved and Result are the captured external outcome for Generation.
	// Announced records that a wake was enqueued for that capture in this
	// generation; it suppresses steady-state re-announcement only and is
	// ignored by recovery.
	Resolved  bool   `json:"resolved"`
	Result    []byte `json:"result,omitempty"`
	Announced bool   `json:"announced"`

	RegisteredAt string `json:"registeredAt"`
	ResolvedAt   string `json:"resolvedAt,omitempty"`
}

// ProcessEventWake is the JSON wake payload announcing a resolved event. The
// consumer acks EventID at exactly Generation.
type ProcessEventWake struct {
	EventID    string `json:"eventId"`
	Generation uint64 `json:"generation"`
	Payload    string `json:"payload"`
}

// processEventRecoveryDone tracks one-shot recovery independently for each
// home, the recoveryDone pattern used by the supervision watcher.
var processEventRecoveryDone sync.Map

func processEventDirPath(homeDir string) string {
	return filepath.Join(home.StateDir(homeDir), processEventDir)
}

// processEventRecordPath returns the collision-safe record path for an event:
// the id is hashed so any opaque id yields a flat filename.
func processEventRecordPath(homeDir, eventID string) string {
	digest := sha256.Sum256([]byte(eventID))
	return filepath.Join(processEventDirPath(homeDir), fmt.Sprintf("v%d-%s.json", ProcessEventSchema, hex.EncodeToString(digest[:])))
}

// writeProcessEventRecord atomically persists a record: temp file, fsync,
// close, durable rename. renameFn is the crash-injection seam; production
// callers pass home.RenameDurable.
func writeProcessEventRecord(homeDir string, rec *ProcessEventRecord, renameFn func(string, string) error) error {
	dir := processEventDirPath(homeDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating process-event dir: %w", err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshaling process-event record: %w", err)
	}
	path := processEventRecordPath(homeDir, rec.EventID)
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("writing process-event temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing process-event temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsync process-event temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing process-event temp: %w", err)
	}
	if err := renameFn(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming process-event record: %w", err)
	}
	return nil
}

// decodeProcessEventRecord parses one record file and refuses any schema this
// tree does not write.
func decodeProcessEventRecord(data []byte) (*ProcessEventRecord, error) {
	var rec ProcessEventRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parsing process-event record: %w", err)
	}
	if rec.SchemaVersion != ProcessEventSchema {
		return nil, fmt.Errorf("unsupported process-event schema version %d (expected %d)", rec.SchemaVersion, ProcessEventSchema)
	}
	return &rec, nil
}

// readProcessEventRecord returns nil, nil when the event is not registered.
func readProcessEventRecord(homeDir, eventID string) (*ProcessEventRecord, error) {
	data, err := os.ReadFile(processEventRecordPath(homeDir, eventID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading process-event record: %w", err)
	}
	return decodeProcessEventRecord(data)
}

// listProcessEventRecords returns every registered record. A record whose
// filename does not match its own event id is refused: recovery must never
// announce under an identity the record does not own.
func listProcessEventRecords(homeDir string) ([]*ProcessEventRecord, error) {
	dir := processEventDirPath(homeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading process-event dir: %w", err)
	}
	var recs []*ProcessEventRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading process-event record %s: %w", entry.Name(), err)
		}
		rec, err := decodeProcessEventRecord(data)
		if err != nil {
			return nil, fmt.Errorf("process-event record %s: %w", entry.Name(), err)
		}
		if rec.EventID == "" || processEventRecordPath(homeDir, rec.EventID) != filepath.Join(dir, entry.Name()) {
			return nil, fmt.Errorf("process-event record %s has invalid event identity", entry.Name())
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// RegisterProcessEvent registers, or re-registers, an event to wait on and
// returns its new generation. Re-registration bumps the generation and clears
// any captured result, so a prior generation's ack no longer matches and the
// event announces again once it resolves. The wake queue carries the event id
// as its key, so the id must be a single non-empty line.
func RegisterProcessEvent(homeDir, eventID, payload string) (uint64, error) {
	return registerProcessEvent(homeDir, eventID, payload, home.RenameDurable)
}

func registerProcessEvent(homeDir, eventID, payload string, renameFn func(string, string) error) (uint64, error) {
	if eventID == "" || strings.ContainsAny(eventID, "\t\r\n") {
		return 0, fmt.Errorf("process-event id %q must be a non-empty single line", eventID)
	}
	prev, err := readProcessEventRecord(homeDir, eventID)
	if err != nil {
		return 0, err
	}
	rec := &ProcessEventRecord{
		SchemaVersion: ProcessEventSchema,
		EventID:       eventID,
		Payload:       payload,
		Generation:    1,
		RegisteredAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if prev != nil {
		rec.Generation = prev.Generation + 1
		rec.AckedGeneration = prev.AckedGeneration
	}
	if err := writeProcessEventRecord(homeDir, rec, renameFn); err != nil {
		return 0, err
	}
	return rec.Generation, nil
}

// EvaluateProcessEvent runs one poll cycle for an event. An unresolved
// condition spends zero wakes; a resolution captures the result durably and
// then spends exactly one wake. An already-announced or already-acked
// generation spends nothing: steady-state ticks never re-announce, only
// RecoverProcessEvents does.
func EvaluateProcessEvent(ctx context.Context, homeDir, eventID string, resolve ProcessEventResolver) error {
	return evaluateProcessEvent(ctx, homeDir, eventID, resolve, home.RenameDurable)
}

func evaluateProcessEvent(ctx context.Context, homeDir, eventID string, resolve ProcessEventResolver, renameFn func(string, string) error) error {
	if resolve == nil {
		return fmt.Errorf("process-event %q: a resolver is required", eventID)
	}
	rec, err := readProcessEventRecord(homeDir, eventID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("process-event %q is not registered", eventID)
	}
	if rec.Generation == rec.AckedGeneration {
		return nil
	}
	if rec.Resolved {
		if rec.Announced {
			return nil
		}
		// Captured, but the wake never left this process: retry the
		// announcement without re-running the resolver.
		return announceProcessEvent(homeDir, rec, renameFn)
	}
	resolved, result, err := resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolving process-event %q: %w", eventID, err)
	}
	if !resolved {
		return nil
	}
	// Step 1: capture BEFORE any wake.
	rec.Resolved = true
	rec.Result = result
	rec.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeProcessEventRecord(homeDir, rec, renameFn); err != nil {
		return fmt.Errorf("capturing process-event %q result: %w", eventID, err)
	}
	// Step 2: announce only what is durably on disk, never the in-memory copy.
	durable, err := readProcessEventRecord(homeDir, eventID)
	if err != nil {
		return err
	}
	return announceProcessEvent(homeDir, durable, renameFn)
}

// announceProcessEvent enqueues the single wake for a captured result and then
// marks the record announced. It refuses a record that does not carry a
// durably captured result: a wake must never be enqueued for an uncaptured
// result (capture-before-wake).
func announceProcessEvent(homeDir string, rec *ProcessEventRecord, renameFn func(string, string) error) error {
	if rec == nil || !rec.Resolved {
		return errors.New("refusing to enqueue a process-event wake: result is not durably captured")
	}
	payload, err := json.Marshal(ProcessEventWake{EventID: rec.EventID, Generation: rec.Generation, Payload: rec.Payload})
	if err != nil {
		return fmt.Errorf("encoding process-event wake: %w", err)
	}
	if err := home.EnqueueWake(homeDir, ProcessEventWakeKind, rec.EventID, string(payload)); err != nil {
		return fmt.Errorf("enqueuing process-event wake: %w", err)
	}
	rec.Announced = true
	if err := writeProcessEventRecord(homeDir, rec, renameFn); err != nil {
		return fmt.Errorf("recording process-event announcement: %w", err)
	}
	return nil
}

// AckProcessEvent records that the consumer handled the wake for exactly
// generation. It is the sole re-announcement stop. An ack for any other
// generation is refused so a stale ack can never silence a newer
// registration, and an ack for a generation with no captured result is
// refused so nothing can pre-empt an announcement that has not happened.
func AckProcessEvent(homeDir, eventID string, generation uint64) error {
	return ackProcessEvent(homeDir, eventID, generation, home.RenameDurable)
}

func ackProcessEvent(homeDir, eventID string, generation uint64, renameFn func(string, string) error) error {
	rec, err := readProcessEventRecord(homeDir, eventID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("process-event %q is not registered", eventID)
	}
	if generation != rec.Generation {
		return fmt.Errorf("process-event %q ack for generation %d does not match current generation %d", eventID, generation, rec.Generation)
	}
	if !rec.Resolved {
		return fmt.Errorf("process-event %q generation %d has no captured result to ack", eventID, generation)
	}
	rec.AckedGeneration = generation
	return writeProcessEventRecord(homeDir, rec, renameFn)
}

// RecoverProcessEvents re-announces every captured-but-unacked event once per
// home per process, returning how many wakes it enqueued. This is the restart
// half of at-least-once delivery: a crash after capture, or after the wake but
// before the ack, is re-delivered here; a crash after the ack is not. Steady
// ticks never call this path. A failed recovery clears the one-shot mark so
// the next cycle retries.
func RecoverProcessEvents(homeDir string) (int, error) {
	if _, loaded := processEventRecoveryDone.LoadOrStore(homeDir, true); loaded {
		return 0, nil
	}
	n, err := recoverProcessEvents(homeDir, home.RenameDurable)
	if err != nil {
		processEventRecoveryDone.Delete(homeDir)
	}
	return n, err
}

func recoverProcessEvents(homeDir string, renameFn func(string, string) error) (int, error) {
	recs, err := listProcessEventRecords(homeDir)
	if err != nil {
		return 0, fmt.Errorf("process-event recovery: %w", err)
	}
	delivered := 0
	for _, rec := range recs {
		if !rec.Resolved || rec.Generation == rec.AckedGeneration {
			continue
		}
		if err := announceProcessEvent(homeDir, rec, renameFn); err != nil {
			return delivered, fmt.Errorf("process-event recovery: %s: %w", rec.EventID, err)
		}
		delivered++
	}
	return delivered, nil
}
