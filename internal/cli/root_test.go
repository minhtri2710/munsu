package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

func TestCheckTangle(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a bare origin repo
	originDir := filepath.Join(tmpDir, "origin.git")
	runCmd(t, "", "git", "init", "--bare", originDir)

	// Clone from the bare origin so origin/HEAD is set up
	repoDir := filepath.Join(tmpDir, "repo")
	runCmd(t, "", "git", "clone", originDir, repoDir)

	// Create an initial commit on the default branch
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")
	runCmd(t, repoDir, "git", "push", "-u", "origin", "HEAD")
	runCmd(t, repoDir, "git", "remote", "set-head", "origin", "--auto")

	// Determine the default branch from origin/HEAD
	out, err := exec.Command("git", "-C", repoDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		t.Fatalf("origin/HEAD not set: %v", err)
	}
	defaultBranch := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
	t.Logf("default branch: %s", defaultBranch)

	// Test 1: on default branch -> no tangle
	if err := checkTangle(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch %q, got: %v", defaultBranch, err)
	}

	// Test 2: create and switch to a feature branch -> tangle
	runCmd(t, repoDir, "git", "checkout", "-b", "feature-branch")
	err = checkTangle(repoDir, "test-project")
	if err == nil {
		t.Fatal("expected tangle error for feature branch, got nil")
	}
	if !strings.Contains(err.Error(), "cannot spawn") {
		t.Fatalf("expected 'cannot spawn' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "test-project") {
		t.Fatalf("expected project name 'test-project' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "feature-branch") {
		t.Fatalf("expected branch name 'feature-branch' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Use a detached HEAD or a worktree") {
		t.Fatalf("expected remediation suggestion in error, got: %v", err)
	}

	// Test 3: switch back to default branch -> no tangle
	runCmd(t, repoDir, "git", "checkout", defaultBranch)
	if err := checkTangle(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch, got: %v", err)
	}

	// Test 4: detached HEAD -> no tangle
	runCmd(t, repoDir, "git", "checkout", "--detach", defaultBranch)
	if err := checkTangle(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on detached HEAD, got: %v", err)
	}

	// Test 5: non-existent directory -> no error (skip check gracefully)
	if err := checkTangle(filepath.Join(tmpDir, "nonexistent"), "test-project"); err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}

	// Test 6: non-git directory -> no error (skip check gracefully)
	plainDir := filepath.Join(tmpDir, "plain")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := checkTangle(plainDir, "test-project"); err != nil {
		t.Fatalf("expected no error for non-git dir, got: %v", err)
	}
	// Test 7: no remote but main branch exists (fallback path)
	noRemoteDir := filepath.Join(tmpDir, "no-remote")
	runCmd(t, "", "git", "init", "-b", "main", noRemoteDir)
	runCmd(t, noRemoteDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, noRemoteDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(noRemoteDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, noRemoteDir, "git", "add", ".")
	runCmd(t, noRemoteDir, "git", "commit", "-m", "initial")

	// On main (default) -> no tangle
	if err := checkTangle(noRemoteDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch (no remote), got: %v", err)
	}

	// On feature branch -> tangle (main detected via fallback)
	runCmd(t, noRemoteDir, "git", "checkout", "-b", "feature-branch")
	err = checkTangle(noRemoteDir, "test-project")
	if err == nil {
		t.Fatal("expected tangle error on feature branch (no remote, fallback), got nil")
	}
	if !strings.Contains(err.Error(), "cannot spawn") {
		t.Fatalf("expected 'cannot spawn' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "feature-branch") {
		t.Fatalf("expected branch name 'feature-branch' in error, got: %v", err)
	}

	// Detached HEAD on no-remote repo -> no tangle
	defaultBranchBR := "main"
	runCmd(t, noRemoteDir, "git", "checkout", "--detach", defaultBranchBR)
	if err := checkTangle(noRemoteDir, "test-project"); err != nil {
		t.Fatalf("expected no error on detached HEAD (no remote), got: %v", err)
	}
}

func TestBuildHarnessLaunch_Agy(t *testing.T) {
	tmpl := harness.Templates[harness.Agy]
	cmd := buildHarnessLaunch(harness.Agy, tmpl)

	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Error("agy launch command should contain --dangerously-skip-permissions")
	}
	if !strings.Contains(cmd, "--model") {
		t.Error("agy launch command should contain --model")
	}
	if !strings.Contains(cmd, "-i") {
		t.Error("agy launch command should contain -i")
	}
	if !strings.Contains(cmd, "Gemini 3.5 Flash (Medium)") {
		t.Error("agy launch command should contain the default model name")
	}
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed: %v\n%s", name, args, err, string(out))
	}
}

// Fixture: r6 transcript capture of pi ready UI (trimmed to key lines).
// Shows thinking off, checkpoint, model line chrome.
const piReadyCapture = `spec-driven-development, spec-to-code-compliance, srcwalk, stitch-design-taste,
supply-chain-risk-auditor, tasks-axi, tdd, teach, test-driven-development,
to-spec, to-tickets, triage, using-agent-skills, variant-analysis, vuln-report,
wayfinder, wizard, wooyun-legacy, writing-beats, writing-fragments,
writing-great-skills, writing-shape, zeroize-audit

[Extensions]
  @eko24ive/pi-ask:src, @ff-labs/pi-fff@latest:src,
@heyhuynhgiabuu/pi-search@latest:dist, @heyhuynhgiabuu/pi-task:dist,
@juicesharp/rpiv-advisor, @ogulcancelik/pi-herdr,
@sting8k/pi-srcwalk@latest:pi-srcwalk, @sting8k/pi-vcc@latest,
@vigolium/piolium@latest:piolium, herdr-agent-state.ts,
joelhooks/pi-rhizomatic:pi-rhizomatic.ts, pi-augment@latest:src,
pi-boomerang@latest, pi-clinepass-provider:src, pi-hashline-edit-pro,
pi-model-switch@latest, pi-rewind@latest:src, rtk.ts

[Themes]
  piolium-srcery

[Skill conflicts]
  "herdr" collision:
    ✓ auto (user) ~/.pi/agent/skills/herdr/SKILL.md
    ✗ ~/.pi/agent/npm/node_modules/@ogulcancelik/pi-herdr/skills/herdr/SKILL.md
(skipped)


 Advisor restored: zai/glm-5.2, high

────────────────────────────────────────────────────────────────────────────────

────────────────────────────────────────────────────────────────────────────────
~/.treehouse/real-estate-320f76/2/real-estate (detached)
0.0%/256k (auto) • xp                      (cliproxyapi) grok-4.5 • thinking off
◆ 1 checkpoint
`

// Fixture: r6 transcript capture of agy trust prompt.
const agyTrustCapture = `Accessing workspace:

/Users/beowulf/.treehouse/real-estate-320f76/2/real-estate

Do you trust the contents of this project?

Antigravity CLI requires permission to read, edit, and execute files here.

> Yes, I trust this folder
  No, exit

  ↑/↓ Navigate · enter Confirm
                                                    Claude Sonnet 4.6 (Thinking)
`

func TestPiReadyPatterns(t *testing.T) {
	patterns := readyPatterns[harness.Pi]
	if len(patterns) == 0 {
		t.Fatal("pi ready patterns should not be empty")
	}

	// Only the patterns that appear in the actual pi capture should match.
	// F6.1 added checkpoint, thinking off, ◆ because old patterns never matched.
	patternsToCheck := []string{"checkpoint", "thinking off", "◆"}
	for _, p := range patternsToCheck {
		if !strings.Contains(piReadyCapture, p) {
			t.Errorf("pi ready pattern %q should match pi capture", p)
		}
	}
}

func TestAgyTrustDetection(t *testing.T) {
	// Verify trust prompt patterns match the actual agy trust capture.
	if !strings.Contains(agyTrustCapture, "Do you trust") {
		t.Error("trust pattern 'Do you trust' should match agy trust capture")
	}
	if !strings.Contains(agyTrustCapture, "Yes, I trust this folder") {
		t.Error("trust pattern 'Yes, I trust this folder' should match agy trust capture")
	}

	// Verify isTrustPrompt detects trust in the agy capture.
	if !isTrustPrompt(agyTrustCapture, harness.Agy) {
		t.Error("isTrustPrompt should detect trust in agy capture")
	}

	// Verify trust is NOT detected in pi capture.
	if isTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("isTrustPrompt should NOT detect trust in pi capture")
	}

	// Verify trust is NOT detected in pi capture when checking agy patterns.
	if isTrustPrompt(piReadyCapture, harness.Agy) {
		t.Error("isTrustPrompt should NOT detect trust in pi capture with agy harness")
	}
}

func TestAgyReadyPatterns(t *testing.T) {
	patterns := readyPatterns[harness.Agy]
	if len(patterns) == 0 {
		t.Fatal("agy ready patterns should not be empty")
	}

	// These patterns should NOT match the trust capture (they're ready patterns).
	for _, p := range patterns {
		if strings.Contains(agyTrustCapture, p) {
			t.Errorf("agy ready pattern %q should NOT match trust capture", p)
		}
	}
}

func TestDefaultReadyPatterns(t *testing.T) {
	if len(defaultReadyPatterns) == 0 {
		t.Fatal("defaultReadyPatterns should not be empty")
	}
	patterns := defaultReadyPatterns
	for _, p := range patterns {
		if p == "" {
			t.Error("defaultReadyPatterns should not contain empty patterns")
		}
	}
}
