package supervision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// ProtocolVersion identifies the watcher protocol format.
// Bump this when the identity/lease contract changes in a backward-incompatible way.
const ProtocolVersion = 1

// WatcherIdentity holds verified metadata for one watcher process.
// It is persisted at start time and read for ownership validation.
type WatcherIdentity struct {
	Home            string `json:"home"`
	PID             int    `json:"pid"`
	ProcessStart    string `json:"process_start"` // unix nanos recorded at start
	Executable      string `json:"executable"`
	BuildVersion    string `json:"build_version"`
	ProtocolVersion int    `json:"protocol_version"`
	StartTime       int64  `json:"start_time"` // unix seconds, for human-readable age
}

// identityPath returns the path to the watcher identity file.
func identityPath(homeDir string) string {
	return filepath.Join(homeDir, "state", ".watcher-identity")
}

// BuildVersion holds the watcher build version. It is set by the CLI layer at
// init time to avoid circular dependencies (supervision cannot import cli).
// Tests must set this explicitly when checking identity values.
var BuildVersion = "0.0.0-dev"

// NewIdentity builds a WatcherIdentity for the current process.
// BuildVersion is read from the package-level variable, which the CLI layer
// sets at init time.
func NewIdentity(homeDir string) WatcherIdentity {
	bv := BuildVersion
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		bv = info.Main.Version
	}
	return WatcherIdentity{
		Home:            homeDir,
		PID:             os.Getpid(),
		ProcessStart:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Executable:      resolveExecPath(),
		BuildVersion:    bv,
		ProtocolVersion: ProtocolVersion,
		StartTime:       time.Now().Unix(),
	}
}

// resolveExecPath returns the resolved executable path, or "unknown" on failure.
func resolveExecPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	// Resolve symlinks to get the real binary path.
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	return resolved
}

// WriteIdentity persists the watcher identity atomically to the state directory.
func WriteIdentity(homeDir string, id WatcherIdentity) error {
	path := identityPath(homeDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating identity directory: %w", err)
	}
	content := formatIdentity(id)
	// Write via temp file + rename for atomicity
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing identity temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming identity file: %w", err)
	}
	return nil
}

// ReadIdentity reads the watcher identity from the state directory.
// Returns nil if no identity file exists or it cannot be parsed.
func ReadIdentity(homeDir string) *WatcherIdentity {
	data, err := os.ReadFile(identityPath(homeDir))
	if err != nil {
		return nil
	}
	return parseIdentity(string(data))
}

// ClearIdentity removes the watcher identity file.
func ClearIdentity(homeDir string) {
	os.Remove(identityPath(homeDir))
}

// formatIdentity serialises a WatcherIdentity to a tab-delimited line.
func formatIdentity(id WatcherIdentity) string {
	return fmt.Sprintf("%s\t%d\t%s\t%s\t%s\t%d\t%d\n",
		id.Home, id.PID, id.ProcessStart, id.Executable,
		id.BuildVersion, id.ProtocolVersion, id.StartTime)
}

// parseIdentity deserialises a tab-delimited identity line.
func parseIdentity(data string) *WatcherIdentity {
	line := strings.TrimSpace(data)
	parts := strings.SplitN(line, "\t", 7)
	if len(parts) < 7 {
		return nil
	}
	id := &WatcherIdentity{
		Home:            parts[0],
		Executable:      parts[3],
		BuildVersion:    parts[4],
		ProtocolVersion: 0,
		StartTime:       0,
	}
	fmt.Sscanf(parts[1], "%d", &id.PID)
	id.ProcessStart = parts[2]
	fmt.Sscanf(parts[5], "%d", &id.ProtocolVersion)
	fmt.Sscanf(parts[6], "%d", &id.StartTime)
	return id
}

// ValidatePIDOwnership checks whether the given PID provably belongs to the
// watcher that wrote the identity file. Returns true only when all of:
//   - The identity file exists and was written by a process with the same PID
//   - The claimed executable matches the one currently running at that PID
//   - The process identified by the PID is still alive
//
// When evidence is ambiguous, returns false (fail-closed).
func ValidatePIDOwnership(homeDir string, pid int) bool {
	id := ReadIdentity(homeDir)
	if id == nil {
		return false
	}
	if id.PID != pid {
		return false
	}
	// Check that the process at pid is still alive.
	if !isProcessAlive(pid) {
		return false
	}
	// Verify the executable at this PID matches our recorded executable.
	// Use the same resolution as resolveExecPath for consistent comparison.
	exePath := resolveExecPath()
	if exePath == "unknown" {
		// Cannot verify — fail closed.
		return false
	}
	if exePath != id.Executable {
		// The running binary path doesn't match — could be a different install.
		return false
	}
	return true
}

// isProcessAlive checks whether a process with the given PID is running.
// Uses kill -0 which tests existence without sending a signal.
func isProcessAlive(pid int) bool {
	cmd := exec.Command("kill", "-0", fmt.Sprintf("%d", pid))
	return cmd.Run() == nil
}

// IdentitySummary returns a human-readable summary of the watcher identity.
func IdentitySummary(id *WatcherIdentity) string {
	if id == nil {
		return "no watcher identity"
	}
	age := "unknown"
	if id.StartTime > 0 {
		age = time.Since(time.Unix(id.StartTime, 0)).Round(time.Second).String()
	}
	return fmt.Sprintf("pid=%d version=%s proto=%d age=%s home=%s",
		id.PID, id.BuildVersion, id.ProtocolVersion, age, id.Home)
}
