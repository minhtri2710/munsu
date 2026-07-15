package selfupdate

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestVersionString verifies that VersionString produces the expected label.
func TestVersionString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abc1234", "0.1.0-dev+abc1234"},
		{"", "0.1.0-dev+"},
		{"deadbeef", "0.1.0-dev+deadbeef"},
	}
	for _, tc := range tests {
		got := VersionString(tc.input)
		if got != tc.expected {
			t.Errorf("VersionString(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// TestShortHEAD_Isolated verifies that ShortHEAD(root) always returns the
// commit of root, regardless of the process CWD.
func TestShortHEAD_Isolated(t *testing.T) {
	// Create repo A and make an initial commit.
	repoA := t.TempDir()
	initRepo(t, repoA, "first commit in A")
	shaA := shortHEADFromCWD(t, repoA)

	// Create repo B, make a different commit, and chdir there.
	repoB := t.TempDir()
	initRepo(t, repoB, "first commit in B")
	shaB := shortHEADFromCWD(t, repoB)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	if err := os.Chdir(repoB); err != nil {
		t.Fatal(err)
	}

	// ShortHEAD(repoA) must return repoA's commit, not the CWD repo's.
	gotA, err := ShortHEAD(repoA)
	if err != nil {
		t.Fatalf("ShortHEAD(repoA): %v", err)
	}
	if gotA != shaA {
		t.Errorf("ShortHEAD(repoA) = %q, want %q (repo A commit)", gotA, shaA)
	}

	// ShortHEAD(repoB) must still work.
	gotB, err := ShortHEAD(repoB)
	if err != nil {
		t.Fatalf("ShortHEAD(repoB): %v", err)
	}
	if gotB != shaB {
		t.Errorf("ShortHEAD(repoB) = %q, want %q (repo B commit)", gotB, shaB)
	}
}

// TestGitDir_AlwaysSetsDir verifies that gitDir sets Dir and reads the
// correct repository.
func TestGitDir_AlwaysSetsDir(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "test commit")

	out, err := gitDir(repo, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		t.Errorf("expected gitDir to detect repo, got: %s", string(out))
	}
}

// --- helpers ---

// initRepo creates a git repo at root and makes an initial commit.
func initRepo(t *testing.T, root, msg string) {
	t.Helper()
	for _, cmd := range []struct {
		args []string
		desc string
	}{
		{[]string{"init"}, "git init"},
		{[]string{"config", "user.email", "test@test"}, "set email"},
		{[]string{"config", "user.name", "test"}, "set name"},
		{[]string{"commit", "--allow-empty", "-m", msg}, "first commit"},
	} {
		c := exec.Command("git", cmd.args...)
		c.Dir = root
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%s: %v", cmd.desc, err)
		}
	}
}

// shortHEADFromCWD returns the short commit at root by running git directly
// from the CWD package (not via ShortHEAD), for use as the expected value.
func shortHEADFromCWD(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("shortHEADFromCWD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

