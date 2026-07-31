package fleet

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestStartTaskRespectsDispatchHoldWithoutChangingQueuedState(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", "project"); err != nil {
		t.Fatal(err)
	}
	if _, err := home.CreateDispatchHold(homeDir, home.DispatchHoldInput{ID: "start-pause", Scope: home.DispatchHoldScope{TaskIDs: []string{"task"}}, Actions: []home.DispatchAction{home.DispatchActionStart}, Reason: "pause"}); err != nil {
		t.Fatal(err)
	}
	if _, err := home.StartTask(homeDir, "task"); !errors.Is(err, home.ErrDispatchHeld) {
		t.Fatalf("start err = %v, want dispatch hold", err)
	}
	current, ok, err := home.ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil || !ok || current.State != "queued" {
		t.Fatalf("current = %+v ok=%v err=%v, want queued", current, ok, err)
	}
}
