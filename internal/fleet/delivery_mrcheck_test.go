//go:build integration

package fleet

import (
	"errors"
	"testing"
)

// TestMergeStatus_MissingIdentityIsUnverifiable pins the retained read-only
// provider-neutral status seam: a task without a delivery identity is
// classified unverifiable (exit 2), never merged.
func TestMergeStatus_MissingIdentityIsUnverifiable(t *testing.T) {
	err := MergeStatus(t.TempDir(), "missing-task")
	var statusErr *MergeStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("MergeStatus error = %T %v, want *MergeStatusError", err, err)
	}
	if !statusErr.Unverifiable {
		t.Fatalf("MergeStatus error = %v, want unverifiable classification", err)
	}
}
