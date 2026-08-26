//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestRetirementIntentEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		force bool
	}{
		{name: "normal", force: false},
		{name: "forced", force: true},
	} {
		t.Run(tc.name+" teardown retains generation-scoped report", func(t *testing.T) {
			homeDir := t.TempDir()
			taskID := "evidence-report-" + tc.name
			auth := canonicalMergeTestAuth(t, homeDir, taskID)
			if err := os.WriteFile(filepath.Join(homeDir, "state", taskID+".meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\n"), 0644); err != nil {
				t.Fatal(err)
			}
			dataDir := filepath.Join(homeDir, "data", taskID)
			if err := os.MkdirAll(dataDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("generation 1 findings\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: tc.force}, fakeTeardown{}, fakeRetirementJournals{}, auth); err != nil {
				t.Fatal(err)
			}
			entries := directoryEntries(t, dataDir)
			t.Logf("%s teardown: data/%s entries=%v", tc.name, taskID, entries)
			body, err := os.ReadFile(filepath.Join(dataDir, "report-g1.md"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s teardown: report-g1.md=%q; report.md absent=%t", tc.name, strings.TrimSpace(string(body)), fileAbsent(filepath.Join(dataDir, "report.md")))
			if string(body) != "generation 1 findings\n" || !fileAbsent(filepath.Join(dataDir, "report.md")) {
				t.Fatal("retired report was not preserved under a generation-scoped name")
			}
			if err := scoutSafetyCheck(Options{HomeDir: homeDir, ID: taskID}, map[string]string{"kind": "scout"}); err == nil {
				t.Fatal("later generation unexpectedly accepted retired report evidence")
			} else {
				t.Logf("%s later-generation safety check refused retired report: %v", tc.name, err)
			}
		})
	}

	t.Run("crash resume commits cleanup once", func(t *testing.T) {
		homeDir := t.TempDir()
		taskID := "evidence-crash"
		auth := canonicalMergeTestAuth(t, homeDir, taskID)
		wtDir := filepath.Join(homeDir, "worktrees", taskID)
		if err := os.MkdirAll(wtDir, 0755); err != nil {
			t.Fatal(err)
		}
		seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt", "fence-wt")
		seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep", "fence-ep")
		writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
		opts := Options{HomeDir: homeDir, ID: taskID, Force: true}
		first := &recordingTeardown{alive: true, disposeErr: errors.New("simulated crash")}
		if _, err := RetireTask(opts, first, fakeRetirementJournals{}, auth); err == nil {
			t.Fatal("expected interrupted cleanup")
		}
		before, err := auth.Get(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("after interrupted cleanup: generation=%d revision=%d phase=%s claim=%s dispose_calls=%d", before.Generation, before.Revision, before.Phase, cleanupStatus(before.CleanupClaim), len(first.disposed))
		second := &recordingTeardown{alive: true}
		if _, err := RetireTask(opts, second, fakeRetirementJournals{}, auth); err != nil {
			t.Fatal(err)
		}
		after, err := auth.Get(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("after resume: generation=%d revision=%d phase=%s claim=%s return_calls=%d revision_delta=%d", after.Generation, after.Revision, after.Phase, cleanupStatus(after.CleanupClaim), len(second.returned), after.Revision-before.Revision)
		if after.Revision != before.Revision+1 || len(second.returned) != 1 {
			t.Fatalf("cleanup resume was not exactly once: before=%d after=%d returned=%v", before.Revision, after.Revision, second.returned)
		}
	})

	t.Run("terminal continuation refuses reopened generation", func(t *testing.T) {
		homeDir := t.TempDir()
		taskID := "evidence-stale"
		auth := canonicalMergeTestAuth(t, homeDir, taskID)
		wtDir := filepath.Join(homeDir, "worktrees", taskID)
		if err := os.MkdirAll(wtDir, 0755); err != nil {
			t.Fatal(err)
		}
		seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
		seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
		writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
		opts := Options{HomeDir: homeDir, ID: taskID, Force: true}
		if _, err := RetireTask(opts, &recordingTeardown{alive: true}, fakeRetirementJournals{}, auth); err != nil {
			t.Fatal(err)
		}
		prior, err := auth.Get(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		reopen := taskauthority.CanonicalReopenRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(uint64(prior.Generation), uint64(prior.Revision)), Reason: "evidence reopen"}
		if _, err := auth.Reopen(mustFleetOperation(t, "evidence-reopen", reopen), reopen); err != nil {
			t.Fatal(err)
		}
		seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-2", "fence-wt-2")
		seedEndpointEvidence(t, auth, taskID, "@2", "lease-ep-2", "fence-ep-2")
		writeRetireMeta(t, homeDir, taskID, "@2", wtDir)
		stale := &recordingTeardown{alive: true}
		_, err = RetireTask(opts, stale, fakeRetirementJournals{}, auth)
		var refusal *RetirementStaleTeardownError
		if !errors.As(err, &refusal) {
			t.Fatalf("continuation error=%T %v", err, err)
		}
		current, err := auth.Get(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("stale continuation refused: prior_generation=%d terminal=%s current_generation=%d current_phase=%s cleanup_claim=%s dispose_calls=%d return_calls=%d", refusal.PriorGeneration, refusal.TerminalStatus, current.Generation, current.Phase, cleanupStatus(current.CleanupClaim), len(stale.disposed), len(stale.returned))
		if current.Generation != 2 || current.Phase != taskauthority.PhaseWorking || current.CleanupClaim != nil || len(stale.disposed) != 0 || len(stale.returned) != 0 {
			t.Fatal("stale continuation disturbed reopened generation")
		}
	})
}

func directoryEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	sort.Strings(result)
	return result
}

func fileAbsent(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func cleanupStatus(claim *taskauthority.CleanupClaim) string {
	if claim == nil {
		return "none"
	}
	return fmt.Sprintf("%s(g%d)", claim.Status, claim.Generation)
}
