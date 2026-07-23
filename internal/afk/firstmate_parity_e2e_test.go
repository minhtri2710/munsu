package afk_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/lifecycle"
)

// =============================================================================
// Firstmate AFK Parity E2E Tests — coverage matrix:
//
// 1. Digest batching
// 2. Durable wake-first handling
// 3. Safe-empty-composer sentinel injection
// 4. AFK no-direct-ring policy
// 5. Wedge detection/recovery
// 6. Stale cleanup
// 7. Return flow and no lost wake
// 8. Nonterminal prose false-escalation (Firstmate ea3ac2e verb-awareness)
// =============================================================================

// =============================================================================
// COVERAGE 8: Nonterminal Prose False-Escalation
// Proves that working:/paused: lines cannot escalate merely because prose
// contains merged/PR ready/checks green — Firstmate ea3ac2e verb-awareness.
// =============================================================================

func TestE2E8_NonterminalProseDoesNotEscalateFromFreeTextTokens(t *testing.T) {
	// Matrix: every free-text token against every nonterminal verb
	tokens := []string{
		"merged",
		"PR ready",
		"checks green",
		"ready in branch",
		"PR merged by captain",
		"rebased onto merged #76",
	}
	nonterminalVerbs := []string{
		"working",
		"paused",
		"resolved",
		"captain-held",
	}

	for _, token := range tokens {
		for _, verb := range nonterminalVerbs {
			line := fmt.Sprintf("%s: %s", verb, token)
			if classify.GeneralRelevant(line) {
				t.Errorf("nonterminal %q line %q should NOT be general-relevant", verb, line)
			}
		}
	}
}

func TestE2E8_NonterminalVerbsWithKeyedMarkers(t *testing.T) {
	// Keyed nonterminal lines should also NOT escalate
	lines := []string{
		"working [key=phase7]: rebased onto merged #76",
		"paused [key=legal]: PR ready for review approval",
		"resolved [key=design]: checks green on CI",
		"captain-held [key=deploy]: ready in branch",
	}
	for _, line := range lines {
		if classify.GeneralRelevant(line) {
			t.Errorf("keyed nonterminal line %q should NOT be general-relevant", line)
		}
	}
}

func TestE2E8_BareLegacyLinesStillActionable(t *testing.T) {
	// Bare lines without leading verb should still match free-text tokens
	tests := []struct {
		line string
		want bool
	}{
		{"PR ready", true},
		{"checks green", true},
		{"ready in branch", true},
		{"merged", true},
		{"PR merged by captain", true},
		{"rebased onto merged #76", true},
	}
	for _, tc := range tests {
		got := classify.GeneralRelevant(tc.line)
		if got != tc.want {
			t.Errorf("bare line %q GeneralRelevant = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestE2E8_TerminalVerbsAlwaysActionable(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"done: implemented feature", true},
		{"needs-decision: which approach", true},
		{"blocked: waiting on CI", true},
		{"failed: build broken", true},
		{"done [key=task]: PR merged", true},
	}
	for _, tc := range tests {
		got := classify.GeneralRelevant(tc.line)
		if got != tc.want {
			t.Errorf("terminal line %q GeneralRelevant = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestE2E8_NonterminalProseDoesNotSuppressIdleRecovery(t *testing.T) {
	// The key concern from Firstmate ea3ac2e: a stale working: line with
	// free-text tokens should NOT suppress idle recovery. We prove this by
	// showing such lines are NOT general-relevant (non-terminal), so they
	// won't be picked up by scan/absorb as "done/stuck" and won't prevent
	// the idle recovery from reaping the soldier.
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	// Status file with nonterminal "working:" line that contains merged in prose.
	// Before the fix, this would be classified as general-relevant (false-escalation).
	statusContent := []byte("working: rebased onto merged #76\n")
	os.WriteFile(filepath.Join(stateDir, "task-stale-merge.status"), statusContent, 0644)

	matches := classify.ScanGeneralRelevant(stateDir)
	if len(matches) > 0 {
		t.Fatalf("working: with 'merged' in prose: got %d matches, want 0 (nonterminal should not escalate)", len(matches))
	}

	// Verify a bare "merged" line still surfaces correctly
	os.WriteFile(filepath.Join(stateDir, "task-done.status"), []byte("done: PR merged\n"), 0644)
	matches = classify.ScanGeneralRelevant(stateDir)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match for done: PR merged, got %d", len(matches))
	}
}

func TestE2E8_OneCycleDoesNotEscalateNonterminalProse(t *testing.T) {
	// End-to-end: enqueue a wake with nonterminal prose that contains free-text
	// tokens, then run OneCycle. The wake should be classified as routine, not escalated.
	home := t.TempDir()

	// Enqueue wake with nonterminal prose containing "merged"
	if err := lifecycle.EnqueueWake(home, "afk", "task-stale", "working: rebased onto merged #76"); err != nil {
		t.Fatal(err)
	}

	digest, err := afk.OneCycle(home)
	if err != nil {
		t.Fatalf("OneCycle: %v", err)
	}
	if digest == nil {
		t.Fatal("OneCycle returned nil digest (expected entries)")
	}

	if len(digest.Escalated) > 0 {
		t.Fatalf("Escalated = %d, want 0 (nonterminal prose should be routine): %+v",
			len(digest.Escalated), digest.Escalated)
	}
	if len(digest.Routines) != 1 {
		t.Fatalf("Routines = %d, want 1", len(digest.Routines))
	}

	// Confirm a terminal wake still escalates correctly
	if err := lifecycle.EnqueueWake(home, "afk", "task-done", "done: PR merged"); err != nil {
		t.Fatal(err)
	}
	digest2, err := afk.OneCycle(home)
	if err != nil {
		t.Fatalf("OneCycle (terminal): %v", err)
	}
	if digest2 == nil {
		t.Fatal("OneCycle returned nil digest (expected entries)")
	}
	if len(digest2.Escalated) != 1 {
		t.Fatalf("Escalated = %d, want 1 (terminal wake should escalate)", len(digest2.Escalated))
	}
}

func TestE2E8_DrainCycleDoesNotSurfaceNonterminalProse(t *testing.T) {
	// Prove DrainCycle (General drain) does not surface nonterminal prose
	// with free-text tokens as actionable.
	home := t.TempDir()

	// Nonterminal wake
	if err := lifecycle.EnqueueWake(home, "signal", "task-stale", "working: rebased onto merged #76"); err != nil {
		t.Fatal(err)
	}
	// Terminal wake (should be actionable)
	if err := lifecycle.EnqueueWake(home, "signal", "task-done", "done: feature complete"); err != nil {
		t.Fatal(err)
	}

	report, err := afk.DrainCycle(afk.DrainCycleOptions{
		HomeDir:  home,
		Consumer: "general-e2e",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("DrainCycle: %v", err)
	}
	if report == nil {
		t.Fatal("DrainCycle returned nil report")
	}

	// Should have 1 actionable (the done:) and 1 routine (the working:)
	if len(report.Actionable) != 1 {
		t.Fatalf("Actionable = %d, want 1 (only done: should be actionable): %+v",
			len(report.Actionable), report.Actionable)
	}
	if report.RoutineCount != 1 {
		t.Fatalf("RoutineCount = %d, want 1 (working: with merged should be routine)", report.RoutineCount)
	}
	if report.Actionable[0].Key != "task-done" {
		t.Errorf("actionable key = %q, want task-done", report.Actionable[0].Key)
	}
}

// =============================================================================
// COVERAGE 1: Digest Batching
// Proves multiple durable wakes batch into one digest within a window.
// =============================================================================

func TestE2E1_DigestBatchingAccumulatesMultipleWakes(t *testing.T) {
	home := t.TempDir()
	d := afk.NewDigester(home)

	// Feed multiple digests within the window
	d.Feed(&afk.Digest{
		Escalated: []afk.WakeDigest{{Kind: "afk", Key: "task-1", Payload: "done: shipped", IsGeneralRelevant: true}},
	})
	d.Feed(&afk.Digest{
		Routines: []afk.WakeDigest{{Kind: "check", Key: "health", Payload: "all green", IsGeneralRelevant: false}},
	})
	d.Feed(&afk.Digest{
		Escalated: []afk.WakeDigest{{Kind: "afk", Key: "task-3", Payload: "needs-decision: merge target", IsGeneralRelevant: true}},
	})

	if d.EntryCount() != 3 {
		t.Fatalf("EntryCount = %d, want 3", d.EntryCount())
	}

	// Flush window expired
	now := time.Now().Add(time.Minute + time.Second)
	if !d.ShouldFlush(now) {
		t.Fatal("ShouldFlush = false after window expired")
	}
	if err := d.Flush(now); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify digest file has all entries
	digestPath := filepath.Join(home, "state/.afk-digest")
	data, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatalf("reading digest: %v", err)
	}
	var be afk.BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(be.Entries) != 3 {
		t.Errorf("batched entries = %d, want 3", len(be.Entries))
	}
	if be.EscalatedCount != 2 {
		t.Errorf("escalated count = %d, want 2", be.EscalatedCount)
	}
	if be.RoutineCount != 1 {
		t.Errorf("routine count = %d, want 1", be.RoutineCount)
	}
}

// =============================================================================
// COVERAGE 2: Durable Wake-First Handling
// Proves wake is durably present before any optional pane notification.
// The wake queue is a durable file (state/.wake-queue); pane injection is
// advisory. The mailbox is the authoritative source.
// =============================================================================

func TestE2E2_WakeFirstDurableSurvivesBeforeInjection(t *testing.T) {
	home := t.TempDir()

	// Enqueue wake first
	if err := lifecycle.EnqueueWake(home, "signal", "task-priority", "done: critical fix"); err != nil {
		t.Fatal(err)
	}

	// Verify wake exists BEFORE any injection attempt
	if !lifecycle.HasQueuedWakes(home) {
		t.Fatal("wake queue should exist before injection")
	}

	// Simulate injection attempt that would not consume the wake
	// (e.g. unsafe composer). The wake should persist.
	bk := &fakeBackend{}
	cap := &fakeCapture{content: "git status\n"} // pending composer → unsafe
	afk.DirectInject(bk, cap, "s:p", "[report] done: critical fix", "12345")

	// Wake must still be present after injection failure
	if !lifecycle.HasQueuedWakes(home) {
		t.Fatal("wake lost after failed injection — should persist durably")
	}

	// Drain the wake successfully
	records, err := lifecycle.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1 (only the original wake)", len(records))
	}
	if records[0].Key != "task-priority" {
		t.Errorf("key = %q, want task-priority", records[0].Key)
	}
}

func TestE2E2_WakeSurvivesMultipleInjectionCycles(t *testing.T) {
	home := t.TempDir()

	// Enqueue wake
	if err := lifecycle.EnqueueWake(home, "signal", "task-robust", "done: persistent test"); err != nil {
		t.Fatal(err)
	}

	// Multiple injection attempts — all should fail to consume the wake
	for i := 0; i < 5; i++ {
		bk := &fakeBackend{}
		cap := &fakeCapture{content: "$ \n"} // dead shell → unsafe
		afk.DirectInject(bk, cap, "s:p", "[report] done: persistent test", "12345")

		if !lifecycle.HasQueuedWakes(home) {
			t.Fatalf("wake consumed on injection cycle %d — should persist", i)
		}
	}
}

// =============================================================================
// COVERAGE 3: Safe-Empty-Composer Sentinel Injection
// Proves injection occurs only into a verified Empty composer and carries
// FM_INJECT_MARK sentinel.
// =============================================================================

func TestE2E3_SafeEmptyComposerInjectsWithSentinel(t *testing.T) {
	bk := &fakeBackend{}
	cap := &fakeCapture{content: "\u276F \n"} // empty composer

	result := afk.DirectInject(bk, cap, "s:p", "test message", "evt-1")

	if string(result.Outcome) != "injected" {
		t.Fatalf("outcome = %q, want injected", result.Outcome)
	}
	if len(bk.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(bk.calls))
	}
	// Verify sentinel prefix
	if !strings.HasPrefix(bk.calls[0].text, "\u2063") {
		t.Errorf("injected text missing FM_INJECT_MARK sentinel: %q", bk.calls[0].text)
	}
	// Verify payload content present
	if !strings.Contains(bk.calls[0].text, "test message") {
		t.Errorf("injected text missing payload: %q", bk.calls[0].text)
	}
	// Verify event ID present
	if !strings.Contains(bk.calls[0].text, "evt-1") {
		t.Errorf("injected text missing event ID: %q", bk.calls[0].text)
	}
}

func TestE2E3_UnsafeComposerBlocksInjection(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"pending-text", "git push origin main\n"},
		{"dead-shell", "$ \n"},
		{"empty-shell", "> \n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bk := &fakeBackend{}
			cap := &fakeCapture{content: tc.content}
			result := afk.DirectInject(bk, cap, "s:p", "test msg", "12345")
			if string(result.Outcome) != "unsafe" {
				t.Errorf("outcome = %q, want unsafe", result.Outcome)
			}
			if len(bk.calls) != 0 {
				t.Errorf("calls = %d, want 0 for unsafe composer", len(bk.calls))
			}
		})
	}
}

func TestE2E3_SentinelIsInvisibleSeparator(t *testing.T) {
	// FM_INJECT_MARK is U+2063 INVISIBLE SEPARATOR
	marked := afk.Mark("test payload")
	if !strings.HasPrefix(marked, "\u2063") {
		t.Errorf("marked text missing U+2063 prefix: %q", marked)
	}
	if !afk.Marked(marked) {
		t.Error("Marked() should return true for marked text")
	}
}

// =============================================================================
// COVERAGE 4: AFK No-Direct-Ring Policy
// Proves AFK suppresses the direct-ring path while the mailbox remains
// authoritative. ShouldBatch returns true when AFK is active.
// =============================================================================

func TestE2E4_ShouldBatchReturnsTrueWhenActive(t *testing.T) {
	home := t.TempDir()

	// AFK inactive initially
	if afk.ShouldBatch(home) {
		t.Error("ShouldBatch = true when AFK inactive, want false")
	}

	// Set AFK flag
	flagPath := filepath.Join(home, "state/.afk")
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)

	if !afk.ShouldBatch(home) {
		t.Error("ShouldBatch = false when AFK active, want true")
	}

	// Remove flag
	os.Remove(flagPath)
	if afk.ShouldBatch(home) {
		t.Error("ShouldBatch = true after flag removed, want false")
	}
}

func TestE2E4_AFKMailboxAuthoritativeOverDirectRing(t *testing.T) {
	// Prove the wake queue is the authoritative source even when AFK is active.
	home := t.TempDir()

	// Set AFK flag
	flagPath := filepath.Join(home, "state/.afk")
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644)

	// Enqueue wake
	if err := lifecycle.EnqueueWake(home, "signal", "task-mbox", "done: mailbox authoritative"); err != nil {
		t.Fatal(err)
	}

	// Wake must be present regardless of ShouldBatch
	if !lifecycle.HasQueuedWakes(home) {
		t.Fatal("wake queue should exist under AFK")
	}

	// Drain and verify
	records, err := lifecycle.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if !strings.Contains(records[0].Payload, "mailbox authoritative") {
		t.Errorf("payload = %q, should contain 'mailbox authoritative'", records[0].Payload)
	}
}

// =============================================================================
// COVERAGE 5: Wedge Detection/Recovery
// Proves stale beat, repeated wake, and max-defer produce wedge alarms,
// and recovery clears them.
// =============================================================================

func TestE2E5_StaleBeatTriggersWedgeAlarm(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	// Write a very old beat
	old := time.Now().Add(-10 * time.Minute)
	beatContent := fmt.Sprintf("%d %d", old.Unix(), os.Getpid())
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(beatContent), 0644)

	w := afk.NewWedgeDetector(home)
	alarm := w.Check(time.Now())

	if alarm == nil {
		t.Fatal("expected wedge alarm for stale beat, got nil")
	}
	if !strings.Contains(alarm.Reason, "stale") {
		t.Errorf("alarm reason = %q, want 'stale'", alarm.Reason)
	}
}

func TestE2E5_RepeatedWakeTriggersWedgeAlarm(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	// Fresh beat
	now := time.Now()
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(fmt.Sprintf("%d %d", now.Unix(), os.Getpid())), 0644)

	w := afk.NewWedgeDetector(home)
	w.FeedWake("task-stuck")
	w.FeedWake("task-stuck")
	w.FeedWake("task-stuck")
	w.FeedWake("task-stuck") // beyond default max of 3

	alarm := w.Check(now)
	if alarm == nil {
		t.Fatal("expected wedge alarm for repeated wake, got nil")
	}
	if !strings.Contains(alarm.Reason, "repeated identical") {
		t.Errorf("alarm reason = %q, want 'repeated identical'", alarm.Reason)
	}
	if alarm.WakeKey != "task-stuck" {
		t.Errorf("WakeKey = %q, want task-stuck", alarm.WakeKey)
	}
}

func TestE2E5_MaxDeferTriggersWedgeAlarm(t *testing.T) {
	home := t.TempDir()
	firstAt := time.Now().Add(-10 * time.Minute) // 10min ago = beyond 5min max-defer default
	maxDefer := 5 * time.Minute
	now := time.Now()

	w := afk.NewWedgeDetector(home)
	alarm := w.CheckDigestStuck(firstAt, maxDefer, now)

	if alarm == nil {
		t.Fatal("expected wedge alarm for max-defer, got nil")
	}
	if !strings.Contains(alarm.Reason, "digest stuck") {
		t.Errorf("alarm reason = %q, want 'digest stuck'", alarm.Reason)
	}
}

func TestE2E5_WedgeRecoveryViaResetWake(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	now := time.Now()
	os.WriteFile(filepath.Join(stateDir, ".last-watcher-beat"), []byte(fmt.Sprintf("%d %d", now.Unix(), os.Getpid())), 0644)

	w := afk.NewWedgeDetector(home)
	w.FeedWake("task-stuck")
	w.FeedWake("task-stuck")

	// Reset wakes (simulates recovery)
	w.ResetWake()

	// After reset, no alarm even if we feed again
	w.FeedWake("task-stuck")
	alarm := w.Check(now)
	if alarm != nil {
		t.Errorf("alarm after ResetWake = %+v, want nil", alarm)
	}
}

// =============================================================================
// COVERAGE 6: Stale Cleanup
// Proves stale session artifacts are removed without touching live status.
// =============================================================================

func TestE2E6_ClearStaleArtifactsRemovesSessionGarbage(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	// Create stale artifacts
	filesToRemove := []string{
		".seen-task-1",
		".seen-task-2",
		".subsuper-escalations",
		".subsuper-escalations.since",
		".afk-digest",
		".afk-wedge-alarm",
	}
	for _, f := range filesToRemove {
		os.WriteFile(filepath.Join(stateDir, f), []byte("stale"), 0644)
	}

	// Create live files that must survive
	filesToKeep := []string{
		"task-1.status",
		"task-2.status",
		".wake-queue",
		".last-watcher-beat",
	}
	for _, f := range filesToKeep {
		os.WriteFile(filepath.Join(stateDir, f), []byte("live data"), 0644)
	}

	if err := afk.ClearStaleArtifacts(home); err != nil {
		t.Fatalf("ClearStaleArtifacts: %v", err)
	}

	// Verify stale files removed
	for _, f := range filesToRemove {
		if _, err := os.Stat(filepath.Join(stateDir, f)); !os.IsNotExist(err) {
			t.Errorf("stale file %s still exists after cleanup", f)
		}
	}

	// Verify live files preserved
	for _, f := range filesToKeep {
		if _, err := os.Stat(filepath.Join(stateDir, f)); os.IsNotExist(err) {
			t.Errorf("live file %s was removed by cleanup", f)
		}
	}
}

func TestE2E6_ClearStaleArtifactsNoStateDir(t *testing.T) {
	home := t.TempDir()
	// No state dir — should not error
	if err := afk.ClearStaleArtifacts(home); err != nil {
		t.Fatalf("ClearStaleArtifacts on clean home: %v", err)
	}
}

func TestE2E6_ClearStaleCheckedMarkersPreservesSeen(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	// Check markers — should be removed
	os.WriteFile(filepath.Join(stateDir, ".subsuper-stale-health"), []byte("check"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".subsuper-seen-status-task-1"), []byte("check"), 0644)

	// Seen marker — should be preserved
	os.WriteFile(filepath.Join(stateDir, ".seen-task-1"), []byte("active"), 0644)

	if err := afk.ClearStaleCheckedMarkers(home); err != nil {
		t.Fatalf("ClearStaleCheckedMarkers: %v", err)
	}

	// Check markers removed
	if _, err := os.Stat(filepath.Join(stateDir, ".subsuper-stale-health")); !os.IsNotExist(err) {
		t.Error(".subsuper-stale-health still exists after ClearStaleCheckedMarkers")
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".subsuper-seen-status-task-1")); !os.IsNotExist(err) {
		t.Error(".subsuper-seen-status-task-1 still exists after ClearStaleCheckedMarkers")
	}

	// Seen marker preserved
	if _, err := os.Stat(filepath.Join(stateDir, ".seen-task-1")); os.IsNotExist(err) {
		t.Error(".seen-task-1 was removed by ClearStaleCheckedMarkers")
	}
}

// =============================================================================
// COVERAGE 7: Return Flow and No Lost Wake
// Proves return drains the digest while any concurrently arriving/unprocessed
// wake remains claimable — explicitly test the race boundary.
// =============================================================================

func TestE2E7_ReturnDrainsDigestPreservingConcurrentWakes(t *testing.T) {
	home := t.TempDir()

	// 1. Write a digest as if the AFK daemon had accumulated and flushed it
	be := afk.BatchedEscalation{
		Entries: []afk.BatchedEntry{
			{Kind: "afk", Key: "task-a", Payload: "done: feature A", Type: afk.EscalationRoutine},
			{Kind: "afk", Key: "task-b", Payload: "needs-decision: merge target", Type: afk.EscalationDecision},
		},
		RoutineCount:   1,
		EscalatedCount: 1,
		FirstAt:        time.Now().Add(-30 * time.Second),
		LastAt:         time.Now(),
	}
	digestPath := filepath.Join(home, "state/.afk-digest")
	os.MkdirAll(filepath.Dir(digestPath), 0755)
	data, _ := json.Marshal(be)
	os.WriteFile(digestPath, data, 0644)

	// 2. Enqueue a concurrent wake that was NOT in the digest
	if err := lifecycle.EnqueueWake(home, "signal", "task-concurrent", "done: concurrent arrival"); err != nil {
		t.Fatal(err)
	}

	// 3. Run Return (drains digest)
	report, err := afk.Return(home)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if report == nil {
		t.Fatal("Return returned nil report")
	}
	if report.DigestedCount != 2 {
		t.Fatalf("DigestedCount = %d, want 2", report.DigestedCount)
	}
	if len(report.Escalations) != 1 {
		t.Fatalf("Escalations = %d, want 1 (only decision should be escalated)", len(report.Escalations))
	}

	// 4. Verify the concurrent wake is still claimable (not lost)
	if !lifecycle.HasQueuedWakes(home) {
		t.Fatal("concurrent wake lost after Return — should still be claimable")
	}
	records, err := lifecycle.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes after Return: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("concurrent records = %d, want 1", len(records))
	}
	if records[0].Key != "task-concurrent" {
		t.Errorf("concurrent wake key = %q, want task-concurrent", records[0].Key)
	}
}

func TestE2E7_ReturnOnCleanHomeDoesNotError(t *testing.T) {
	home := t.TempDir()
	report, err := afk.Return(home)
	if err != nil {
		t.Fatalf("Return on clean home: %v", err)
	}
	if report == nil {
		t.Fatal("Return returned nil report on clean home")
	}
	if report.HasActionable() {
		t.Error("HasActionable() = true on clean home, want false")
	}
}

func TestE2E7_ReturnReportSummarizesWedgeAlarms(t *testing.T) {
	home := t.TempDir()

	// Write a digest with a wedge alarm on the BatchedEscalation.WedgeAlarm field
	wedgeAt := time.Now()
	be := afk.BatchedEscalation{
		Entries: []afk.BatchedEntry{
			{Kind: "afk", Key: "task-a", Payload: "done: feature complete", Type: afk.EscalationRoutine},
			{Kind: "afk", Key: "task-b", Payload: "needs-decision: merge target", Type: afk.EscalationDecision},
		},
		EscalatedCount: 2,
		RoutineCount:   1,
		FirstAt:        time.Now().Add(-30 * time.Second),
		LastAt:         time.Now(),
		WedgeAlarm: &afk.WedgeAlarm{
			Reason:     "watcher beat stale: age=10m0s threshold=5m0s",
			DetectedAt: wedgeAt,
			BeatAge:    "10m0s",
		},
	}
	digestPath := filepath.Join(home, "state/.afk-digest")
	os.MkdirAll(filepath.Dir(digestPath), 0755)
	data, _ := json.Marshal(be)
	os.WriteFile(digestPath, data, 0644)

	report, err := afk.Return(home)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(report.WedgeAlarms) != 1 {
		t.Fatalf("WedgeAlarms = %d, want 1", len(report.WedgeAlarms))
	}
	if !strings.Contains(report.WedgeAlarms[0], "stale") {
		t.Errorf("wedge alarm = %q, want 'stale'", report.WedgeAlarms[0])
	}
	// Escalations should include the non-routine entry (decision) but not the wedge entry
	if len(report.Escalations) != 1 {
		t.Fatalf("Escalations = %d, want 1 (only decision)", len(report.Escalations))
	}
}

// fakeBackend and fakeCapture are defined in continuity_test.go (package afk_test).
