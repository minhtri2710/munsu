//go:build integration

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func writeRawTaskMeta(t *testing.T, homeDir, id, content string) {
	t.Helper()
	path, err := home.MetaFilePath(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeTaskStatus(t *testing.T, homeDir, id, content string) {
	t.Helper()
	path, err := home.StatusFilePath(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestSendCmd_UsesBoundBackend verifies that send reads the backend from the
// task's canonical endpoint binding instead of using the global config.
func TestSendCmd_UsesBoundBackend(t *testing.T) {
	tmpDir := t.TempDir()
	mustBindCLIEndpoint(t, tmpDir, "test-task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@0"})

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	writeTaskStatus(t, tmpDir, "test-task", "working: spawned\n")

	// Leave only git on PATH so whichever backend is attempted will fail
	// and tell us which one it tried.
	gitOnlyPath(t)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "test-task", "echo hello", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error (tmux not on PATH), got nil")
	}
	// send resolves the session backend from the binding (tmux, not the
	// global config herdr). The shared constructBackend fails closed when tmux
	// is absent on PATH with a typed "<name>: not found on PATH" error.
	if !strings.Contains(err.Error(), "tmux: not found on PATH") {
		t.Errorf("expected error mentioning 'tmux: not found on PATH' (from the bound backend), got: %v", err)
	}
	if strings.Contains(err.Error(), "herdr") {
		t.Errorf("send must use the bound backend (tmux), not the global config (herdr), got: %v", err)
	}
}

// TestSendCmd_UnknownBoundBackendFailsClosed verifies that an unknown bound
// backend name fails closed instead of falling through to the global config.
func TestSendCmd_UnknownBoundBackendFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	mustBindCLIEndpoint(t, tmpDir, "test-task", taskauthority.EndpointBinding{Backend: "nonexistent", Handle: "@0"})

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Leave only git on PATH so backend resolution fails
	gitOnlyPath(t)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "test-task", "echo hello", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error for unknown backend (fallthrough), got nil")
	}
	// A malformed bound backend is authoritative and must fail closed rather than
	// falling through to a different backend.
	if !strings.Contains(err.Error(), "unknown bound session backend") || !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("unknown bound backend error = %v", err)
	}
}

// TestPeekCmd_UsesBoundBackend verifies that peek reads the backend from the
// task's canonical endpoint binding instead of using the global config.
func TestPeekCmd_UsesBoundBackend(t *testing.T) {
	tmpDir := t.TempDir()
	mustBindCLIEndpoint(t, tmpDir, "test-task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@0"})

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"peek", "test-task", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error (tmux not on PATH), got nil")
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Errorf("expected error mentioning 'tmux' (from the bound backend), got: %v", err)
	}
}

// TestPeekCmd_RejectsMetaWithoutBoundBackend verifies that peek does not fall
// back to mutable global config for a captain whose meta records no backend.
func TestPeekCmd_RejectsMetaWithoutBoundBackend(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	metaContent := "window=@0\nkind=captain\n" // no backend field
	writeRawTaskMeta(t, tmpDir, "captain:c1", metaContent)

	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"peek", "captain:c1", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected missing bound backend error")
	}
	if !strings.Contains(err.Error(), "task does not record the pane's backend") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendCmd_MarksCaptainLine(t *testing.T) {
	// Contract: General→Captain sends must carry the from-general marker so the
	// Second answers via parent status, not chat-only.
	line := "report progress on munsu-rank-rename"
	marked := home.MarkFromGeneral(line)
	if !home.IsFromGeneral(marked) {
		t.Fatalf("expected marker on captain send line")
	}
	if marked == line {
		t.Fatalf("expected prefix")
	}
}

func TestSendCmd_CaptainSendFailsGracefully(t *testing.T) {
	// General sending to a captain whose pane is dead or unprovisioned must
	// fail gracefully (not hit the uplink guard). No outbox for new sends;
	// pending is retained durably until converge reconciles the ack.
	tmpDir := t.TempDir()
	metaContent := "kind=captain\nsm_id=munsu\nhome=" + filepath.Join(tmpDir, "captain-home") + "\n"
	writeRawTaskMeta(t, tmpDir, "captain:munsu", metaContent)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "captain:munsu", "report status", "--home", tmpDir})
	err := root.Execute()
	if err == nil {
		return // outbox enqueue success is acceptable
	}
	if strings.Contains(err.Error(), "uplink use munsu report") {
		t.Fatalf("send to captain:<id> must not be blocked by uplink guard: %v", err)
	}
}

// --- Gate refusal tests for send and teardown ---

// seedRetireAuthority seeds the task in the canonical Task Authority over
// homeDir, mirroring production spawn (tasks are created via the Authority at
// launch). The teardown command composes the Authority over the resolved task
// home (Task 7.7), and the retirement transition fails closed without the
// authoritative record.
// A review task is created against a working ship task, as it must be.
func seedRetireAuthority(t *testing.T, taskID, kind string) string {
	t.Helper()
	var homeDir string
	req := taskauthority.CanonicalCreateRequest{Owner: "owner", Kind: kind, Reason: "create"}
	if kind == taskauthority.KindReview {
		homeDir = deliveryGuardShip(t, "t-ship")
		req.ReviewTaskID, req.ReviewHead = "t-ship", deliveryGuardHead
	} else {
		homeDir = t.TempDir()
		initCLITestHome(t, homeDir)
		req.ScoutScope = "investigate the requested question"
	}
	auth := testAuthorityFor(t, homeDir)
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	req.HomeID, req.TaskID = auth.HomeID(), tid
	if _, err := auth.Create(mustCanonicalOp(t, "op-create-"+taskID, req), req); err != nil {
		t.Fatal(err)
	}
	if kind != taskauthority.KindReview {
		// A launch that began but bound nothing leaves teardown nothing to
		// release.
		launch := taskauthority.CanonicalBeginSpawnRequest{HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), SnapshotDigest: strings.Repeat("d", 64), Backend: "tmux", Harness: "pi", Model: "model", Effort: "high", Mode: "local-only", Kind: kind, LaunchID: "launch-" + taskID, WindowLabel: "window-" + taskID, WorktreeReservationID: "wt-" + taskID, WorktreeFenceToken: "fence-" + taskID, EndpointReservationID: "ep-" + taskID, EndpointFenceToken: "ep-fence-" + taskID, SeatPathDirs: []string{"/usr/bin"}, EndpointIncarnation: "ep-inc-" + taskID, Reason: "spawn"}
		launch.SeatEnv = taskauthority.SeatEnv{TempDir: "/tmp", UserHome: "/home/tester"}
		if _, err := auth.BeginSpawn(mustCanonicalOp(t, "op-launch-"+taskID, launch), launch); err != nil {
			t.Fatal(err)
		}
	}
	return homeDir
}

// The CLI teardown holds while a report its reporter filed is not settled, and
// --force retires past it. A scout's done report never held; any other
// kind's does. A review task stands in for the other kinds: it reaches the
// hold with no launched worktree, and its done report is the verdict line the
// watcher writes.
func TestTeardownCmdHoldsForUnsettledReports(t *testing.T) {
	const refusal = "report(s) its parent has not settled"
	for _, tc := range []struct {
		name    string
		kind    string
		state   string
		settle  bool
		force   bool
		wantErr string
	}{
		{"unsettled report", taskauthority.KindScout, "failed", false, false, refusal},
		{"settled report", taskauthority.KindScout, "failed", true, false, ""},
		{"forced past an unsettled report", taskauthority.KindScout, "failed", false, true, ""},
		{"unsettled scout done report", taskauthority.KindScout, "done", false, false, ""},
		{"unsettled review done report", taskauthority.KindReview, "done", false, false, refusal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			soldierID := "teardown-report-test"
			tmpDir := seedRetireAuthority(t, soldierID, tc.kind)
			if err := os.MkdirAll(filepath.Join(tmpDir, "data", soldierID), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fleet.ReportPath(tmpDir, soldierID, 1), []byte("# report"), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.kind == taskauthority.KindReview {
				if _, err := orchestrator.AppendReportStatus(tmpDir, soldierID, domain.ReportStatusLine(tc.state, "verdict recorded", "review-verdict-d1")); err != nil {
					t.Fatal(err)
				}
			} else if err := reportAs(t, "soldier", tmpDir, tmpDir, soldierID, tc.kind, tc.state, "report"); err != nil {
				t.Fatalf("report: %v", err)
			}
			if tc.settle {
				settleReports(t, tmpDir)
			}
			args := []string{"teardown", soldierID, "--home", tmpDir}
			if tc.force {
				args = append(args, "--force")
			}
			out, err := runRoot(t, args...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("teardown = %v, want %q\n%s", err, tc.wantErr, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("teardown = %v, want it retired\n%s", err, out)
			}
			cursor, err := home.ReportCursorPath(tmpDir, soldierID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(cursor); !os.IsNotExist(err) {
				t.Fatalf("report cursor after teardown: %v, want it removed", err)
			}
		})
	}
}

// seedDoneScoutForPromote seeds one done scout task through the canonical
// Authority and writes the generation-named report the promote preflight
// requires.
func seedDoneScoutForPromote(t *testing.T, homeDir, taskID, project string) *taskauthority.Canonical {
	t.Helper()
	auth := testAuthorityFor(t, homeDir)
	tid := mustTaskIDFor(t, taskID)
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      tid,
		Owner:       "owner",
		Description: "explore",
		Kind:        "scout",
		ScoutScope:  "investigate the requested question",
		Reason:      "test",
	}
	if pid, err := domain.NewProjectID(project); err == nil {
		createReq.Project = pid
	}
	if _, err := auth.Create(mustCanonicalOp(t, "op-create-scout-"+taskID, createReq), createReq); err != nil {
		t.Fatal(err)
	}
	completeReq := taskauthority.CanonicalCompleteRequest{
		HomeID:       auth.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(1, 1),
		To:           taskauthority.PhaseDone,
		Reason:       "explored",
	}
	if _, err := auth.Complete(mustCanonicalOp(t, "op-complete-scout-"+taskID, completeReq), completeReq); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(homeDir, "data", taskID)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.ReportPath(homeDir, taskID, agg.Generation), []byte("# findings"), 0644); err != nil {
		t.Fatal(err)
	}
	return auth
}

// TestPromoteRoutesThroughTaskAuthority proves `promote` commits the scout →
// ship kind change through the composed canonical Task Authority.
func TestPromoteRoutesThroughTaskAuthority(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "proj-x")
	auth := seedDoneScoutForPromote(t, homeDir, "scout-a", "proj-x")

	if out, err := runTaskCommand(t, []string{"promote", "scout-a", "--home", homeDir}); err != nil {
		t.Fatalf("promote: %v\n%s", err, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "scout-a"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Definition.Kind != "ship" {
		t.Fatalf("canonical kind = %q, want ship", agg.Definition.Kind)
	}
}

// TestPromoteRefusesNonTerminalScout proves `promote` fails closed through
// the Authority: a queued scout (or any non-terminal scout) is refused and
// the canonical record is never mutated.
func TestPromoteRefusesNonTerminalScout(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	tid := mustTaskIDFor(t, "scout-b")
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      tid,
		Owner:       "owner",
		Description: "explore",
		Kind:        "scout",
		ScoutScope:  "investigate the requested question",
		Reason:      "test",
	}
	if _, err := auth.Create(mustCanonicalOp(t, "op-create-scout-b", createReq), createReq); err != nil {
		t.Fatal(err)
	}
	if out, err := runTaskCommand(t, []string{"promote", "scout-b", "--home", homeDir}); err == nil {
		t.Fatalf("promote queued scout succeeded:\n%s", out)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Definition.Kind != "scout" || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("aggregate mutated on refused promote = %+v", agg)
	}
}

// TestPromoteRefusesAbsentTask proves `promote` fails closed on a task with
// no canonical record: the CLI preflight reads the Authority and the typed
// ErrNotFound invariant is surfaced without creating or mutating anything.
func TestPromoteRefusesAbsentTask(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	out, err := runTaskCommand(t, []string{"promote", "scout-absent", "--home", homeDir})
	if err == nil {
		t.Fatalf("promote absent task succeeded:\n%s", out)
	}
	if !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("promote absent task err = %v, want typed ErrNotFound", err)
	}
	// Nothing was created: the canonical view stays empty.
	if _, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "scout-absent")); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("absent task appears after failed promote: %v", err)
	}
}

// gitOnlyPath restricts PATH to git alone: the gate-agent check refuses when git
// cannot run, and the backend lookups under test must still find no tmux or herdr.
func gitOnlyPath(t *testing.T) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestWriteTeardownStepsListsPartialSteps proves a failed teardown still lists
// the steps it completed, and a nil result prints nothing.
func TestWriteTeardownStepsListsPartialSteps(t *testing.T) {
	var b strings.Builder
	writeTeardownSteps(&b, &fleet.TeardownResult{Steps: []string{"released lease", "cleanup pending"}})
	if want := "  - released lease\n  - cleanup pending\n"; b.String() != want {
		t.Fatalf("steps = %q, want %q", b.String(), want)
	}

	var none strings.Builder
	writeTeardownSteps(&none, nil)
	if none.Len() != 0 {
		t.Fatalf("nil result printed %q", none.String())
	}
}
