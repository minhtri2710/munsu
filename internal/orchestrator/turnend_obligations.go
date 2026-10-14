// Turn-end obligations are actions that must be performed at the end of a turn
// and are persisted under the munsu home so they survive process restarts.
// Obligations differ by role: general, captain, and soldier each have a
// distinct set of obligations.

package orchestrator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Role identifies which role the obligation set targets.
type Role string

const (
	RoleGeneral Role = "general"
	RoleCaptain Role = "captain"
	RoleSoldier Role = "soldier"
)

// ObligationKind describes one kind of turn-end obligation.
type ObligationKind string

// Cleanup: temp artifacts, stale turnend markers, closed-key hygiene.
const Cleanup ObligationKind = "cleanup"

// State represents the lifecycle state of an obligation.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Obligation is a single turn-end obligation.
type Obligation struct {
	Kind      ObligationKind `json:"kind"`
	State     State          `json:"state"`
	Key       string         `json:"key,omitempty"` // terminal key bound to this obligation
	Detail    string         `json:"detail,omitempty"`
	CreatedAt int64          `json:"created_at"` // unix nanos
	ClosedAt  int64          `json:"closed_at,omitempty"`
}

// Obligations returns the set of obligations for the given role.
func Obligations(role Role) []Obligation {
	now := time.Now().UnixNano()
	switch role {
	case RoleGeneral:
		return []Obligation{
			{
				Kind:      Cleanup,
				State:     StateOpen,
				Detail:    "clean temp artifacts, stale turnend markers, closed-key hygiene",
				CreatedAt: now,
			},
		}
	case RoleCaptain:
		return []Obligation{
			{
				Kind:      Cleanup,
				State:     StateOpen,
				Detail:    "clean up soldier artifacts, stale turnend markers, closed-key hygiene",
				CreatedAt: now,
			},
		}
	case RoleSoldier:
		return []Obligation{
			{
				Kind:      Cleanup,
				State:     StateOpen,
				Detail:    "clean temp artifacts, stale turnend markers, closed-key hygiene",
				CreatedAt: now,
			},
		}
	default:
		return nil
	}
}

// --- Per-role obligation persistence ---

const obligationsDir = "state/.obligations"

// obligationsPath returns the per-role durable obligations file path.
func obligationsPath(homeDir string, role Role) string {
	return filepath.Join(homeDir, obligationsDir, string(role)+".obligations")
}

// LoadObligations reads the persisted obligation state for the given role.
// Returns the persisted obligations, or the default set if no persisted state
// exists. An empty/zero-length file returns nil.
func LoadObligations(homeDir string, role Role) ([]Obligation, error) {
	p := obligationsPath(homeDir, role)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Obligations(role), nil
		}
		return nil, fmt.Errorf("reading obligations for %s: %w", role, err)
	}
	defer f.Close()
	obligations, err := parseObligations(f)
	if err != nil {
		return nil, err
	}
	if len(obligations) == 0 {
		return nil, nil
	}
	return obligations, nil
}

// SaveObligations persists the obligation state for the given role.
func SaveObligations(homeDir string, role Role, obligations []Obligation) error {
	return writeObligationsFile(obligationsPath(homeDir, role), obligations)
}

// CompleteObligation marks an obligation of the given kind as closed for a role.
func CompleteObligation(homeDir string, role Role, kind ObligationKind) (bool, error) {
	obligations, err := LoadObligations(homeDir, role)
	if err != nil {
		return false, err
	}
	now := time.Now().UnixNano()
	found := false
	for i, o := range obligations {
		if o.Kind == kind && o.State == StateOpen {
			obligations[i].State = StateClosed
			obligations[i].ClosedAt = now
			found = true
		}
	}
	if !found {
		return false, nil
	}
	return true, SaveObligations(homeDir, role, obligations)
}

// ClearCompleted removes all closed obligations from persisted state.
func ClearCompleted(homeDir string, role Role) error {
	obligations, err := LoadObligations(homeDir, role)
	if err != nil {
		return err
	}
	var open []Obligation
	for _, o := range obligations {
		if o.State == StateOpen {
			open = append(open, o)
		}
	}
	return SaveObligations(homeDir, role, open)
}

// obligationFields is the fixed column count of a serialized obligation line.
const obligationFields = 6

// parseObligations reads obligation entries from a file.
// Format: kind\tstate\tkey\tdetail\tcreated_at\tclosed_at — exactly six
// tab-separated fields, key empty when the obligation has no key. A line that
// does not match (wrong field count, unknown kind or state, non-integer
// timestamp) fails the whole load: obligation state is not something to guess.
func parseObligations(f *os.File) ([]Obligation, error) {
	var obligations []Obligation
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != obligationFields {
			return nil, fmt.Errorf("obligation line %d: got %d fields, want %d", lineNo, len(parts), obligationFields)
		}
		kind := ObligationKind(parts[0])
		if kind != Cleanup {
			return nil, fmt.Errorf("obligation line %d: unknown kind %q", lineNo, parts[0])
		}
		state := State(parts[1])
		if state != StateOpen && state != StateClosed {
			return nil, fmt.Errorf("obligation line %d: unknown state %q", lineNo, parts[1])
		}
		createdAt, err := strconv.ParseInt(parts[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("obligation line %d: created_at %q: %w", lineNo, parts[4], err)
		}
		closedAt, err := strconv.ParseInt(parts[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("obligation line %d: closed_at %q: %w", lineNo, parts[5], err)
		}
		obligations = append(obligations, Obligation{
			Kind:      kind,
			State:     state,
			Key:       parts[2],
			Detail:    parts[3],
			CreatedAt: createdAt,
			ClosedAt:  closedAt,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return obligations, nil
}

// writeObligationsFile serializes obligations to a file, one six-field
// tab-separated line each (key empty when the obligation has no key).
func writeObligationsFile(p string, obligations []Obligation) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating obligations dir: %w", err)
	}
	var b strings.Builder
	for _, o := range obligations {
		b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d\n",
			o.Kind, o.State, o.Key, o.Detail, o.CreatedAt, o.ClosedAt))
	}
	return os.WriteFile(p, []byte(b.String()), 0644)
}
