// Package waker handles durable wake queue operations and guard checks.
package waker

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	wakeQueueFile   = "state/.wake-queue"
	watcherBeatFile = "state/.last-watcher-beat"
	staleThreshold  = 300 * time.Second
)

// Record is a single wake queue entry.
type Record struct {
	Epoch   string
	Seq     string
	Kind    string
	Key     string
	Payload string
}

// Drain reads and clears the wake queue.
func Drain(homeDir string) ([]Record, error) {
	qPath := filepath.Join(homeDir, wakeQueueFile)

	f, err := os.Open(qPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []Record
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		records = append(records, Record{
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

// PrintRecords prints drained records in a readable format.
func PrintRecords(records []Record) {
	for _, r := range records {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", r.Epoch, r.Seq, r.Kind, r.Key, r.Payload)
	}
}

// CheckGuard checks for operational warnings.
func CheckGuard(homeDir string) []string {
	var warnings []string

	// Check watcher liveness
	beatFile := filepath.Join(homeDir, watcherBeatFile)
	if data, err := os.ReadFile(beatFile); err == nil {
		var ts int64
		fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &ts)
		age := time.Since(time.Unix(ts, 0))
		if age > staleThreshold {
			w := fmt.Sprintf("WATCHER BEACON STALE - last beat %v ago (grace %v)", age.Round(time.Second), staleThreshold)
			warnings = append(warnings, w)
		}
	} else {
		warnings = append(warnings, "WATCHER NEVER STARTED - no liveness beacon")
	}

	// Check queued wakes
	qPath := filepath.Join(homeDir, wakeQueueFile)
	if fi, err := os.Stat(qPath); err == nil && fi.Size() > 0 {
		warnings = append(warnings, "QUEUED WAKES PENDING - drain with munsu wake-drain")
	}

	// Print bordered warnings
	for _, w := range warnings {
		border := strings.Repeat("●", len(w)+4)
		fmt.Println(border)
		fmt.Println("● " + w + " ●")
		fmt.Println(border)
	}

	return warnings
}
