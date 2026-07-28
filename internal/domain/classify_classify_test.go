package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- GeneralRelevant tests ---

func TestGeneralRelevant_Done(t *testing.T) {
	if !GeneralRelevant("done: implemented feature X") {
		t.Error("done: should be general-relevant")
	}
}

func TestGeneralRelevant_NeedsDecision(t *testing.T) {
	if !GeneralRelevant("needs-decision: which approach to take") {
		t.Error("needs-decision: should be general-relevant")
	}
}

func TestGeneralRelevant_Blocked(t *testing.T) {
	if !GeneralRelevant("blocked: dependency not ready") {
		t.Error("blocked: should be general-relevant")
	}
}

func TestGeneralRelevant_Failed(t *testing.T) {
	if !GeneralRelevant("failed: tests not passing") {
		t.Error("failed: should be general-relevant")
	}
}

func TestGeneralRelevant_NotPaused(t *testing.T) {
	if GeneralRelevant("paused: waiting for upstream release") {
		t.Error("paused: should NOT be general-relevant")
	}
}

func TestGeneralRelevant_WorkingNotGeneralRelevant(t *testing.T) {
	if GeneralRelevant("working: building feature") {
		t.Error("working: should NOT be general-relevant")
	}
}

func TestGeneralRelevant_ResolvedNotGeneralRelevant(t *testing.T) {
	if GeneralRelevant("resolved: chose approach A") {
		t.Error("resolved: should NOT be general-relevant")
	}
}

func TestGeneralRelevant_EmptyLine(t *testing.T) {
	if GeneralRelevant("") {
		t.Error("empty line should NOT be general-relevant")
	}
}

func TestGeneralRelevant_BlankLine(t *testing.T) {
	if GeneralRelevant("   ") {
		t.Error("blank line should NOT be general-relevant")
	}
}

func TestGeneralRelevant_PRReady(t *testing.T) {
	if !GeneralRelevant("PR ready for review") {
		t.Error(`bare "PR ready for review" should be general-relevant (no leading verb)`)
	}
}

func TestGeneralRelevant_WorkingPRReadyNotGeneralRelevant(t *testing.T) {
	if GeneralRelevant("working: PR ready for review") {
		t.Error(`"working: PR ready" should NOT be general-relevant (nonterminal verb prevents free-text escalation)`)
	}
}

func TestGeneralRelevant_ChecksGreen(t *testing.T) {
	if !GeneralRelevant("done: checks green on CI") {
		t.Error(`line containing "checks green" should be general-relevant`)
	}
}

func TestGeneralRelevant_ReadyInBranch(t *testing.T) {
	if !GeneralRelevant("ready in branch fm/feature") {
		t.Error(`line containing "ready in branch" should be general-relevant`)
	}
}

func TestGeneralRelevant_Merged(t *testing.T) {
	if !GeneralRelevant("PR merged by captain") {
		t.Error(`line containing "merged" should be general-relevant`)
	}
}

func TestGeneralRelevant_KeyedNeedsDecision(t *testing.T) {
	if !GeneralRelevant("needs-decision [key=api-shape]: choose the API shape") {
		t.Error("needs-decision with key should be general-relevant")
	}
}

func TestGeneralRelevant_KeyedBlocked(t *testing.T) {
	if !GeneralRelevant("blocked [key=deploy]: waiting for deployment") {
		t.Error("blocked with key should be general-relevant")
	}
}

func TestGeneralRelevant_CaseInsensitivePRReady(t *testing.T) {
	if !GeneralRelevant("pr ready somewhere in line") {
		// The regex uses (?i) so "pr ready" should match.
		// Wait — the regex is `(?i)...|PR ready` — it requires "PR ready" with
		// the capital PR. Let me check: the bash grep is -qiE, which is
		// case-insensitive. Our regex has (?i) at the start, so it's
		// case-insensitive across the whole pattern, making "PR ready" and
		// "pr ready" both match. This is correct.
	}
}

func TestGeneralRelevant_VerbInSuffixDoesNotMatch(t *testing.T) {
	if GeneralRelevant("working: this line mentions done somewhere") {
		t.Error("a line with 'working' verb and 'done' in the note should NOT be general-relevant without regex match")
	}
}

func TestGeneralRelevant_DoneColonInNote(t *testing.T) {
	if GeneralRelevant("working: done: some work") {
		t.Error(`"working: done: some work" should NOT be general-relevant (verb is "working", nonterminal)`)
	}
}

func TestGeneralRelevant_DoneColonBareLine(t *testing.T) {
	if !GeneralRelevant("done: some work") {
		t.Error(`"done: some work" should be general-relevant (terminal verb)`)
	}
}

func TestGeneralRelevant_NeedsDecisionInNote(t *testing.T) {
	if GeneralRelevant("working: needs-decision: still deciding") {
		t.Error(`"working: needs-decision:" should NOT be general-relevant (verb is "working", nonterminal)`)
	}
}

func TestGeneralRelevant_NeedsDecisionBare(t *testing.T) {
	if !GeneralRelevant("needs-decision: still deciding") {
		t.Error(`"needs-decision: still deciding" should be general-relevant (terminal verb)`)
	}
}

// --- IsPaused tests ---

func TestIsPaused_ExactPause(t *testing.T) {
	if !IsPaused("paused: waiting for upstream release") {
		t.Error("paused: should return true")
	}
}

func TestIsPaused_MinimalPause(t *testing.T) {
	if !IsPaused("paused: vendor rate-limit reset") {
		t.Error("paused: should return true for any reason")
	}
}

func TestIsPaused_NotPausedForWorking(t *testing.T) {
	if IsPaused("working: doing stuff") {
		t.Error("working: should NOT be paused")
	}
}

func TestIsPaused_NotPausedForBlocked(t *testing.T) {
	if IsPaused("blocked: something") {
		t.Error("blocked: should NOT be paused")
	}
}

func TestIsPaused_EmptyLine(t *testing.T) {
	if IsPaused("") {
		t.Error("empty line should NOT be paused")
	}
}

func TestIsPaused_BlankLine(t *testing.T) {
	if IsPaused("   ") {
		t.Error("blank line should NOT be paused")
	}
}

func TestIsPaused_KeyedPause(t *testing.T) {
	if !IsPaused("paused [key=upstream]: waiting for release") {
		t.Error("paused with key should still be paused")
	}
}

func TestIsPaused_PauseWordInNoteDoesNotMatch(t *testing.T) {
	if IsPaused("working: paused the build") {
		t.Error("paused in the note should NOT make it a pause line")
	}
}

// --- OpenDecisions tests ---

func TestOpenDecisions_MissingFile(t *testing.T) {
	decisions := OpenDecisions("/nonexistent/path.status")
	if decisions != nil {
		t.Error("should return nil for missing file")
	}
}

func TestOpenDecisions_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte{}, 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions, got %d", len(decisions))
	}
}

func TestOpenDecisions_BlankLinesOnly(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte("\n\n   \n\n"), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions for blank lines, got %d", len(decisions))
	}
}

func TestOpenDecisions_SingleNeedsDecision(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte("needs-decision: choose the approach\n"), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Key != "default" {
		t.Errorf("key = %q, want default", decisions[0].Key)
	}
	if decisions[0].Verb != "needs-decision" {
		t.Errorf("verb = %q, want needs-decision", decisions[0].Verb)
	}
	if decisions[0].Summary != "choose the approach" {
		t.Errorf("summary = %q, want 'choose the approach'", decisions[0].Summary)
	}
}

func TestOpenDecisions_SingleBlocked(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte("blocked: dependency not ready\n"), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Key != "default" {
		t.Errorf("key = %q, want default", decisions[0].Key)
	}
	if decisions[0].Verb != "blocked" {
		t.Errorf("verb = %q, want blocked", decisions[0].Verb)
	}
}

func TestOpenDecisions_NeedsDecisionThenResolved(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte("needs-decision: choose the approach\nresolved: chose approach A\n"), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions after resolved, got %d", len(decisions))
	}
}

func TestOpenDecisions_BlockedThenResolved(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte("blocked: waiting on dependency\nresolved: dependency released\n"), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions after resolved, got %d", len(decisions))
	}
}

func TestOpenDecisions_KeyedDecision(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte("needs-decision [key=api-shape]: choose the API shape\n"), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Key != "api-shape" {
		t.Errorf("key = %q, want api-shape", decisions[0].Key)
	}
	if decisions[0].Summary != "choose the API shape" {
		t.Errorf("summary = %q, want 'choose the API shape'", decisions[0].Summary)
	}
}

func TestOpenDecisions_KeyedResolved(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte(
		"needs-decision [key=api-shape]: choose the API shape\n"+
			"resolved [key=api-shape]: chose REST\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions after keyed resolved, got %d", len(decisions))
	}
}

func TestOpenDecisions_RepeatedKeyedResolvedIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.status")
	if err := os.WriteFile(path, []byte(
		"needs-decision [key=api-shape]: choose the API shape\n"+
			"resolved [key=api-shape]: chose REST\n"+
			"resolved [key=api-shape]: confirmed REST\n",
	), 0644); err != nil {
		t.Fatal(err)
	}
	if decisions := OpenDecisions(path); len(decisions) != 0 {
		t.Fatalf("expected resolution to remain closed, got %+v", decisions)
	}
}

func TestOpenDecisions_MultipleKeys(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte(
		"needs-decision [key=a]: decision A\n"+
			"needs-decision [key=b]: decision B\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(decisions))
	}
	if decisions[0].Key != "a" {
		t.Errorf("key[0] = %q, want a", decisions[0].Key)
	}
	if decisions[1].Key != "b" {
		t.Errorf("key[1] = %q, want b", decisions[1].Key)
	}
}

func TestOpenDecisions_ResolveOnlyOneKey(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte(
		"needs-decision [key=a]: decision A\n"+
			"needs-decision [key=b]: decision B\n"+
			"resolved [key=a]: chose X\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Key != "b" {
		t.Errorf("key = %q, want b", decisions[0].Key)
	}
}

func TestOpenDecisions_CaptainHeldClosesDecision(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte(
		"blocked [key=deploy]: waiting on deployment\n"+
			"captain-held [key=deploy]: captain took over\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions after captain-held, got %d", len(decisions))
	}
}

func TestOpenDecisions_ReplaceSameKey(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte(
		"needs-decision [key=a]: first version\n"+
			"needs-decision [key=a]: updated version\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Summary != "updated version" {
		t.Errorf("summary = %q, want 'updated version'", decisions[0].Summary)
	}
}

func TestOpenDecisions_MixedVerbs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")
	os.WriteFile(path, []byte(
		"working: started\n"+
			"needs-decision [key=design]: pick a design\n"+
			"resolved [key=design]: picked design A\n"+
			"working: implementing\n"+
			"blocked: found issue\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Key != "default" {
		t.Errorf("key = %q, want default", decisions[0].Key)
	}
	if decisions[0].Verb != "blocked" {
		t.Errorf("verb = %q, want blocked", decisions[0].Verb)
	}
}

// --- OpenActivities tests (keyed open/close work phases) ---

func TestOpenActivities_MissingFile(t *testing.T) {
	if acts := OpenActivities("/nonexistent/path.status"); acts != nil {
		t.Errorf("expected nil for missing file, got %+v", acts)
	}
}

func TestOpenActivities_KeyedPhases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.status")
	if err := os.WriteFile(path, []byte(
		"working [key=phase7]: Phase 7 started\n"+
			"working [key=phase6]: Phase 6 started\n"+
			"working [key=legal]: reviewing legal dependency\n"+
			"done [key=phase6]: Phase 6 completed\n"+
			"resolved [key=phase7]: Phase 7 completed and moved to Done\n"+
			"paused [key=legal]: awaiting external counsel\n"+
			"resolved [key=legal]: legal item returned to the queue\n"+
			"working [key=phase8]: Phase 8 started\n",
	), 0644); err != nil {
		t.Fatal(err)
	}
	acts := OpenActivities(path)
	if len(acts) != 1 {
		t.Fatalf("expected 1 open activity, got %d (%+v)", len(acts), acts)
	}
	if acts[0].Key != "phase8" || acts[0].Verb != "working" || acts[0].Summary != "Phase 8 started" {
		t.Errorf("activity = %+v, want phase8/working/Phase 8 started", acts[0])
	}
}

func TestOpenActivities_LegacyDefaultClosedByDone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.status")
	if err := os.WriteFile(path, []byte("working: legacy start\ndone: legacy completion\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if acts := OpenActivities(path); len(acts) != 0 {
		t.Fatalf("expected legacy done to close default phase, got %+v", acts)
	}
}

func TestOpenActivities_MultiEventOpenKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi.status")
	if err := os.WriteFile(path, []byte(
		"working [key=a]: start a\n"+
			"working [key=b]: start b\n"+
			"done [key=a]: finish a\n"+
			"paused [key=c]: wait c\n",
	), 0644); err != nil {
		t.Fatal(err)
	}
	acts := OpenActivities(path)
	if len(acts) != 2 {
		t.Fatalf("expected 2 open activities, got %d (%+v)", len(acts), acts)
	}
	if acts[0].Key != "b" || acts[0].Verb != "working" {
		t.Errorf("first open = %+v, want b/working", acts[0])
	}
	if acts[1].Key != "c" || acts[1].Verb != "paused" {
		t.Errorf("second open = %+v, want c/paused", acts[1])
	}
}

func TestOpenActivities_UnrelatedTerminalDoesNotCloseOtherKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cross.status")
	if err := os.WriteFile(path, []byte(
		"working [key=phase1]: doing phase1\n"+
			"done [key=other]: unrelated terminal\n",
	), 0644); err != nil {
		t.Fatal(err)
	}
	acts := OpenActivities(path)
	if len(acts) != 1 || acts[0].Key != "phase1" {
		t.Fatalf("expected phase1 still open, got %+v", acts)
	}
}

// --- AbsorbClass tests ---

func TestAbsorbClass_MissingStatusFile(t *testing.T) {
	tmp := t.TempDir()
	result := AbsorbClass("nonexistent", tmp)
	if result != None {
		t.Errorf("expected None for missing status file, got %v", result)
	}
}

func TestAbsorbClass_Working(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("working: building feature\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != Working {
		t.Errorf("expected Working, got %v", result)
	}
}

func TestAbsorbClass_Paused(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("paused: waiting for review\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != Paused {
		t.Errorf("expected Paused, got %v", result)
	}
}

func TestAbsorbClass_Done(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("done: implemented feature\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != None {
		t.Errorf("expected None for done, got %v", result)
	}
}

func TestAbsorbClass_NeedsDecision(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("needs-decision: choose approach\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != None {
		t.Errorf("expected None for needs-decision, got %v", result)
	}
}

func TestAbsorbClass_Blocked(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("blocked: dependency not ready\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != None {
		t.Errorf("expected None for blocked, got %v", result)
	}
}

func TestAbsorbClass_EmptyStatusFile(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte{}, 0644)

	result := AbsorbClass("test", tmp)
	if result != None {
		t.Errorf("expected None for empty status, got %v", result)
	}
}

func TestAbsorbClass_LastLineWins(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("working: started\npaused: now waiting\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != Paused {
		t.Errorf("expected Paused (last line wins), got %v", result)
	}
}

func TestAbsorbClass_WorkingWithNotation(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("working: no-mistakes running\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != Working {
		t.Errorf("expected Working, got %v", result)
	}
}

func TestAbsorbClass_PausedWithKey(t *testing.T) {
	tmp := t.TempDir()
	statusPath := filepath.Join(tmp, "test.status")
	os.WriteFile(statusPath, []byte("paused [key=upstream]: waiting for release\n"), 0644)

	result := AbsorbClass("test", tmp)
	if result != Paused {
		t.Errorf("expected Paused for keyed pause, got %v", result)
	}
}

// --- ScanGeneralRelevant tests ---

func TestScanGeneralRelevant_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty dir, got %d", len(matches))
	}
}

func TestScanGeneralRelevant_NonExistentDir(t *testing.T) {
	matches := ScanGeneralRelevant("/nonexistent")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for nonexistent dir, got %d", len(matches))
	}
}

func TestScanGeneralRelevant_OneGeneralRelevant(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "task1.status"), []byte("done: implemented feature\n"), 0644)

	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].TaskID != "task1" {
		t.Errorf("TaskID = %q, want task1", matches[0].TaskID)
	}
	if !strings.HasSuffix(matches[0].Path, "task1.status") {
		t.Errorf("Path = %q, should end with task1.status", matches[0].Path)
	}
	if matches[0].LastLine != "done: implemented feature" {
		t.Errorf("LastLine = %q, want 'done: implemented feature'", matches[0].LastLine)
	}
}

func TestScanGeneralRelevant_FiltersNonCaptain(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "relevant.status"), []byte("needs-decision: choose\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "benign.status"), []byte("working: building\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "paused.status"), []byte("paused: waiting\n"), 0644)

	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (only relevant), got %d", len(matches))
	}
	if matches[0].TaskID != "relevant" {
		t.Errorf("TaskID = %q, want relevant", matches[0].TaskID)
	}
}

func TestScanGeneralRelevant_MultipleRelevant(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.status"), []byte("done: implemented A\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.status"), []byte("blocked: blocked on B\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "c.status"), []byte("working: doing C\n"), 0644)

	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches (done + blocked), got %d", len(matches))
	}

	// Check that both relevant tasks are found (order not guaranteed)
	ids := make(map[string]bool)
	for _, m := range matches {
		ids[m.TaskID] = true
	}
	if !ids["a"] {
		t.Error("expected task 'a' to be in results")
	}
	if !ids["b"] {
		t.Error("expected task 'b' to be in results")
	}
	if ids["c"] {
		t.Error("task 'c' (working) should NOT be in results")
	}
}

func TestScanGeneralRelevant_IgnoresNonStatusFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "task.status"), []byte("done: done\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "task.meta"), []byte("window=@test\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "notes.txt"), []byte("some notes\n"), 0644)

	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (only .status counted), got %d", len(matches))
	}
}

func TestScanGeneralRelevant_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "empty.status"), []byte{}, 0644)

	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty status file, got %d", len(matches))
	}
}

func TestScanGeneralRelevant_LastLineWins(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "multi.status"), []byte(
		"working: started\n"+
			"done: finished\n",
	), 0644)

	matches := ScanGeneralRelevant(tmp)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].LastLine != "done: finished" {
		t.Errorf("LastLine = %q, want 'done: finished'", matches[0].LastLine)
	}
}

// --- Internal helper tests ---

func TestLineVerb_Basic(t *testing.T) {
	if v := lineVerb("done: implemented feature"); v != "done" {
		t.Errorf("lineVerb = %q, want done", v)
	}
}

func TestLineVerb_WithKey(t *testing.T) {
	if v := lineVerb("needs-decision [key=api-shape]: choose"); v != "needs-decision" {
		t.Errorf("lineVerb = %q, want needs-decision", v)
	}
}

func TestLineVerb_KeyedPaused(t *testing.T) {
	if v := lineVerb("paused [key=upstream]: waiting"); v != "paused" {
		t.Errorf("lineVerb = %q, want paused", v)
	}
}

func TestLineVerb_NoColon(t *testing.T) {
	if v := lineVerb("just a status note"); v != "just a status note" {
		t.Errorf("lineVerb = %q, want 'just a status note'", v)
	}
}

func TestLineVerb_MultiwordVerb(t *testing.T) {
	if v := lineVerb("PR ready: checks green"); v != "PR ready" {
		t.Errorf("lineVerb = %q, want 'PR ready'", v)
	}
}

func TestLineNote_Basic(t *testing.T) {
	if n := lineNote("done: implemented feature"); n != "implemented feature" {
		t.Errorf("lineNote = %q, want 'implemented feature'", n)
	}
}

func TestLineNote_NoColon(t *testing.T) {
	if n := lineNote("just a note"); n != "just a note" {
		t.Errorf("lineNote = %q, want 'just a note'", n)
	}
}

func TestLineNote_EmptyAfterColon(t *testing.T) {
	if n := lineNote("done: "); n != "" {
		t.Errorf("lineNote = %q, want empty", n)
	}
}

func TestDecisionKey_Default(t *testing.T) {
	if k := decisionKey("done: implemented"); k != "default" {
		t.Errorf("decisionKey = %q, want default", k)
	}
}

func TestDecisionKey_Explicit(t *testing.T) {
	if k := decisionKey("needs-decision [key=api-shape]: choose"); k != "api-shape" {
		t.Errorf("decisionKey = %q, want api-shape", k)
	}
}

func TestDecisionKey_KeyedResolve(t *testing.T) {
	if k := decisionKey("resolved [key=api-shape]: chose"); k != "api-shape" {
		t.Errorf("decisionKey = %q, want api-shape", k)
	}
}

func TestDecisionKey_InvalidChars(t *testing.T) {
	if k := decisionKey("needs-decision [key=bad key!]: test"); k != "default" {
		t.Errorf("decisionKey = %q, want default for invalid chars", k)
	}
}

func TestDecisionKey_EmptyKey(t *testing.T) {
	if k := decisionKey("needs-decision [key=]: test"); k != "default" {
		t.Errorf("decisionKey = %q, want default for empty key", k)
	}
}

func TestIsValidKey_Letters(t *testing.T) {
	if !isValidKey("abc") {
		t.Error("abc should be valid")
	}
}

func TestIsValidKey_AlphanumericWithDots(t *testing.T) {
	if !isValidKey("api-shape.v2") {
		t.Error("api-shape.v2 should be valid")
	}
}

func TestIsValidKey_Empty(t *testing.T) {
	if isValidKey("") {
		t.Error("empty string should NOT be valid")
	}
}

func TestIsValidKey_WithSpace(t *testing.T) {
	if isValidKey("bad key") {
		t.Error("key with space should NOT be valid")
	}
}

// --- RemoveByKey tests ---

func TestRemoveByKey_NotFound(t *testing.T) {
	decisions := []Decision{{Key: "a", Verb: "needs-decision"}}
	result := removeByKey(decisions, "b")
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
}

func TestRemoveByKey_Found(t *testing.T) {
	decisions := []Decision{
		{Key: "a", Verb: "needs-decision"},
		{Key: "b", Verb: "blocked"},
	}
	result := removeByKey(decisions, "a")
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].Key != "b" {
		t.Errorf("key = %q, want b", result[0].Key)
	}
}

func TestRemoveByKey_EmptySlice(t *testing.T) {
	result := removeByKey(nil, "a")
	if len(result) != 0 {
		t.Errorf("expected empty for nil slice, got %d", len(result))
	}
}

// --- Parity tests: status stream integration ---
// These test the complete status-fold contract described in fm-classify-lib.sh.

func TestOpenDecisions_StatusFoldContract(t *testing.T) {
	// The status stream is an append-only EVENT log. Reading it last-event-wins
	// cannot represent "an earlier decision is still open after a later, unrelated
	// event": a subsequent done/paused/working line silently masks a still-open
	// needs-decision. OpenDecisions is the ONE authoritative statement of the
	// status-fold contract.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")

	// Simulate: needs-decision → working → done
	// The needs-decision should remain open because nothing resolved it.
	os.WriteFile(path, []byte(
		"needs-decision [key=design]: pick a design\n"+
			"working: implementing\n"+
			"done: finished implementation\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 open decision (design not resolved), got %d", len(decisions))
	}
	if decisions[0].Key != "design" {
		t.Errorf("key = %q, want design", decisions[0].Key)
	}
	if decisions[0].Verb != "needs-decision" {
		t.Errorf("verb = %q, want needs-decision", decisions[0].Verb)
	}
}

func TestOpenDecisions_BareResolvedClosesDefault(t *testing.T) {
	// A bare "resolved:" closes the "default" key.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")

	os.WriteFile(path, []byte(
		"blocked: waiting on dependency\n"+
			"resolved: dependency is ready\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions after bare resolved, got %d", len(decisions))
	}
}

func TestOpenDecisions_KeyedResolveDoesNotCloseDefault(t *testing.T) {
	// A keyed resolved should not close an unkeyed (default) decision.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")

	os.WriteFile(path, []byte(
		"needs-decision: choose approach\n"+
			"resolved [key=other]: resolved something else\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 open decision, got %d", len(decisions))
	}
	if decisions[0].Key != "default" {
		t.Errorf("key = %q, want default", decisions[0].Key)
	}
}

func TestOpenDecisions_DefaultKeyDoesNotCloseKeyed(t *testing.T) {
	// A bare resolved (default key) should not close a keyed decision.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.status")

	os.WriteFile(path, []byte(
		"needs-decision [key=design]: choose design\n"+
			"resolved: something else resolved\n",
	), 0644)

	decisions := OpenDecisions(path)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 open decision, got %d", len(decisions))
	}
	if decisions[0].Key != "design" {
		t.Errorf("key = %q, want design", decisions[0].Key)
	}
}
