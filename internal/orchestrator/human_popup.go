package orchestrator

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// humanNeededEvent is one Human-needed status line the watcher has just
// surfaced as a wake.
type humanNeededEvent struct {
	TaskID string
	Line   string
}

// popupHumanNeeded shows a best-effort popup for each Human-needed event on
// the backend bound to its task. A backend without backend.HumanNotifier, or
// a task with no resolvable backend, is skipped without error. Each popup runs
// concurrently under the backend's command timeout; its outcome is logged and
// audited in the event log as popup.sent or popup.failed and is never
// retried. The popup names the verb and task only: the gate itself stays in
// the ledger and mailbox, and nothing here answers it.
func popupHumanNeeded(homeDir string, events []humanNeededEvent) {
	var wg sync.WaitGroup
	for _, ev := range events {
		meta, err := home.ReadMeta(homeDir, ev.TaskID)
		if err != nil {
			continue
		}
		bk, _, err := backend.BackendForTask(homeDir, meta)
		if err != nil {
			continue
		}
		notifier, ok := bk.(backend.HumanNotifier)
		if !ok {
			continue
		}
		verb := domain.LineVerb(ev.Line)
		wg.Add(1)
		go func() {
			defer wg.Done()
			eventType, detail := "popup.sent", verb
			if err := notifier.Notify("munsu: Human needed", fmt.Sprintf("%s on %s", verb, ev.TaskID)); err != nil {
				eventType, detail = "popup.failed", verb+": "+strings.Join(strings.Fields(err.Error()), " ")
				fmt.Fprintf(os.Stderr, "warning: popup for %s: %v\n", ev.TaskID, err)
			}
			if _, err := Append(homeDir, eventType, ev.TaskID, verb, detail); err != nil {
				fmt.Fprintf(os.Stderr, "warning: popup audit for %s: %v\n", ev.TaskID, err)
			}
		}()
	}
	wg.Wait()
}
