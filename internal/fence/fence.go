// Package fence confines a launched seat's whole process tree to a set of
// writable roots with an OS sandbox (macOS sandbox-exec). It is defense in depth
// beside the shell lexer and the git shim (ADR-0005 §6, ADR-0014): those refuse
// by reading argv; the fence refuses at the syscall, so a wrapped program, an
// absolute-path git, or a reset PATH cannot write outside the roots.
//
// New validates one launch's facts and builds the profile. Wrap turns the launch
// argv into the fenced argv. Probe proves, before the launch is trusted, that
// the exact profile refuses a write it must refuse. A fence is never trusted
// without a successful Probe.
//
// The fence is darwin-only. On every other GOOS New returns *UnsupportedError;
// whether a launch then refuses or records "no fence" is the caller's decision.
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
// resolves symlinks.
type Launch struct {
	Role    Role
	Harness string // harness kind; selects the harness state directories
	// Home is the munsu home. A soldier's state/, data/, .journal/ and .lock/
	// stay writable. A reviewer's home is never writable: it is named only so
	// HarnessStateDir can be validated against it, and may be empty without one.
	Home    string
	Primary string // primary checkout; never writable
	// Worktree is the soldier's bound worktree (writable) or the reviewer's
	// checkout under review (never writable).
	Worktree  string
	GitDir    string   // the worktree's git dir: <CommonDir>/worktrees/<name>
	CommonDir string   // the repository's common git dir
	Branch    string   // soldier only: the task-local branch, e.g. mu/<task>
	Files     []string // exact files also writable, with their atomic .tmp.<pid>.<hex> siblings
	// HarnessStateDir is the per-launch harness state directory (for pi, the
	// task's PI_CODING_AGENT_DIR). It is writable under both roles: a proper
	// subdirectory of Home's state/, never a home root and never overlapping the
	// primary checkout, worktree, git dir or common dir. Only the pi kind has one.
	HarnessStateDir string
	// GateRepo and GateState are the no-mistakes write set of a soldier that
	// drives a gate run: this project's gate repository (writable subtree) and
	// the gate's state database (writable with its -wal, -shm and -journal
	// files). Both or neither; a reviewer has none.
	GateRepo  string
	GateState string
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
	ProfileDigest string // sha256 hex of the profile text
	Steps         []ProbeStep
}
