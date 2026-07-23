package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/mailbox"
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
	}
	cmd.AddCommand(newInboxProcessCmd())
	// The default "show" behavior is the existing inbox view.
	cmd.RunE = withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
		return renderInbox(ctx.Home, cmd.OutOrStdout())
	})
	return cmd
}

// newInboxProcessCmd creates the `munsu inbox process` subcommand.
// The captain agent calls this to process an incoming NotificationRef:
// reads the envelope from its own inbox and writes the ProcessingAck.
//
// This is the smallest internal adapter for the captain to complete the
// mailbox processing loop. Used by the captain agent (Pi/Claude) when it
// receives a NotificationRef via marked pane input.
func newInboxProcessCmd() *cobra.Command {
	var outcome string

	cmd := &cobra.Command{
		Use:   "process <notification-ref>",
		Short: "Process a mailbox NotificationRef (captain-side ack adapter)",
		Long: `Process a mailbox NotificationRef by reading the envelope from the
receiver's inbox, validating all provenance fields, and writing a ProcessingAck.

Called by the captain agent when it receives a NotificationRef via marked pane
input from the General. The envelope payload is the actual command; the ref
points to the envelope in the captain's own inbox.

Usage:
  munsu inbox process '{"message_id":"...","sender_identity":"..."}'

The outcome flag defaults to "done". Valid outcomes: done, failed, needs-decision,
blocked, paused.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			refJSON := args[0]

			ref, err := mailbox.ParseNotificationRef(refJSON)
			if err != nil {
				return usageError("invalid_ref",
					"NotificationRef must be valid JSON with message_id and sender_identity fields",
					fmt.Sprintf("parsing NotificationRef: %v", err))
			}

			recv, err := mailbox.NewReceiver(ctx.Home)
			if err != nil {
				return operationError("receiver_init_failed",
					"Ensure MUNSU_HOME points to a valid captain or general home with provenance",
					fmt.Sprintf("creating receiver: %v", err))
			}

			res := recv.Process(ref, outcome)
			if !res.Ok() {
				return operationError("process_failed",
					"Check that the envelope exists in state/.inbox/<sender>/<id>.json",
					fmt.Sprintf("processing notification: %v", res.Err))
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "inbox.process",
				Status:        "success",
				Data: contract.MessageResult{
					Message: fmt.Sprintf("processed message %s from %s (outcome=%s)", ref.MessageID, ref.SenderIdentity, outcome),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&outcome, "outcome", "done", "Processing outcome (done, failed, needs-decision, blocked, paused)")
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
