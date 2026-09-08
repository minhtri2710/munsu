// Package waker handles durable wake queue operations and guard checks.
package orchestrator

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ConditionCode is a stable machine-readable code for a guard condition.
type ConditionCode string

const (
	ConditionQueuedWakesPending ConditionCode = "queued_wakes_pending"
	ConditionWatcherAbsent      ConditionCode = "watcher_absent"
	ConditionWatcherStale       ConditionCode = "watcher_stale"
	ConditionAgedWakePending    ConditionCode = "aged_wake_pending"
)

// MaterialWakeAgeThreshold is the age at which a material (done/failed/needs-decision/blocked)
// wake makes the guard unhealthy rather than indeterminate.
const MaterialWakeAgeThreshold = 5 * time.Minute

// ConditionInfo pairs a stable condition code with its human-readable message.
type ConditionInfo struct {
	Code    ConditionCode
	Message string
}

// GuardResult summarizes the shared guard evaluation for both the CLI
// middleware and the contract guard command.
type GuardResult struct {
	BeatStatus BeatStatus
	InFlight   int
	Conditions []ConditionInfo
}

// EvaluateGuard returns the current guard state combining beat liveness,
// queued-wake status, aged wake threshold, and the caller-supplied in-flight
// task count. Aged material wakes (beyond MaterialWakeAgeThreshold) produce
// an unhealthy condition rather than merely indeterminate.
// Both the pre-run middleware and the contract guard command use this to
// produce consistent warnings and state.
func EvaluateGuard(homeDir string, inFlight int, now time.Time) GuardResult {
	result := GuardResult{
		InFlight: inFlight,
	}

	result.BeatStatus = ReadBeatStatus(homeDir, now)

	if HasQueuedWakes(homeDir) {
		msg := "QUEUED WAKES PENDING - claim with munsu wake claim"
		if HasAgedMaterialWake(homeDir, now) {
			msg = "QUEUED WAKES PENDING (aged) - material wake beyond threshold, guard unhealthy - claim with munsu wake claim"
			result.Conditions = append(result.Conditions, ConditionInfo{
				Code:    ConditionAgedWakePending,
				Message: msg,
			})
		} else {
			result.Conditions = append(result.Conditions, ConditionInfo{
				Code:    ConditionQueuedWakesPending,
				Message: msg,
			})
		}
	}

	return result
}

// HasAgedMaterialWake returns true when the wake queue contains at least one
// material wake (done/failed/needs-decision/blocked) older than
// MaterialWakeAgeThreshold.
func HasAgedMaterialWake(homeDir string, now time.Time) bool {
	data, err := os.ReadFile(QueuePath(homeDir))
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return false
	}
	threshold := now.Add(-MaterialWakeAgeThreshold).Unix()
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 5 {
			continue
		}
		// Check for material states in the payload. parts[3] is the wake key
		// (the taskID for signal/uplink wakes); PayloadHasMaterialMarker uses
		// it to check the signal marker at the anchored "<taskID>: " position.
		if PayloadHasMaterialMarker(parts[3], parts[4]) {
			var epoch int64
			if _, err := fmt.Sscanf(parts[0], "%d", &epoch); err == nil && epoch > 0 && epoch < threshold {
				return true
			}
		}
	}
	return false
}

// PayloadHasMaterialMarker reports whether a wake queue payload carries a
// material-state marker (done:/failed:/needs-decision:/blocked:) at its
// structural position. Material wakes take two producer shapes: a signal wake
// payload is "<taskID>: <state>: <msg> [event=N]" (the marker follows the
// "<key>: " prefix, where key is the taskID) and an uplink wake payload is
// "<state>: <msg> [task=X key=Y]" (the marker is at the start). Checking both
// anchored positions matches both shapes while rejecting a marker that merely
// appears mid-message in a non-material payload. This is the single predicate
// both the guard (HasAgedMaterialWake) and watch (oldestMaterialWakeAge) use.
func PayloadHasMaterialMarker(key, payload string) bool {
	for state := range wakeMaterialStates {
		marker := state + ":"
		if strings.HasPrefix(payload, marker) || strings.HasPrefix(payload, key+": "+marker) {
			return true
		}
	}
	return false
}
