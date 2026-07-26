// Package domain defines core munsu domain types, task state reading/writing,
// event stream folding, and delivery business rules.
package domain

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// --- Task State & Metadata ---

// StateDir returns the path to the state directory under the given homeDir.
func StateDir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

func metaPath(homeDir string, id string) (string, error) {
	return filepath.Join(StateDir(homeDir), id+".meta"), nil
}

func statusPath(homeDir string, id string) (string, error) {
	return filepath.Join(StateDir(homeDir), id+".status"), nil
}

// WriteMeta writes a task meta file at $MUNSU_HOME/state/<id>.meta.
func WriteMeta(homeDir string, id string, meta map[string]string) error {
	_, unlock, err := acquireMetaLock(homeDir, id)
	if err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	defer unlock()

	return writeMetaLocked(homeDir, id, meta)
}

func writeMetaLocked(homeDir string, id string, meta map[string]string) error {
	p, err := metaPath(homeDir, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	var b strings.Builder
	for k, v := range meta {
		b.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}
	tmpF, err := os.CreateTemp(filepath.Dir(p), id+".meta.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp meta file: %w", err)
	}
	tmpPath := tmpF.Name()
	if _, err := tmpF.WriteString(b.String()); err != nil {
		tmpF.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp meta file: %w", err)
	}
	if err := tmpF.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp meta file: %w", err)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp meta file: %w", err)
	}
	return nil
}

// ReadMeta reads a task meta file at $MUNSU_HOME/state/<id>.meta.
func ReadMeta(homeDir string, id string) (map[string]string, error) {
	p, err := metaPath(homeDir, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("reading task meta %s: %w", id, err)
	}
	defer f.Close()

	meta := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning task meta %s: %w", id, err)
	}
	return meta, nil
}

// AppendStatus appends a status line to $MUNSU_HOME/state/<id>.status with OS flock safety.
func AppendStatus(homeDir string, id, line string) error {
	p, err := statusPath(homeDir, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening status file: %w", err)
	}
	defer f.Close()

	// Atomic OS flock to prevent append clobbering by concurrent processes
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err == nil {
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}

	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("writing status line: %w", err)
	}
	return nil
}

// ReadStatus reads all status lines from $MUNSU_HOME/state/<id>.status.
func ReadStatus(homeDir string, id string) ([]string, error) {
	p, err := statusPath(homeDir, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading status %s: %w", id, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// ParseStatusKey extracts an optional [key=<slug>] annotation from a status message.
func ParseStatusKey(line string) (message, key string) {
	startMarker := " [key="
	idx := strings.LastIndex(line, startMarker)
	if idx < 0 {
		startMarker = "[key="
		idx = strings.LastIndex(line, startMarker)
	}
	if idx >= 0 {
		end := strings.Index(line[idx+len(startMarker):], "]")
		if end >= 0 {
			keyVal := line[idx+len(startMarker) : idx+len(startMarker)+end]
			if keyVal != "" {
				key = keyVal
				message = strings.TrimSpace(line[:idx])
				return
			}
		}
	}
	return line, ""
}

// --- Compare-and-Swap Meta Lock ---

type CASError struct {
	Key      string
	Expected string
	Actual   string
}

func (e *CASError) Error() string {
	return fmt.Sprintf("cas conflict: key %q expected %q but got %q", e.Key, e.Expected, e.Actual)
}

func lockPath(homeDir, id string) string {
	p, _ := metaPath(homeDir, id)
	return p + ".lock"
}

func acquireMetaLock(homeDir, id string) (*os.File, func(), error) {
	lp := lockPath(homeDir, id)
	if err := os.MkdirAll(filepath.Dir(lp), 0755); err != nil {
		return nil, nil, fmt.Errorf("creating state directory for lock: %w", err)
	}
	f, err := os.OpenFile(lp, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("acquiring flock: %w", err)
	}
	return f, func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func CompareAndSwapMeta(homeDir, id string, checks, updates map[string]string) (map[string]string, error) {
	_, unlock, err := acquireMetaLock(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("cas: %w", err)
	}
	defer unlock()

	meta, err := ReadMeta(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("cas: reading meta: %w", err)
	}

	for k, expectedV := range checks {
		actualV, ok := meta[k]
		if !ok {
			if expectedV == "" {
				continue
			}
			return nil, &CASError{Key: k, Expected: expectedV, Actual: actualV}
		}
		if actualV != expectedV {
			return nil, &CASError{Key: k, Expected: expectedV, Actual: actualV}
		}
	}

	for k, v := range updates {
		meta[k] = v
	}

	if err := writeMetaLocked(homeDir, id, meta); err != nil {
		return nil, fmt.Errorf("cas: writing meta: %w", err)
	}

	return meta, nil
}

// --- Delivery Domain Models ---

type PRStatus string

const (
	PROpen   PRStatus = "open"
	PRClosed PRStatus = "closed"
	PRMerged PRStatus = "merged"
)

type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckFailed  CheckStatus = "failed"
	CheckPending CheckStatus = "pending"
	CheckSkipped CheckStatus = "skipped"
)

type ReviewState string

const (
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes-requested"
	ReviewPending          ReviewState = "pending"
	ReviewDismissed        ReviewState = "dismissed"
)

type CheckRun struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
}

type Review struct {
	State ReviewState `json:"state"`
	Body  string      `json:"body"`
}

type PR struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Status     PRStatus   `json:"status"`
	BaseBranch string     `json:"baseBranch"`
	HeadBranch string     `json:"headBranch"`
	Checks     []CheckRun `json:"checks,omitempty"`
	Reviews    []Review   `json:"reviews,omitempty"`
}

func (pr PR) CanMerge() bool {
	if pr.Status != PROpen {
		return false
	}
	for _, c := range pr.Checks {
		if c.Status == CheckFailed {
			return false
		}
	}
	hasApproval := false
	for _, r := range pr.Reviews {
		switch r.State {
		case ReviewChangesRequested:
			return false
		case ReviewApproved:
			hasApproval = true
		}
	}
	return hasApproval
}

func (r Review) IsApproving() bool {
	return r.State == ReviewApproved
}

type DeliveryIdentity struct {
	Provider   string `json:"provider"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	BaseRef    string `json:"baseRef"`
	HeadRef    string `json:"headRef"`
	HeadSHA    string `json:"headSHA"`
	CapturedAt string `json:"capturedAt"`
}

func ValidateIdentity(id *DeliveryIdentity) error {
	switch {
	case id == nil:
		return fmt.Errorf("delivery identity is nil")
	case id.Provider == "":
		return fmt.Errorf("delivery identity: provider is required")
	case id.Owner == "":
		return fmt.Errorf("delivery identity: owner is required")
	case id.Repo == "":
		return fmt.Errorf("delivery identity: repo is required")
	case id.Number <= 0:
		return fmt.Errorf("delivery identity: PR number must be positive, got %d", id.Number)
	case id.URL == "":
		return fmt.Errorf("delivery identity: URL is required")
	case id.BaseRef == "":
		return fmt.Errorf("delivery identity: baseRef is required")
	case id.HeadRef == "":
		return fmt.Errorf("delivery identity: headRef is required")
	case id.HeadSHA == "":
		return fmt.Errorf("delivery identity: headSHA is required")
	case id.CapturedAt == "":
		return fmt.Errorf("delivery identity: capturedAt is required")
	}
	return nil
}
