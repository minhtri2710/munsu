package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/spf13/cobra"
)

// newInboxCmd creates the `munsu inbox` command.
// It shows a convenience view for the General: actionable wakes and last captain status lines.
func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show actionable wakes and last captain status lines (General convenience view)",
		Long: `Display a compact inbox view for the General, combining:

  - Pending wakes from the wake queue
  - Last captain status lines (state/captain:<id>.status) that are general-relevant

No behavior change to the watcher. Use 'munsu inbox' before 'munsu wake claim' or
'munsu wake-drain' to preview what needs attention.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return renderInbox(ctx.Home, cmd.OutOrStdout())
		}),
	}
	return cmd
}

// renderInbox prints the inbox view to the given writer.
func renderInbox(homeDir string, w interface{ Write([]byte) (int, error) }) error {
	stateDir := filepath.Join(homeDir, "state")
	out := func(format string, a ...interface{}) {
		fmt.Fprintf(w, format, a...)
	}

	hasContent := false

	// --- Section 1: Pending wakes ---
	pending := countQueuedWakes(homeDir)
	if pending > 0 {
		hasContent = true
		out("Pending wakes: %d\n", pending)
		out("  Run `munsu wake claim <consumer-id>` or `munsu wake-drain` to process.\n\n")
	} else {
		out("Wakes: none pending\n\n")
	}

	// --- Section 2: Captain status lines ---
	out("Captain status:\n")
	captainLines := formatCaptainStatusLines(stateDir)
	if len(captainLines) == 0 {
		out("  No captains registered.\n")
	} else {
		hasContent = true
		for _, cl := range captainLines {
			out("  %s\n", cl)
		}
		if !anyActionable(captainLines) {
			out("\n  All captains nominal (no actionable status).\n")
		} else {
			out("\n  Actionable: review captains with ! marker (done/failed/blocked/needs-decision).\n")
			out("  Use `munsu captain list` for the full register.\n")
		}
	}

	if !hasContent {
		out("\nAll quiet. No actionable wakes or captain status.\n")
	}

	return nil
}

// formatCaptainStatusLines reads state/captain:<id>.status files and returns
// formatted display lines with actionable markers.
func formatCaptainStatusLines(stateDir string) []string {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}

	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "captain:") || !strings.HasSuffix(name, ".status") {
			continue
		}

		lastLine := readLastLine(filepath.Join(stateDir, name))
		if lastLine == "" {
			continue
		}

		captainID := strings.TrimPrefix(name[:len(name)-len(".status")], "captain:")
		marker := " "
		if classify.GeneralRelevant(lastLine) {
			marker = "!"
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", marker, captainID, lastLine))
	}
	return lines
}

// readLastLine reads the last non-empty line from a file.
func readLastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) != "" {
			lastLine = text
		}
	}
	return lastLine
}

// anyActionable returns true if any captain line has the actionable (!) marker.
func anyActionable(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, "!") {
			return true
		}
	}
	return false
}
