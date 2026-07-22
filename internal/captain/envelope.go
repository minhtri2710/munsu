package captain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// CommandEnvelopeDir is the state subdirectory for durable command envelopes.
const CommandEnvelopeDir = ".command-envelope"

// CaptainEnvelopeSubdir is the copied envelope directory under a captain home.
const CaptainEnvelopeSubdir = ".command-envelope"

// SchemaVersionEnvelope is the stable schema identifier for command envelopes.
const SchemaVersionEnvelope = "munsu.command-envelope/v1"

// Envelope status constants.
const (
	EnvelopeStatusPending   = "pending"
	EnvelopeStatusDelivered = "delivered"
	EnvelopeStatusCompleted = "completed"
	EnvelopeStatusFailed    = "failed"
)

// CommandEnvelope is the durable idempotent General-to-Captain command envelope.
// It is the authority for Captain actions — the pane marker is notification only.
// Survives captain pane death; Converge/outbox redelivers safely.
type CommandEnvelope struct {
	SchemaVersion    string   `json:"schema_version"`
	EnvelopeID       string   `json:"envelope_id"`
	TargetCaptainID  string   `json:"target_captain_id"`
	IdempotencyKey   string   `json:"idempotency_key,omitempty"`
	TargetTasks      []string `json:"target_tasks,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
	ForbiddenActions []string `json:"forbidden_actions,omitempty"`
	RequireReady     bool     `json:"require_ready"`
	Message          string   `json:"message"`
	CreatedAt        int64    `json:"created_at"`
	DeliveredAt      int64    `json:"delivered_at,omitempty"`
	Status           string   `json:"status"`
}

// --- path helpers ---

// envelopeDir returns parentHome/state/.command-envelope/.
func envelopeDir(home string) string {
	return filepath.Join(home, "state", CommandEnvelopeDir)
}

// idempotencyIndexDir returns parentHome/state/.command-envelope/.idempotency/.
func idempotencyIndexDir(home string) string {
	return filepath.Join(home, "state", CommandEnvelopeDir, ".idempotency")
}

// captainEnvelopeDir returns captainHome/state/.command-envelope/.
func captainEnvelopeDir(home string) string {
	return filepath.Join(home, "state", CaptainEnvelopeSubdir)
}

// envelopePath returns the path to an envelope JSON file.
func envelopePath(home, envelopeID string) string {
	return filepath.Join(envelopeDir(home), envelopeID+".json")
}

// captainEnvelopePath returns the path to an envelope copy in a captain home.
func captainEnvelopePath(captainHome, envelopeID string) string {
	return filepath.Join(captainEnvelopeDir(captainHome), envelopeID+".json")
}

// idempotencyIndexPath returns the path for an idempotency key index entry.
func idempotencyIndexPath(home, key string) string {
	return filepath.Join(idempotencyIndexDir(home), key)
}

// --- ID generation ---

// NewEnvelopeID generates a random hex envelope ID.
func NewEnvelopeID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating envelope id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// --- envelope CRUD ---

// CreateEnvelope stores a command envelope durably in the General home.
// If IdempotencyKey is set and a prior envelope with that key exists, returns
// (false, nil) — idempotent no-op. The envelope's SchemaVersion, CreatedAt,
// and Status are initialized automatically.
func CreateEnvelope(home string, env *CommandEnvelope) (bool, error) {
	if env.EnvelopeID == "" {
		id, err := NewEnvelopeID()
		if err != nil {
			return false, err
		}
		env.EnvelopeID = id
	}
	env.SchemaVersion = SchemaVersionEnvelope
	env.CreatedAt = time.Now().UnixNano()
	env.Status = EnvelopeStatusPending

	dir := envelopeDir(home)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating envelope dir: %w", err)
	}

	// Idempotency: if key already indexed, this is a no-op.
	if env.IdempotencyKey != "" {
		idxDir := idempotencyIndexDir(home)
		if err := os.MkdirAll(idxDir, 0755); err != nil {
			return false, fmt.Errorf("creating idempotency index dir: %w", err)
		}
		idxPath := idempotencyIndexPath(home, env.IdempotencyKey)
		if existing, err := os.ReadFile(idxPath); err == nil && len(existing) > 0 {
			return false, nil
		}
	}

	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshaling envelope: %w", err)
	}

	path := envelopePath(home, env.EnvelopeID)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return false, fmt.Errorf("writing envelope: %w", err)
	}

	// Write idempotency index after successful envelope write.
	if env.IdempotencyKey != "" {
		idxPath := idempotencyIndexPath(home, env.IdempotencyKey)
		if err := os.WriteFile(idxPath, []byte(env.EnvelopeID), 0644); err != nil {
			return false, fmt.Errorf("writing idempotency index: %w", err)
		}
	}

	return true, nil
}

// GetEnvelope reads a command envelope. Returns nil, nil if not found.
func GetEnvelope(home, envelopeID string) (*CommandEnvelope, error) {
	data, err := os.ReadFile(envelopePath(home, envelopeID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var env CommandEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling envelope %s: %w", envelopeID, err)
	}
	return &env, nil
}

// ListPendingEnvelopes returns all pending envelopes for a given captain ID,
// ordered by creation time (FIFO).
func ListPendingEnvelopes(home, captainID string) ([]*CommandEnvelope, error) {
	all, err := listEnvelopes(home, EnvelopeStatusPending)
	if err != nil {
		return nil, err
	}
	var filtered []*CommandEnvelope
	for _, env := range all {
		if env.TargetCaptainID == captainID {
			filtered = append(filtered, env)
		}
	}
	return filtered, nil
}

// listEnvelopes lists envelopes in the given home filtered by status.
// Empty status returns all non-dotfile JSON files.
func listEnvelopes(home, status string) ([]*CommandEnvelope, error) {
	dir := envelopeDir(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)

	var result []*CommandEnvelope
	for _, p := range paths {
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			continue
		}
		var env CommandEnvelope
		if unmarshalErr := json.Unmarshal(data, &env); unmarshalErr != nil {
			continue
		}
		if status != "" && env.Status != status {
			continue
		}
		result = append(result, &env)
	}
	return result, nil
}

// markEnvelopeStatus updates an envelope's status (and optionally delivered_at).
func markEnvelopeStatus(home, envelopeID, status string, deliveredAt int64) error {
	env, err := GetEnvelope(home, envelopeID)
	if err != nil {
		return err
	}
	if env == nil {
		return fmt.Errorf("envelope %s not found", envelopeID)
	}
	env.Status = status
	if deliveredAt > 0 {
		env.DeliveredAt = deliveredAt
	}
	return writeEnvelope(home, env)
}

// MarkEnvelopeDelivered sets the envelope status to delivered.
func MarkEnvelopeDelivered(home, envelopeID string) error {
	return markEnvelopeStatus(home, envelopeID, EnvelopeStatusDelivered, time.Now().UnixNano())
}

// MarkEnvelopeCompleted sets the envelope status to completed.
func MarkEnvelopeCompleted(home, envelopeID string) error {
	return markEnvelopeStatus(home, envelopeID, EnvelopeStatusCompleted, 0)
}

// MarkEnvelopeFailed sets the envelope status to failed.
func MarkEnvelopeFailed(home, envelopeID string) error {
	return markEnvelopeStatus(home, envelopeID, EnvelopeStatusFailed, 0)
}

// writeEnvelope serializes and writes an envelope to disk.
func writeEnvelope(home string, env *CommandEnvelope) error {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling envelope: %w", err)
	}
	path := envelopePath(home, env.EnvelopeID)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing envelope: %w", err)
	}
	return nil
}

// --- captain push / delivery ---

// PushEnvelopeToCaptain copies one pending envelope for a captain into
// the captain's own state/.command-envelope/. Returns the envelope or nil
// if there is no pending envelope with the given ID for the captain.
func PushEnvelopeToCaptain(parentHome string, captainHome string, env *CommandEnvelope) error {
	dir := captainEnvelopeDir(captainHome)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating captain envelope dir: %w", err)
	}
	// Marshal and write to captain home for the captain agent to read.
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling envelope for captain: %w", err)
	}
	dest := captainEnvelopePath(captainHome, env.EnvelopeID)
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return fmt.Errorf("writing envelope to captain home: %w", err)
	}
	return nil
}

// FlushEnvelopeSend delivers all pending envelopes for one captain.
// For each pending envelope:
// 1. Copies the envelope to the captain's state
// 2. Sends the marked message via typed prompt submission
// 3. Marks the envelope delivered only when acknowledged
// Stops at the first unacknowledged result; earlier delivered envelopes stay delivered.
func FlushEnvelopeSend(parentHome string, sm Info) error {
	pending, err := ListPendingEnvelopes(parentHome, sm.ID)
	if err != nil {
		return fmt.Errorf("%s: listing pending envelopes: %v", sm.ID, err)
	}
	if len(pending) == 0 {
		return nil
	}

	taskID := taskIDForCaptain(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return fmt.Errorf("%s: %d envelope(s) pending but no task meta — relaunch captain then converge", sm.ID, len(pending))
	}
	if meta["kind"] != "captain" {
		return fmt.Errorf("%s: meta kind=%q, expected captain — envelope retained", sm.ID, meta["kind"])
	}

	windowID := meta["window"]
	if windowID == "" {
		return fmt.Errorf("%s: no window in meta — envelope retained", sm.ID)
	}

	bk, _, bkErr := backendForTask(parentHome, meta)
	if bkErr != nil {
		return fmt.Errorf("%s: cannot resolve backend — envelope retained: %v", sm.ID, bkErr)
	}

	for _, env := range pending {
		// Push envelope to captain home BEFORE prompting, so the authoritative
		// envelope exists when the agent begins processing. Keep the parent
		// envelope pending until prompt is acknowledged; a copied-but-pending
		// envelope is safe for retry.
		if err := PushEnvelopeToCaptain(parentHome, sm.Home, env); err != nil {
			return fmt.Errorf("%s: pushing envelope %s to captain home: %v", sm.ID, env.EnvelopeID, err)
		}

		// Send the message with marker via typed prompt submission.
		msg := marker.MarkFromGeneral(env.Message)
		result := session.SubmitPrompt(bk, windowID, msg)
		if !result.Acknowledged() {
			// Envelope was pushed to captain home but parent still pending;
			// next converge retry will find it already in captain home.
			return fmt.Errorf("%s: envelope %s send not acknowledged (status=%s) — envelope retained in parent", sm.ID, env.EnvelopeID, result.Status)
		}

		// Only mark delivered in parent home after acknowledged submission.
		if err := MarkEnvelopeDelivered(parentHome, env.EnvelopeID); err != nil {
			return fmt.Errorf("%s: envelope %s delivered but failed to update status: %v", sm.ID, env.EnvelopeID, err)
		}

		fmt.Printf("  %s: envelope %s delivered\n", sm.ID, env.EnvelopeID)
	}
	return nil
}

// GetCaptainEnvelope reads an envelope from a captain's own state.
// The captain calls this to inspect the authoritative envelope for the
// current command.
func GetCaptainEnvelope(captainHome, envelopeID string) (*CommandEnvelope, error) {
	data, err := os.ReadFile(captainEnvelopePath(captainHome, envelopeID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var env CommandEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling captain envelope %s: %w", envelopeID, err)
	}
	return &env, nil
}
