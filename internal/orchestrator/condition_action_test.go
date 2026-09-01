package orchestrator

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// conditionActionProbe is one registration under test: a condition whose
// settled-ness the test drives, and an action that counts its runs.
type conditionActionProbe struct {
	settled     bool
	result      string
	observed    int
	actionCalls int
	actionErr   error
}

func (p *conditionActionProbe) registration() ConditionAction {
	return ConditionAction{
		Condition: func(context.Context) (bool, []byte, error) {
			p.observed++
			if !p.settled {
				return false, nil, nil
			}
			return true, []byte(p.result), nil
		},
		Action: func(string, []byte) error {
			p.actionCalls++
			return p.actionErr
		},
	}
}

// registerProbe registers a probe under name and removes it from the process
// registry when the test ends.
func registerProbe(t *testing.T, homeDir, name string, p *conditionActionProbe) string {
	t.Helper()
	if err := RegisterConditionAction(homeDir, name, p.registration()); err != nil {
		t.Fatalf("RegisterConditionAction: %v", err)
	}
	eventID := conditionActionEventID(name)
	t.Cleanup(func() {
		conditionActionRegistrations.Delete(conditionActionKey{homeDir, eventID})
	})
	return eventID
}

// tick runs one watcher-cadence evaluation, the same call the cycle makes.
func tick(t *testing.T, homeDir, eventID string, p *conditionActionProbe) error {
	t.Helper()
	return evaluateConditionAction(context.Background(), homeDir, eventID, p.registration(), home.RenameDurable)
}

// dispatch drains this home's process-event wakes through the prefix router.
func dispatch(t *testing.T, homeDir string) {
	t.Helper()
	if _, err := consumeProcessEventWakes(homeDir, &testRetirementPort{}); err != nil {
		t.Fatalf("consumeProcessEventWakes: %v", err)
	}
}

func markerExists(t *testing.T, homeDir, eventID string) bool {
	t.Helper()
	fired, err := hasFiredMarker(homeDir, eventID)
	if err != nil {
		t.Fatalf("hasFiredMarker: %v", err)
	}
	return fired
}

// failingRename is the crash seam for a rename that never lands, so the caller
// leaves exactly the state a process that died before the rename would.
func failingRename(string, string) error { return errors.New("simulated crash") }

// TestConditionAction_FiresOnceOnFirstStableTrue proves the whole delivery
// path: the settled edge captures and announces one wake, the dispatcher runs
// the action, and every later tick is a no-op that spends nothing.
func TestConditionAction_FiresOnceOnFirstStableTrue(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true, result: "ready"}
	eventID := registerProbe(t, homeDir, "gate", p)

	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if p.actionCalls != 0 {
		t.Fatalf("action ran inline in the resolver: %d calls", p.actionCalls)
	}
	dispatch(t, homeDir)
	if p.actionCalls != 1 {
		t.Fatalf("action calls after first dispatch = %d, want 1", p.actionCalls)
	}
	if !markerExists(t, homeDir, eventID) {
		t.Fatal("no fired marker after the action ran")
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.AckedGeneration != rec.Generation {
		t.Fatalf("generation %d not acked (acked %d)", rec.Generation, rec.AckedGeneration)
	}

	// A second true tick must spend no wake and run nothing.
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("second true tick spent %d wakes, want 0", len(wakes))
	}
	if p.actionCalls != 1 {
		t.Fatalf("action calls after second tick = %d, want 1", p.actionCalls)
	}
}

// TestConditionAction_TransientTrueDoesNotFireEarly proves the single
// confirmed edge: while the condition reports unsettled, nothing fires, no
// marker is written and no wake is spent -- and the C1 refusal says so.
func TestConditionAction_TransientTrueDoesNotFireEarly(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{result: "ready"}
	eventID := registerProbe(t, homeDir, "gate", p)

	// true, then false again: the resolver owns its own debounce and reports
	// unsettled for both, so this layer must not fire on the flap.
	for i, settled := range []bool{false, false} {
		p.settled = settled
		err := tick(t, homeDir, eventID, p)
		if !errors.Is(err, errConditionActionNotStable) {
			t.Fatalf("tick %d: err = %v, want errConditionActionNotStable", i, err)
		}
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("unsettled condition spent %d wakes, want 0", len(wakes))
	}
	if markerExists(t, homeDir, eventID) {
		t.Fatal("unsettled condition wrote a fired marker")
	}
	if p.actionCalls != 0 {
		t.Fatalf("unsettled condition ran the action %d times", p.actionCalls)
	}

	// Settled at last: now it fires.
	p.settled = true
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("settled tick: %v", err)
	}
	dispatch(t, homeDir)
	if p.actionCalls != 1 {
		t.Fatalf("action calls after settling = %d, want 1", p.actionCalls)
	}
}

// TestConditionAction_RestartAfterMarkerDoesNotRefire builds the crashed
// intermediate a process leaves when it dies after writing the marker but
// before acking, then drives restart recovery over it: the redelivered wake
// must be acked and the action must not run again.
func TestConditionAction_RestartAfterMarkerDoesNotRefire(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true, result: "ready"}
	eventID := registerProbe(t, homeDir, "gate", p)
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// Claim the wake the way the dispatcher does, then crash right after the
	// marker rename lands and before the ack.
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 {
		t.Fatalf("announced %d wakes, want 1", len(wakes))
	}
	if err := fireConditionAction(homeDir, eventID, p.registration(), []byte("ready"), crashAfterRename(1)); err == nil {
		t.Fatal("injected crash did not surface")
	}
	if !markerExists(t, homeDir, eventID) {
		t.Fatal("crashed intermediate has no marker")
	}
	if rec := mustReadProcessEvent(t, homeDir, eventID); rec.AckedGeneration == rec.Generation {
		t.Fatal("crashed intermediate is already acked")
	}
	if p.actionCalls != 1 {
		t.Fatalf("action calls before restart = %d, want 1", p.actionCalls)
	}

	// Restart: the registry is rebuilt by re-registering, then recovery
	// re-announces the captured-but-unacked event.
	if err := RegisterConditionAction(homeDir, "gate", p.registration()); err != nil {
		t.Fatalf("re-register after restart: %v", err)
	}
	reannounced, err := RecoverProcessEvents(homeDir)
	if err != nil {
		t.Fatalf("RecoverProcessEvents: %v", err)
	}
	if reannounced != 1 {
		t.Fatalf("recovery re-announced %d, want 1", reannounced)
	}
	dispatch(t, homeDir)

	if p.actionCalls != 1 {
		t.Fatalf("action ran again after restart: %d calls, want 1", p.actionCalls)
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.AckedGeneration != rec.Generation {
		t.Fatalf("redelivered wake left unacked: generation %d, acked %d", rec.Generation, rec.AckedGeneration)
	}
}

// TestConditionAction_RestartBeforeMarkerRefires builds the other crashed
// intermediate -- action ran, marker rename never landed -- and drives
// re-entry: at-least-once means the action runs a second time, and the
// marker is what stops the third.
func TestConditionAction_RestartBeforeMarkerRefires(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true, result: "ready"}
	eventID := registerProbe(t, homeDir, "gate", p)
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 1 {
		t.Fatalf("announced %d wakes, want 1", len(wakes))
	}
	if err := fireConditionAction(homeDir, eventID, p.registration(), []byte("ready"), failingRename); err == nil {
		t.Fatal("injected crash did not surface")
	}
	if markerExists(t, homeDir, eventID) {
		t.Fatal("crashed intermediate must have no marker")
	}
	if p.actionCalls != 1 {
		t.Fatalf("action calls before restart = %d, want 1", p.actionCalls)
	}

	if err := RegisterConditionAction(homeDir, "gate", p.registration()); err != nil {
		t.Fatalf("re-register after restart: %v", err)
	}
	if _, err := RecoverProcessEvents(homeDir); err != nil {
		t.Fatalf("RecoverProcessEvents: %v", err)
	}
	dispatch(t, homeDir)

	if p.actionCalls != 2 {
		t.Fatalf("action calls after re-entry = %d, want 2 (at-least-once)", p.actionCalls)
	}
	if !markerExists(t, homeDir, eventID) {
		t.Fatal("re-entry did not record the fired marker")
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.AckedGeneration != rec.Generation {
		t.Fatalf("re-entry left the wake unacked: generation %d, acked %d", rec.Generation, rec.AckedGeneration)
	}
}

// TestConditionAction_ActionFailureRetriesNextCycle proves a transient action
// failure does not cost the run until the next process start: the drained wake
// is re-queued, the event stays unacked, and the next cycle re-delivers it and
// fires once.
func TestConditionAction_ActionFailureRetriesNextCycle(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true, result: "ready", actionErr: errors.New("transient action failure")}
	eventID := registerProbe(t, homeDir, "gate", p)
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("tick: %v", err)
	}

	dispatch(t, homeDir)
	if p.actionCalls != 1 {
		t.Fatalf("action calls after the failing dispatch = %d, want 1", p.actionCalls)
	}
	if markerExists(t, homeDir, eventID) {
		t.Fatal("a failed action wrote a fired marker")
	}
	if rec := mustReadProcessEvent(t, homeDir, eventID); rec.AckedGeneration == rec.Generation {
		t.Fatal("a failed action acked the event")
	}
	queued := 0
	for _, wake := range mustReadWakeQueue(t, homeDir) {
		if wake.Kind == ProcessEventWakeKind && wake.Key == eventID {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("queued wakes for %q after the failure = %d, want 1 (retried next cycle)", eventID, queued)
	}

	// Next cycle, with the transient failure gone: the same wake runs the
	// action to completion without any restart.
	p.actionErr = nil
	dispatch(t, homeDir)
	if p.actionCalls != 2 {
		t.Fatalf("action calls after the retry = %d, want 2", p.actionCalls)
	}
	if !markerExists(t, homeDir, eventID) {
		t.Fatal("the retry did not record the fired marker")
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.AckedGeneration != rec.Generation {
		t.Fatalf("the retry left the wake unacked: generation %d, acked %d", rec.Generation, rec.AckedGeneration)
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("a succeeding retry re-queued %d wakes, want 0", len(wakes))
	}
}

// TestConditionAction_ClearReArmsAFreshFire proves re-arm is explicit and
// complete: clearing removes both durable halves, and only then does a fresh
// registration fire again.
func TestConditionAction_ClearReArmsAFreshFire(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true, result: "ready"}
	eventID := registerProbe(t, homeDir, "gate", p)
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("tick: %v", err)
	}
	dispatch(t, homeDir)
	if p.actionCalls != 1 {
		t.Fatalf("action calls = %d, want 1", p.actionCalls)
	}

	// Re-registering without a clear must not re-arm anything.
	if err := RegisterConditionAction(homeDir, "gate", p.registration()); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("tick after re-register: %v", err)
	}
	dispatch(t, homeDir)
	if p.actionCalls != 1 {
		t.Fatalf("re-registration re-fired: %d calls, want 1", p.actionCalls)
	}

	if err := ClearConditionAction(homeDir, "gate"); err != nil {
		t.Fatalf("ClearConditionAction: %v", err)
	}
	if markerExists(t, homeDir, eventID) {
		t.Fatal("clear left the fired marker behind")
	}
	if rec, err := readProcessEventRecord(homeDir, eventID); err != nil || rec != nil {
		t.Fatalf("clear left the process-event record behind: %v, %v", rec, err)
	}

	eventID = registerProbe(t, homeDir, "gate", p)
	if err := tick(t, homeDir, eventID, p); err != nil {
		t.Fatalf("tick after clear: %v", err)
	}
	dispatch(t, homeDir)
	if p.actionCalls != 2 {
		t.Fatalf("action calls after clear and re-register = %d, want 2", p.actionCalls)
	}
}

// TestConditionAction_FireRefusesWhenMarkerPresent enters the already-fired
// refusal directly: the durable marker is the stop, whatever the caller asks.
func TestConditionAction_FireRefusesWhenMarkerPresent(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true, result: "ready"}
	eventID := conditionActionEventID("gate")
	if err := writeFiredMarker(homeDir, eventID, home.RenameDurable); err != nil {
		t.Fatalf("writeFiredMarker: %v", err)
	}
	err := fireConditionAction(homeDir, eventID, p.registration(), nil, home.RenameDurable)
	if !errors.Is(err, errConditionActionAlreadyFired) {
		t.Fatalf("err = %v, want errConditionActionAlreadyFired", err)
	}
	if p.actionCalls != 0 {
		t.Fatalf("refused fire still ran the action %d times", p.actionCalls)
	}
}

// TestConditionAction_RegisterRefusesInvalidRegistration enters both
// registration refusals: an id the wake queue cannot carry, and a half of the
// pair that is missing.
func TestConditionAction_RegisterRefusesInvalidRegistration(t *testing.T) {
	homeDir := t.TempDir()
	p := &conditionActionProbe{settled: true}

	for _, name := range []string{"", "two\nlines", "tab\tname"} {
		if err := RegisterConditionAction(homeDir, name, p.registration()); err == nil {
			t.Fatalf("RegisterConditionAction accepted name %q", name)
		}
	}
	for _, ca := range []ConditionAction{
		{Action: p.registration().Action},
		{Condition: p.registration().Condition},
		{},
	} {
		if err := RegisterConditionAction(homeDir, "gate", ca); err == nil {
			t.Fatal("RegisterConditionAction accepted an incomplete pair")
		}
	}
	if rec, err := readProcessEventRecord(homeDir, conditionActionEventID("gate")); err != nil || rec != nil {
		t.Fatalf("a refused registration armed a process event: %v, %v", rec, err)
	}
}

// TestConditionAction_DispatcherRoutesByPrefix proves the generalized
// dispatcher: a condition-action wake and a merged-poll wake drained together
// each reach their own owner, and a wake whose prefix nothing owns is dropped
// loudly and left unacked for restart recovery rather than re-queued.
func TestConditionAction_DispatcherRoutesByPrefix(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	retirement := &testRetirementPort{}

	pollEventID := mergedPollEventID("task-1")
	if _, err := RegisterProcessEvent(homeDir, pollEventID, "task-1"); err != nil {
		t.Fatalf("RegisterProcessEvent merged-poll: %v", err)
	}
	if err := EvaluateProcessEvent(context.Background(), homeDir, pollEventID, mergedPollResolver(retirement, homeDir, "task-1")); err != nil {
		t.Fatalf("EvaluateProcessEvent merged-poll: %v", err)
	}

	p := &conditionActionProbe{settled: true, result: "ready"}
	condEventID := registerProbe(t, homeDir, "gate", p)
	if err := tick(t, homeDir, condEventID, p); err != nil {
		t.Fatalf("tick: %v", err)
	}

	orphanEventID := "no-owner:gate"
	if _, err := RegisterProcessEvent(homeDir, orphanEventID, "gate"); err != nil {
		t.Fatalf("RegisterProcessEvent orphan: %v", err)
	}
	orphanCalls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, orphanEventID, resolvedWith("x", &orphanCalls)); err != nil {
		t.Fatalf("EvaluateProcessEvent orphan: %v", err)
	}

	var outcomes map[string]error
	stderr := captureStderr(t, func() {
		var err error
		outcomes, err = consumeProcessEventWakes(homeDir, retirement)
		if err != nil {
			t.Errorf("consumeProcessEventWakes: %v", err)
		}
	})

	if outcome, acted := outcomes["task-1"]; !acted || outcome != nil {
		t.Fatalf("merged-poll outcome = %v (acted %v), want nil", outcome, acted)
	}
	if len(retirement.retiredPaths) != 1 || retirement.retiredPaths[0] != checkPath {
		t.Fatalf("merged-poll retirement paths = %v, want [%s]", retirement.retiredPaths, checkPath)
	}
	if p.actionCalls != 1 {
		t.Fatalf("condition-action calls = %d, want 1", p.actionCalls)
	}
	if !strings.Contains(stderr, "no owner for this event prefix") {
		t.Fatalf("unowned prefix was not reported: %q", stderr)
	}
	if rec := mustReadProcessEvent(t, homeDir, orphanEventID); rec.AckedGeneration == rec.Generation {
		t.Fatal("unowned prefix wake was acked")
	}
	if wakes := drainProcessEventWakes(t, homeDir); len(wakes) != 0 {
		t.Fatalf("dispatcher re-queued %d wakes, want 0", len(wakes))
	}
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatalf("merged poll artifact still present: %v", err)
	}
}

// TestConditionAction_WatcherCycleEvaluatesAndFires proves the wiring the
// consumer relies on: registering is the only step a rule takes, and the
// watcher's own cadence then observes the condition, announces it and runs
// the action on the next cycle -- no agent turn per re-check.
func TestConditionAction_WatcherCycleEvaluatesAndFires(t *testing.T) {
	homeDir := setupRunCycleTest(t)
	retirement := &testRetirementPort{}
	p := &conditionActionProbe{result: "ready"}
	eventID := registerProbe(t, homeDir, "gate", p)

	// Unsettled: the cycle observes and spends nothing.
	runMergedPollCycle(t, homeDir, retirement)
	if p.observed == 0 {
		t.Fatal("the cycle never evaluated the registered condition")
	}
	if p.actionCalls != 0 {
		t.Fatalf("unsettled condition fired: %d calls", p.actionCalls)
	}

	// Settled: this cycle announces, the next one runs the action.
	p.settled = true
	runMergedPollCycle(t, homeDir, retirement)
	if p.actionCalls != 0 {
		t.Fatalf("action ran in the announcing cycle: %d calls", p.actionCalls)
	}
	runMergedPollCycle(t, homeDir, retirement)
	if p.actionCalls != 1 {
		t.Fatalf("action calls after the consuming cycle = %d, want 1", p.actionCalls)
	}
	if !markerExists(t, homeDir, eventID) {
		t.Fatal("no fired marker after the cycle ran the action")
	}

	// Every later cycle is a no-op.
	runMergedPollCycle(t, homeDir, retirement)
	if p.actionCalls != 1 {
		t.Fatalf("a later cycle re-fired: %d calls, want 1", p.actionCalls)
	}
}
