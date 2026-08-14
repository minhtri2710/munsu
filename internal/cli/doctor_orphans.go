package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
)

// Exit codes of the orphan scan, so a member or CI can script it:
// 0 nothing to look at, 1 leftovers found, 2 nothing conclusive but a human
// should look.
const (
	orphanExitClean       = 0
	orphanExitGarbage     = 1
	orphanExitNeedsMember = 2
)

// runOrphanScan wires the existing writer fence to `munsu doctor --orphans`.
// The scan reports and stops there: it never signals a process. Termination
// stays a member decision until munsu can tell a leftover from a daemon
// somebody started on purpose (ADR-BEO-40-01 §5).
func runOrphanScan(out io.Writer, homeDir string) (int, error) {
	report, err := fleet.NewRuntimeWriterFence().InspectOrphans(homeDir)
	if err != nil {
		return orphanExitNeedsMember, fmt.Errorf("orphan scan: %w", err)
	}
	writeOrphanReport(out, report)
	switch {
	case len(report.Garbage) > 0:
		return orphanExitGarbage, nil
	case len(report.Unknown) > 0:
		return orphanExitNeedsMember, nil
	default:
		return orphanExitClean, nil
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
