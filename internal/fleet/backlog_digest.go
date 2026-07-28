package fleet

import (
	"fmt"
	"strings"
)

// DigestItem is a metadata-only view of a backlog item for the session-start
// digest. Task descriptions/bodies are explicitly excluded.
type DigestItem struct {
	ID    string    `json:"id"`
	State TaskState `json:"state"`
	Kind  string    `json:"kind,omitempty"`
}

// BacklogDigest is a bounded, metadata-only summary of the backlog for
// session-start. It never includes task descriptions/bodies.
type BacklogDigest struct {
	Total       int          `json:"total"`
	Queued      int          `json:"queued"`
	QueuedIDs   []string     `json:"queued_ids,omitempty"`
	InFlight    int          `json:"in_flight"`
	InFlightIDs []string     `json:"in_flight_ids,omitempty"`
	Blocked     int          `json:"blocked"`
	BlockedIDs  []string     `json:"blocked_ids,omitempty"`
	Done        int          `json:"done"`
	Items       []DigestItem `json:"items,omitempty"`
}

// BuildDigest reads the backlog via the selected backend and returns
// a bounded, metadata-only digest. It is fail-open: on any error it returns
// a zero digest (all counts 0) instead of blocking.
func BuildDigest(homeDir string) *BacklogDigest {
	items, err := ListItems(homeDir, StateQueued) // zero filter = all
	if err != nil || len(items) == 0 {
		return &BacklogDigest{}
	}

	d := &BacklogDigest{
		Total: len(items),
	}

	// Use a bounded max to prevent unbounded output.
	max := len(items)
	if max > 80 {
		max = 80
	}

	for _, item := range items {
		switch item.State {
		case StateQueued:
			d.Queued++
			if len(d.QueuedIDs) < max {
				d.QueuedIDs = append(d.QueuedIDs, item.ID)
			}
		case StateInFlight:
			d.InFlight++
			if len(d.InFlightIDs) < max {
				d.InFlightIDs = append(d.InFlightIDs, item.ID)
			}
		case StateBlocked:
			d.Blocked++
			if len(d.BlockedIDs) < max {
				d.BlockedIDs = append(d.BlockedIDs, item.ID)
			}
		case StateDone:
			d.Done++
		}
	}

	// Populate a bounded Items slice for rendering (metadata only — no descriptions).
	for i := 0; i < max && i < len(items); i++ {
		d.Items = append(d.Items, DigestItem{
			ID:    items[i].ID,
			State: items[i].State,
			Kind:  items[i].Kind,
		})
	}

	return d
}

// String renders the digest as a compact, human-readable block for
// session-start output. It never includes task bodies/descriptions.
func (d *BacklogDigest) String() string {
	if d == nil || d.Total == 0 {
		return "  (backlog empty or absent)"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "  total: %d items | queued: %d | in-flight: %d | blocked: %d | done: %d\n",
		d.Total, d.Queued, d.InFlight, d.Blocked, d.Done)

	if d.InFlight > 0 {
		fmt.Fprintf(&b, "  in-flight: %s\n", strings.Join(d.InFlightIDs, ", "))
	}
	if d.Blocked > 0 {
		fmt.Fprintf(&b, "  blocked: %s\n", strings.Join(d.BlockedIDs, ", "))
	}
	if d.Queued > 0 {
		fmt.Fprintf(&b, "  queued: %s\n", strings.Join(d.QueuedIDs, ", "))
	}

	return strings.TrimRight(b.String(), "\n")
}

// HasUnfinished returns true when the digest contains any queued, in-flight,
// or blocked items — useful for the session-start header.
func (d *BacklogDigest) HasUnfinished() bool {
	if d == nil {
		return false
	}
	return d.Queued > 0 || d.InFlight > 0 || d.Blocked > 0
}
