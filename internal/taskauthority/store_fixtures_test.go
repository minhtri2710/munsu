package taskauthority

import (
	"testing"
	"time"
)

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
