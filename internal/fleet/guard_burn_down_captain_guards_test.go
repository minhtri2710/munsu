package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// ----------------------------------------------------------------------------
// Group A: captain_configreread.go (1 guard)
// ----------------------------------------------------------------------------

func TestEnsureConfigRereadRequirement_NilSender(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Write task meta for captain in parent home with window and backend.
	taskID := taskIDForCaptain(captainID)
	if err := mhome.WriteMeta(parentHome, taskID, map[string]string{
		"kind":    "captain",
		"window":  "win-1",
		"sm_id":   captainID,
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	err := EnsureConfigRereadRequirement(parentHome, captainHome, 1, "test-digest", nil)
	if err == nil || !strings.Contains(err.Error(), "captain mailbox sender capability is required") {
		t.Fatalf("EnsureConfigRereadRequirement err = %v, want sender capability required", err)
	}
}

// ----------------------------------------------------------------------------
// Group B: captain_mailbox.go (3 guards)
// ----------------------------------------------------------------------------

func TestResendNotification_NoWindowInMeta(t *testing.T) {
	parentHome, _, captainID := setupTestHomes(t)
	taskID := taskIDForCaptain(captainID)
	// Write meta without window key
	if err := mhome.WriteMeta(parentHome, taskID, map[string]string{
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	sm := Info{ID: captainID}
	env := &home.Envelope{MessageID: "msg-1", SenderIdentity: "general"}
	err := resendNotification(parentHome, sm, env, &captainTestMailboxSender{})
	if err == nil || !strings.Contains(err.Error(), "no window in meta") {
		t.Fatalf("resendNotification err = %v, want no window in meta", err)
	}
}

func TestResendNotification_NilSender(t *testing.T) {
	parentHome, _, captainID := setupTestHomes(t)
	taskID := taskIDForCaptain(captainID)
	if err := mhome.WriteMeta(parentHome, taskID, map[string]string{
		"window":  "win-1",
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	sm := Info{ID: captainID}
	env := &home.Envelope{MessageID: "msg-1", SenderIdentity: "general"}
	err := resendNotification(parentHome, sm, env, nil)
	if err == nil || !strings.Contains(err.Error(), "captain mailbox sender capability is required") {
		t.Fatalf("resendNotification err = %v, want sender capability required", err)
	}
}

func TestResendNotification_NotAcknowledged(t *testing.T) {
	parentHome, _, captainID := setupTestHomes(t)
	taskID := taskIDForCaptain(captainID)
	if err := mhome.WriteMeta(parentHome, taskID, map[string]string{
		"window":  "win-1",
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	sm := Info{ID: captainID}
	env := &home.Envelope{MessageID: "msg-1", SenderIdentity: "general"}
	sender := &captainTestMailboxSender{acknowledged: false}
	err := resendNotification(parentHome, sm, env, sender)
	if err == nil || !strings.Contains(err.Error(), "resend not acknowledged (status=submitted)") {
		t.Fatalf("resendNotification err = %v, want resend not acknowledged", err)
	}
}

// ----------------------------------------------------------------------------
// Group C: captain_seed_worktree.go (6 guards)
// ----------------------------------------------------------------------------

func TestIsManagedWorktree_NotDirectory(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := isManagedWorktree(filePath)
	if err == nil || !strings.Contains(err.Error(), "exists but is not a directory") {
		t.Fatalf("isManagedWorktree err = %v, want exists but is not a directory", err)
	}
}

func TestSeedCaptain_RefusesMissingRepoBeforeMutation(t *testing.T) {
	h := filepath.Join(t.TempDir(), "captain")
	err := SeedCaptain(CaptainSeedOptions{ID: "captain", Home: h, ParentHome: t.TempDir(), Integration: fakeIntegrationPort{}})
	if err == nil || !strings.Contains(err.Error(), "a project repo is required") {
		t.Fatalf("SeedCaptain err = %v, want project repo required", err)
	}
	if _, statErr := os.Stat(h); !os.IsNotExist(statErr) {
		t.Fatalf("refused seed created %s (stat err = %v)", h, statErr)
	}
}

func TestResolveDefaultBranch_MalformedSymRef(t *testing.T) {
	repoPath := t.TempDir()
	cmd := exec.Command("git", "init", repoPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v, %s", err, string(out))
	}
	cmd = exec.Command("git", "-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/heads/main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git symbolic-ref: %v, %s", err, string(out))
	}

	_, err := resolveDefaultBranch(repoPath)
	if err == nil || !strings.Contains(err.Error(), "unexpected origin/HEAD format") {
		t.Fatalf("resolveDefaultBranch err = %v, want unexpected origin/HEAD format", err)
	}
}

func TestSeedFromWorktree_NilIntegration(t *testing.T) {
	err := seedFromWorktree("id", "homePath", "repoPath", "parentHome", "charter", false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "captain integration capability is required") {
		t.Fatalf("seedFromWorktree err = %v, want integration capability required", err)
	}
}

// ----------------------------------------------------------------------------
// Group D: captain_soldier_queue.go (7 guards)
// ----------------------------------------------------------------------------

func TestEmitReadyEvent_EmptyHome(t *testing.T) {
	_, err := EmitReadyEvent("", "task-1", "key-1", "1")
	if err == nil || !strings.Contains(err.Error(), "emit ready: empty home") {
		t.Fatalf("EmitReadyEvent err = %v, want empty home", err)
	}
}

func TestEmitReadyEvent_EmptyTaskID(t *testing.T) {
	_, err := EmitReadyEvent(t.TempDir(), "", "key-1", "1")
	if err == nil || !strings.Contains(err.Error(), "emit ready: empty task ID") {
		t.Fatalf("EmitReadyEvent err = %v, want empty task ID", err)
	}
}

func TestValidateReadyEvent_KeyMismatch(t *testing.T) {
	ev := &ReadyEvent{EventID: "e1", TaskID: "t1", Key: "actual-key"}
	err := ValidateReadyEvent(ev, "t1", "expected-key", "")
	if err == nil || !strings.Contains(err.Error(), "ready event: key mismatch") {
		t.Fatalf("ValidateReadyEvent err = %v, want key mismatch", err)
	}
}

func TestValidateReadyEvent_NilEvent(t *testing.T) {
	err := ValidateReadyEvent(nil, "t1", "k1", "1")
	if err == nil || !strings.Contains(err.Error(), "ready event: nil") {
		t.Fatalf("ValidateReadyEvent err = %v, want nil event", err)
	}
}

func TestValidateReadyEvent_GenerationMismatch(t *testing.T) {
	ev := &ReadyEvent{EventID: "e1", TaskID: "t1", Key: "k1", EndpointGeneration: 1}
	err := ValidateReadyEvent(ev, "t1", "k1", "2")
	if err == nil || !strings.Contains(err.Error(), "ready event: generation mismatch 1 != 2 (stale event)") {
		t.Fatalf("ValidateReadyEvent err = %v, want generation mismatch", err)
	}
}

func TestValidateReadyEvent_EmptyEventID(t *testing.T) {
	ev := &ReadyEvent{EventID: "", TaskID: "t1", Key: "k1"}
	err := ValidateReadyEvent(ev, "t1", "k1", "")
	if err == nil || !strings.Contains(err.Error(), "ready event: empty event ID") {
		t.Fatalf("ValidateReadyEvent err = %v, want empty event ID", err)
	}
}

func TestValidateReadyEvent_TaskIDMismatch(t *testing.T) {
	ev := &ReadyEvent{EventID: "e1", TaskID: "task-actual", Key: "k1"}
	err := ValidateReadyEvent(ev, "task-expected", "k1", "")
	if err == nil || !strings.Contains(err.Error(), "ready event: task ID mismatch") {
		t.Fatalf("ValidateReadyEvent err = %v, want task ID mismatch", err)
	}
}
