package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// op builds a valid operation with a deterministic digest derived from taskID.
func op(id, taskID string) Operation {
	return Operation{ID: id, Digest: digestOf("op:" + id + ":" + taskID)}
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mustAggregate(t *testing.T, taskID string, generation uint64, phase string) Aggregate {
	t.Helper()
	agg, err := NewAggregate(taskID, "owner", "work", "ship", "", "")
	if err != nil {
		t.Fatal(err)
	}
	agg.Generation = Generation(generation)
	agg.Phase = Phase(phase)
	agg.Revision = FirstRevision
	if err := validateAggregate(agg); err != nil {
		t.Fatal(err)
	}
	return agg
}

func mustHold(t *testing.T, id, action, taskID, reason string) DispatchHold {
	t.Helper()
	hold := DispatchHold{
		SchemaVersion: TaskAuthoritySchema,
		ID:            id,
		Scope:         DispatchHoldScope{TaskIDs: []string{taskID}},
		Actions:       []DispatchAction{DispatchAction(action)},
		Reason:        reason,
		CreatedAt:     time.Now().UnixNano(),
	}
	if taskID == "" {
		hold.Scope = DispatchHoldScope{}
	}
	if err := validateHold(hold); err != nil {
		t.Fatal(err)
	}
	return hold
}
