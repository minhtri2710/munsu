package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// ----------------------------------------------------------------------------
// Group A: captain_configreread.go (1 guard)
// ----------------------------------------------------------------------------

func TestEnsureConfigRereadRequirement_NilSender(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)

	// Write task meta for captain in parent home with window and backend.
	taskID := taskIDForCaptain(captainID)
	if err := home.WriteMeta(parentHome, taskID, map[string]string{
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
	if err := home.WriteMeta(parentHome, taskID, map[string]string{
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
	if err := home.WriteMeta(parentHome, taskID, map[string]string{
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
	if err := home.WriteMeta(parentHome, taskID, map[string]string{
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

func TestSeedFromWorktree_NilIntegration(t *testing.T) {
	err := seedFromWorktree("id", "homePath", "repoPath", "parentHome", "charter", false, "", nil)
	if err == nil || !strings.Contains(err.Error(), "captain integration capability is required") {
		t.Fatalf("seedFromWorktree err = %v, want integration capability required", err)
	}
}
