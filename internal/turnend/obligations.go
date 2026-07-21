// Package turnend implements durable role-aware turn-end obligations.
//
// Turn-end obligations are actions that must be performed at the end of a turn
// and are persisted under the munsu home so they survive process restarts.
// Obligations differ by role: general, captain, and soldier each have a
// distinct set of obligations.
package turnend

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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

const (
	// ReportRelay: material status change must be relayed to the parent channel.
	// Applies to captains (relay to general) and soldiers (relay to captain/general).
	ReportRelay ObligationKind = "report-relay"

	// Cleanup: temp artifacts, stale turnend markers, closed-key hygiene.
	// Applies to all roles.
	Cleanup ObligationKind = "cleanup"
)

// State represents the lifecycle state of an obligation.
type State string

const (
	StateOpen    State = "open"
	StateClosed  State = "closed"
)

// Obligation is a single turn-end obligation.
type Obligation struct {
	Kind      ObligationKind `json:"kind"`
	State     State          `json:"state"`
	Detail    string         `json:"detail,omitempty"`
	CreatedAt int64          `json:"created_at"` // unix nanos
	ClosedAt  int64          `json:"closed_at,omitempty"`
}

// Obligations returns the set of obligations for the given role.
// The returned list is the role's canonical obligation set — all obligations
// start in StateOpen. Consumers should call LoadObligations to get the
// persisted state of each obligation.
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
				Kind:      ReportRelay,
				State:     StateOpen,
				Detail:    "relay material status changes to General parent channel",
				CreatedAt: now,
			},
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
				Kind:      ReportRelay,
				State:     StateOpen,
				Detail:    "relay material status changes to supervisor parent channel",
				CreatedAt: now,
			},
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

// --- Durable persistence ---

const obligationsDir = "state/.obligations"

// obligationsPath returns the per-role durable obligations file path.
func obligationsPath(homeDir string, role Role) string {
	return filepath.Join(homeDir, obligationsDir, string(role)+".obligations")
}

// LoadObligations reads the persisted obligation state for the given role.
// Returns the persisted obligations, or the default set if no persisted state
// exists. An empty/zero-length file is treated as "all closed" — returns nil.
func LoadObligations(homeDir string, role Role) ([]Obligation, error) {
	p := obligationsPath(homeDir, role)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			// No persisted state — return the canonical default set.
			return Obligations(role), nil
		}
		return nil, fmt.Errorf("reading obligations for %s: %w", role, err)
	}
	defer f.Close()

	var obligations []Obligation
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: kind\tstate\tdetail\tcreated_at\tclosed_at
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		createdAt := parseInt(parts[3])
		var closedAt int64
		if len(parts) >= 5 {
			closedAt = parseInt(parts[4])
		}
		obligations = append(obligations, Obligation{
			Kind:      ObligationKind(parts[0]),
			State:     State(parts[1]),
			Detail:    parts[2],
			CreatedAt: createdAt,
			ClosedAt:  closedAt,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning obligations for %s: %w", role, err)
	}

	// Empty file — all obligations closed.
	if len(obligations) == 0 {
		return nil, nil
	}

	return obligations, nil
}

// SaveObligations persists the obligation state for the given role.
func SaveObligations(homeDir string, role Role, obligations []Obligation) error {
	p := obligationsPath(homeDir, role)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating obligations dir: %w", err)
	}

	var b strings.Builder
	for _, o := range obligations {
		b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%d\t%d\n", o.Kind, o.State, o.Detail, o.CreatedAt, o.ClosedAt))
	}

	return os.WriteFile(p, []byte(b.String()), 0644)
}

// CompleteObligation marks an obligation of the given kind as closed.
// It loads the current persisted state, finds the matching open obligation,
// marks it closed, and saves. Returns true if a matching open obligation was
// found and closed, false if none was found.
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

	if err := SaveObligations(homeDir, role, obligations); err != nil {
		return false, err
	}

	return true, nil
}

// ClearCompleted removes all closed obligations from the persisted state,
// leaving only open obligations. This aligns with teardown cleanup and
// prevents accumulation of stale closed records.
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

// ClearAll removes ALL persisted obligation state for the given role.
// Used during full teardown when the session/task is being destroyed.
func ClearAll(homeDir string, role Role) error {
	p := obligationsPath(homeDir, role)
	err := os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// MaterialStates returns true if the given state is a material state that
// warrants report-relay. This mirrors the materialStates set in the report CLI.
func MaterialStates(state string) bool {
	switch state {
	case "done", "failed", "needs-decision", "blocked":
		return true
	default:
		return false
	}
}

// MaterialReportExists returns true if the task has a material status line
// (done, failed, needs-decision, blocked) in its status file.
func MaterialReportExists(homeDir, taskID string) bool {
	statusPath := filepath.Join(homeDir, "state", taskID+".status")
	f, err := os.Open(statusPath)
	if err != nil {
		return false
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lastLine = line
		}
	}
	if lastLine == "" {
		return false
	}
	return MaterialStates(lineVerb(lastLine))
}

// lineVerb extracts the leading verb from a status line.
func lineVerb(line string) string {
	before, _, _ := strings.Cut(line, ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		before = strings.TrimSpace(before[:idx])
	}
	return strings.TrimSpace(before)
}

// parseInt parses an int64 from a string; returns 0 on failure.
func parseInt(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
