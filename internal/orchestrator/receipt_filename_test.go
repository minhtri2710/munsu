package orchestrator

import (
	"testing"
)

// TestValidateTermKey pins the durable-safe slug contract for terminal keys:
// the key becomes a filename segment, so it must be a non-empty [A-Za-z0-9_-]
// slug. A dot would make the <stem>.<termKey> encoding ambiguous; a tab or
// newline could forge a field or a whole record.
func TestValidateTermKey(t *testing.T) {
	accept := []string{"default", "terminal-1", "k", "A_b-9", "0"}
	reject := []string{"", "2.k", "a b", "a\tb", "a\nb", "v1.2", "a/b", "a%b"}
	for _, k := range accept {
		if err := ValidateTermKey(k); err != nil {
			t.Errorf("ValidateTermKey(%q) = %v, want nil", k, err)
		}
	}
	for _, k := range reject {
		if err := ValidateTermKey(k); err == nil {
			t.Errorf("ValidateTermKey(%q) = nil, want error", k)
		}
	}
}

// TestListAllReceipts_DottedTaskID is the core oracle. A task ID that contains
// a dot (legal on Unix, where DurableKey is the identity) must round-trip
// through the receipt filename without the reader misattributing the
// stem/termKey boundary. The receipt for (v1.2, k) must read back as exactly
// (v1.2, k), not (v1, 2.k). This fails on a first-dot split and passes on a
// last-dot split.
func TestListAllReceipts_DottedTaskID(t *testing.T) {
	captainHome := t.TempDir()
	if err := WriteReceipt(captainHome, "v1.2", "k", "done", "complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	got, err := listAllReceipts(captainHome)
	if err != nil {
		t.Fatalf("listAllReceipts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listAllReceipts returned %d receipts, want 1: %+v", len(got), got)
	}
	if got[0].TaskID != "v1.2" || got[0].TermKey != "k" {
		t.Errorf("listAllReceipts = (%q, %q), want (v1.2, k)", got[0].TaskID, got[0].TermKey)
	}
}

// TestListPendingReceipts_DottedTaskID exercises the second parser through the
// same shared helper.
func TestListPendingReceipts_DottedTaskID(t *testing.T) {
	captainHome := t.TempDir()
	if err := WriteReceipt(captainHome, "v1.2", "k", "failed", "boom"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	got, err := ListPendingReceipts(captainHome)
	if err != nil {
		t.Fatalf("ListPendingReceipts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListPendingReceipts returned %d receipts, want 1: %+v", len(got), got)
	}
	if got[0].TaskID != "v1.2" || got[0].TermKey != "k" {
		t.Errorf("ListPendingReceipts = (%q, %q), want (v1.2, k)", got[0].TaskID, got[0].TermKey)
	}
}
