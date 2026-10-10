package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
	"github.com/spf13/cobra"
)

// TestWorktreeReclaimRefusesUnreadableMeta is the F024 oracle. `worktree
// reclaim` decides which worktrees are live from the task-meta projection; a
// task whose .meta cannot be read is not evidence that it holds no worktree.
// Before the fix the command swallowed the ReadMeta error and skipped the
// task, so a live worktree behind an unreadable .meta would be classified
// orphaned and destroyed. The command must instead refuse and reclaim nothing.
//
// The .meta is made unreadable the way F003 proved destructive: a single line
// larger than bufio's default 64KB token makes ReadMeta fail with
// bufio.ErrTooLong while the file stays a readable regular file the reclaim
// path could otherwise act on.
func TestWorktreeReclaimRefusesUnreadableMeta(t *testing.T) {
	// Pin the git worktree fallback provider: no treehouse on PATH.
	testutil.SetPath(t, t.TempDir())

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatalf("initializing home: %v", err)
	}

	const id = "task-live"
	if err := home.WriteMeta(tmpDir, id, map[string]string{"worktree": "/pool/wt-live"}); err != nil {
		t.Fatalf("seeding task meta: %v", err)
	}

	metaPath, err := home.MetaFilePath(tmpDir, id)
	if err != nil {
		t.Fatalf("resolving meta path: %v", err)
	}
	oversized := "worktree=" + strings.Repeat("x", 70*1024) + "\n"
	if err := os.WriteFile(metaPath, []byte(oversized), 0o644); err != nil {
		t.Fatalf("corrupting task meta: %v", err)
	}
	orphanDir := filepath.Join(tmpDir, ".worktrees", "orphan")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("creating orphan worktree: %v", err)
	}
	orphanGit := filepath.Join(orphanDir, ".git")
	if err := os.WriteFile(orphanGit, []byte("gitdir: /nowhere"), 0o644); err != nil {
		t.Fatalf("seeding orphan worktree: %v", err)
	}
	// Precondition: the corrupted file is a readable regular file whose read now
	// fails; that is the state the reclaim path must refuse rather than skip.
	if _, err := home.ReadMeta(tmpDir, id); err == nil {
		t.Fatal("precondition: ReadMeta must fail on the oversized meta")
	}

	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})

	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = stdoutWriter
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()

	err = root.Execute()
	stdoutWriter.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err == nil {
		t.Fatal("reclaim must refuse when a listed task's meta is unreadable")
	}
	if !strings.Contains(err.Error(), "reading task meta") {
		t.Fatalf("reclaim must fail closed on the unreadable projection, got: %v", err)
	}
	if strings.Contains(output, "returning orphaned worktree") || strings.Contains(output, "Reclaimed") {
		t.Fatalf("reclaim must not act on an orphan after a meta read failure, got stdout: %q", output)
	}
	if _, err := os.Stat(orphanGit); err != nil {
		t.Fatalf("reclaim must leave the orphan worktree untouched: %v", err)
	}
}

func TestWorktreeReclaimSparesReservedUnboundWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("PATH", "/dev/null")
	initCLITestHome(t, tmpDir)
	projectName := "reserved-project"
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("creating registered repo: %v", err)
	}
	if err := fleet.Add(tmpDir, projectName, repoPath, true); err != nil {
		t.Fatalf("registering project: %v", err)
	}

	auth := testAuthorityFor(t, tmpDir)
	taskID := mustTaskIDFor(t, "reserved-unbound")
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		t.Fatal(err)
	}
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      taskID,
		Owner:       "general",
		Description: "reserved worktree test",
		Kind:        "ship",
		Project:     projectID,
		Reason:      "reclaim test",
	}
	if _, err := auth.Create(mustCanonicalOp(t, "reclaim-reserved-create", createReq), createReq); err != nil {
		t.Fatalf("creating task authority record: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("reading task authority record: %v", err)
	}
	const reservationID = "wt-reserved-unbound"
	launchReq := taskauthority.CanonicalBeginSpawnRequest{
		HomeID:                auth.HomeID(),
		TaskID:                taskID,
		Precondition:          domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		SnapshotDigest:        strings.Repeat("a", 64),
		Backend:               "tmux",
		Harness:               "pi",
		Model:                 "model",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               projectName,
		LaunchID:              "launch-reserved-unbound",
		WindowLabel:           "window-reserved-unbound",
		WorktreeReservationID: reservationID,
		WorktreeFenceToken:    "wt-fence-reserved-unbound",
		EndpointReservationID: "ep-reserved-unbound",
		EndpointFenceToken:    "ep-fence-reserved-unbound",
		EndpointIncarnation:   "ep-inc-reserved-unbound",
		Reason:                "reclaim test",
	}
	var reservedPath, reservedGit string
	statusWorktrees := func(homeDir string) ([]backend.WorktreeEntry, error) {
		if _, err := auth.BeginSpawn(mustCanonicalOp(t, "reclaim-reserved-launch", launchReq), launchReq); err != nil {
			return nil, fmt.Errorf("committing launch intent: %w", err)
		}
		var ok bool
		reservedPath, ok, err = backend.ReservedWorktreePath(homeDir, repoPath, reservationID)
		if err != nil || !ok {
			return nil, fmt.Errorf("resolving reserved worktree path: path=%q ok=%v err=%v", reservedPath, ok, err)
		}
		reservedGit = filepath.Join(reservedPath, ".git")
		if err := os.MkdirAll(reservedPath, 0o755); err != nil {
			return nil, fmt.Errorf("materializing reserved worktree: %w", err)
		}
		if err := os.WriteFile(reservedGit, []byte("gitdir: /nowhere"), 0o644); err != nil {
			return nil, fmt.Errorf("seeding reserved worktree: %w", err)
		}
		return backend.StatusWorktrees(homeDir)
	}

	root := &cobra.Command{Use: "test"}
	root.AddCommand(newWorktreeCmdWithStatus(statusWorktrees))
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = stdoutWriter
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()

	err = root.Execute()
	stdoutWriter.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if strings.Contains(output, "returning orphaned worktree: "+reservedPath) {
		t.Fatalf("reclaim must spare the reserved-but-unbound worktree, got stdout: %q", output)
	}
	if !strings.Contains(output, "Reclaimed 0 orphaned worktrees") {
		t.Fatalf("reclaim must report no reclaimed worktrees, got stdout: %q", output)
	}
	if _, err := os.Stat(reservedGit); err != nil {
		t.Fatalf("reclaim must leave the reserved worktree untouched: %v", err)
	}
}

// TestWorktreeReclaimSparesTreehouseLeaseHolderWorktree drives the real
// treehouse provider path end to end. A semantic fake `treehouse` on PATH emits
// `status --json` with a live-holder and a dead-holder worktree and records each
// `return`, so reclaim exercises backend.StatusWorktrees (the status --json
// decode) and backend.ReturnWorktree (treehouse return) rather than an injected
// status seam. Reclaim must spare the worktree whose lease_holder is a live
// launch reservation and return the one held by a dead reservation — the
// reservation-keyed holder query the F024 treehouse residual
// (f024-treehouse-holder-status-dep) closed.
func TestWorktreeReclaimSparesTreehouseLeaseHolderWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	initCLITestHome(t, tmpDir)
	projectName := "treehouse-project"
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("creating registered repo: %v", err)
	}
	if err := fleet.Add(tmpDir, projectName, repoPath, true); err != nil {
		t.Fatalf("registering project: %v", err)
	}

	auth := testAuthorityFor(t, tmpDir)
	taskID := mustTaskIDFor(t, "treehouse-holder")
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		t.Fatal(err)
	}
	createReq := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "treehouse holder test", Kind: "ship", Project: projectID, Reason: "reclaim test"}
	if _, err := auth.Create(mustCanonicalOp(t, "th-create", createReq), createReq); err != nil {
		t.Fatalf("creating task authority record: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("reading task authority record: %v", err)
	}
	const reservationID = "wt-treehouse-holder"
	launchReq := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: strings.Repeat("c", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: projectName, LaunchID: "launch-treehouse-holder", WindowLabel: "window-treehouse-holder", WorktreeReservationID: reservationID, WorktreeFenceToken: "wt-fence-treehouse-holder", EndpointReservationID: "ep-treehouse-holder", EndpointFenceToken: "ep-fence-treehouse-holder", EndpointIncarnation: "ep-inc-treehouse-holder", Reason: "reclaim test"}
	if _, err := auth.BeginSpawn(mustCanonicalOp(t, "th-launch", launchReq), launchReq); err != nil {
		t.Fatalf("committing launch intent: %v", err)
	}

	const heldPath = "/pool/held-by-live-reservation"
	const orphanPath = "/pool/orphan-lease"
	tracePath := filepath.Join(tmpDir, "treehouse-returns")
	statusJSON := `[{"path":"` + heldPath + `","lease_holder":"` + reservationID + `"},` +
		`{"path":"` + orphanPath + `","lease_holder":"some-dead-reservation"}]`
	// Semantic fake treehouse: answer `status --json` with the two pooled
	// worktrees and append every `return` target to the trace file, so the test
	// observes a real provider-level return rather than only reclaim's stdout.
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "status" ] && [ "$2" = "--json" ]; then` + "\n" +
		`  echo '` + statusJSON + `'` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`if [ "$1" = "return" ]; then` + "\n" +
		`  for a in "$@"; do last="$a"; done` + "\n" +
		`  echo "$last" >> "` + tracePath + `"` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`>&2 echo "fake treehouse: unexpected args: $*"` + "\n" +
		`exit 1` + "\n"
	testutil.FakeOnPath(t, "treehouse", script)

	root := &cobra.Command{Use: "test"}
	root.AddCommand(newWorktreeCmd())
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = stdoutWriter
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()

	err = root.Execute()
	stdoutWriter.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err == nil || !strings.Contains(err.Error(), "not canonically reconciled") {
		t.Fatalf("reclaim error = %v, want fail-closed lease reconciliation refusal", err)
	}
	if strings.Contains(output, "returning orphaned worktree:") || strings.Contains(output, "Reclaimed") {
		t.Fatalf("reclaim must preflight every lease before provider return, got stdout: %q", output)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("provider must not return live or unreconciled leases; trace stat error=%v", err)
	}
}

// TestWorktreeReclaimIsFencedAgainstTheWorktreePool proves reclaim runs its
// snapshot->return pass under the worktree-pool fence: while the fence is held
// (as a launch holds it around its lease), reclaim cannot proceed and fails
// closed with a lock timeout instead of racing the lease. This is the fence that
// closes the spawn/reclaim reservation race a status reread alone cannot.
func TestWorktreeReclaimIsFencedAgainstTheWorktreePool(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	initCLITestHome(t, tmpDir)

	lk, err := fleet.LockWorktreePool(tmpDir)
	if err != nil {
		t.Fatalf("holding worktree-pool fence: %v", err)
	}
	defer lk.Release()

	root := &cobra.Command{Use: "test"}
	root.AddCommand(newWorktreeCmd())
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	if err := root.Execute(); !errors.Is(err, home.ErrLockTimeout) {
		t.Fatalf("reclaim must fail closed on the held worktree-pool fence with ErrLockTimeout, got: %v", err)
	}
}

func TestWorktreeReclaimAllowsRetiredReservation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("PATH", "/usr/bin:/bin")
	initCLITestHome(t, tmpDir)
	projectName := "retired-project"
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", repoPath},
		{"-C", repoPath, "config", "user.email", "test@example.invalid"},
		{"-C", repoPath, "config", "user.name", "test"},
	} {
		if out, err := exec.Command("/usr/bin/git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", repoPath, "add", "README.md"},
		{"-C", repoPath, "commit", "-qm", "seed"},
	} {
		if out, err := exec.Command("/usr/bin/git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := fleet.Add(tmpDir, projectName, repoPath, true); err != nil {
		t.Fatal(err)
	}
	auth := testAuthorityFor(t, tmpDir)
	taskID := mustTaskIDFor(t, "retired-reservation")
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "retired reservation", Kind: "ship", Project: projectID, Reason: "reclaim test"}
	if _, err := auth.Create(mustCanonicalOp(t, "retired-create", create), create); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	const reservationID = "wt-retired-reservation"
	launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: strings.Repeat("b", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: projectName, LaunchID: "launch-retired", WindowLabel: "window-retired", WorktreeReservationID: reservationID, WorktreeFenceToken: "fence-retired", EndpointReservationID: "ep-retired", EndpointFenceToken: "ep-fence-retired", EndpointIncarnation: "ep-inc-retired", Reason: "reclaim test"}
	if _, err := auth.BeginSpawn(mustCanonicalOp(t, "retired-launch", launch), launch); err != nil {
		t.Fatal(err)
	}
	agg, err = auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	retire := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reclaim test"}
	if _, err := auth.Retire(mustCanonicalOp(t, "retired-retire", retire), retire); err != nil {
		t.Fatal(err)
	}
	agg, err = auth.Get(taskID)
	if err != nil || agg.Phase != taskauthority.PhaseRetired || agg.Launch == nil || agg.Worktree != nil {
		t.Fatalf("retired aggregate = %+v, err=%v", agg, err)
	}
	// Adapted fixture: the current contract releases an unbound reservation
	// only once canonical retirement cleanup has completed. The base test
	// predates that contract, so it completes cleanup here through the
	// canonical API before reclaim.
	if err := auth.ReconcileRetirementCleanup(taskID, agg.Generation, taskauthority.CleanupCompleted, func() error { return nil }); err != nil {
		t.Fatalf("completing canonical retirement cleanup for fixture: %v", err)
	}
	reservedPath, ok, err := backend.ReservedWorktreePath(tmpDir, repoPath, reservationID)
	if err != nil || !ok {
		t.Fatalf("reserved path = %q, ok=%v, err=%v", reservedPath, ok, err)
	}
	if out, err := exec.Command("/usr/bin/git", "-C", repoPath, "worktree", "add", "--detach", reservedPath, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()
	oldStdout := os.Stdout
	os.Stdout = writer
	err = root.Execute()
	writer.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if !strings.Contains(output, "returning orphaned worktree: "+reservedPath) {
		t.Fatalf("retired reservation should be reclaimable, got stdout: %q", output)
	}
	if !strings.Contains(output, "Reclaimed 1 orphaned worktrees") {
		t.Fatalf("retired reservation should report one reclaimed worktree, got stdout: %q", output)
	}
	if _, err := os.Stat(reservedPath); !os.IsNotExist(err) {
		t.Fatalf("retired reservation worktree should be removed, stat err=%v", err)
	}
}

const heldReservationID = "wt-held-reservation"

// unboundReservationFixture creates a ship task with a launch reservation that
// was never bound to a worktree, and materializes the reserved worktree as a
// clean detached checkout. It returns the canonical authority, the task ID and
// the reserved path.
func unboundReservationFixture(t *testing.T) (*taskauthority.Canonical, domain.TaskID, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("PATH", "/usr/bin:/bin")
	initCLITestHome(t, tmpDir)
	projectName := "held-project"
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", repoPath},
		{"-C", repoPath, "config", "user.email", "test@example.invalid"},
		{"-C", repoPath, "config", "user.name", "test"},
		{"-C", repoPath, "commit", "--allow-empty", "-qm", "seed"},
	} {
		if out, err := exec.Command("/usr/bin/git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := fleet.Add(tmpDir, projectName, repoPath, true); err != nil {
		t.Fatal(err)
	}
	auth := testAuthorityFor(t, tmpDir)
	taskID := mustTaskIDFor(t, "held-reservation")
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "held reservation", Kind: "ship", Project: projectID, Reason: "reclaim test"}
	if _, err := auth.Create(mustCanonicalOp(t, "held-create", create), create); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: strings.Repeat("c", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: projectName, LaunchID: "launch-held", WindowLabel: "window-held", WorktreeReservationID: heldReservationID, WorktreeFenceToken: "fence-held", EndpointReservationID: "ep-held", EndpointFenceToken: "ep-fence-held", EndpointIncarnation: "ep-inc-held", Reason: "reclaim test"}
	if _, err := auth.BeginSpawn(mustCanonicalOp(t, "held-launch", launch), launch); err != nil {
		t.Fatal(err)
	}
	reservedPath, ok, err := backend.ReservedWorktreePath(tmpDir, repoPath, heldReservationID)
	if err != nil || !ok {
		t.Fatalf("reserved path = %q, ok=%v, err=%v", reservedPath, ok, err)
	}
	if out, err := exec.Command("/usr/bin/git", "-C", repoPath, "worktree", "add", "--detach", reservedPath, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return auth, taskID, reservedPath
}

// installHeldTreehouse fakes treehouse so `status --json` reports reservedPath
// leased by reservationID, and records every `return` target in the returned
// trace file. The trace is absent until a provider return happens.
func installHeldTreehouse(t *testing.T, reservedPath, reservationID string) string {
	t.Helper()
	tracePath := filepath.Join(t.TempDir(), "treehouse-returns")
	statusJSON := `[{"path":"` + reservedPath + `","lease_holder":"` + reservationID + `"}]`
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "status" ] && [ "$2" = "--json" ]; then` + "\n" +
		`  echo '` + statusJSON + `'` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`if [ "$1" = "return" ]; then` + "\n" +
		`  for a in "$@"; do last="$a"; done` + "\n" +
		`  echo "$last" >> "` + tracePath + `"` + "\n" +
		`  exit 0` + "\n" +
		`fi` + "\n" +
		`>&2 echo "fake treehouse: unexpected args: $*"` + "\n" +
		`exit 1` + "\n"
	testutil.FakeOnPath(t, "treehouse", script)
	return tracePath
}

// retireHeldTask retires the held task through the canonical API.
func retireHeldTask(t *testing.T, auth *taskauthority.Canonical, taskID domain.TaskID) {
	t.Helper()
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	retire := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reclaim test"}
	if _, err := auth.Retire(mustCanonicalOp(t, "held-retire", retire), retire); err != nil {
		t.Fatal(err)
	}
}

// runWorktreeReclaimOutput runs `munsu worktree reclaim` and returns its stdout.
func runWorktreeReclaimOutput(t *testing.T) string {
	t.Helper()
	output, err := runWorktreeReclaim(t)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	return output
}

// runWorktreeReclaim runs `munsu worktree reclaim` and returns its stdout and
// the command error.
func runWorktreeReclaim(t *testing.T) (string, error) {
	t.Helper()
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()
	oldStdout := os.Stdout
	os.Stdout = writer
	err = root.Execute()
	writer.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	return output, err
}

// TestWorktreeReclaimSparesUnboundReservationUntilRetirementCompletes pins the
// states that keep an unbound reservation active: a task that is Done or
// Resolved, and a task that is Retired while its cleanup claim is still active.
func TestWorktreeReclaimSparesUnboundReservationUntilRetirementCompletes(t *testing.T) {
	cases := []struct {
		name string
		hold func(t *testing.T, auth *taskauthority.Canonical, taskID domain.TaskID)
	}{
		{"done", func(t *testing.T, auth *taskauthority.Canonical, taskID domain.TaskID) {
			completeHeldTask(t, auth, taskID, taskauthority.PhaseDone)
		}},
		{"resolved", func(t *testing.T, auth *taskauthority.Canonical, taskID domain.TaskID) {
			completeHeldTask(t, auth, taskID, taskauthority.PhaseResolved)
		}},
		{"retired with cleanup active", func(t *testing.T, auth *taskauthority.Canonical, taskID domain.TaskID) {
			agg, err := auth.Get(taskID)
			if err != nil {
				t.Fatal(err)
			}
			retire := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reclaim test"}
			if _, err := auth.Retire(mustCanonicalOp(t, "held-retire", retire), retire); err != nil {
				t.Fatal(err)
			}
			agg, err = auth.Get(taskID)
			if err != nil || agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
				t.Fatalf("retired aggregate cleanup claim = %+v, err=%v, want active", agg.CleanupClaim, err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, taskID, reservedPath := unboundReservationFixture(t)
			tc.hold(t, auth, taskID)
			output := runWorktreeReclaimOutput(t)
			if strings.Contains(output, "returning orphaned worktree: "+reservedPath) || !strings.Contains(output, "Reclaimed 0 orphaned worktrees") {
				t.Fatalf("unbound reservation must stay held while %s, got stdout: %q", tc.name, output)
			}
			if _, err := os.Stat(reservedPath); err != nil {
				t.Fatalf("held reserved worktree was removed: %v", err)
			}
		})
	}
}

// TestWorktreeReclaimReturnsReleasedTreehouseLeaseHolder pins the treehouse
// case of G809: treehouse reports the retired unbound reservation as the lease
// holder, and reclaim must treat that holder as reconciled and return the clean
// worktree through the provider.
func TestWorktreeReclaimReturnsReleasedTreehouseLeaseHolder(t *testing.T) {
	auth, taskID, reservedPath := unboundReservationFixture(t)
	retireHeldTask(t, auth, taskID)
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ReconcileRetirementCleanup(taskID, agg.Generation, taskauthority.CleanupCompleted, func() error { return nil }); err != nil {
		t.Fatalf("completing canonical retirement cleanup for fixture: %v", err)
	}
	tracePath := installHeldTreehouse(t, reservedPath, heldReservationID)
	output, err := runWorktreeReclaim(t)
	if err != nil {
		t.Fatalf("reclaim refused a released treehouse lease holder: %v", err)
	}
	if !strings.Contains(output, "returning orphaned worktree: "+reservedPath) || !strings.Contains(output, "Reclaimed 1 orphaned worktrees") {
		t.Fatalf("released treehouse lease holder should be reclaimed, got stdout: %q", output)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil || strings.TrimSpace(string(trace)) != reservedPath {
		t.Fatalf("provider return trace = %q, err=%v, want %q", trace, err, reservedPath)
	}
}

// TestWorktreeReclaimSparesTreehouseLeaseHolderBeforeCleanupCompletes pins the
// held state for the same treehouse lease holder: while the retired task's
// cleanup is still active, the reservation stays live, its path is spared, and
// the provider is never called. The pass does not refuse, because the holder is
// a live reservation, not an unreconciled one.
func TestWorktreeReclaimSparesTreehouseLeaseHolderBeforeCleanupCompletes(t *testing.T) {
	auth, taskID, reservedPath := unboundReservationFixture(t)
	retireHeldTask(t, auth, taskID)
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("retired aggregate cleanup claim = %+v, want active", agg.CleanupClaim)
	}
	tracePath := installHeldTreehouse(t, reservedPath, heldReservationID)
	output, err := runWorktreeReclaim(t)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if strings.Contains(output, "returning orphaned worktree:") || !strings.Contains(output, "Reclaimed 0 orphaned worktrees") {
		t.Fatalf("reclaim must spare the held reservation, got stdout: %q", output)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("provider must not return a lease whose reservation is not released; trace stat error=%v", err)
	}
}

func completeHeldTask(t *testing.T, auth *taskauthority.Canonical, taskID domain.TaskID, to taskauthority.Phase) {
	t.Helper()
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), To: to, Reason: "reclaim test"}
	if _, err := auth.Complete(mustCanonicalOp(t, "held-complete-"+string(to), complete), complete); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeReclaimSparesRetiredWorktreeEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("PATH", "/usr/bin:/bin")
	initCLITestHome(t, tmpDir)
	projectName := "retired-project"
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", repoPath},
		{"-C", repoPath, "config", "user.email", "test@example.invalid"},
		{"-C", repoPath, "config", "user.name", "test"},
	} {
		if out, err := exec.Command("/usr/bin/git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", repoPath, "add", "README.md"},
		{"-C", repoPath, "commit", "-qm", "seed"},
	} {
		if out, err := exec.Command("/usr/bin/git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := fleet.Add(tmpDir, projectName, repoPath, true); err != nil {
		t.Fatal(err)
	}
	auth := testAuthorityFor(t, tmpDir)
	taskID := mustTaskIDFor(t, "retired-reservation")
	projectID, err := domain.NewProjectID(projectName)
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: taskID, Owner: "general", Description: "retired reservation", Kind: "ship", Project: projectID, Reason: "reclaim test"}
	if _, err := auth.Create(mustCanonicalOp(t, "retired-create", create), create); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	const reservationID = "wt-retired-reservation"
	launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: strings.Repeat("b", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: projectName, LaunchID: "launch-retired", WindowLabel: "window-retired", WorktreeReservationID: reservationID, WorktreeFenceToken: "fence-retired", EndpointReservationID: "ep-retired", EndpointFenceToken: "ep-fence-retired", EndpointIncarnation: "ep-inc-retired", Reason: "reclaim test"}
	if _, err := auth.BeginSpawn(mustCanonicalOp(t, "retired-launch", launch), launch); err != nil {
		t.Fatal(err)
	}
	reservedPath, ok, err := backend.ReservedWorktreePath(tmpDir, repoPath, reservationID)
	if err != nil || !ok {
		t.Fatalf("reserved path = %q, ok=%v, err=%v", reservedPath, ok, err)
	}
	if out, err := exec.Command("/usr/bin/git", "-C", repoPath, "worktree", "add", "--detach", reservedPath, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	agg, err = auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	binding := taskauthority.WorktreeBinding{RepositoryIdentity: "repo", Path: reservedPath, GitDir: "git", CommonDir: "common", BaseHead: "head", LeaseID: reservationID, FenceToken: "fence-retired", BoundAtUnix: 1}
	bind := taskauthority.CanonicalBindWorktreeRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Binding: binding, Reason: "reclaim test"}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "retired-bind", bind), bind); err != nil {
		t.Fatal(err)
	}
	agg, err = auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	retire := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reclaim test"}
	if _, err := auth.Retire(mustCanonicalOp(t, "retired-retire", retire), retire); err != nil {
		t.Fatal(err)
	}
	agg, err = auth.Get(taskID)
	if err != nil || agg.Phase != taskauthority.PhaseRetired || agg.Launch == nil || agg.Worktree != nil || agg.Retirement == nil || agg.Retirement.Worktree == nil || agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("retired aggregate missing worktree cleanup custody = %+v, err=%v", agg, err)
	}
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()
	oldStdout := os.Stdout
	os.Stdout = writer
	err = root.Execute()
	writer.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err != nil {
		t.Fatalf("reclaim should leave the canonically retired worktree evidence untouched: %v", err)
	}
	if strings.Contains(output, "returning orphaned worktree: "+reservedPath) || !strings.Contains(output, "Reclaimed 0 orphaned worktrees") {
		t.Fatalf("retired worktree evidence must retain custody, got stdout: %q", output)
	}
	if _, err := os.Stat(reservedPath); err != nil {
		t.Fatalf("retired worktree must remain until canonical retirement: %v", err)
	}
	agg, err = auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ReconcileRetirementCleanup(taskID, agg.Generation, taskauthority.CleanupCompleted, func() error { return nil }); err != nil {
		t.Fatalf("completing canonical retirement cleanup for fixture: %v", err)
	}
	stdout, writer, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout = os.Stdout
	os.Stdout = writer
	outputDone = make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()
	err = root.Execute()
	writer.Close()
	os.Stdout = oldStdout
	output = <-outputDone
	stdout.Close()
	if err != nil || !strings.Contains(output, "Reclaimed 1 orphaned worktrees") {
		t.Fatalf("canonically completed retirement reclaim = %q, err=%v", output, err)
	}
	if _, err := os.Stat(reservedPath); !os.IsNotExist(err) {
		t.Fatalf("canonically released worktree should be returned, stat err=%v", err)
	}
}

func TestWorktreeReclaimReturnsCleanUnownedOrphan(t *testing.T) {
	_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()
	err = root.Execute()
	writer.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "returning orphaned worktree: "+worktreePath) || !strings.Contains(output, "Reclaimed 1 orphaned worktrees") {
		t.Fatalf("clean orphan reclaim output = %q", output)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil || !strings.Contains(string(trace), worktreePath) {
		t.Fatalf("provider return trace = %q, err=%v", trace, err)
	}
}

func TestWorktreeReclaimStopsOnUncertainProviderReturn(t *testing.T) {
	_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then printf '%s\\n' '[{\"path\":\""+worktreePath+"\",\"lease_holder\":\"\"}]'; exit 0; fi\n"+
		"if [ \"$1\" = \"return\" ]; then echo \"$3\" >> \""+tracePath+"\"; exit 1; fi\nexit 1\n")
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "return outcome uncertain") {
		t.Fatalf("reclaim error = %v, want uncertain provider outcome", err)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil || !strings.Contains(string(trace), worktreePath) {
		t.Fatalf("provider return trace = %q, err=%v", trace, err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("failed return was incorrectly inferred complete: %v", err)
	}
}

func TestWorktreeReclaimPreflightsBeforeReturningAnyCandidate(t *testing.T) {
	_, _, firstPath, tracePath := setupCleanOrphanWorktree(t)
	_, _, secondPath, _ := setupCleanOrphanWorktree(t)
	artifact := filepath.Join(secondPath, ".soldier-charter.md")
	if err := os.WriteFile(artifact, []byte("unanchored launch content\\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	statusFile := filepath.Join(filepath.Dir(tracePath), "treehouse-status.json")
	entries, err := json.Marshal([]map[string]string{{"path": firstPath, "lease_holder": ""}, {"path": secondPath, "lease_holder": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusFile, entries, 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then cat \""+statusFile+"\"; exit 0; fi\n"+
		"if [ \"$1\" = \"return\" ]; then echo \"$3\" >> \""+tracePath+"\"; exit 0; fi\nexit 1\n")
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "not safe to reclaim") {
		t.Fatalf("reclaim error = %v, want unsafe-candidate preflight refusal", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("earlier clean candidate was returned before later unsafe candidate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(firstPath, ".git")); err != nil {
		t.Fatalf("earlier clean candidate worktree changed: %v", err)
	}
}

func TestWorktreeReclaimPreservesDirtyOrphan(t *testing.T) {
	_, _, worktreePath, tracePath := setupCleanOrphanWorktree(t)
	artifact := filepath.Join(worktreePath, "user-data.txt")
	if err := os.WriteFile(artifact, []byte("must survive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()
	err = root.Execute()
	writer.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err == nil || !strings.Contains(err.Error(), "not safe to reclaim") {
		t.Fatalf("dirty orphan reclaim error = %v, want fail-closed refusal", err)
	}
	if strings.Contains(output, "returning orphaned worktree:") || strings.Contains(output, "Reclaimed") {
		t.Fatalf("dirty orphan preflight must precede all provider returns, got stdout: %q", output)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("provider returned dirty worktree: %v", err)
	}
	if got, err := os.ReadFile(artifact); err != nil || string(got) != "must survive\n" {
		t.Fatalf("protected untracked content = %q, err=%v", got, err)
	}
}

func TestWorktreeReclaimSparesAuthoritativelyBoundWorktree(t *testing.T) {
	// Pin the git worktree fallback provider: no treehouse on PATH.
	testutil.SetPath(t, t.TempDir())

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	initCLITestHome(t, tmpDir)
	auth := testAuthorityFor(t, tmpDir)
	taskID := mustTaskIDFor(t, "authoritative-live")
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      taskID,
		Owner:       "general",
		Description: "authoritative worktree test",
		Kind:        "ship",
		Reason:      "reclaim test",
	}
	if _, err := auth.Create(mustCanonicalOp(t, "reclaim-authoritative-create", createReq), createReq); err != nil {
		t.Fatalf("creating task authority record: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("reading task authority record: %v", err)
	}

	orphanDir := filepath.Join(tmpDir, ".worktrees", "authoritative")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("creating orphan worktree: %v", err)
	}
	orphanGit := filepath.Join(orphanDir, ".git")
	if err := os.WriteFile(orphanGit, []byte("gitdir: /nowhere"), 0o644); err != nil {
		t.Fatalf("seeding orphan worktree: %v", err)
	}
	binding := taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo",
		Path:               orphanDir,
		GitDir:             "git",
		CommonDir:          "common",
		BaseHead:           "head",
		LeaseID:            "lease",
		FenceToken:         "fence",
		BoundAtUnix:        time.Now().Unix(),
	}
	bindReq := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       taskID,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      binding,
		Reason:       "reclaim test",
	}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "reclaim-authoritative-bind", bindReq), bindReq); err != nil {
		t.Fatalf("binding authoritative worktree: %v", err)
	}

	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = stdoutWriter
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()

	err = root.Execute()
	stdoutWriter.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if strings.Contains(output, "returning orphaned worktree: "+orphanDir) {
		t.Fatalf("reclaim must spare the authoritative worktree, got stdout: %q", output)
	}
	if _, err := os.Stat(orphanGit); err != nil {
		t.Fatalf("reclaim must leave the authoritative worktree untouched: %v", err)
	}
}
