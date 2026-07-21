package supervision

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/hometag"
)

// ProtocolVersion identifies the watcher protocol format.
// Bump this when the identity/lease contract changes in a backward-incompatible way.
// ProtocolVersion identifies the watcher protocol format.
// Bump this when the identity/lease contract changes in a backward-incompatible way.
const ProtocolVersion = 1

// CommitSHAFormat is the length of the canonical commit SHA stored in
// the identity's CommitSHA field. Short SHAs (7+ chars) are stored as-is;
// full 40-char SHAs are also valid. Comparison uses unambiguous prefix matching.
const CommitSHAFormat = "short"

// BuildIdentity is a comparable build identity backed by a verified commit SHA.
// Two identities match if one's SHA is a prefix of the other (unambiguous
// prefix match). Zero value never matches anything.
type BuildIdentity struct {
	sha string
}

// NewBuildIdentity creates a BuildIdentity from a commit SHA.
func NewBuildIdentity(sha string) BuildIdentity {
	return BuildIdentity{sha: strings.TrimSpace(sha)}
}

// IsZero returns true when the identity is unset.
func (b BuildIdentity) IsZero() bool { return b.sha == "" }

// String returns the commit SHA for display, or "unknown" for zero values.
func (b BuildIdentity) String() string {
	if b.IsZero() {
		return "unknown"
	}
	return b.sha
}

// Matches reports whether this build identity matches another.
// Short and full SHAs match when one is an unambiguous prefix of the other.
// Never matches if either identity is zero.
func (b BuildIdentity) Matches(other BuildIdentity) bool {
	if b.IsZero() || other.IsZero() {
		return false
	}
	a, b2 := b.sha, other.sha
	return strings.HasPrefix(a, b2) || strings.HasPrefix(b2, a)
}

// WatcherIdentity holds verified metadata for one watcher process.
// It is persisted at start time and read for ownership validation.
type WatcherIdentity struct {
	Home            string `json:"home"`
	PID             int    `json:"pid"`
	ProcessStart    string `json:"process_start"` // unix nanos recorded at start
	Executable      string `json:"executable"`
	BuildVersion    string `json:"build_version"`
	ProtocolVersion int    `json:"protocol_version"`
	StartTime       int64  `json:"start_time"` // unix captains, for human-readable age
	CommitSHA       string `json:"commit_sha"` // verified commit SHA for identity comparison
}

// BuildIdentity returns a typed BuildIdentity from the watcher identity's CommitSHA.
func (id *WatcherIdentity) BuildIdentity() BuildIdentity {
	if id == nil {
		return BuildIdentity{}
	}
	return NewBuildIdentity(id.CommitSHA)
}

// identityPath returns the path to the watcher identity file.
func identityPath(homeDir string) string {
	return filepath.Join(homeDir, "state", ".watcher-identity")
}

// BuildVersion holds the watcher build version for human-readable display.
// It is propagated from cli.Version at startup via the binary's ldflags.
// Tests must set this explicitly when checking identity values.
var BuildVersion = "0.0.0-dev"

// CommitSHA holds the verified commit SHA for this build, set via ldflags
// at build time (e.g. -X supervision.CommitSHA=abc1234).
// Used for typed identity comparison in the watcher handshake.
// Empty means the commit is unknown — comparisons fail closed.
var CommitSHA = ""

// NewIdentity builds a WatcherIdentity for the current process.
// BuildVersion is read from the package-level variable, which is propagated
// from cli.Version via ldflags. CommitSHA is read from the package-level
// variable, also set via ldflags. Home is stored in canonical form so
// ownership checks compare equal across path aliases (symlink, relative, Abs).
func NewIdentity(homeDir string) WatcherIdentity {
	bv := BuildVersion
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		bv = info.Main.Version
	}
	cs := CommitSHA
	if info, ok := debug.ReadBuildInfo(); ok && cs == "" && info.Settings != nil {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				cs = s.Value
				break
			}
		}
	}
	executable, processStart, err := processIdentity(os.Getpid())
	if err != nil {
		executable = "unknown"
		processStart = "unknown"
	}
	now := time.Now()
	return WatcherIdentity{
		Home:            hometag.Canonical(homeDir),
		PID:             os.Getpid(),
		ProcessStart:    processStart,
		Executable:      executable,
		BuildVersion:    bv,
		ProtocolVersion: ProtocolVersion,
		StartTime:       now.Unix(),
		CommitSHA:       cs,
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

// ClearIdentityIfMatches removes only the identity written by this process generation.
func ClearIdentityIfMatches(homeDir string, expected WatcherIdentity) {
	current := ReadIdentity(homeDir)
	if current == nil || current.PID != expected.PID || current.ProcessStart != expected.ProcessStart {
		return
	}
	os.Remove(identityPath(homeDir))
}

// formatIdentity serialises a WatcherIdentity to a tab-delimited line.
// Protocol v1: 7 fields (no CommitSHA).
// Protocol v2: 8 fields (includes CommitSHA as last field).
func formatIdentity(id WatcherIdentity) string {
	return fmt.Sprintf("%s\t%d\t%s\t%s\t%s\t%d\t%d\t%s\n",
		id.Home, id.PID, id.ProcessStart, id.Executable,
		id.BuildVersion, id.ProtocolVersion, id.StartTime, id.CommitSHA)
}

// parseIdentity deserialises a tab-delimited identity line.
// Handles both 7-field (protocol v1) and 8-field (protocol v2) formats.
func parseIdentity(data string) *WatcherIdentity {
	line := strings.TrimSpace(data)
	parts := strings.SplitN(line, "\t", 8)
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
	// 8th field is CommitSHA (protocol v2); empty if not present.
	if len(parts) >= 8 {
		id.CommitSHA = parts[7]
	}
	return id
}

// ValidatePIDOwnership checks whether the given PID provably belongs to the
// watcher that wrote the identity file. Returns true only when all of:
//   - The identity file exists and was written by a process with the same PID
//   - The identity home matches the home being operated on (no cross-home)
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
	// Identity must bind to this home. Reject copied identity files and
	// prevent ensure/stop from treating another home's watcher as local.
	if id.Home == "" || hometag.Canonical(id.Home) != hometag.Canonical(homeDir) {
		return false
	}
	if id.Executable == "" || id.Executable == "unknown" || id.ProcessStart == "" || id.ProcessStart == "unknown" {
		return false
	}
	executable, processStart, err := processIdentity(pid)
	if err != nil {
		return false
	}
	if executable != id.Executable || processStart != id.ProcessStart {
		return false
	}
	return true
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
	cs := id.CommitSHA
	if cs == "" {
		cs = "-"
	}
	return fmt.Sprintf("pid=%d version=%s commit=%s proto=%d age=%s home=%s",
		id.PID, id.BuildVersion, cs, id.ProtocolVersion, age, id.Home)
}
