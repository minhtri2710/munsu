package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// resolvedWith returns a resolver that always resolves with result and counts
// its invocations.
func resolvedWith(result string, calls *int) ProcessEventResolver {
	return func(context.Context) (bool, []byte, error) {
		*calls++
		return true, []byte(result), nil
	}
}

func unresolved(calls *int) ProcessEventResolver {
	return func(context.Context) (bool, []byte, error) {
		*calls++
		return false, nil, nil
	}
}

// crashAfterRename performs the real durable rename and then reports a crash
// on the nth rename it sees, so the durable state is exactly what a process
// that died right after that rename would leave behind.
func crashAfterRename(n int) func(string, string) error {
	calls := 0
	return func(from, to string) error {
		if err := home.RenameDurable(from, to); err != nil {
			return err
		}
		calls++
		if calls == n {
			return errors.New("simulated crash")
		}
		return nil
	}
}

func drainProcessEventWakes(t *testing.T, homeDir string) []ProcessEventWake {
	t.Helper()
	records, err := home.DrainWakes(homeDir)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	var wakes []ProcessEventWake
	for _, rec := range records {
		if rec.Kind != ProcessEventWakeKind {
			t.Fatalf("unexpected wake kind %q", rec.Kind)
		}
		var w ProcessEventWake
		if err := json.Unmarshal([]byte(rec.Payload), &w); err != nil {
			t.Fatalf("wake payload %q: %v", rec.Payload, err)
		}
		if w.EventID != rec.Key {
			t.Fatalf("wake key %q does not match payload event id %q", rec.Key, w.EventID)
		}
		wakes = append(wakes, w)
	}
	return wakes
}

func mustReadProcessEvent(t *testing.T, homeDir, eventID string) *ProcessEventRecord {
	t.Helper()
	rec, err := readProcessEventRecord(homeDir, eventID)
	if err != nil {
		t.Fatalf("readProcessEventRecord: %v", err)
	}
	if rec == nil {
		t.Fatalf("event %q is not registered", eventID)
	}
	return rec
}

func TestProcessEvent_EmptyResultSurvivesSerializationAndCapture(t *testing.T) {
	homeDir := t.TempDir()
	// An empty but meaningful captured result (e.g. []byte{}) must round-trip
	// through the durable record and not collapse to nil, or the captured
	// external outcome is silently lost (the old ,omitempty bug).
	rec := &ProcessEventRecord{
		SchemaVersion: ProcessEventSchema,
		EventID:       "ev-empty",
		Generation:    1,
		Resolved:      true,
		Result:        []byte{},
	}
	if err := writeProcessEventRecord(homeDir, rec, home.RenameDurable); err != nil {
		t.Fatal(err)
	}
	got, err := readProcessEventRecord(homeDir, "ev-empty")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result == nil {
		t.Fatal("empty result round-tripped to nil: captured outcome lost")
	}
	if len(got.Result) != 0 {
		t.Fatalf("round-tripped result length = %d, want 0", len(got.Result))
	}

	// The full capture-before-wake cycle must also keep the empty result and
	// still deliver exactly one wake for it (an empty result is a real outcome).
	homeDir2 := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir2, "ev-e2", "p"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	emptyResolver := func(context.Context) (bool, []byte, error) {
		calls++
		return true, []byte{}, nil
	}
	if err := EvaluateProcessEvent(context.Background(), homeDir2, "ev-e2", emptyResolver); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("resolver ran %d times, want 1", calls)
	}
	ronCycle := mustReadProcessEvent(t, homeDir2, "ev-e2")
	if !ronCycle.Resolved || ronCycle.Result == nil || len(ronCycle.Result) != 0 || !ronCycle.Announced {
		t.Fatalf("record = %+v, want resolved+announced with empty (non-nil) result", ronCycle)
	}
	wakes := drainProcessEventWakes(t, homeDir2)
	if len(wakes) != 1 || wakes[0].EventID != "ev-e2" || wakes[0].Generation != 1 {
		t.Fatalf("wakes = %+v, want one wake for ev-e2 generation 1", wakes)
	}
}

func TestProcessEvent_ResolutionSpendsExactlyOneWakeCarryingGeneration(t *testing.T) {
	homeDir := t.TempDir()
	gen, err := RegisterProcessEvent(homeDir, "ev-1", "opaque\tpayload")
	if err != nil || gen != 1 {
		t.Fatalf("Register = %d, %v", gen, err)
	}
	calls := 0
	for i := 0; i < 3; i++ {
		if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-1", resolvedWith("result", &calls)); err != nil {
			t.Fatalf("Evaluate %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("resolver ran %d times after capture, want 1", calls)
	}
	wakes := drainProcessEventWakes(t, homeDir)
	if len(wakes) != 1 || wakes[0].EventID != "ev-1" || wakes[0].Generation != 1 || wakes[0].Payload != "opaque\tpayload" {
		t.Fatalf("wakes = %+v, want one wake for ev-1 generation 1", wakes)
	}
	rec := mustReadProcessEvent(t, homeDir, "ev-1")
	if !rec.Resolved || string(rec.Result) != "result" || !rec.Announced {
		t.Fatalf("record = %+v, want resolved+announced with captured result", rec)
	}
}

func TestProcessEvent_UnresolvedConditionSpendsZeroWakes(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-wait", ""); err != nil {
		t.Fatal(err)
	}
	calls := 0
	for i := 0; i < 5; i++ {
		if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-wait", unresolved(&calls)); err != nil {
			t.Fatalf("Evaluate %d: %v", i, err)
		}
	}
	if calls != 5 {
		t.Fatalf("resolver ran %d times, want 5", calls)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("unresolved condition spent %d wakes", len(wakes))
	}
	if n, err := recoverProcessEvents(homeDir, home.RenameDurable); err != nil || n != 0 {
		t.Fatalf("recovery of an unresolved event = %d, %v; want 0 wakes", n, err)
	}
}

// Crash matrix: after capture, before wake.
func TestProcessEvent_CrashAfterCaptureBeforeWake_RedeliversOnRecover(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-cap", "p"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	// Rename 1 inside Evaluate is the capture; die right after it lands.
	err := evaluateProcessEvent(context.Background(), homeDir, "ev-cap", resolvedWith("r", &calls), crashAfterRename(1))
	if err == nil || !strings.Contains(err.Error(), "simulated crash") {
		t.Fatalf("Evaluate = %v, want simulated crash", err)
	}
	rec := mustReadProcessEvent(t, homeDir, "ev-cap")
	if !rec.Resolved || rec.Announced || string(rec.Result) != "r" {
		t.Fatalf("record = %+v, want captured and not announced", rec)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("wake enqueued before the crash: %+v", wakes)
	}

	n, err := recoverProcessEvents(homeDir, home.RenameDurable)
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v; want 1", n, err)
	}
	wakes := drainProcessEventWakes(t, homeDir)
	if len(wakes) != 1 || wakes[0].EventID != "ev-cap" || wakes[0].Generation != 1 {
		t.Fatalf("wakes = %+v, want one wake for ev-cap generation 1", wakes)
	}
	if calls != 1 {
		t.Fatalf("recovery re-ran the resolver: %d calls", calls)
	}
}

// Crash matrix: after wake, before ack.
func TestProcessEvent_CrashAfterWakeBeforeAck_RedeliversOnRecover(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-wake", "p"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-wake", resolvedWith("r", &calls)); err != nil {
		t.Fatal(err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 {
		t.Fatalf("wakes = %+v, want exactly one", wakes)
	}
	if rec := mustReadProcessEvent(t, homeDir, "ev-wake"); !rec.Announced {
		t.Fatalf("record = %+v, want announced", rec)
	}
	// Steady ticks in the same process do not re-announce.
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-wake", resolvedWith("r", &calls)); err != nil {
		t.Fatal(err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("steady tick re-announced: %+v", wakes)
	}

	// Restart before ack: Announced is not a stop; only the ack is.
	n, err := recoverProcessEvents(homeDir, home.RenameDurable)
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v; want 1", n, err)
	}
	wakes := drainProcessEventWakes(t, homeDir)
	if len(wakes) != 1 || wakes[0].EventID != "ev-wake" || wakes[0].Generation != 1 {
		t.Fatalf("wakes = %+v, want one re-delivered wake", wakes)
	}
	if calls != 1 {
		t.Fatalf("resolver ran %d times, want 1", calls)
	}
}

// Crash matrix: after ack.
func TestProcessEvent_CrashAfterAck_NotRedelivered(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-ack", "p"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-ack", resolvedWith("r", &calls)); err != nil {
		t.Fatal(err)
	}
	wakes := drainProcessEventWakes(t, homeDir)
	if len(wakes) != 1 {
		t.Fatalf("wakes = %+v", wakes)
	}
	if err := AckProcessEvent(homeDir, wakes[0].EventID, wakes[0].Generation); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	rec := mustReadProcessEvent(t, homeDir, "ev-ack")
	if rec.AckedGeneration != rec.Generation {
		t.Fatalf("record = %+v, want acked generation", rec)
	}
	n, err := recoverProcessEvents(homeDir, home.RenameDurable)
	if err != nil || n != 0 {
		t.Fatalf("recover = %d, %v; want 0", n, err)
	}
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-ack", resolvedWith("r", &calls)); err != nil {
		t.Fatal(err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("acked event re-announced: %+v", wakes)
	}
	if calls != 1 {
		t.Fatalf("resolver ran %d times, want 1", calls)
	}
}

// Crash matrix: generation mismatch. An old generation's ack neither stops the
// new registration nor is accepted after the bump.
func TestProcessEvent_OldGenerationAckDoesNotSuppressNewRegistration(t *testing.T) {
	homeDir := t.TempDir()
	if gen, err := RegisterProcessEvent(homeDir, "ev-gen", "p1"); err != nil || gen != 1 {
		t.Fatalf("Register = %d, %v", gen, err)
	}
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-gen", resolvedWith("r1", &calls)); err != nil {
		t.Fatal(err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 || wakes[0].Generation != 1 {
		t.Fatalf("wakes = %+v", wakes)
	}
	if err := AckProcessEvent(homeDir, "ev-gen", 1); err != nil {
		t.Fatal(err)
	}

	gen, err := RegisterProcessEvent(homeDir, "ev-gen", "p2")
	if err != nil || gen != 2 {
		t.Fatalf("re-Register = %d, %v; want generation 2", gen, err)
	}
	rec := mustReadProcessEvent(t, homeDir, "ev-gen")
	if rec.Resolved || rec.Announced || rec.Result != nil || rec.AckedGeneration != 1 || rec.Payload != "p2" {
		t.Fatalf("record after re-registration = %+v", rec)
	}

	// The generation-mismatch guard: acking the old generation again is refused.
	err = AckProcessEvent(homeDir, "ev-gen", 1)
	if err == nil || !strings.Contains(err.Error(), "does not match current generation 2") {
		t.Fatalf("stale ack = %v, want generation-mismatch refusal", err)
	}
	if rec := mustReadProcessEvent(t, homeDir, "ev-gen"); rec.AckedGeneration != 1 || rec.Generation != 2 {
		t.Fatalf("stale ack mutated the record: %+v", rec)
	}

	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-gen", resolvedWith("r2", &calls)); err != nil {
		t.Fatal(err)
	}
	wakes := drainProcessEventWakes(t, homeDir)
	if len(wakes) != 1 || wakes[0].Generation != 2 || wakes[0].Payload != "p2" {
		t.Fatalf("wakes = %+v, want one wake for generation 2", wakes)
	}
	if calls != 2 {
		t.Fatalf("resolver ran %d times, want 2", calls)
	}
	// And a restart before the generation-2 ack re-delivers generation 2.
	n, err := recoverProcessEvents(homeDir, home.RenameDurable)
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v; want 1", n, err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 || wakes[0].Generation != 2 {
		t.Fatalf("wakes = %+v", wakes)
	}
}

// Guard: capture-before-wake. A capture whose rename was lost leaves the
// durable record unresolved; the announcement must refuse rather than enqueue
// a wake for a result nobody can read back.
func TestProcessEvent_RefusesWakeWhenCaptureDidNotLand(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-lost", "p"); err != nil {
		t.Fatal(err)
	}
	dropped := 0
	lostRename := func(from, to string) error {
		dropped++
		return os.Remove(from)
	}
	calls := 0
	err := evaluateProcessEvent(context.Background(), homeDir, "ev-lost", resolvedWith("r", &calls), lostRename)
	if err == nil || !strings.Contains(err.Error(), "result is not durably captured") {
		t.Fatalf("Evaluate = %v, want capture-before-wake refusal", err)
	}
	if dropped != 1 {
		t.Fatalf("renames attempted = %d, want only the capture", dropped)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("wake enqueued for an uncaptured result: %+v", wakes)
	}
	if rec := mustReadProcessEvent(t, homeDir, "ev-lost"); rec.Resolved || rec.Announced {
		t.Fatalf("record = %+v, want still unresolved", rec)
	}
}

func TestProcessEvent_RecoverRunsOncePerHomePerProcess(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-once", "p"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := evaluateProcessEvent(context.Background(), homeDir, "ev-once", resolvedWith("r", &calls), crashAfterRename(1)); err == nil {
		t.Fatal("expected simulated crash")
	}
	n, err := RecoverProcessEvents(homeDir)
	if err != nil || n != 1 {
		t.Fatalf("first recover = %d, %v; want 1", n, err)
	}
	n, err = RecoverProcessEvents(homeDir)
	if err != nil || n != 0 {
		t.Fatalf("second recover = %d, %v; want 0 (already ran for this home)", n, err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 {
		t.Fatalf("wakes = %+v, want exactly one", wakes)
	}
}

func TestProcessEvent_RecoverFailureClearsOneShotMark(t *testing.T) {
	homeDir := t.TempDir()
	dir := processEventDirPath(homeDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "v1-"+strings.Repeat("0", 64)+".json")
	if err := os.WriteFile(bad, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverProcessEvents(homeDir); err == nil {
		t.Fatal("expected recovery to fail on a corrupt record")
	}
	if _, loaded := processEventRecoveryDone.Load(homeDir); loaded {
		t.Fatal("failed recovery must not mark the home as recovered")
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if n, err := RecoverProcessEvents(homeDir); err != nil || n != 0 {
		t.Fatalf("retry = %d, %v", n, err)
	}
}

func TestProcessEvent_RegisterRefusesUnusableIDs(t *testing.T) {
	homeDir := t.TempDir()
	for _, id := range []string{"", "a\tb", "a\nb", "a\rb"} {
		if _, err := RegisterProcessEvent(homeDir, id, "p"); err == nil || !strings.Contains(err.Error(), "non-empty single line") {
			t.Fatalf("Register(%q) = %v, want refusal", id, err)
		}
	}
	if recs, err := listProcessEventRecords(homeDir); err != nil || len(recs) != 0 {
		t.Fatalf("records = %v, %v; want none", recs, err)
	}
}

func TestProcessEvent_EvaluateRefusesMissingResolverAndUnregisteredEvent(t *testing.T) {
	homeDir := t.TempDir()
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-x", nil); err == nil || !strings.Contains(err.Error(), "a resolver is required") {
		t.Fatalf("nil resolver = %v", err)
	}
	calls := 0
	err := EvaluateProcessEvent(context.Background(), homeDir, "ev-x", resolvedWith("r", &calls))
	if err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("unregistered = %v", err)
	}
	if calls != 0 {
		t.Fatal("resolver ran for an unregistered event")
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("wakes = %+v", wakes)
	}
}

func TestProcessEvent_EvaluatePropagatesResolverError(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-err", "p"); err != nil {
		t.Fatal(err)
	}
	err := EvaluateProcessEvent(context.Background(), homeDir, "ev-err", func(context.Context) (bool, []byte, error) {
		return false, nil, errors.New("provider down")
	})
	if err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("Evaluate = %v", err)
	}
	if rec := mustReadProcessEvent(t, homeDir, "ev-err"); rec.Resolved {
		t.Fatalf("resolver error captured a result: %+v", rec)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("wakes = %+v", wakes)
	}
}

func TestProcessEvent_AckRefusesUnregisteredAndUncaptured(t *testing.T) {
	homeDir := t.TempDir()
	if err := AckProcessEvent(homeDir, "ev-none", 1); err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("unregistered ack = %v", err)
	}
	if _, err := RegisterProcessEvent(homeDir, "ev-uncaptured", "p"); err != nil {
		t.Fatal(err)
	}
	err := AckProcessEvent(homeDir, "ev-uncaptured", 1)
	if err == nil || !strings.Contains(err.Error(), "has no captured result to ack") {
		t.Fatalf("uncaptured ack = %v", err)
	}
	rec := mustReadProcessEvent(t, homeDir, "ev-uncaptured")
	if rec.AckedGeneration != 0 {
		t.Fatalf("refused ack was recorded: %+v", rec)
	}
	// The refused ack must not pre-empt the announcement.
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-uncaptured", resolvedWith("r", &calls)); err != nil {
		t.Fatal(err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 {
		t.Fatalf("wakes = %+v, want one", wakes)
	}
}

func TestProcessEvent_ReadRefusesUnsupportedSchema(t *testing.T) {
	homeDir := t.TempDir()
	rec := &ProcessEventRecord{SchemaVersion: ProcessEventSchema + 1, EventID: "ev-schema", Generation: 1}
	if err := writeProcessEventRecord(homeDir, rec, home.RenameDurable); err != nil {
		t.Fatal(err)
	}
	if _, err := readProcessEventRecord(homeDir, "ev-schema"); err == nil || !strings.Contains(err.Error(), "unsupported process-event schema version") {
		t.Fatalf("read = %v", err)
	}
	if _, err := recoverProcessEvents(homeDir, home.RenameDurable); err == nil || !strings.Contains(err.Error(), "unsupported process-event schema version") {
		t.Fatalf("recover = %v", err)
	}
}

func TestProcessEvent_RecoverRefusesRecordUnderForeignIdentity(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "ev-real", "p"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, "ev-real", resolvedWith("r", &calls)); err != nil {
		t.Fatal(err)
	}
	drainProcessEventWakes(t, homeDir)
	// Move the captured record under another event's filename.
	if err := os.Rename(processEventRecordPath(homeDir, "ev-real"), processEventRecordPath(homeDir, "ev-other")); err != nil {
		t.Fatal(err)
	}
	n, err := recoverProcessEvents(homeDir, home.RenameDurable)
	if err == nil || !strings.Contains(err.Error(), "invalid event identity") || n != 0 {
		t.Fatalf("recover = %d, %v; want identity refusal", n, err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("recovery announced under a foreign identity: %+v", wakes)
	}
}

func TestProcessEvent_RecordPathIsFlatAndSchemaVersioned(t *testing.T) {
	homeDir := t.TempDir()
	for _, id := range []string{"../escape", "a/b", "captain:munsu", "with space"} {
		p := processEventRecordPath(homeDir, id)
		if filepath.Dir(p) != processEventDirPath(homeDir) {
			t.Fatalf("record for %q escapes the process-event dir: %s", id, p)
		}
		if !strings.HasPrefix(filepath.Base(p), "v1-") || !strings.HasSuffix(p, ".json") {
			t.Fatalf("record name for %q = %s", id, filepath.Base(p))
		}
	}
	if processEventDirPath(homeDir) != filepath.Join(home.StateDir(homeDir), ".process-events") {
		t.Fatalf("dir = %s", processEventDirPath(homeDir))
	}
}
