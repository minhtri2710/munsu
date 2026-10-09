package fleet

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefuseStaleTaskBranch(t *testing.T) {
	t.Run("new generation refuses another commit before launch", func(t *testing.T) {
		f := newLaunchFixture(t, "stale-branch")
		reopenTaskForFallbackTest(t, f)
		branch := "mu/" + f.taskID
		runGitForSpawnBinding(t, f.repoPath, "switch", "-c", branch)
		if err := os.WriteFile(filepath.Join(f.repoPath, "prior-generation.txt"), []byte("prior generation\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGitForSpawnBinding(t, f.repoPath, "add", "prior-generation.txt")
		runGitForSpawnBinding(t, f.repoPath, "commit", "-m", "prior generation work")
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = f.repoPath
		commitBytes, err := cmd.Output()
		if err != nil {
			t.Fatalf("git rev-parse HEAD: %v", err)
		}
		commit := strings.TrimSpace(string(commitBytes))
		runGitForSpawnBinding(t, f.repoPath, "switch", "main")

		err = runLaunchPhases(f, "")
		if err == nil || !strings.Contains(err.Error(), "spawn refused") {
			t.Fatalf("runLaunchPhases error = %v, want stale branch refusal", err)
		}
		for _, want := range []string{
			branch,
			commit,
			"git bundle create <file> " + branch,
			"git branch -D " + branch,
			"git branch -m " + branch + " <new-name>",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err, want)
			}
		}
		agg := f.aggregate()
		if agg.Generation != 2 {
			t.Fatalf("generation = %s, want new generation 2", agg.Generation)
		}
		if agg.LaunchEvidence != nil || agg.AcquiredEndpoint != nil || f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
			t.Fatalf("refusal launched a soldier: evidence=%+v acquired=%+v endpointCreates=%d submissions=%d", agg.LaunchEvidence, agg.AcquiredEndpoint, f.endpoints.createCount(), f.endpoints.submitCount())
		}
		for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName, ManifestName} {
			if _, err := os.Stat(filepath.Join(f.runner.wtPath, name)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("launch artifact %s exists after refusal (stat err=%v)", name, err)
			}
		}
	})

	t.Run("absent branch proceeds to next phase", func(t *testing.T) {
		f := newLaunchFixture(t, "absent-branch")
		if err := runLaunchPhases(f, "stale-task-branch"); !errors.Is(err, errCrashSimulated) {
			t.Fatalf("runLaunchPhases = %v, want guard phase to proceed", err)
		}
		if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
			t.Fatalf("endpoint activity before guard phase exit: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
		}
	})

	t.Run("branch at base head proceeds", func(t *testing.T) {
		f := newLaunchFixture(t, "base-branch")
		branch := "mu/" + f.taskID
		runGitForSpawnBinding(t, f.repoPath, "branch", branch)
		if err := runLaunchPhases(f, "stale-task-branch"); !errors.Is(err, errCrashSimulated) {
			t.Fatalf("runLaunchPhases = %v, want branch-at-base phase to proceed", err)
		}
	})

	t.Run("same-generation recorded launch re-entry permits its branch", func(t *testing.T) {
		f := newLaunchFixture(t, "same-generation-branch")
		if err := runLaunchPhases(f, ""); err != nil {
			t.Fatalf("initial launch: %v", err)
		}
		first := f.aggregate()
		if first.LaunchEvidence == nil || first.Worktree == nil {
			t.Fatalf("initial launch evidence/binding missing: %+v", first)
		}

		worktree := first.Worktree.Path
		if err := os.WriteFile(filepath.Join(worktree, "soldier-work.txt"), []byte("soldier work\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGitForSpawnBinding(t, worktree, "add", "soldier-work.txt")
		runGitForSpawnBinding(t, worktree, "commit", "-m", "soldier work")
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = worktree
		commitBytes, err := cmd.Output()
		if err != nil {
			t.Fatalf("git rev-parse HEAD: %v", err)
		}
		commit := strings.TrimSpace(string(commitBytes))
		branch := "mu/" + f.taskID
		runGitForSpawnBinding(t, worktree, "branch", branch, commit)

		if err := runLaunchPhases(f, ""); err != nil {
			t.Fatalf("same-generation recovery with its task branch: %v", err)
		}
		if f.endpoints.createCount() != 1 || f.endpoints.submitCount() != 1 {
			t.Fatalf("re-entry duplicated launch: endpointCreates=%d submissions=%d", f.endpoints.createCount(), f.endpoints.submitCount())
		}
	})
}
