package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// backendForTask resolves a session backend for task meta.
// Overridden in tests to inject a fake backend.
var backendForTask = session.BackendForTask

// SendOutboxDir is the parent-home directory for durable captain marked-send
// entries that could not be delivered because the pane was dead or SendKeys failed.
const SendOutboxDir = ".captain-send-outbox"

// sendOutboxCaptainDir returns state/.captain-send-outbox/<smID>/.
func sendOutboxCaptainDir(parentHome, smID string) string {
	return filepath.Join(parentHome, "state", SendOutboxDir, smID)
}

// EnqueueSendOutbox persists a marked captain send for later delivery.
// Deprecated: Replaced by SendMailboxToCaptain (mailbox envelope/pending/ack).
// This function is retained for one-shot migration (DrainLegacyCommandTransport)
// and should not be used for new writes.
func EnqueueSendOutbox(parentHome, smID, message string) error {
	if strings.TrimSpace(smID) == "" {
		return fmt.Errorf("enqueue send outbox: empty captain id")
	}
	if message == "" {
		return fmt.Errorf("enqueue send outbox: empty message")
	}
	if strings.Contains(message, "\n") {
		return fmt.Errorf("enqueue send outbox: message must be a single line")
	}

	dir := sendOutboxCaptainDir(parentHome, smID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating send outbox dir: %w", err)
	}

	// Nanosecond name keeps FIFO order under lexicographic sort.
	name := fmt.Sprintf("%d.pending", time.Now().UnixNano())
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("id=%s\ncreated=%s\nmessage=%s\n",
		smID, time.Now().UTC().Format(time.RFC3339Nano), message)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing send outbox entry: %w", err)
	}
	return nil
}

// readSendOutboxEntry parses one outbox file (key=value lines; message is last field value).
func readSendOutboxEntry(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		result[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if result["message"] == "" {
		return nil, fmt.Errorf("outbox entry %s: missing message", path)
	}
	return result, nil
}

// listSendOutboxPaths returns pending outbox files for smID in FIFO order.
func listSendOutboxPaths(parentHome, smID string) ([]string, error) {
	dir := sendOutboxCaptainDir(parentHome, smID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".pending") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

// FlushSendOutbox delivers queued marked sends for one captain using typed prompt
// submission. On acknowledged delivery each entry is removed. If the prompt is not
// acknowledged (stalled, endpoint dead, backend failure), entries remain and a clear
// error is returned. Partial flush stops on first unacknowledged result (earlier
// entries already removed stay delivered).
// Deprecated: Replaced by SendMailboxToCaptain and ReconcileMailboxPending.
// This function is retained only for one-shot migration via DrainLegacyCommandTransport.
func FlushSendOutbox(parentHome string, sm Info) error {
	paths, err := listSendOutboxPaths(parentHome, sm.ID)
	if err != nil {
		return fmt.Errorf("%s: listing send outbox: %v", sm.ID, err)
	}
	if len(paths) == 0 {
		return nil
	}

	taskID := taskIDForCaptain(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return fmt.Errorf("%s: %d send(s) queued but no task meta — relaunch captain then converge", sm.ID, len(paths))
	}
	if meta["kind"] != "captain" {
		return fmt.Errorf("%s: meta kind=%q, expected captain — outbox retained", sm.ID, meta["kind"])
	}
	if meta["sm_id"] != sm.ID {
		return fmt.Errorf("%s: meta sm_id=%q does not match — outbox retained", sm.ID, meta["sm_id"])
	}
	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		return fmt.Errorf("%s: cannot canonicalize home — outbox retained: %v", sm.ID, err)
	}
	if meta["home"] != canonSM {
		return fmt.Errorf("%s: meta home mismatch — outbox retained", sm.ID)
	}
	windowID := meta["window"]
	if windowID == "" {
		return fmt.Errorf("%s: no window in meta — outbox retained", sm.ID)
	}

	bk, _, bkErr := backendForTask(parentHome, meta)
	if bkErr != nil {
		return fmt.Errorf("%s: cannot resolve backend — outbox retained: %v", sm.ID, bkErr)
	}

	for _, path := range paths {
		entry, readErr := readSendOutboxEntry(path)
		if readErr != nil {
			return fmt.Errorf("%s: reading outbox entry: %v", sm.ID, readErr)
		}
		if entry["id"] != "" && entry["id"] != sm.ID {
			return fmt.Errorf("%s: outbox entry id=%q mismatch — outbox retained", sm.ID, entry["id"])
		}
		msg := entry["message"]

		// Use typed prompt submission.
		result := session.SubmitPrompt(bk, windowID, msg)
		if !result.Acknowledged() {
			// Never remove on unacknowledged result — preserves outbox for retry.
			return fmt.Errorf("%s: outbox send not acknowledged (status=%s) — outbox retained", sm.ID, result.Status)
		}

		// Only remove after acknowledged delivery.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%s: sent but failed to remove outbox entry %s: %v", sm.ID, path, err)
		}
		fmt.Printf("  %s: delivered queued send from outbox\n", sm.ID)
	}
	return nil
}

// CaptainIDFromTask resolves the registry/sm id for a captain task.
// Prefers meta sm_id; falls back to stripping the captain: task-id prefix.
func CaptainIDFromTask(taskID string, meta map[string]string) string {
	if meta != nil {
		if id := strings.TrimSpace(meta["sm_id"]); id != "" {
			return id
		}
	}
	return strings.TrimPrefix(taskID, "captain:")
}
