package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

// newInboxCmd creates the `munsu inbox` command.
// It shows a convenience view for the General: actionable wakes and last captain status lines.
// Also provides captain-side subcommands: receive (validate/load, no ack) and ack (accepted).
func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show queued and claimed wakes and last captain status lines (General convenience view)",
		Long: `Display a compact inbox view for the General, combining:

  - Pending wakes from the wake queue
  - Claimed wakes still held in a lease, with their derived state
  - Last captain status lines (state/captain:<id>.status) that are general-relevant

No behavior change to the watcher. Use 'munsu inbox' before 'munsu wake claim' or
'munsu wake claim' to preview what needs attention.

Captain-side subcommands:
  receive <ref>  — validate/load a mailbox envelope (no ack)
  ack <ref>      — accept a mailbox notification into agent context (writes accepted ack)`,
	}
	cmd.AddCommand(newInboxReceiveCmd())
	cmd.AddCommand(newInboxAckCmd())
	// The default "show" behavior is the existing inbox view.
	cmd.RunE = withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
		return renderInbox(ctx.Home, cmd.OutOrStdout())
	})
	return cmd
}

// newInboxReceiveCmd creates the `munsu inbox receive` subcommand.
// The captain agent calls this to validate and load an incoming NotificationRef:
// reads the envelope from its own inbox, validates all provenance fields,
// and returns the envelope payload. Writes NO ack.
//
// Usage by captain agent (after receiving NotificationRef via SubmitPrompt):
//
//	munsu inbox receive '{"message_id":"...","sender_identity":"..."}'
//
// This is the first step of the two-step inbox protocol:
//  1. munsu inbox receive <ref>  — inspect the incoming command
//  2. munsu inbox ack <ref>      — accept into context (after reading payload)
func newInboxReceiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receive <notification-ref>",
		Short: "Validate and load a mailbox envelope, returning the payload (no ack)",
		Long: `Receive a mailbox NotificationRef: reads the envelope from the
receiver's inbox, validates all provenance fields, and outputs the envelope
payload. Writes NO acknowledgment — call 'munsu inbox ack <ref>' separately
after the command has been accepted into agent context.

Called by the captain agent when it receives a NotificationRef via marked pane
input from the General. The envelope payload is the actual command; the ref
points to the envelope in the captain's own inbox.

Usage:
  munsu inbox receive '{"message_id":"...","sender_identity":"..."}'

This produces output with kind=inbox.receive.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			refJSON := args[0]

			ref, err := home.ParseNotificationRef(refJSON)
			if err != nil {
				return usageError("invalid_ref",
					"NotificationRef must be valid JSON with message_id and sender_identity fields",
					fmt.Sprintf("parsing NotificationRef: %v", err))
			}

			recv, err := inboxReceiver(ctx.Home)
			if err != nil {
				return operationError("receiver_init_failed",
					"Ensure MUNSU_HOME points to a home with provenance for this role; a soldier also needs MUNSU_TASK_ID",
					fmt.Sprintf("creating receiver: %v", err))
			}

			env, err := recv.Receive(ref)
			if err != nil {
				return operationError("receive_failed",
					"Check that the envelope exists in state/.inbox/<sender>/<id>.json",
					fmt.Sprintf("receiving notification: %v", err))
			}

			return writeContract(cmd, Response[InboxReceiveResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "inbox.receive",
				Status:        "success",
				Data: InboxReceiveResult{
					MessageID:      env.MessageID,
					SenderIdentity: env.SenderIdentity,
					Payload:        env.Payload,
				},
			})
		}),
	}

	configureContractCommand(cmd)
	return cmd
}

// newInboxAckCmd creates the `munsu inbox ack` subcommand.
// The captain agent calls this to write the "accepted" ProcessingAck after
// taking the command into its agent context.
//
// Usage by captain agent (after reading the payload via inbox receive):
//
//	munsu inbox ack '{"message_id":"...","sender_identity":"..."}'
//
// The ack means the command was accepted into agent context — NOT that it
// completed. Completion is tracked through separate report/relay flows.
func newInboxAckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ack <notification-ref>",
		Short: "Accept a mailbox notification into agent context (writes accepted ack)",
		Long: `Acknowledge a mailbox NotificationRef: validates the envelope and
writes a fixed "accepted" ProcessingAck. The captain agent calls this after
it has taken the incoming command into its context.

Idempotent: calling multiple times with the same ref is safe and preserves
original timestamp. Any conflicting existing outcome fails closed.

The ack means "accepted into agent context", NOT "completed". Completion
is tracked through 'munsu report' flows.

Usage:
  munsu inbox ack '{"message_id":"...","sender_identity":"..."}'

This produces output with kind=inbox.ack.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			refJSON := args[0]

			ref, err := home.ParseNotificationRef(refJSON)
			if err != nil {
				return usageError("invalid_ref",
					"NotificationRef must be valid JSON with message_id and sender_identity fields",
					fmt.Sprintf("parsing NotificationRef: %v", err))
			}

			recv, err := inboxReceiver(ctx.Home)
			if err != nil {
				return operationError("receiver_init_failed",
					"Ensure MUNSU_HOME points to a home with provenance for this role; a soldier also needs MUNSU_TASK_ID",
					fmt.Sprintf("creating receiver: %v", err))
			}

			ack, err := recv.Ack(ref)
			if err != nil {
				return operationError("ack_failed",
					"Check that the envelope exists and hasn't been acked with a different outcome",
					fmt.Sprintf("acknowledging notification: %v", err))
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "inbox.ack",
				Status:        "success",
				Data: MessageResult{
					Message: fmt.Sprintf("accepted message %s from %s (outcome=%s)", ref.MessageID, ref.SenderIdentity, ack.Outcome),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	return cmd
}

// inboxReceiver constructs the receiver for the agent running this command.
//
// A general or captain owns the home it runs in, so its receiver identity is
// that home's provenance. A soldier owns no home: it is launched with
// MUNSU_HOME set to its dispatcher's home, so its receiver identity is the
// durable task record that home holds for MUNSU_TASK_ID. Both paths derive
// identity from durable state and fail closed when it is absent.
func inboxReceiver(homeDir string) (*home.Receiver, error) {
	if os.Getenv("MUNSU_ROLE") != "soldier" {
		return home.NewReceiver(homeDir)
	}
	taskID := os.Getenv("MUNSU_TASK_ID")
	if taskID == "" {
		return nil, fmt.Errorf("MUNSU_ROLE=soldier without MUNSU_TASK_ID: a soldier is identified by its task")
	}
	return home.NewSoldierReceiver(homeDir, taskID)
}

// renderInbox prints the inbox view to the given writer.
func renderInbox(homeDir string, w interface{ Write([]byte) (int, error) }) error {
	stateDir := filepath.Join(homeDir, "state")
	out := func(format string, a ...interface{}) {
		fmt.Fprintf(w, format, a...)
	}

	hasContent := false

	// --- Section 1: Wakes, queued and claimed ---
	pending := countQueuedWakes(homeDir)
	now := time.Now()
	claimed, claimedErr := orchestrator.ListClaimedWakes(homeDir, now)
	switch {
	case pending > 0:
		out("Pending wakes: %d\n", pending)
		out("  Under herdr each is delivered as a prompt once the General is idle; elsewhere run `munsu wake claim --consumer <id>`.\n")
	case len(claimed) > 0:
		out("Wakes: none queued, %d claimed\n", len(claimed))
	case claimedErr != nil:
		out("Wakes: none queued, claimed unknown\n")
	default:
		out("Wakes: none pending\n")
	}
	if claimedErr != nil {
		out("Claimed wakes: unknown (%v)\n", claimedErr)
	} else if len(claimed) > 0 {
		out("Claimed wakes: %d\n", len(claimed))
		lastLease := ""
		for _, c := range claimed {
			if c.LeaseID != lastLease {
				lastLease = c.LeaseID
				out("  %s held by %s", c.LeaseID, c.Consumer)
				if !c.ClaimedAt.IsZero() {
					out(", claimed %s ago", now.Sub(c.ClaimedAt).Round(time.Second))
				}
				out("\n")
			}
			out("    %s:%s %s %s %q [%s]", c.Wake.Epoch, c.Wake.Seq, c.Wake.Kind, c.Wake.Key, c.Wake.Payload, c.State)
			if c.Evidence != "" {
				out(": %s", c.Evidence)
			}
			out("\n")
		}
	}
	out("\n")
	hasContent = pending > 0 || len(claimed) > 0 || claimedErr != nil

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

// formatCaptainStatusLines reads durable-stem status files for logical
// captain:<id> IDs and returns formatted display lines with actionable markers.
func formatCaptainStatusLines(stateDir string) []string {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}

	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".status") {
			continue
		}
		id, err := home.ReverseDurableKey(strings.TrimSuffix(name, ".status"))
		if err != nil || !strings.HasPrefix(id, "captain:") {
			continue
		}

		lastLine := readLastLine(filepath.Join(stateDir, name))
		if lastLine == "" {
			continue
		}

		captainID := strings.TrimPrefix(id, "captain:")
		marker := " "
		if domain.GeneralRelevant(lastLine) {
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
	_ = home.EachLine(f, func(text string) {
		if strings.TrimSpace(text) != "" {
			lastLine = text
		}
	})
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
