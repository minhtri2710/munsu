package cli

import (
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
	if _, err := exec.LookPath("treehouse"); err == nil {
		t.Skip("requires the git worktree fallback provider")
	}

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

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
	if err := fleet.Add(tmpDir, projectName, repoPath, "", true); err != nil {
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
	status := func(homeDir string) (string, error) {
		if _, err := auth.BeginSpawn(mustCanonicalOp(t, "reclaim-reserved-launch", launchReq), launchReq); err != nil {
			return "", fmt.Errorf("committing launch intent: %w", err)
		}
		var ok bool
		reservedPath, ok, err = backend.ReservedWorktreePath(homeDir, repoPath, reservationID)
		if err != nil || !ok {
			return "", fmt.Errorf("resolving reserved worktree path: path=%q ok=%v err=%v", reservedPath, ok, err)
		}
		reservedGit = filepath.Join(reservedPath, ".git")
		if err := os.MkdirAll(reservedPath, 0o755); err != nil {
			return "", fmt.Errorf("materializing reserved worktree: %w", err)
		}
		if err := os.WriteFile(reservedGit, []byte("gitdir: /nowhere"), 0o644); err != nil {
			return "", fmt.Errorf("seeding reserved worktree: %w", err)
		}
		return backend.WorktreeStatus(homeDir)
	}

	root := &cobra.Command{Use: "test"}
	root.AddCommand(newWorktreeCmdWithStatus(status))
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
	if _, err := os.Stat(reservedGit); err != nil {
		t.Fatalf("reclaim must leave the reserved worktree untouched: %v", err)
	}
}

func TestWorktreeReclaimAllowsRetiredReservation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("PATH", "/dev/null")
	initCLITestHome(t, tmpDir)
	projectName := "retired-project"
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Add(tmpDir, projectName, repoPath, "", true); err != nil {
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
	reservedPath, ok, err := backend.ReservedWorktreePath(tmpDir, repoPath, reservationID)
	if err != nil || !ok {
		t.Fatalf("reserved path = %q, ok=%v, err=%v", reservedPath, ok, err)
	}
	if err := os.MkdirAll(reservedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reservedPath, ".git"), []byte("gitdir: /nowhere"), 0o644); err != nil {
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
}

func TestWorktreeReclaimSparesAuthoritativelyBoundWorktree(t *testing.T) {
	if _, err := exec.LookPath("treehouse"); err == nil {
		t.Skip("requires the git worktree fallback provider")
	}

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
		Head:               "head",
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
