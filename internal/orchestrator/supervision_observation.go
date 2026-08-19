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
	"time"
)

// CycleObservation captures typed internal measurements from one watcher
// scan/enqueue cycle.
type CycleObservation struct {
	// ScannedTasks is the number of task .meta files the watcher examined for
	// staleness this cycle.
	ScannedTasks int
	// StaleByTask is the measured stale duration per task, keyed by task id.
	// Ages are reported here only — never into WakeReason.Message or a
	// fingerprint input, so duplicate suppression is preserved.
	StaleByTask map[string]time.Duration
	// SuppressedDuplicates is the number of watcher task wakes skipped because
	// the durable fingerprint marker already matched (duplicate suppression).
	SuppressedDuplicates int
}

// newCycleObservation returns an empty observation with the per-task map ready.
func newCycleObservation() *CycleObservation {
	return &CycleObservation{StaleByTask: map[string]time.Duration{}}
}

// logCycleObservation writes the cycle's internal measurements to stderr so
// the watcher daemon surfaces them without touching any CLI response contract
// (stderr is separate from the contract stdout).
func logCycleObservation(obs *CycleObservation) {
	if obs == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "[watcher-obs] scanned=%d stale=%d suppressed=%d\n",
		obs.ScannedTasks, len(obs.StaleByTask), obs.SuppressedDuplicates)
}
