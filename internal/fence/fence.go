// Package fence confines a launched seat's whole process tree to a set of
// writable roots with an OS sandbox (macOS sandbox-exec), refuses every
// unix-socket connect but DNS's and a gate soldier's no-mistakes daemon's, and
// refuses the removal or replacement of a claude or tmux socket. It also
// reports every outbound connect to the Sandbox log: an observe-only (allow
// network-outbound (with report)) sits before the socket denies, which are
// last-match-wins, so it logs seat egress and refuses nothing. It is defense
// in depth beside the shell lexer and the git shim: those refuse by reading
// argv; the fence refuses at the syscall, so a wrapped program, an
// absolute-path git, or a reset PATH cannot write outside the roots.
//
// New validates one launch's facts and builds the profile. Wrap turns the launch
// argv into the fenced argv. Probe proves, before the launch is trusted, that
// the exact profile refuses a write it must refuse. A fence is never trusted
// without a successful Probe.
//
// The fence is darwin-only. On every other GOOS New refuses an unknown role,
// then returns *UnsupportedError, and Supported reports it without a launch;
// the caller refuses the launch unless config fence-unsupported=allow.
package fence

import "fmt"

// Role selects the allowed set.
type Role string

const (
	// RoleSoldier may write its bound worktree and the git paths a worktree's
	// commits need, never the primary checkout.
	RoleSoldier Role = "soldier"
	// RoleReviewer may not write anywhere in the checkout or worktree under
	// review, including every .git path; it keeps only seat state, temp roots
	// and its own files.
	RoleReviewer Role = "reviewer"
)

// Launch carries the facts one fenced launch needs. Every path must be absolute
// and exist (except Files and HarnessStateDir, whose parent must exist); New
// resolves symlinks, for Files in the parent only.
type Launch struct {
	Role    Role
	Harness string // harness kind; selects the harness state directories
	// Home is the munsu home. No seat writes anything in it but its own Files
	// (a soldier's status, ack log and current scout report) and its
	// HarnessStateDir. A reviewer may leave it empty when it has no
	// HarnessStateDir.
	Home string
	// UserHome is the Human's home as the launch intent recorded it; absolute.
	UserHome string
	Primary  string // primary checkout; never writable
	// Worktree is the soldier's bound worktree (writable) or the reviewer's
	// checkout under review (never writable).
	Worktree  string
	GitDir    string   // the worktree's git dir: <CommonDir>/worktrees/<name>
	CommonDir string   // the repository's common git dir
	Branch    string   // soldier only: the task-local branch, e.g. mu/<task>
	Files     []string // exact files also writable, with their atomic .tmp.<pid>.<hex> siblings; a soldier's own home files
	// HarnessStateDir is the per-launch harness state directory (the task's
	// PI_CODING_AGENT_DIR for pi, its CLAUDE_CONFIG_DIR for claude). It is
	// writable under both roles: a proper subdirectory of Home's state/, never a
	// home root and never overlapping the primary checkout, worktree, git dir or
	// common dir.
	HarnessStateDir string
	// GateRepo is the no-mistakes write set of a soldier that drives a gate
	// run: this project's gate repository, of which only objects/, refs/ and
	// logs/ are writable. It lies directly under NM_HOME/repos, and the seat
	// may connect to NM_HOME/socket. A reviewer has none.
	GateRepo string
	// WritableRoots are a soldier's extra writable directories from the project's
	// config (fence-writable-roots), checked by ValidateWritableRoots. Checkouts
	// are the other registered projects' checkout and git common dirs they must
	// also stay clear of. A reviewer has neither.
	WritableRoots []string
	Checkouts     []string
	// PathDirs are the directories on the seat's launch PATH. The seat can write
	// none of them, nor rename or replace one, whatever else it may write.
	PathDirs []string
	// ShimDir is the git shim directory on that PATH, or empty. It is refused
	// like a PATH dir, and every directory above it is locked against chmod,
	// rename and removal whatever else the seat may write.
	ShimDir string
}

// UnsupportedError is returned by New on a GOOS with no fence implementation.
type UnsupportedError struct{ GOOS string }

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("fence: no write fence implemented for GOOS %s", e.GOOS)
}

// Outcome is the observed result of one probe step.
type Outcome string

const (
	OutcomeAllowed       Outcome = "allowed"        // the control write to an allowed root succeeded
	OutcomeRefused       Outcome = "refused"        // the write failed with "Operation not permitted"
	OutcomeWritten       Outcome = "written"        // the write succeeded: the fence is open
	OutcomeControlFailed Outcome = "control-failed" // a write to an allowed root failed: the profile did not compile or the sandbox did not run
	OutcomeUnavailable   Outcome = "unavailable"    // sandbox-exec is missing
	OutcomeError         Outcome = "error"          // any other probe error
)

// ProbeStep is one command run under the profile and what it showed.
type ProbeStep struct {
	Command []string // sandbox-exec, then the profile digest in place of the profile text, then the probe
	Outcome Outcome
	Detail  string // stderr or the error text; empty for a refusal
}

// Evidence is the typed probe record for LaunchEvidence. The first step is the
// allowed-root control; every later step is a write the profile must refuse.
// Probe always returns it, including on failure.
type Evidence struct {
	Role          Role
	Harness       string
	ProfileDigest string   // sha256 hex of the profile text
	WritableRoots []string // the launch's resolved extra writable roots
	Steps         []ProbeStep
}
