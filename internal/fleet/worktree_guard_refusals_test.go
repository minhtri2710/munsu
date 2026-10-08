//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

// Each test builds the state one refusal guard exists for and asserts the
// guard's own message, so a later refusal with a similar message cannot pass.
// Every refusal must also leave the worktree and its lease untouched.

// Disjunct built: an untracked entry that is neither manifest-listed nor a
// deferred guard directory.
func TestWorktreeGuardRefusalUnlistedContentRetainsLease(t *testing.T) {
	opts, auth, worktree := retireScoutFixture(t, true)
	stray := filepath.Join(worktree, "stray-unlisted.txt")
	if err := os.WriteFile(stray, []byte("unlisted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	charterPath := filepath.Join(worktree, CharterName)

	// Force skips the ship safety check (retirement_task.go:661), so the
	// untracked entry reaches the pre-return disposal check.
	opts.Force = true
	rec := &recordingTeardown{alive: false}
	_, err := RetireTask(opts, rec, fakeRetirementJournals{}, auth)
	if err == nil || !strings.Contains(err.Error(), "worktree has unlisted content: ?? stray-unlisted.txt") {
		t.Fatalf("RetireTask error = %v, want unlisted-content refusal", err)
	}
	if len(rec.returned) != 0 {
		t.Fatalf("returned=%v, want lease retained", rec.returned)
	}
	if got, readErr := os.ReadFile(stray); readErr != nil || string(got) != "unlisted\n" {
		t.Fatalf("unlisted file removed or changed: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(charterPath); statErr != nil {
		t.Fatalf("manifest-listed charter removed by refused disposal: %v", statErr)
	}
}

// Disjunct built: guardDir empty while a launch script requires a guard.
func TestWorktreeGuardRefusalPrepareLaunchFilesRequiresGuardDir(t *testing.T) {
	prepared, err := prepareLaunchFiles("charter", nil, &LaunchEnvelope{}, "prompt", []byte("script"), harness.Pi, "", "launch|1|fence")
	if err == nil || !strings.Contains(err.Error(), "deferred launch guard identity path and content are required") {
		t.Fatalf("prepareLaunchFiles error = %v, want guard identity refusal", err)
	}
	if prepared.files != nil || prepared.manifest != nil {
		t.Fatalf("refused preparation returned files=%v manifest=%q", prepared.names, prepared.manifest)
	}
}

// Disjunct built: a second .soldier-launch-guard- directory beside the one the
// manifest declares.
func TestWorktreeGuardRefusalVerifyRejectsSiblingGuardDirectory(t *testing.T) {
	tmp := t.TempDir()
	wt, digest := setupWorktreeWithManifest(t, filepath.Join(tmp, "worktree"), filepath.Join(tmp, "remote.git"), nil)
	sibling := filepath.Join(wt, ".soldier-launch-guard-stray-2")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}

	err := VerifyLaunchArtifacts(wt, digest)
	if err == nil || !strings.Contains(err.Error(), `unexpected deferred launch guard directory ".soldier-launch-guard-stray-2"`) {
		t.Fatalf("VerifyLaunchArtifacts error = %v, want sibling guard refusal", err)
	}
	if _, statErr := os.Stat(sibling); statErr != nil {
		t.Fatalf("sibling guard directory removed by verification: %v", statErr)
	}
}

// Disjunct built: git status reports an entry in a reservation that has no
// canonical binding.
func TestWorktreeGuardRefusalUnboundWorktreeRejectsUntrackedEntry(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "reservation")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	setupGitRepo(t, wt, filepath.Join(tmp, "remote.git"))
	stray := filepath.Join(wt, "stray-unbound.txt")
	if err := os.WriteFile(stray, []byte("unbound\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := verifyUnboundWorktreeClean(wt)
	if err == nil || !strings.Contains(err.Error(), "worktree has unanchored or dirty content") {
		t.Fatalf("verifyUnboundWorktreeClean error = %v, want unanchored-content refusal", err)
	}
	if _, statErr := os.Stat(stray); statErr != nil {
		t.Fatalf("untracked reservation content removed by verification: %v", statErr)
	}
}
