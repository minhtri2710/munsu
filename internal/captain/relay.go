// Package captain implements persistent domain supervisors (captains).
package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// RelayTerminalReceipts scans the captain's home for pending terminal receipts
// (un-acked Soldier→Captain material reports) and relays them to the General's
// state via parentHome. After relay, writes an ack to mark the receipt as
// acknowledged. This is the Captain→General hop of the one-hop routing.
//
// Returns the number of receipts relayed.
func RelayTerminalReceipts(captainHome, parentHome string) (int, error) {
	pending, err := turnend.ListPendingReceipts(captainHome)
	if err != nil {
		return 0, fmt.Errorf("listing pending receipts: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	relayed := 0
	for _, pr := range pending {
		// Relay to General: write status under captain namespace
		// state/captain:<captainID>.relay-<taskID>.<key>.status
		// First resolve captain ID from provenance marker
		captainID, err := readCaptainID(captainHome)
		if err != nil {
			return relayed, fmt.Errorf("reading captain id for relay: %w", err)
		}

		relayTaskID := fmt.Sprintf("captain:%s.relay-%s", captainID, pr.TaskID)
		relayLine := fmt.Sprintf("%s: soldier %s [key=%s]", pr.State, pr.TaskID, pr.TermKey)

		if err := task.AppendStatus(parentHome, relayTaskID, relayLine); err != nil {
			return relayed, fmt.Errorf("relaying receipt for %s/%s: %w", pr.TaskID, pr.TermKey, err)
		}

		// Also relay to event log for permanent durability
		now := time.Now().UnixNano()
		eventContent := fmt.Sprintf("terminal_uplink_task=%s key=%s captain=%s relayed_at=%d\n",
			pr.TaskID, pr.TermKey, captainID, now)
		eventPath := filepath.Join(parentHome, "state", relayTaskID+".turnend")
		if err := os.MkdirAll(filepath.Dir(eventPath), 0755); err == nil {
			os.WriteFile(eventPath, []byte(eventContent), 0644)
		}

		// Write ack in captain home (marks receipt as acknowledged)
		if err := turnend.WriteAck(captainHome, pr.TaskID, pr.TermKey); err != nil {
			return relayed, fmt.Errorf("writing ack for %s/%s: %w", pr.TaskID, pr.TermKey, err)
		}

		// Complete per-task obligation in captain home
		if _, err := turnend.CompleteTaskObligation(captainHome, pr.TaskID, turnend.ReportRelay); err != nil {
			return relayed, fmt.Errorf("completing obligation for %s: %w", pr.TaskID, err)
		}

		relayed++
	}
	return relayed, nil
}

// readCaptainID reads the captain ID from the provenance marker.
func readCaptainID(captainHome string) (string, error) {
	markerPath := filepath.Join(captainHome, ProvenanceMarkerName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		// Fallback: try to extract from directory name
		return filepath.Base(captainHome), nil
	}
	// Format: munsu-v2 <id>
	parts := strings.Fields(string(data))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return filepath.Base(captainHome), nil
}
