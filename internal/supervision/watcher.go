// Package supervision provides the event-driven watcher backbone.
package supervision

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/lock"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

const (
	wakeQueueFile   = "state/.wake-queue"
	watcherBeatFile = "state/.last-watcher-beat"
	pollInterval    = 5 * time.Second
	staleThreshold  = 300 * time.Second // 5 min
)

// WakeReason describes why the watcher exited.
type WakeReason struct {
	Kind    string // signal, stale, check, heartbeat
	TaskIDs []string
	Message string
}

// Run starts the watcher loop. It acquires the watcher lock and polls
// until an actionable wake is found, then exits with the reason.
func Run(homeDir string) (*WakeReason, error) {
	// Acquire watcher lock
	acquired, err := lock.Acquire(homeDir)
	if err != nil {
		return nil, fmt.Errorf("watcher lock: %w", err)
	}
	if !acquired {
		return nil, fmt.Errorf("another watcher is already running")
	}
	defer lock.Release(homeDir)

	// Handle SIGTERM for graceful cleanup
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Touch liveness beacon
	touchBeat(homeDir)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return &WakeReason{Kind: "signal", Message: "watcher interrupted"}, nil

		case <-ticker.C:
			touchBeat(homeDir)

			reason := scanFleet(homeDir)
			if reason != nil {
				return reason, nil
			}
		}
	}
}

// ArmBackground launches the watcher as a background process.
// If restart is true, signals any existing watcher first.
func ArmBackground(homeDir string, restart bool) error {
	if restart {
		// Signal existing watcher via its lock file
		beatFile := filepath.Join(homeDir, watcherBeatFile)
		if data, err := os.ReadFile(beatFile); err == nil {
			var pid int
			fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
			if pid > 0 {
				proc, err := os.FindProcess(pid)
				if err == nil {
					proc.Signal(syscall.SIGTERM)
					time.Sleep(500 * time.Millisecond)
				}
			}
		}
	}

	// Fork a child process
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding munsu binary: %w", err)
	}

	cmd := exec.Command(execPath, "watch")
	cmd.Dir = homeDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}

	fmt.Printf("Watcher armed (pid %d)\n", cmd.Process.Pid)
	return nil
}

// scanFleet checks all live tasks for actionable events.
func scanFleet(homeDir string) *WakeReason {
	metasDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(metasDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".meta")

		// Read meta
		meta, err := task.ReadMeta(homeDir, id)
		if err != nil {
			continue
		}

		windowID, hasWindow := meta["window"]
		if !hasWindow {
			continue
		}

		// Check pane liveness
		bk := session.Default()
		alive := bk.Alive(windowID)

		if !alive {
			return &WakeReason{
				Kind:    "stale",
				TaskIDs: []string{id},
				Message: fmt.Sprintf("pane %s is dead", windowID),
			}
		}

		// Check status log for recent activity
		statusPath := filepath.Join(homeDir, "state", id+".status")
		if fi, err := os.Stat(statusPath); err == nil {
			age := time.Since(fi.ModTime())
			if age > staleThreshold {
				return &WakeReason{
					Kind:    "stale",
					TaskIDs: []string{id},
					Message: fmt.Sprintf("pane %s idle for %v", windowID, age.Round(time.Second)),
				}
			}
		}

		// Check wake queue
		qPath := filepath.Join(homeDir, wakeQueueFile)
		if fi, err := os.Stat(qPath); err == nil && fi.Size() > 0 {
			return &WakeReason{
				Kind:    "signal",
				TaskIDs: []string{id},
				Message: "queued wake records present",
			}
		}
	}

	return nil
}

// touchBeat writes the current time and PID to the liveness beacon.
func touchBeat(homeDir string) {
	beatFile := filepath.Join(homeDir, watcherBeatFile)
	os.MkdirAll(filepath.Dir(beatFile), 0755)
	content := fmt.Sprintf("%d %d", time.Now().Unix(), os.Getpid())
	os.WriteFile(beatFile, []byte(content), 0644)
}

// QueueWake adds a wake record to the durable wake queue.
func QueueWake(homeDir, kind, key, payload string) error {
	qPath := filepath.Join(homeDir, wakeQueueFile)
	os.MkdirAll(filepath.Dir(qPath), 0755)

	f, err := os.OpenFile(qPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	line := fmt.Sprintf("%d\t%d\t%s\t%s\t%s\n", time.Now().Unix(), os.Getpid(), kind, key, payload)
	_, err = f.WriteString(line)
	return err
}

// DrainWakeQueue reads and clears the wake queue.
func DrainWakeQueue(homeDir string) ([]WakeRecord, error) {
	qPath := filepath.Join(homeDir, wakeQueueFile)

	f, err := os.Open(qPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []WakeRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		records = append(records, WakeRecord{
			Epoch:   parts[0],
			Seq:     parts[1],
			Kind:    parts[2],
			Key:     parts[3],
			Payload: parts[4],
		})
	}

	// Clear the queue
	os.Remove(qPath)

	return records, scanner.Err()
}

// WakeRecord represents a single wake queue entry.
type WakeRecord struct {
	Epoch   string
	Seq     string
	Kind    string
	Key     string
	Payload string
}

// CheckGuard checks for operational issues and prints warnings.
func CheckGuard(homeDir string) []string {
	var warnings []string

	// Check watcher liveness
	beatFile := filepath.Join(homeDir, watcherBeatFile)
	if data, err := os.ReadFile(beatFile); err == nil {
		var ts int64
		fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &ts)
		age := time.Since(time.Unix(ts, 0))
		if age > staleThreshold {
			w := fmt.Sprintf("WATCHER DOWN - last beat %v ago (grace %v)", age.Round(time.Second), staleThreshold)
			warnings = append(warnings, w)
		}
	} else {
		warnings = append(warnings, "WATCHER NEVER STARTED")
	}

	// Check queued wakes
	qPath := filepath.Join(homeDir, wakeQueueFile)
	if fi, err := os.Stat(qPath); err == nil && fi.Size() > 0 {
		warnings = append(warnings, "QUEUED WAKES PENDING - drain with munsu wake-drain")
	}

	return warnings
}
