package home

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const wakeResolutionDir = "state/.wake-resolutions"

type wakeResolutionRecord struct {
	LeaseID   string `json:"lease_id"`
	EventID   string `json:"event_id"`
	Summary   string `json:"summary"`
	State     string `json:"state"`
	UpdatedAt int64  `json:"updated_at"`
}

func ResolveWake(homeDir, leaseID, eventID, summary string) error {
	leaseID = strings.TrimSpace(leaseID)
	eventID = strings.TrimSpace(eventID)
	summary = strings.TrimSpace(summary)
	if leaseID == "" || eventID == "" || summary == "" {
		return fmt.Errorf("claim-id, event-id, and summary are required")
	}
	record, _ := readWakeResolution(homeDir, leaseID, eventID)
	if record != nil && record.State == "completed" {
		return nil
	}
	if record == nil {
		found, err := leaseContainsEvent(homeDir, leaseID, eventID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("event %q is not present in lease %q", eventID, leaseID)
		}
		if err := writeWakeResolution(homeDir, wakeResolutionRecord{LeaseID: leaseID, EventID: eventID, Summary: summary, State: "prepared", UpdatedAt: time.Now().Unix()}); err != nil {
			return err
		}
	}
	found, err := leaseContainsEvent(homeDir, leaseID, eventID)
	if err != nil && !strings.Contains(err.Error(), "not found or expired") {
		return err
	}
	if record != nil && record.State == "prepared" && !found {
		elsewhere, findErr := wakeEventExists(homeDir, eventID)
		if findErr != nil {
			return findErr
		}
		if elsewhere {
			_ = os.Remove(resolutionPath(homeDir, leaseID, eventID))
			return fmt.Errorf("event %q was reclaimed and remains pending", eventID)
		}
	}
	if found {
		if err := AckWakes(homeDir, leaseID, []string{eventID}); err != nil {
			_ = os.Remove(resolutionPath(homeDir, leaseID, eventID))
			return err
		}
	}
	return writeWakeResolution(homeDir, wakeResolutionRecord{LeaseID: leaseID, EventID: eventID, Summary: summary, State: "completed", UpdatedAt: time.Now().Unix()})
}

func leaseContainsEvent(homeDir, leaseID, eventID string) (bool, error) {
	f, err := os.Open(LeaseFilePath(homeDir, leaseID))
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("lease %q not found or expired", leaseID)
		}
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return false, fmt.Errorf("lease %q is empty", leaseID)
	}
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 3)
		if len(parts) >= 2 && parts[0]+":"+parts[1] == eventID {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func resolutionPath(homeDir, leaseID, eventID string) string {
	return filepath.Join(homeDir, wakeResolutionDir, resolutionFileName(leaseID, eventID))
}

// resolutionFileName maps one wake resolution to its durable file name.
func resolutionFileName(leaseID, eventID string) string {
	return strings.NewReplacer("/", "_", ":", "_").Replace(leaseID + "-" + eventID + ".json")
}

func readWakeResolution(homeDir, leaseID, eventID string) (*wakeResolutionRecord, error) {
	data, err := os.ReadFile(resolutionPath(homeDir, leaseID, eventID))
	if err != nil {
		return nil, err
	}
	var record wakeResolutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func writeWakeResolution(homeDir string, record wakeResolutionRecord) error {
	path := resolutionPath(homeDir, record.LeaseID, record.EventID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func wakeEventExists(homeDir, eventID string) (bool, error) {
	if data, err := os.ReadFile(WakeQueuePath(homeDir)); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) >= 2 && parts[0]+":"+parts[1] == eventID {
				return true, nil
			}
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	entries, err := os.ReadDir(LeaseDir(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		found, err := leaseContainsEvent(homeDir, entry.Name(), eventID)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func wakeResolutionCompleted(homeDir, eventID string) bool {
	entries, err := os.ReadDir(filepath.Join(homeDir, wakeResolutionDir))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(homeDir, wakeResolutionDir, entry.Name()))
		if err != nil {
			continue
		}
		var record wakeResolutionRecord
		if json.Unmarshal(data, &record) == nil && record.EventID == eventID && record.State == "completed" {
			return true
		}
	}
	return false
}
