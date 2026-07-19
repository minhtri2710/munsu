// Package waker handles durable wake queue operations and guard checks.
package waker

import (
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

// Record is a single wake queue entry, imported from lifecycle.
type Record lifecycle.WakeRecord

// Drain reads and clears the wake queue.
func Drain(homeDir string) ([]Record, error) {
	records, err := lifecycle.DrainWakes(homeDir)
	if err != nil {
		return nil, err
	}
	out := make([]Record, len(records))
	for i, r := range records {
		out[i] = Record(r)
	}
	return out, nil
}

// PrintRecords prints drained records in a readable format.
func PrintRecords(records []Record) {
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
	Conditions []string
}

// EvaluateGuard returns the current guard state combining beat liveness,
// queued-wake status, and the caller-supplied in-flight task count.
// Both the pre-run middleware and the contract guard command use this to
// produce consistent warnings and state.
func EvaluateGuard(homeDir string, inFlight int, now time.Time) GuardResult {
	result := GuardResult{
		InFlight: inFlight,
	}

	result.BeatStatus = lifecycle.ReadBeatStatus(homeDir, now)

	if lifecycle.HasQueuedWakes(homeDir) {
		result.Conditions = append(result.Conditions, "QUEUED WAKES PENDING - drain with munsu wake-drain")
	}

	return result
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
