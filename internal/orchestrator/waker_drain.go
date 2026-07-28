// Package waker handles durable wake queue operations and guard checks.
package orchestrator

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
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

// Drain reads and clears the wake queue.
func Drain(homeDir string) ([]WakeRecord, error) {
	records, err := lifecycle.DrainWakes(homeDir)
	if err != nil {
		return nil, err
	}
	out := make([]WakeRecord, len(records))
	for i, r := range records {
		out[i] = WakeRecord(r)
	}
	return out, nil
}

// PrintRecords prints drained records in a readable format.
func PrintRecords(records []WakeRecord) {
	if len(records) == 0 {
		fmt.Println("ok: no pending wakes")
		return
	}
	for _, r := range records {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", r.Epoch, r.Seq, r.Kind, r.Key, r.Payload)
	}
}

// GuardResult summarizes the shared guard evaluation for both the CLI
// middleware and the contract guard command.
type GuardResult struct {
	BeatStatus lifecycle.BeatStatus
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

	result.BeatStatus = lifecycle.ReadBeatStatus(homeDir, now)

	if lifecycle.HasQueuedWakes(homeDir) {
		msg := "QUEUED WAKES PENDING - drain with munsu wake-drain"
		if HasAgedMaterialWake(homeDir, now) {
			msg = "QUEUED WAKES PENDING (aged) - material wake beyond threshold, guard unhealthy"
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
	data, err := os.ReadFile(lifecycle.QueuePath(homeDir))
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
		// Check for material states in the payload.
		payload := parts[4]
		if strings.HasPrefix(payload, "done:") || strings.HasPrefix(payload, "failed:") ||
			strings.HasPrefix(payload, "needs-decision:") || strings.HasPrefix(payload, "blocked:") {
			var epoch int64
			if _, err := fmt.Sscanf(parts[0], "%d", &epoch); err == nil && epoch > 0 && epoch < threshold {
				return true
			}
		}
	}
	return false
}

// GuardWarnings returns the current operational guard warnings without writing output.
func GuardWarnings(homeDir string) []string {
	var warnings []string

	status := lifecycle.ReadBeatStatus(homeDir, time.Now())
	if !status.Exists {
		warnings = append(warnings, "WATCHER NEVER STARTED - no liveness beacon")
	} else if status.Stale {
		warnings = append(warnings, fmt.Sprintf(
			"WATCHER BEACON STALE - last beat %v ago (grace %v)",
			status.Age.Round(time.Second), lifecycle.StaleThreshold()))
	}
	if lifecycle.HasQueuedWakes(homeDir) {
		warnings = append(warnings, "QUEUED WAKES PENDING - drain with munsu wake-drain")
	}
	return warnings
}

// CheckGuard checks for operational warnings.
func CheckGuard(homeDir string) []string {
	warnings := GuardWarnings(homeDir)
	for _, warning := range warnings {
		border := strings.Repeat("\u25cf", len(warning)+4)
		fmt.Println(border)
		fmt.Println("\u25cf " + warning + " \u25cf")
		fmt.Println(border)
	}
	if len(warnings) == 0 {
		fmt.Println("ok: no tangles; watcher fresh")
	}
	return warnings
}
