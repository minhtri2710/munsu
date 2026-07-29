package home

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const wakeResolutionLog = ".wake-resolutions"

func ResolveWake(homeDir, leaseID, eventID, summary string) error {
	leaseID = strings.TrimSpace(leaseID)
	eventID = strings.TrimSpace(eventID)
	summary = strings.TrimSpace(summary)
	if leaseID == "" || eventID == "" || summary == "" {
		return fmt.Errorf("claim-id, event-id, and summary are required")
	}
	if wakeResolutionExists(homeDir, leaseID, eventID) {
		return nil
	}
	found, err := leaseContainsEvent(homeDir, leaseID, eventID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("event %q is not present in lease %q", eventID, leaseID)
	}
	if err := AckWakes(homeDir, leaseID, []string{eventID}); err != nil {
		return err
	}
	path := filepath.Join(homeDir, "state", wakeResolutionLog)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	line := fmt.Sprintf("%d\t%s\t%s\t%s\n", time.Now().Unix(), leaseID, eventID, strings.ReplaceAll(summary, "\n", " "))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
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

func wakeResolutionExists(homeDir, leaseID, eventID string) bool {
	f, err := os.Open(filepath.Join(homeDir, "state", wakeResolutionLog))
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 4)
		if len(parts) >= 3 && parts[1] == leaseID && parts[2] == eventID {
			return true
		}
	}
	return false
}
