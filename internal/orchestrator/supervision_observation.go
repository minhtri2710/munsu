// Typed internal supervision measurements (issue #546): the watcher cycle
// records scan count, measured stale age per task, and how many wakes were
// suppressed as duplicates. These are INTERNAL observations — never CLI
// contract fields, never lifecycle truth, never inputs to wake decisions, and
// never serialized into the response boundary. They exist so the supervision
// path can be measured deterministically without inventing a telemetry
// subsystem.
//
// Wall-clock ages live ONLY here and in the claim-path latency observation in
// internal/home. They are deliberately NOT written into WakeReason.Message or
// any fingerprint input, so wake fingerprints stay stable and duplicate
// suppression is preserved: a task that is still stale keeps the same
// fingerprint until it stops being stale (see wakeFingerprint).
package orchestrator

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// CycleObservation captures typed internal measurements from one watcher
// scan/enqueue cycle.
type cycleObservation struct {
	scannedTasks         int
	staleByTask          map[string]time.Duration
	suppressedDuplicates int
	now                  func() time.Time
}

// newCycleObservation returns an empty observation with the per-task map ready.
func newCycleObservation() *cycleObservation {
	return &cycleObservation{staleByTask: map[string]time.Duration{}, now: time.Now}
}

// logCycleObservation writes the cycle's internal measurements to stderr so
// the watcher daemon surfaces them without touching any CLI response contract
// (stderr is separate from the contract stdout).
//
// This one is deliberately per-cycle, unlike the check loop's refusal reports,
// which fire on state change (see reportCheckRefusal). The difference is what
// the line is: a measurement of the cycle that just ran, whose absence is
// itself information and whose volume is one line per cycle no matter how much
// state exists. A refusal report is a fact about one artifact, identical on
// every repeat and growing with the number of artifacts. Suppressing a
// measurement would hide a silent watcher; repeating a fact only buries it.
func logCycleObservation(obs *cycleObservation) {
	if obs == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "[watcher-obs] scanned=%d suppressed=%d\n",
		obs.scannedTasks, obs.suppressedDuplicates)
	ids := make([]string, 0, len(obs.staleByTask))
	for id := range obs.staleByTask {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(os.Stderr, "[watcher-obs] stale task=%s age=%s\n", id, obs.staleByTask[id])
	}
}

func logWakeClaimObservation(result *ClaimResult) {
	if result == nil {
		return
	}
	for i, latency := range result.WakeToClaimLatencies {
		fmt.Fprintf(os.Stderr, "[watcher-obs] wake=%d wake-to-claim=%s\n", i, latency)
	}
}
