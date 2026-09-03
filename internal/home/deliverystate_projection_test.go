package home

import (
	"testing"
)

// TestDeliveryStateProjectionReaderHonorsMerged verifies the sole projection
// reader (ListMeta in taskmeta.go) still interprets delivery_state=merged as
// the authoritative LastStatus after the domain.DeliveryState type was removed
// and consolidated onto the canonical fleet.DeliveryState. The reader keys off
// the literal "delivery_state" meta field and the literal values "merged" /
// "delivered", so the vocabulary cleanup must not alter this behavior.
func TestDeliveryStateProjectionReaderHonorsMerged(t *testing.T) {
	tmp := t.TempDir()

	if err := WriteMeta(tmp, "t-merged", map[string]string{
		"kind":          "ship",
		"delivery_state": "merged",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := ListMeta(tmp)
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListMeta returned %d entries, want 1: %v", len(got), got)
	}
	if got[0].LastStatus != "merged" {
		t.Fatalf("ListMeta LastStatus = %q, want merged (projection reader must surface delivery_state)", got[0].LastStatus)
	}
}

// TestDeliveryStateProjectionReaderHonorsDelivered verifies the second
// lifecycle value the reader hard-codes, "delivered", also supersedes any
// stale status projection after consolidation.
func TestDeliveryStateProjectionReaderHonorsDelivered(t *testing.T) {
	tmp := t.TempDir()

	// A stale status line that the delivery projection must override.
	if err := AppendStatus(tmp, "t-delivered", "working: still in flight"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := WriteMeta(tmp, "t-delivered", map[string]string{
		"kind":          "ship",
		"delivery_state": "delivered",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := ListMeta(tmp)
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListMeta returned %d entries, want 1: %v", len(got), got)
	}
	if got[0].LastStatus != "delivered" {
		t.Fatalf("ListMeta LastStatus = %q, want delivered (projection must supersede stale status)", got[0].LastStatus)
	}
}

// TestDeliveryStateProjectionReaderIgnoresNonTerminalStates verifies the reader
// only honors the terminal merged/delivered values: a non-terminal delivery_state
// such as review-ready must NOT override a real status projection.
func TestDeliveryStateProjectionReaderIgnoresNonTerminalStates(t *testing.T) {
	tmp := t.TempDir()

	if err := AppendStatus(tmp, "t-review", "working: awaiting review"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := WriteMeta(tmp, "t-review", map[string]string{
		"kind":          "ship",
		"delivery_state": "review-ready",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := ListMeta(tmp)
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListMeta returned %d entries, want 1: %v", len(got), got)
	}
	if got[0].LastStatus != "working: awaiting review" {
		t.Fatalf("ListMeta LastStatus = %q, want status projection (review-ready must not override)", got[0].LastStatus)
	}
}
