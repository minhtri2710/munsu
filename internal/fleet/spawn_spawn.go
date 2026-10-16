package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Args holds all input parameters for spawning a soldier.
type Args struct {
	ID              string
	ProjectName     string
	Yolo            bool
	Force           bool                       // --force flag; bypass captain task authority checks
	Backend         string                     // --backend flag value — optional assertion against the resolved project snapshot
	HarnessFlag     string                     // --harness flag value; empty = resolve from config
	ModelFlag       string                     // --model flag; empty = matched profile, then project model
	EffortFlag      string                     // --effort flag; empty = matched profile, else none
	TaskDescription string                     // optional dispatch matching text; empty = brief/id fallback
	HomeDir         string                     // if empty, resolved via home.Resolve
	Endpoints       EndpointCapabilities       // required endpoint lifecycle capability
	ArmFunc         func(homeDir string) error // ensures the home's watcher after launch; nil = none
	// Authority is the composed canonical Task Authority targeting the exact
	// home the Runner resolves (the CLI composition root supplies it from
	// Ctx.TaskAuthority(); tests inject a canonical home-backed Authority). It
	// owns the canonical spawn preconditions — readiness, the generation-
	// scoped worktree/endpoint bindings, and the durable Dispatch Holds that
	// gate the spawn action. It is required for the worktree binding cutover
	// (Task 4.1): bindWorktree fails closed when it is nil. Construction stays
	// side-effect free; no package global carries it.
	Authority *taskauthority.Canonical

	// IncarnationMint is an optional opaque incarnation generator for the
	// endpoint binding. When nil, a crypto/rand generator is used. Tests
	// inject a deterministic func. The generated value is persisted by
	// taskauthority as an opaque generation-bound identity and reused (not
	// re-minted) on retry/recovery of the same launch operation.
	IncarnationMint IncarnationMintFunc
}

// Run executes the full spawn orchestration sequence by delegating to Runner.
//
//	resolve home → validate → brief exists → project path → worktree.AssertNotTangled
//	→ worktree.Get → resolve harness → model/effort → write .soldier-launch.sh + .soldier-brief.md + meta
//	→ start session → send brief → arm watcher
//
// On error after worktree lease, the worktree is returned to the pool (fail-closed).
func Spawn(args Args) (string, error) {
	return NewRunner(args).Run()
}

// ResolveBriefProject resolves one declared project's immutable snapshot once
// and returns the delivery mode derived from its captured forge step, plus the
// project's tamper check. When selectMode is false, the task's
// recorded DeliveryContract owns the delivery mode and only the tamper check is
// returned.
func ResolveBriefProject(homeDir, projectName string, selectMode bool) (mode, tamperCheck string, err error) {
	snap, err := ResolveProjectSnapshot(homeDir, projectName)
	if err != nil {
		return "", "", classifySnapshotError(projectName, err)
	}
	resolved := snap.Config()
	if !selectMode {
		return "", resolved.TamperCheck, nil
	}
	return taskauthority.DeliveryModeForForge(deliveryStep(resolved.ForgeStep)), resolved.TamperCheck, nil
}

// ensureReflog pre-creates, unfenced before the soldier launches, the reflog
// <gitDir>/logs/<ref> as an empty regular file, for a ref such as
// refs/heads/<branch> or refs/remotes/<remote>/<name>. A detached `git worktree
// add` (internal/backend/worktree.go) leaves the branch reflog absent, so the
// seat's first fenced `git checkout -b <branch>` would have to create it; the
// fence refuses create, unlink and rename under logs/ and allows only
// appending to this exact file (B1), so the seat cannot swap the reflog for a
// symlink an unfenced push or fetch then writes through
// (fence-reflog-symlink-deputy). gitDir is the common git dir.
//
// It fails closed: every directory component from logs/ down to the reflog's
// parent must be a real directory (os.Lstat and os.MkdirAll both follow a
// symlinked component, which would move the O_EXCL create into the link's
// target); the reflog itself must be absent or already a regular file; and it
// is created with O_EXCL so a link a racing writer planted is never followed or
// truncated. A regular file left by a prior run is kept (git appends to it),
// and an EEXIST from a concurrent spawn winning the create is accepted the
// same way after re-classifying the winner's entry.
//
// Human words (domain.Words): grantor the Human, channel supervisor-relay:dialog,
// words=selected, G1168: "B1 (Recommended): B on the seat branch reflog,
// worktree HEAD and gate reflog; munsu pre-creates the gate branch reflog at
// spawn (probe first); logs/refs/remotes residual becomes its own task",
// building on G1167: "B: dò trước rồi làm (Recommended): munsu pre-creates the
// soldier's own branch reflog files; fence allows data writes to exactly those,
// refuses create/unlink/rename elsewhere in logs/".
func ensureReflog(gitDir, ref string) error {
	logs := filepath.Join(gitDir, "logs")
	if err := ensureDirs(parentDirs(logs, ref)); err != nil {
		return err
	}
	reflog := filepath.Join(logs, filepath.FromSlash(ref))
	fi, err := os.Lstat(reflog)
	if os.IsNotExist(err) {
		f, cerr := os.OpenFile(reflog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if cerr != nil {
			if !errors.Is(cerr, os.ErrExist) {
				return fmt.Errorf("reflog %s: %w", reflog, cerr)
			}
			// A concurrent spawn created it first; classify the winner's
			// entry by the same rules, still refusing a link planted in the
			// Lstat-to-create window.
			if fi, err = os.Lstat(reflog); err != nil {
				return fmt.Errorf("reflog %s: %w", reflog, err)
			}
		} else {
			return f.Close()
		}
	} else if err != nil {
		return fmt.Errorf("reflog %s: %w", reflog, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("reflog %s is a symlink or not a regular file; refusing to launch", reflog)
	}
	return nil
}

// ensureBranchReflog pre-creates the branch's namespace dirs under refs/heads,
// which the fence refuses the seat creating, removing or renaming, and its
// refs/heads reflog, both under ensureReflog's fail-closed rules.
// Words: Human, typed, 2026-10-16: "các task cần lời của tôi thì hỏi concil rồi quyết" (council 3/3: pre-create refs/heads/mu at spawn; seat cannot create/rename it)
func ensureBranchReflog(gitDir, branch string) error {
	if err := ensureDirs(parentDirs(filepath.Join(gitDir, "refs"), "heads/"+branch)); err != nil {
		return err
	}
	return ensureReflog(gitDir, "refs/heads/"+branch)
}

// parentDirs returns base and every directory between it and the slashed rel.
func parentDirs(base, rel string) []string {
	dirs := []string{base}
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		dirs = append(dirs, filepath.Join(dirs[len(dirs)-1], p))
	}
	return dirs
}

// ensureDirs creates each missing directory in order and refuses one that is a
// symlink or not a directory.
func ensureDirs(dirs []string) error {
	for _, d := range dirs {
		fi, err := os.Lstat(d)
		if os.IsNotExist(err) {
			if err := os.Mkdir(d, 0o755); err != nil {
				if !errors.Is(err, os.ErrExist) {
					return fmt.Errorf("dir %s: %w", d, err)
				}
				// A concurrent spawn created it first; classify the winner's
				// entry by the same rules instead of refusing the race loser.
				if fi, err = os.Lstat(d); err != nil {
					return fmt.Errorf("dir %s: %w", d, err)
				}
			} else {
				continue
			}
		} else if err != nil {
			return fmt.Errorf("dir %s: %w", d, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("dir %s is a symlink or not a directory; refusing to launch", d)
		}
	}
	return nil
}

// ensureRemoteReflogs pre-creates, as ensureReflog does, the reflog of every
// remote-tracking ref in commonDir, and returns those reflog paths. The seat
// may only append to them (fence-reflog-remotes-residual): fetch writes its
// remote-tracking reflogs on the fly, so a launch fetch without a pre-created
// reflog would leave the seat's fence no file to append to.
func ensureRemoteReflogs(commonDir string) ([]string, error) {
	out, err := exec.Command("git", "--git-dir="+commonDir, "for-each-ref", "--format=%(refname)", "refs/remotes").Output()
	if err != nil {
		return nil, fmt.Errorf("remote reflogs: %w", err)
	}
	var paths []string
	for _, ref := range strings.Fields(string(out)) {
		if err := ensureReflog(commonDir, ref); err != nil {
			return nil, err
		}
		paths = append(paths, filepath.Join(commonDir, "logs", filepath.FromSlash(ref)))
	}
	return paths, nil
}

// preflightDelivery runs the delivery-level mode preflight before
// worktree acquisition. It verifies environmental readiness for the
// resolved delivery mode (e.g. gh auth, remotes).
func (r *Runner) preflightDelivery() error {
	return deliveryPreflight(r.effectiveMode, r.projPath, r.forgeStep)
}

func deliveryPreflight(mode, projPath string, forge taskauthority.DeliveryStep) error {
	result, err := Preflight(mode, projPath, forge)
	if err != nil {
		return fmt.Errorf("delivery preflight: %w", err)
	}
	if !result.Feasible {
		return fmt.Errorf("delivery preflight for %q blocked: %s", mode, formatPreflightFailures(result.Checks))
	}
	return nil
}

func formatPreflightFailures(checks []Check) string {
	var b strings.Builder
	for _, c := range checks {
		if !c.OK {
			if b.Len() > 0 {
				b.WriteString("; ")
			}
			b.WriteString(c.Name)
			if c.Detail != "" {
				b.WriteString(": ")
				b.WriteString(c.Detail)
			}
		}
	}
	return b.String()
}
