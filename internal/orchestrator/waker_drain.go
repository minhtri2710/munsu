// Package waker handles durable wake queue operations and guard checks.
package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
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
		if hasAgedMaterialWake(homeDir, now) {
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

// hasAgedMaterialWake returns true when the wake queue contains at least one
// material wake (done/failed/needs-decision/blocked) older than
// MaterialWakeAgeThreshold.
func hasAgedMaterialWake(homeDir string, now time.Time) bool {
	epoch, ok := OldestMaterialWake(homeDir)
	return ok && epoch < now.Add(-MaterialWakeAgeThreshold).Unix()
}

// OldestMaterialWake returns the enqueue epoch of the oldest material wake in
// the wake queue. ok is false when the queue holds none or cannot be read.
func OldestMaterialWake(homeDir string) (epoch int64, ok bool) {
	records, err := mhome.PeekWakes(homeDir)
	if err != nil {
		return 0, false
	}
	for _, r := range records {
		// r.Key is the taskID for signal/uplink wakes; payloadHasMaterialMarker
		// uses it to check the signal marker at the anchored "<taskID>: " position.
		if !payloadHasMaterialMarker(r.Key, r.Payload) {
			continue
		}
		var e int64
		if _, err := fmt.Sscanf(r.Epoch, "%d", &e); err != nil || e <= 0 {
			continue
		}
		if !ok || e < epoch {
			epoch, ok = e, true
		}
	}
	return epoch, ok
}

// payloadHasMaterialMarker reports whether a wake queue payload carries a
// material-state marker (done:/failed:/needs-decision:/blocked:) at its
// structural position. Material wakes take two producer shapes: a signal wake
// payload is "<taskID>: <state>: <msg> [event=N]" (the marker follows the
// "<key>: " prefix, where key is the taskID) and an uplink wake payload is
// "<state>: <msg> [task=X key=Y]" (the marker is at the start). Checking both
// anchored positions matches both shapes while rejecting a marker that merely
// appears mid-message in a non-material payload. OldestMaterialWake
// applies it for both the guard and watch.
func payloadHasMaterialMarker(key, payload string) bool {
	for _, state := range domain.MaterialVerbs {
		marker := state + ":"
		if strings.HasPrefix(payload, marker) || strings.HasPrefix(payload, key+": "+marker) {
			return true
		}
	}
	return false
}
