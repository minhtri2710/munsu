//go:build integration

package fleet

import (
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestDeliveryStateSingleVocabularyRoundTrip proves the consolidated canonical
// fleet.DeliveryState vocabulary (the only DeliveryState definition left after
// domain.DeliveryState was removed) still round-trips through the sole
// projection reader in internal/home/taskmeta.go. It writes the meta using the
// canonical fleet constants and asserts the home reader surfaces the terminal
// state, demonstrating the consolidation preserved the exact string contract
// the reader depends on (meta key "delivery_state" and value "merged").
func TestDeliveryStateSingleVocabularyRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	taskID := "t-roundtrip"

	if err := mhome.WriteMeta(tmp, taskID, map[string]string{
		"kind":           "ship",
		MetaDeliveryState: string(DeliveryStateMerged),
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	entries, err := mhome.ListMeta(tmp)
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListMeta returned %d entries, want 1: %v", len(entries), entries)
	}
	if entries[0].LastStatus != "merged" {
		t.Fatalf("ListMeta LastStatus = %q, want merged (canonical fleet.DeliveryStateMerged must match reader literal)", entries[0].LastStatus)
	}

	// And the key constant itself must equal the literal the reader keys off.
	if MetaDeliveryState != "delivery_state" {
		t.Fatalf("MetaDeliveryState = %q, want delivery_state (reader depends on this literal)", MetaDeliveryState)
	}
}
