// Package decisionhold implements the decision-hold lifecycle for durable
// unresolved captain decisions discovered during investigations or reviews.
//
// Hold identity: <origin-id>-decision-<decision-key>
// Operations map to the existing task status/backlog conventions:
//   - Hold      → appends "needs-decision: ..." to the originating task status
//   - Complete  → records completion attestation on the originating task
//   - Verify    → read-only check that no stale needs-decision lines remain
//   - Resolve   → records the answer and unblocks dependent work
package decisionhold

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/task"
)

// HoldID returns the stable identity for a decision hold.
// Both originID and decisionKey must be non-empty privacy-safe slugs.
func HoldID(originID, decisionKey string) string {
	return originID + "-decision-" + decisionKey
}

// Decision describes a single captain decision hold.
type Decision struct {
	// OriginID is the task that discovered this decision.
	OriginID string
	// DecisionKey is the stable identifier for this decision.
	DecisionKey string
	// Reason is the one-line summary of the decision needed.
	Reason string
}

// HoldResult describes the outcome of a hold operation.
type HoldResult struct {
	HoldID string
	// Created is true when this hold is new (not already tracked).
	Created bool
}

// holdsPath returns the path to the holds directory under homeDir.
func holdsDir(homeDir string) string {
	return filepath.Join(homeDir, "holds")
}

// holdFilePath returns the path to the hold metadata file.
func holdFilePath(homeDir, originID, decisionKey string) string {
	return filepath.Join(holdsDir(homeDir), HoldID(originID, decisionKey)+".hold")
}

// Create records a new decision hold by appending a needs-decision status line
// to the originating task. Idempotent: repeating with the same originID and
// decisionKey is a no-op (returns Created=false).
func Create(homeDir, originID, decisionKey, reason string) (*HoldResult, error) {
	if originID == "" {
		return nil, fmt.Errorf("origin-id must not be empty")
	}
	if decisionKey == "" {
		return nil, fmt.Errorf("decision-key must not be empty")
	}
	if reason == "" {
		return nil, fmt.Errorf("reason must not be empty")
	}

	hid := HoldID(originID, decisionKey)
	holdFile := holdFilePath(homeDir, originID, decisionKey)

	// Idempotency check: if hold file already exists, this is a no-op.
	if _, err := os.Stat(holdFile); err == nil {
		return &HoldResult{HoldID: hid, Created: false}, nil
	}

	// Ensure holds directory exists.
	if err := os.MkdirAll(holdsDir(homeDir), 0755); err != nil {
		return nil, fmt.Errorf("creating holds directory: %w", err)
	}

	// Write the hold metadata file.
	content := fmt.Sprintf("origin-id=%s\ndecision-key=%s\nreason=%s\n", originID, decisionKey, reason)
	if err := os.WriteFile(holdFile, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("writing hold file: %w", err)
	}

	// Append needs-decision status to the originating task.
	statusLine := fmt.Sprintf("needs-decision: %s [key=%s]", reason, decisionKey)
	if err := task.AppendStatus(homeDir, originID, statusLine); err != nil {
		return nil, fmt.Errorf("appending needs-decision status: %w", err)
	}

	return &HoldResult{HoldID: hid, Created: true}, nil
}

// DecisionHold represents a single hold record loaded from disk.
type DecisionHold struct {
	OriginID    string
	DecisionKey string
	Reason      string
	// Resolved is true when the answer has been recorded.
	Resolved bool
	// Answer is the captain's decision, empty when unresolved.
	Answer string
}

// ListUnresolved returns all unresolved decision holds for an origin task.
// A hold is unresolved if it has no .resolved counterpart file.
func ListUnresolved(homeDir, originID string) ([]DecisionHold, error) {
	if originID == "" {
		return nil, fmt.Errorf("origin-id must not be empty")
	}

	dh := holdsDir(homeDir)
	dir, err := os.Open(dh)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no holds directory means no holds
		}
		return nil, fmt.Errorf("opening holds directory: %w", err)
	}
	defer dir.Close()

	entries, err := dir.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("reading holds directory: %w", err)
	}

	prefix := originID + "-decision-"
	var holds []DecisionHold
	for _, fi := range entries {
		name := fi.Name()
		if !strings.HasSuffix(name, ".hold") {
			continue
		}
		if strings.HasSuffix(name, ".hold.resolved") {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".hold")
		if key == "" {
			continue
		}

		hold, err := loadHold(homeDir, originID, key)
		if err != nil {
			continue // skip unreadable holds
		}
		if hold.Resolved {
			continue // skip resolved holds
		}
		holds = append(holds, *hold)
	}
	return holds, nil
}

// loadHold reads a single hold record from disk.
func loadHold(homeDir, originID, decisionKey string) (*DecisionHold, error) {
	holdFile := holdFilePath(homeDir, originID, decisionKey)
	data, err := os.ReadFile(holdFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("hold %q not found", HoldID(originID, decisionKey))
		}
		return nil, err
	}

	d := &DecisionHold{
		OriginID:    originID,
		DecisionKey: decisionKey,
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "reason":
			d.Reason = strings.TrimSpace(v)
		case "answer":
			d.Answer = strings.TrimSpace(v)
		}
	}

	// Check if resolved file exists.
	resolvedFile := holdFilePath(homeDir, originID, decisionKey) + ".resolved"
	if _, err := os.Stat(resolvedFile); err == nil {
		d.Resolved = true
		resolvedData, err := os.ReadFile(resolvedFile)
		if err == nil {
			// Try to extract answer from resolved file.
			rs := bufio.NewScanner(strings.NewReader(string(resolvedData)))
			for rs.Scan() {
				line := strings.TrimSpace(rs.Text())
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				if strings.TrimSpace(k) == "answer" && d.Answer == "" {
					d.Answer = strings.TrimSpace(v)
				}
			}
		}
	}

	return d, nil
}

// Verify checks that the originating task has no stale needs-decision status
// lines for the given decision keys. Returns a list of unresolved keys.
// When keys is nil, checks all holds for the originID.
func Verify(homeDir, originID string, keys []string) ([]string, error) {
	if originID == "" {
		return nil, fmt.Errorf("origin-id must not be empty")
	}

	// Collect the set of decision keys to check.
	var checkKeys []string
	if len(keys) > 0 {
		checkKeys = keys
	} else {
		allHolds, err := listAllHolds(homeDir, originID)
		if err != nil {
			return nil, err
		}
		for _, h := range allHolds {
			checkKeys = append(checkKeys, h.DecisionKey)
		}
	}

	if len(checkKeys) == 0 {
		return nil, nil
	}

	// Read status once.
	statusLines, err := task.ReadStatus(homeDir, originID)
	if err != nil {
		return nil, fmt.Errorf("reading status for %s: %w", originID, err)
	}

	// Build resolved/needs-decision maps from status lines.
	resolvedMap := make(map[string]bool)
	needsDecisionMap := make(map[string]bool)
	for _, line := range statusLines {
		_, key := task.ParseStatusKey(line)
		if key == "" {
			continue
		}
		if strings.HasPrefix(line, "resolved:") {
			resolvedMap[key] = true
		}
		if strings.HasPrefix(line, "needs-decision:") {
			needsDecisionMap[key] = true
		}
	}

	// Check disk-only resolved status for keys not in any status line.
	for _, k := range checkKeys {
		if !resolvedMap[k] && !needsDecisionMap[k] {
			_, resolved, err := ReadResolution(homeDir, originID, k)
			if err == nil && resolved {
				resolvedMap[k] = true
			}
		}
	}

	var unresolvedKeys []string
	for _, k := range checkKeys {
		if resolvedMap[k] {
			continue
		}
		if needsDecisionMap[k] {
			unresolvedKeys = append(unresolvedKeys, k)
		}
	}

	return unresolvedKeys, nil
}

// listAllHolds returns all decision holds (resolved and unresolved) for an origin.
func listAllHolds(homeDir, originID string) ([]DecisionHold, error) {
	dh := holdsDir(homeDir)
	dir, err := os.Open(dh)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer dir.Close()

	entries, err := dir.Readdir(-1)
	if err != nil {
		return nil, err
	}

	prefix := originID + "-decision-"
	var holds []DecisionHold
	seen := make(map[string]bool)

	for _, fi := range entries {
		name := fi.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		// Strip suffix: .hold or .hold.resolved
		key := name
		key = strings.TrimSuffix(key, ".resolved")
		key = strings.TrimSuffix(key, ".hold")
		key = strings.TrimPrefix(key, prefix)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true

		hold, err := loadHold(homeDir, originID, key)
		if err != nil {
			continue
		}
		holds = append(holds, *hold)
	}
	return holds, nil
}

// Complete marks that the given decision keys for an origin task have been
// fully processed. When keys contains a single element "--none", it is an
// explicit semantic attestation that the reviewed surface has no unresolved
// captain decision.
func Complete(homeDir, originID string, keys []string) error {
	if originID == "" {
		return fmt.Errorf("origin-id must not be empty")
	}
	if len(keys) == 0 {
		return fmt.Errorf("at least one key or --none required")
	}

	// --none: attest that no decisions were found.
	if len(keys) == 1 && keys[0] == "--none" {
		// Write a completion marker with no decisions.
		completeFile := filepath.Join(holdsDir(homeDir), originID+".complete")
		if err := os.MkdirAll(holdsDir(homeDir), 0755); err != nil {
			return fmt.Errorf("creating holds directory: %w", err)
		}
		content := "none=true\n"
		if err := os.WriteFile(completeFile, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing completion file: %w", err)
		}
		return nil
	}

	// Verify each key has a hold and mark them complete.
	for _, key := range keys {
		_, err := loadHold(homeDir, originID, key)
		if err != nil {
			return fmt.Errorf("hold %s for key %q: %w", HoldID(originID, key), key, err)
		}
		if err := writeAnswer(homeDir, originID, key, "recorded (decision noted)"); err != nil {
			return err
		}
	}
	return nil
}

// writeAnswer writes a resolved file for the hold and appends a resolved status
// to the originating task.
func writeAnswer(homeDir, originID, decisionKey, answer string) error {
	resolvedFile := holdFilePath(homeDir, originID, decisionKey) + ".resolved"
	content := fmt.Sprintf("answer=%s\n", answer)
	if err := os.WriteFile(resolvedFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing resolved file: %w", err)
	}
	return nil
}

// Resolve records the captain's answer for a decision hold and unblocks
// dependent tasks. The unblockDeps list contains task IDs that were blocked
// on this decision and should be marked ready.
func Resolve(homeDir, originID, decisionKey, answer string, unblockDeps []string) error {
	if originID == "" {
		return fmt.Errorf("origin-id must not be empty")
	}
	if decisionKey == "" {
		return fmt.Errorf("decision-key must not be empty")
	}
	if answer == "" {
		return fmt.Errorf("answer must not be empty")
	}

	// Verify the hold exists.
	_, err := loadHold(homeDir, originID, decisionKey)
	if err != nil {
		return fmt.Errorf("resolving hold %s: %w", HoldID(originID, decisionKey), err)
	}

	// Write resolved marker.
	if err := writeAnswer(homeDir, originID, decisionKey, answer); err != nil {
		return err
	}

	// Append resolved status to the originating task.
	statusLine := fmt.Sprintf("resolved: %s [key=%s]", answer, decisionKey)
	if err := task.AppendStatus(homeDir, originID, statusLine); err != nil {
		return fmt.Errorf("appending resolved status: %w", err)
	}

	// Unblock dependent tasks.
	for _, depID := range unblockDeps {
		if depID == "" {
			continue
		}
		if err := task.AppendStatus(homeDir, depID, fmt.Sprintf("unblocked: decision resolved [key=%s]", decisionKey)); err != nil {
			return fmt.Errorf("unblocking %s: %w", depID, err)
		}
	}

	return nil
}

// ReadResolution reads the captain's answer for a resolved hold.
// Returns nil if the hold is unresolved or does not exist.
func ReadResolution(homeDir, originID, decisionKey string) (answer string, resolved bool, err error) {
	hold, err := loadHold(homeDir, originID, decisionKey)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return "", false, nil
		}
		return "", false, err
	}
	return hold.Answer, hold.Resolved, nil
}
