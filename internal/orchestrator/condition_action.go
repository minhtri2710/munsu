package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// Condition-action registration (ADR-0021 Decision 2, C1).
//
// The ergonomic "when X becomes true, do Y once" surface over the P1
// process-event source. A registration pairs a condition (a
// ProcessEventResolver) with an action; delivery goes through
// process-event-source and nothing else, so the durable order is P1's:
// capture the condition's observation, then spend exactly one wake, then run
// the action when that wake is consumed, then ack.
//
// Three rules decide fire-once, and none of them is held in memory:
//
//   - Stability is a single confirmed edge. The condition reports resolved
//     only when it deems itself settled, and this layer trusts that edge --
//     capture-before-wake is the confirmation. There is no N-consecutive
//     counter here; a flapping condition owns its own debounce inside its
//     resolver.
//   - The fired-marker is the stop. It is a file of its own, sibling to but
//     distinct from the P1 record, written by atomic rename AFTER the action
//     returns. P1's record stays domain-neutral: it carries no fired bit.
//   - Re-arm is explicit. The marker outlives firing, so re-registering never
//     re-fires; ClearConditionAction removes the marker and the P1 record so a
//     fresh registration arms a fresh fire.
//
// Marker-after-action inherits P1's at-least-once window: a crash between the
// action returning and the marker landing re-announces at restart and runs the
// action a second time. Actions registered here must be idempotent, exactly as
// the merged-poll action is.
//
// C1 is additive: nothing in the binary registers a condition-action yet, so
// RegisterConditionAction and ClearConditionAction carry deadcode waivers
// naming the consumer that will call them. The watcher already evaluates and
// dispatches whatever is registered, so no consumer needs to touch this file.

// conditionActionEventPrefix keys the process event of a registration; the
// event payload is the registration name. It is deliberately distinct from
// mergedPollEventPrefix: the wake dispatcher routes on this prefix.
const conditionActionEventPrefix = "cond-action:"

// conditionActionDir is the private state directory holding one fired-marker
// per fired registration, relative to home.StateDir.
const conditionActionDir = ".condition-actions"

// conditionActionSchema versions the fired-marker filename, so a marker this
// tree did not write is never mistaken for one it did.
const conditionActionSchema = 1

// errConditionActionAlreadyFired refuses a second firing of a registration
// whose durable marker is already on disk. The dispatcher treats it as the
// no-op it is and acks the redelivered wake.
var errConditionActionAlreadyFired = errors.New("condition-action has already fired: clear it to re-arm")

// errConditionActionNotStable reports that one evaluation observed no settled
// condition. Nothing fired and no marker was written; the next cycle looks
// again.
var errConditionActionNotStable = errors.New("condition-action condition is not stably true: not fired")

// ConditionAction pairs the condition to wait on with the action to run once
// it is stably true. Action receives the result the condition captured.
type ConditionAction struct {
	Condition ProcessEventResolver
	Action    func(homeDir string, result []byte) error
}

// conditionActionKey identifies a registration; registrations are per home.
type conditionActionKey struct {
	homeDir string
	eventID string
}

// conditionActionRegistrations holds this process's live registrations. The
// action is a closure and cannot be persisted, so the registry is re-built by
// whoever registers at each process start; the durable half -- the P1 record
// and the fired-marker -- is what decides whether an action still owes a run.
var conditionActionRegistrations sync.Map // conditionActionKey -> ConditionAction

func conditionActionEventID(name string) string { return conditionActionEventPrefix + name }

func conditionActionDirPath(homeDir string) string {
	return filepath.Join(home.StateDir(homeDir), conditionActionDir)
}

// firedMarkerPath returns the marker path for an event id, hashed the way
// processEventRecordPath hashes it so any opaque name yields a flat filename.
func firedMarkerPath(homeDir, eventID string) string {
	digest := sha256.Sum256([]byte(eventID))
	return filepath.Join(conditionActionDirPath(homeDir), fmt.Sprintf("v%d-%s.fired", conditionActionSchema, hex.EncodeToString(digest[:])))
}

// hasFiredMarker reports whether this registration has already fired.
// Presence is the whole fact: the file's contents are for an operator reading
// the state directory, never for a decision made here.
func hasFiredMarker(homeDir, eventID string) (bool, error) {
	_, err := os.Stat(firedMarkerPath(homeDir, eventID))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("reading condition-action fired marker: %w", err)
}

// writeFiredMarker atomically records that a registration has fired: temp
// file, fsync, close, durable rename -- the writeProcessEventRecord pattern.
// renameFn is the crash-injection seam; production callers pass
// home.RenameDurable.
func writeFiredMarker(homeDir, eventID string, renameFn func(string, string) error) error {
	dir := conditionActionDirPath(homeDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating condition-action dir: %w", err)
	}
	path := firedMarkerPath(homeDir, eventID)
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("writing condition-action fired temp: %w", err)
	}
	if _, err := f.Write([]byte(time.Now().UTC().Format(time.RFC3339) + "\n")); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing condition-action fired temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsync condition-action fired temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing condition-action fired temp: %w", err)
	}
	if err := renameFn(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming condition-action fired marker: %w", err)
	}
	return nil
}

// RegisterConditionAction registers, or re-establishes, a condition-action for
// this home. A registration whose marker is already on disk is stored so a
// wake still in flight from before a restart finds its owner, but it arms no
// new process-event generation: it stays inert until ClearConditionAction.
func RegisterConditionAction(homeDir, name string, ca ConditionAction) error {
	if name == "" || strings.ContainsAny(name, "\t\r\n") {
		return fmt.Errorf("condition-action name %q must be a non-empty single line", name)
	}
	if ca.Condition == nil || ca.Action == nil {
		return fmt.Errorf("condition-action %q requires both a condition and an action", name)
	}
	eventID := conditionActionEventID(name)
	fired, err := hasFiredMarker(homeDir, eventID)
	if err != nil {
		return err
	}
	if !fired {
		// Arm a generation only when there is nothing in flight: an
		// unacked record is a capture this registration still owes an
		// action, and bumping its generation would discard it.
		rec, err := readProcessEventRecord(homeDir, eventID)
		if err != nil {
			return err
		}
		if rec == nil || rec.Generation == rec.AckedGeneration {
			if _, err := RegisterProcessEvent(homeDir, eventID, name); err != nil {
				return err
			}
		}
	}
	conditionActionRegistrations.Store(conditionActionKey{homeDir, eventID}, ca)
	return nil
}

// ClearConditionAction is the explicit re-arm: it removes the fired-marker and
// the P1 record, so the next registration observes the condition again and
// fires again. Nothing clears a marker implicitly -- a condition falling false
// does not re-arm its action.
func ClearConditionAction(homeDir, name string) error {
	eventID := conditionActionEventID(name)
	// Marker first. A crash between the two removals must leave the weaker
	// state (armed, not fired), never a marker whose record is gone.
	if err := os.Remove(firedMarkerPath(homeDir, eventID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing condition-action fired marker: %w", err)
	}
	if err := os.Remove(processEventRecordPath(homeDir, eventID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing condition-action process-event record: %w", err)
	}
	conditionActionRegistrations.Delete(conditionActionKey{homeDir, eventID})
	return nil
}

// evaluateConditionAction runs one poll cycle for a registration. A fired
// registration is inert. Otherwise P1 observes the condition: an unsettled
// condition spends nothing and is reported as errConditionActionNotStable --
// the C1 decision not to fire and not to write a marker -- while a settled one
// has been captured durably and announced as the single wake that will run the
// action.
func evaluateConditionAction(ctx context.Context, homeDir, eventID string, ca ConditionAction, renameFn func(string, string) error) error {
	fired, err := hasFiredMarker(homeDir, eventID)
	if err != nil {
		return err
	}
	if fired {
		return nil
	}
	if err := evaluateProcessEvent(ctx, homeDir, eventID, ca.Condition, renameFn); err != nil {
		return err
	}
	rec, err := readProcessEventRecord(homeDir, eventID)
	if err != nil {
		return err
	}
	if rec == nil || !rec.Resolved {
		return errConditionActionNotStable
	}
	return nil
}

// evaluateConditionActions evaluates every live registration for this home on
// the watcher's existing interval, spending no agent turn per re-check. An
// unsettled condition is the normal case and is not logged.
func evaluateConditionActions(ctx context.Context, homeDir string) {
	conditionActionRegistrations.Range(func(k, v any) bool {
		key, ok := k.(conditionActionKey)
		if !ok || key.homeDir != homeDir {
			return true
		}
		err := evaluateConditionAction(ctx, homeDir, key.eventID, v.(ConditionAction), home.RenameDurable)
		if err != nil && !errors.Is(err, errConditionActionNotStable) {
			fmt.Fprintf(os.Stderr, "condition-action %q evaluation failed (retried next cycle): %v\n", key.eventID, err)
		}
		return true
	})
}

// fireConditionAction runs the action for one announced capture, exactly once.
// It refuses a registration whose fired-marker is already on disk: that marker
// is the durable stop, and a redelivered wake must not run the action again.
// The marker is written only after the action returns, so the crash window
// between them re-delivers rather than losing the run.
func fireConditionAction(homeDir, eventID string, ca ConditionAction, result []byte, renameFn func(string, string) error) error {
	fired, err := hasFiredMarker(homeDir, eventID)
	if err != nil {
		return err
	}
	if fired {
		return errConditionActionAlreadyFired
	}
	if err := ca.Action(homeDir, result); err != nil {
		return fmt.Errorf("condition-action %q action: %w", eventID, err)
	}
	return writeFiredMarker(homeDir, eventID, renameFn)
}

// consumeConditionActionWake is the dispatcher arm for a condition-action
// wake. An already-fired registration is acked and does nothing; a fresh one
// runs its action, records the marker and is acked. Any failure leaves the
// event unacked so RecoverProcessEvents re-announces it at restart.
func consumeConditionActionWake(homeDir string, announced ProcessEventWake, rec *ProcessEventRecord) {
	entry, ok := conditionActionRegistrations.Load(conditionActionKey{homeDir, announced.EventID})
	if !ok {
		fmt.Fprintf(os.Stderr, "process-event wake %q dropped: no live condition-action registration\n", announced.EventID)
		return
	}
	err := fireConditionAction(homeDir, announced.EventID, entry.(ConditionAction), rec.Result, home.RenameDurable)
	if err != nil && !errors.Is(err, errConditionActionAlreadyFired) {
		fmt.Fprintf(os.Stderr, "condition-action %q failed (re-announced on restart): %v\n", announced.EventID, err)
		return
	}
	if err := AckProcessEvent(homeDir, announced.EventID, announced.Generation); err != nil {
		fmt.Fprintf(os.Stderr, "process-event %q ack failed (re-announced on restart): %v\n", announced.EventID, err)
	}
}
