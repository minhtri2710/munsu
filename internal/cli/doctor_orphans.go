package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
)

// Exit codes of the orphan scan, so a member or CI can script it: 0 nothing to
// look at, 1 leftovers found, 2 nothing conclusive but a human should look.
// They reach the shell as a contract error status through WriteContractError,
// the way every other exit code in this CLI does.
//
// GARBAGE wins over UNKNOWN when a report holds both, so a machine with real
// leftovers always signals 1. The consequence is deliberate: exit 1 means "at
// least one leftover, and there may be unresolved processes too — read the
// report". Inverting the priority would make such a machine report 2, which a
// `if munsu doctor --orphans; then ...; fi` script reads as nothing to clean.
const (
	orphanExitGarbage     = exitOperation
	orphanExitNeedsMember = exitUsage
)

// runOrphanScan wires the existing writer fence to `munsu doctor --orphans`.
// The scan reports and stops there: it never signals a process. Termination
// stays a member decision until munsu can tell a leftover from a daemon
// somebody started on purpose (ADR-BEO-40-01 §5).
func runOrphanScan(out io.Writer, homeDir string) error {
	return runOrphanScanWith(out, fleet.NewRuntimeWriterFence(), homeDir)
}

// runOrphanScanWith is runOrphanScan over an explicit fence, so every exit
// path can be driven from a test without a machine in a particular state.
func runOrphanScanWith(out io.Writer, fence fleet.CompositeWriterFence, homeDir string) error {
	report, err := fence.InspectOrphans(homeDir)
	if err != nil {
		if errors.Is(err, fleet.ErrProcessInventoryUnsupported) {
			fmt.Fprintf(out, "Orphan scan (home: %s)\n  orphan detection is unavailable on this platform (%v)\n", homeDir, err)
			return orphanError(orphanExitNeedsMember, "orphan_scan_failed",
				"Orphan detection is unavailable on this platform", fmt.Sprintf("orphan scan: %v", err))
		}
		// A scan that failed resolved nothing, so it exits like an
		// unresolved finding rather than like a leftover: a script must not
		// read a broken scan as "garbage found".
		return orphanError(orphanExitNeedsMember, "orphan_scan_failed",
			"Fix the scan failure, then re-run `munsu doctor --orphans`", fmt.Sprintf("orphan scan: %v", err))
	}
	writeOrphanReport(out, report)
	switch {
	case len(report.Garbage) > 0:
		return orphanError(orphanExitGarbage, "orphans_found",
			"Inspect each reported PID and decide by hand; munsu never terminates them",
			fmt.Sprintf("%d processes whose owning run has ended, %d unresolved", len(report.Garbage), len(report.Unknown)))
	case len(report.Unknown) > 0:
		return orphanError(orphanExitNeedsMember, "orphans_unresolved",
			"Inspect each reported PID and decide by hand; munsu never terminates them",
			fmt.Sprintf("%d processes no oracle could resolve", len(report.Unknown)))
	default:
		return nil
	}
}

func orphanError(status int, code, action, message string) error {
	return &contractError{
		status: status,
		value: ErrorResponse{
			SchemaVersion: SchemaVersion,
			Kind:          "error",
			Status:        "error",
			Error:         ErrorEnvelope{ErrorCode: code, Action: action, Message: message},
		},
	}
}

func writeOrphanReport(out io.Writer, report fleet.OrphanReport) {
	fmt.Fprintf(out, "Orphan scan (home: %s)\n", report.Home)
	fmt.Fprintf(out, "  %d processes scanned, %d carry an ownership marker, %d unreadable\n", report.Scanned, report.Marked, report.Unreadable)

	writeOrphanGroup(out, "GARBAGE", "owning run has ended", report.Garbage)
	writeOrphanGroup(out, "UNKNOWN", "no oracle resolved the owning run — only a member can decide", report.Unknown)
	writeOrphanGroup(out, "OWNED", "owning run is alive", report.Owned)

	if len(report.StaleArtifacts) > 0 {
		fmt.Fprintf(out, "\nStale writer identities (%d) — process verified gone, artifact left behind:\n", len(report.StaleArtifacts))
		for _, path := range report.StaleArtifacts {
			fmt.Fprintf(out, "  %s\n", path)
		}
	}
	for _, note := range report.Notes {
		fmt.Fprintf(out, "\nNote: %s\n", note)
	}
	fmt.Fprintf(out, "\nThis scan does not terminate anything. A process without an ownership\n")
	fmt.Fprintf(out, "marker is outside its reach and is not reported at all.\n")
}

func writeOrphanGroup(out io.Writer, verdict, meaning string, processes []fleet.OrphanProcess) {
	fmt.Fprintf(out, "\n%s (%d) — %s:\n", verdict, len(processes), meaning)
	if len(processes) == 0 {
		fmt.Fprintf(out, "  none\n")
		return
	}
	for _, process := range processes {
		fmt.Fprintf(out, "  PID %d%s %s\n", process.PID, orphanParentage(process), orphanIdentity(process))
		if process.ExecutablePath != "" {
			fmt.Fprintf(out, "      %s\n", process.ExecutablePath)
		}
		if process.Reason != "" {
			fmt.Fprintf(out, "      %s\n", process.Reason)
		}
		if verdict != string(fleet.VerdictOwned) {
			fmt.Fprintf(out, "      Fix: inspect with `ps -p %d -o command=`, then decide by hand\n", process.PID)
		}
	}
}

func orphanParentage(process fleet.OrphanProcess) string {
	if process.PPID == 0 && process.PGID == 0 {
		return ""
	}
	return fmt.Sprintf(" (ppid %d, pgid %d)", process.PPID, process.PGID)
}

func orphanIdentity(process fleet.OrphanProcess) string {
	parts := []string{process.Layer}
	if process.Kind != "" {
		parts = append(parts, process.Kind)
	}
	if process.TaskID != "" {
		parts = append(parts, "task "+process.TaskID)
	}
	return strings.Join(parts, " ")
}
