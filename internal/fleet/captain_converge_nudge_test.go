package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

func TestConvergeSubmitsFastForwardAndPendingNudges(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	const id = "nudge-converge"
	captainHome := filepath.Join(parent, "captains", id)
	worktreeCaptainHome(t, captainHome, id)
	sourceRepo := readCaptainProvenance(captainHome)
	if sourceRepo == "" {
		t.Fatal("captain provenance has no source repository")
	}
	if err := os.WriteFile(filepath.Join(sourceRepo, "AGENTS.md"), []byte("# Updated instructions\n"), 0644); err != nil {
		t.Fatal(err)
	}
	convergeNudgeGit(t, sourceRepo, "add", "AGENTS.md")
	convergeNudgeGit(t, sourceRepo, "commit", "-m", "update instructions")
	convergeNudgeGit(t, sourceRepo, "push", "origin", "main")
	updatedCommit := convergeNudgeGit(t, sourceRepo, "rev-parse", "main")

	storeTestDocuments(t, parent, config.FleetBaseDocument{
		SchemaVersion:  config.FleetBaseSchemaVersion,
		Config:         config.ProjectOverlay{Backend: "tmux", SoldierHarness: "pi"},
		CaptainProfile: config.CaptainProfile{Harness: "pi"},
	}, []testProjectRecord{{Name: id, Path: captainHome}}, []testCaptainRecord{{ID: id, Home: captainHome, Project: id}})
	if err := config.StoreProjectOverlay(parent, id, config.ProjectOverlay{}); err != nil {
		t.Fatal(err)
	}
	writeCaptainMeta(t, parent, id, captainHome, "nudge-window")

	endpoint := &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}
	caps := ConvergeCapabilities{
		Continuity:   noopCaptainContinuity{},
		Messaging:    noopCaptainMessaging{},
		Watcher:      noopCaptainWatcher{},
		Notification: &captainNotificationTransport{acknowledged: true},
		Mailbox:      &captainTestMailboxSender{acknowledged: true},
		Probe:        &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}},
		Nudge:        endpoint,
	}
	registered := []Info{{ID: id, Home: captainHome}}
	if _, err := Converge(parent, registered, caps); err != nil {
		t.Fatalf("Converge fast-forward: %v", err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("fast-forward nudge calls = %d, want 1", endpoint.calls)
	}
	assertConvergeNudgeApplied(t, parent, id, updatedCommit)

	// A pending marker left by an earlier failed send is retried even when no
	// new fast-forward occurs in this converge cycle.
	digest, err := instructionSurfaceDigest(captainHome, updatedCommit)
	if err != nil {
		t.Fatal(err)
	}
	message := "instruction surface changed in " + updatedCommit[:8]
	if err := writeNudgeMarker(parent, id, captainHome, updatedCommit, digest, message); err != nil {
		t.Fatalf("write pending nudge marker: %v", err)
	}
	if _, err := Converge(parent, registered, caps); err != nil {
		t.Fatalf("Converge pending nudge retry: %v", err)
	}
	if endpoint.calls != 2 {
		t.Fatalf("pending nudge retry calls = %d, want 2 total", endpoint.calls)
	}
	assertConvergeNudgeApplied(t, parent, id, updatedCommit)
}

func assertConvergeNudgeApplied(t *testing.T, parent, id, commit string) {
	t.Helper()
	marker, err := readNudgeMarker(parent, id)
	if err != nil {
		t.Fatal(err)
	}
	if marker != nil {
		t.Fatalf("nudge marker remains after acknowledged submission: %v", marker)
	}
	meta, err := home.ReadMeta(parent, taskIDForCaptain(id))
	if err != nil {
		t.Fatal(err)
	}
	if meta["applied_commit"] != commit {
		t.Fatalf("applied_commit = %q, want %q", meta["applied_commit"], commit)
	}
	if meta["applied_digest"] == "" {
		t.Fatal("applied_digest is empty after acknowledged nudge")
	}
}

func convergeNudgeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
