package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// testSeatLiveness is the starting thresholds ReadSeatLiveness returns for an
// unset config, with the beat threshold as the silence threshold.
var testSeatLiveness = config.SeatLiveness{SilentAfter: StaleThreshold(), RepeatNudge: 5, RepeatEnd: 8}

type testEndpointProbe struct{}

func (testEndpointProbe) Probe(string, taskauthority.EndpointBinding, string) (bool, error) {
	return false, nil
}

type testCycleSender struct{}

func (testCycleSender) Alive(string, taskauthority.EndpointBinding, string) (bool, error) {
	return false, nil
}
func (testCycleSender) Send(string, taskauthority.EndpointBinding, string) mhome.BoundSendResult {
	return mhome.BoundSendResult{}
}

func testScanFleet(home string) *WakeReason {
	reasons := scanFleetWithProbe(home, false, testEndpointProbe{}, testSeatLiveness, nil)
	if len(reasons) == 0 {
		return nil
	}
	return reasons[0]
}

type testWatcherHooks struct {
	activate func(string)
}

func (h testWatcherHooks) Activate(home string) {
	if h.activate != nil {
		h.activate(home)
	}
}

var activeTestHooks WatcherHooks = NoopWatcherHooks{}

func testRunCycle(home string) (bool, error) {
	return RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, activeTestHooks, nil)
}

// --- handleStale / resetStreak tests (first-seen stale timestamp) ---

func TestHandleStale_FirstSeen(t *testing.T) {
	delete(staleFirstSeen, "test-task-1")

	reason := handleStale("test-task-1", "pane foo is dead")
	if reason == nil {
		t.Fatal("expected non-nil reason")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
	if len(reason.TaskIDs) != 1 || reason.TaskIDs[0] != "test-task-1" {
		t.Errorf("TaskIDs = %v, want [test-task-1]", reason.TaskIDs)
	}
	// Entry must exist with a recent timestamp.
	staleFirstSeenMu.Lock()
	ts, exists := staleFirstSeen["test-task-1"]
	staleFirstSeenMu.Unlock()
	if !exists {
		t.Error("staleFirstSeen entry should exist after handleStale")
	}
	if time.Since(ts) > time.Second {
		t.Errorf("staleFirstSeen timestamp too old: %v ago", time.Since(ts))
	}
}

func TestResetStreak(t *testing.T) {
	delete(staleFirstSeen, "test-reset")
	// Inject past timestamp.
	staleFirstSeenMu.Lock()
	staleFirstSeen["test-reset"] = time.Now().Add(-30 * time.Second)
	staleFirstSeenMu.Unlock()

	resetStreak("test-reset")
	staleFirstSeenMu.Lock()
	_, exists := staleFirstSeen["test-reset"]
	staleFirstSeenMu.Unlock()
	if exists {
		t.Error("staleFirstSeen should be deleted after reset")
	}
}

func TestStaleFirstSeen_ConcurrentAccess(t *testing.T) {
	const workers = 32
	const perWorker = 100
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("race-task-%d", i%4)
			for n := 0; n < perWorker; n++ {
				handleStale(id, "concurrent")
				if n%3 == 0 {
					resetStreak(id)
				}
			}
		}(i)
	}
	wg.Wait()
}

// --- scanFleet integration tests ---

func TestScanFleet_NoStateDir(t *testing.T) {
	tmp := t.TempDir()
	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for no state dir, got %v", reason)
	}
}

func TestScanFleet_EmptyStateDir(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for empty state dir, got %v", reason)
	}
}

// A .meta window names no seat: only a canonical bound endpoint is probed.
func TestScanFleet_MetaWindowWithoutBindingNotProbed(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".hidden.meta"), []byte("window=@test\n"), 0644)
	if err := mhome.WriteMeta(tmp, "meta-only", map[string]string{"kind": "ship", "window": "@nonexistent99"}); err != nil {
		t.Fatal(err)
	}

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil when no canonical endpoint is bound, got %v", reason)
	}
}

func TestScanFleet_QueuedTaskNotProbed(t *testing.T) {
	tmp := t.TempDir()
	seedTask(t, tmp, "no-win")

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for a task with no bound endpoint, got %v", reason)
	}
}

func TestScanFleet_MultipleTasks_OnlyOneBound(t *testing.T) {
	tmp := t.TempDir()

	// Task with no bound endpoint
	seedTask(t, tmp, "no-win")
	// Task with a bound endpoint (but window doesn't exist, so pane is dead)
	seedBoundSoldier(t, tmp, "has-win", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent99"})

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected a stale reason for dead pane")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}

func TestScanFleet_StaleStatusFile(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")

	seedBoundSoldier(t, tmp, "stale-task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent99"})

	// Write a status file with old modification time
	statusPath := filepath.Join(stateDir, "stale-task.status")
	os.WriteFile(statusPath, []byte("working: started\n"), 0644)

	// Set mod time to be older than stale threshold
	past := time.Now().Add(-(StaleThreshold() + time.Second))
	os.Chtimes(statusPath, past, past)

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected stale reason for old status file")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
	// The pane dead check fires before status staleness check
	if !strings.Contains(reason.Message, "dead") && !strings.Contains(reason.Message, "idle beyond threshold") {
		t.Errorf("message should mention dead or idle, got: %q", reason.Message)
	}
}

func TestScanFleet_RecentStatusNoStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")

	seedBoundSoldier(t, tmp, "recent-task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent99"})

	// Write a recent status file
	statusPath := filepath.Join(stateDir, "recent-task.status")
	os.WriteFile(statusPath, []byte("working: active\n"), 0644)

	// The pane is dead but the status is recent — pane staleness takes precedence
	// But the status staleness check won't trigger because modtime is recent
	// The pane liveness check will trigger stale first
	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected stale reason for dead pane (recent status doesn't prevent pane check)")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}

func TestScanFleet_NoMetaFiles(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Non-human-needed orphan status without meta stays quiet.
	os.WriteFile(filepath.Join(stateDir, "orphan.status"), []byte("working: stray\n"), 0644)
	// Completion is an audit projection: it stays quiet like working.
	os.WriteFile(filepath.Join(stateDir, "finished.status"), []byte("done: finished\n"), 0644)
	// Human-needed status (including Captain return-channel files) must wake
	// even without a companion .meta — parent status is the return path.
	os.WriteFile(filepath.Join(stateDir, "another.status"), []byte("blocked: needs credentials\n"), 0644)

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected signal for human-needed status without meta")
	}
	if reason.Kind != "signal" || len(reason.TaskIDs) != 1 || reason.TaskIDs[0] != "another" {
		t.Fatalf("unexpected reason: %+v", reason)
	}
}

func TestScanFleet_MultipleTasks_MixedStates(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")

	// Task 1: bound but pane is dead
	seedBoundSoldier(t, tmp, "dead-task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent1"})
	// Task 2: no bound endpoint (should be skipped)
	seedTask(t, tmp, "no-win-task")
	// Task 3: bound to a dead window, with an old status file
	seedBoundSoldier(t, tmp, "old-status-task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent2"})

	statusPath := filepath.Join(stateDir, "old-status-task.status")
	os.WriteFile(statusPath, []byte("working: started\n"), 0644)
	past := time.Now().Add(-(StaleThreshold() + time.Second))
	os.Chtimes(statusPath, past, past)

	// scanFleet probes the bound tasks in task-ID order — should find a stale task
	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected a stale reason")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}

func TestRunCycle_QueuedWakeDoesNotEmitAnotherWake(t *testing.T) {
	tmp := t.TempDir()

	seedTask(t, tmp, "no-window-task")
	if err := EnqueueWake(tmp, "status", "task-1", "done: ready"); err != nil {
		t.Fatal(err)
	}

	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("queued wakes must not cause the watcher to enqueue another wake")
	}

	records, err := DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("queue grew from one pending wake to %d records", len(records))
	}
}

func TestRunCycle_DeduplicatesUnchangedConditionAndEmitsAfterChange(t *testing.T) {
	tmp := t.TempDir()
	seedBoundSoldier(t, tmp, "task-1", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent-watch-cycle"})

	emitted, err := testRunCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("first cycle emitted=%v err=%v, want true nil", emitted, err)
	}
	emitted, err = testRunCycle(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("unchanged condition emitted a duplicate wake")
	}

	if err := mhome.AppendStatus(tmp, "task-1", "failed: changed condition"); err != nil {
		t.Fatal(err)
	}
	emitted, err = testRunCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("changed condition emitted=%v err=%v, want true nil", emitted, err)
	}

	records, err := DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("drained %d wakes, want one initial and one changed-condition wake", len(records))
	}
}

func TestRunCycle_StatusSignalFromParentStatus(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	os.MkdirAll(state, 0755)
	// Captain return-channel status without requiring dead pane.
	os.WriteFile(filepath.Join(state, "captain:api.meta"), []byte("window=w1\nkind=captain\nbackend=tmux\n"), 0644)
	statusPath, err := mhome.StatusFilePath(home, "captain:api")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte("needs-decision: PR https://example/1 needs a call [key=x]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	emitted, err := testRunCycle(home)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected wake from human-needed parent status")
	}
	// Second cycle with same status must not re-emit (fingerprint dedupe).
	emitted2, err := testRunCycle(home)
	if err != nil {
		t.Fatal(err)
	}
	if emitted2 {
		t.Fatal("expected status signal to be deduped on second cycle")
	}
}

// TestReturnChannelClosedLoop proves Phase 3 C1 without live herdr/pane:
// marked General send (contract) → Captain appends status for logical ID
// captain:<id> using its durable filename stem → watcher RunCycle enqueues a
// signal wake → wake claim leases it.
func TestReturnChannelClosedLoop(t *testing.T) {
	homeDir := t.TempDir()
	state := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}

	// 1) Marked send path (General → Captain). Pane inject is backend-specific;
	// the greppable contract is the marker prefix used by munsu send for kind=captain.
	req := "report progress on return-channel-e2e"
	marked := mhome.MarkFromGeneral(req)
	if !mhome.IsFromGeneral(marked) {
		t.Fatal("expected FromGeneral marker on captain send line")
	}
	if !strings.HasPrefix(marked, mhome.FromGeneralLabel) {
		t.Fatalf("marker label missing: %q", marked)
	}

	// 2) Captain answers on parent return channel (no pane required).
	captainID := "captain:munsu"
	statusLine := "blocked: closed-loop proof needs a decision [key=return-channel-e2e]"
	if err := mhome.AppendStatus(homeDir, captainID, statusLine); err != nil {
		t.Fatal(err)
	}
	// Optional meta present as in a live captain registry entry; signal path
	// must not require dead pane (return channel wakes while captain is alive).
	if err := mhome.WriteMeta(homeDir, captainID, map[string]string{
		"kind":    "captain",
		"window":  "captain-pane-alive",
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	// Non-relevant status must stay quiet (working is not general-relevant).
	if err := mhome.AppendStatus(homeDir, "captain:noise", "working: still grinding"); err != nil {
		t.Fatal(err)
	}

	// 3) Watcher one-shot cycle enqueues the human-needed signal.
	emitted, err := testRunCycle(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected signal wake from human-needed parent status")
	}

	// 4) General leases the wake via claim (not legacy drain).
	claim, err := ClaimWakes(homeDir, "general-e2e", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("expected claimable signal wake after return-channel status")
	}

	var found bool
	for _, w := range claim.Wakes {
		if w.Kind == "signal" && w.Key == captainID && strings.Contains(w.Payload, "blocked") {
			found = true
			if w.Payload != statusLine {
				t.Fatalf("payload = %q, want %q", w.Payload, statusLine)
			}
			break
		}
	}
	if !found {
		t.Fatalf("claimed wakes missing captain signal: %+v", claim.Wakes)
	}
	if claim.LeaseID == "" || claim.Consumer != "general-e2e" {
		t.Fatalf("bad lease: id=%q consumer=%q", claim.LeaseID, claim.Consumer)
	}

	// Queue emptied for claimed records; second claim is empty unless new signal.
	claim2, err := ClaimWakes(homeDir, "general-e2e", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim2.Wakes) != 0 {
		t.Fatalf("expected empty second claim, got %+v", claim2.Wakes)
	}

	// Unchanged status must not re-emit after fingerprint state.
	emitted2, err := testRunCycle(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if emitted2 {
		t.Fatal("expected fingerprint dedupe on unchanged captain status")
	}
}

func TestShouldAbsorbStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Dead pane + leftover working: must NOT absorb (possible finish/wedge).
	os.WriteFile(filepath.Join(stateDir, "dead-working.status"), []byte("working: leftover\n"), 0644)
	if shouldAbsorbStale(tmp, "dead-working", false, testSeatLiveness) {
		t.Error("dead pane with working status should not absorb")
	}

	// Alive pane + working: idle-healthy absorb.
	if !shouldAbsorbStale(tmp, "dead-working", true, testSeatLiveness) {
		t.Error("alive pane with working status should absorb")
	}

	// Paused absorbs regardless of pane liveness.
	os.WriteFile(filepath.Join(stateDir, "paused.status"), []byte("paused: waiting on human\n"), 0644)
	if !shouldAbsorbStale(tmp, "paused", false, testSeatLiveness) {
		t.Error("paused should absorb even when pane is dead")
	}

	// A reported completion is not a stale condition, whatever the pane state.
	os.WriteFile(filepath.Join(stateDir, "done.status"), []byte("done: finished\n"), 0644)
	for _, alive := range []bool{true, false} {
		if !shouldAbsorbStale(tmp, "done", alive, testSeatLiveness) {
			t.Errorf("done should absorb via shouldAbsorbStale (paneAlive=%v)", alive)
		}
	}
}

// --- Pause resurface table-driven tests ---

func TestShouldAbsorbStale_PauseResurface(t *testing.T) {
	tests := []struct {
		name       string
		statusLine string
		pauseAge   time.Duration // age of status file (positive = in the past)
		paneAlive  bool
		wantAbsorb bool
	}{
		{
			name:       "recent pause absorbs",
			statusLine: "paused: waiting on human",
			pauseAge:   time.Minute,
			paneAlive:  false,
			wantAbsorb: true,
		},
		{
			name:       "pause beyond resurface threshold surfaces as stale",
			statusLine: "paused: waiting on human",
			pauseAge:   10 * time.Minute,
			paneAlive:  false,
			wantAbsorb: false,
		},
		{
			name:       "pause at boundary — just under — absorbs",
			statusLine: "paused: waiting on human",
			pauseAge:   4*time.Minute + 59*time.Second,
			paneAlive:  false,
			wantAbsorb: true,
		},
		{
			name:       "pause at boundary — just over — surfaces",
			statusLine: "paused: waiting on human",
			pauseAge:   5*time.Minute + 1*time.Second,
			paneAlive:  false,
			wantAbsorb: false,
		},
		{
			name:       "recent pause with alive pane absorbs",
			statusLine: "paused: waiting on review",
			pauseAge:   time.Minute,
			paneAlive:  true,
			wantAbsorb: true,
		},
		{
			name:       "stale pause with alive pane surfaces",
			statusLine: "paused: waiting on review",
			pauseAge:   10 * time.Minute,
			paneAlive:  true,
			wantAbsorb: false,
		},
		{
			name:       "working status not affected by pause resurface",
			statusLine: "working: still going",
			pauseAge:   time.Minute,
			paneAlive:  true,
			wantAbsorb: true,
		},
		{
			name:       "done status reported completion — absorbs",
			statusLine: "done: finished",
			pauseAge:   time.Minute,
			paneAlive:  true,
			wantAbsorb: true,
		},
		{
			name:       "done status absorbs with a dead pane",
			statusLine: "done: finished",
			pauseAge:   time.Minute,
			paneAlive:  false,
			wantAbsorb: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			stateDir := filepath.Join(tmp, "state")
			os.MkdirAll(stateDir, 0755)

			statusPath := filepath.Join(stateDir, "test-pause.status")
			if err := os.WriteFile(statusPath, []byte(tt.statusLine+"\n"), 0644); err != nil {
				t.Fatal(err)
			}

			// Set mod time to simulate pause age.
			past := time.Now().Add(-tt.pauseAge)
			if err := os.Chtimes(statusPath, past, past); err != nil {
				t.Fatal(err)
			}

			got := shouldAbsorbStale(tmp, "test-pause", tt.paneAlive, testSeatLiveness)
			if got != tt.wantAbsorb {
				t.Errorf("shouldAbsorbStale = %v, want %v (pauseAge=%v, paneAlive=%v)",
					got, tt.wantAbsorb, tt.pauseAge, tt.paneAlive)
			}
		})
	}
}

func TestScanFleet_CaptainKindAbsorbsStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Idle captain with dead pane and non-terminal status must not flood stale.
	mhome.WriteMeta(tmp, "captain:domain", map[string]string{
		"kind":    "captain",
		"window":  "@nonexistent-captain",
		"backend": "tmux",
	})
	statusPath, err := mhome.StatusFilePath(tmp, "captain:domain")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte("working: idle healthy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-(StaleThreshold() + time.Second))
	if err := os.Chtimes(statusPath, past, past); err != nil {
		t.Fatal(err)
	}

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Fatalf("expected nil for idle captain (status-signal only), got %+v", reason)
	}
}

func TestScanFleet_CaptainTerminalStillSignals(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "captain:domain", map[string]string{
		"kind":    "captain",
		"window":  "@nonexistent-captain",
		"backend": "tmux",
	})
	statusPath, err := mhome.StatusFilePath(tmp, "captain:domain")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte("failed: handoff broke\n"), 0644); err != nil {
		t.Fatal(err)
	}

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected signal for human-needed captain status")
	}
	if reason.Kind != "signal" {
		t.Fatalf("kind = %q, want signal", reason.Kind)
	}
}

// TestScanFleet_CaptainHomeOwnCaptainKindSignal proves the watcher reads a
// captain home by the one home predicate: in a captain home (a marker or only
// .captain-provenance) a captain-kind task's human-needed status is not
// relayed to the General, and the General's own home still signals it.
func TestScanFleet_CaptainHomeOwnCaptainKindSignal(t *testing.T) {
	cases := []struct {
		name       string
		arrange    func(t *testing.T, homeDir string)
		wantSignal bool
	}{
		{name: "block: .captain-provenance and no marker", arrange: func(t *testing.T, homeDir string) {
			if err := os.WriteFile(filepath.Join(homeDir, mhome.CaptainProvenanceName), []byte("source-repo\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "block: valid marker", arrange: func(t *testing.T, homeDir string) {
			if err := mhome.SeedCaptainProvenance(homeDir, "domain"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "allow: the General's home", arrange: func(*testing.T, string) {}, wantSignal: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			tc.arrange(t, tmp)
			if err := mhome.WriteMeta(tmp, "captain:domain", map[string]string{
				"kind":    "captain",
				"window":  "@nonexistent-captain",
				"backend": "tmux",
			}); err != nil {
				t.Fatal(err)
			}
			statusPath, err := mhome.StatusFilePath(tmp, "captain:domain")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statusPath, []byte("failed: handoff broke\n"), 0644); err != nil {
				t.Fatal(err)
			}
			reason := testScanFleet(tmp)
			if signaled := reason != nil && reason.Kind == "signal"; signaled != tc.wantSignal {
				t.Fatalf("testScanFleet = %+v, want signal = %v", reason, tc.wantSignal)
			}
		})
	}
}

func TestRunCycle_StaleFingerprintStableAcrossPolls(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	seedBoundSoldier(t, tmp, "task-1", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@nonexistent-stable"})
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: started\n"), 0644)

	emitted, err := testRunCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("first cycle emitted=%v err=%v, want true nil", emitted, err)
	}
	// Second and third cycles must not re-enqueue: message no longer embeds wall-clock age.
	for i := 0; i < 2; i++ {
		emitted, err = testRunCycle(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if emitted {
			t.Fatalf("cycle %d re-emitted stale despite stable fingerprint", i+2)
		}
	}
	records, err := DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("drained %d wakes, want exactly 1", len(records))
	}
	if strings.Contains(records[0].Payload, "idle for") {
		t.Fatalf("stale message must not embed wall-clock age: %q", records[0].Payload)
	}
}

// setupRunCycleTest creates a home with state dir for runCycle tests.
func setupRunCycleTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	return tmp
}

// resetRecovery resets recoveryDone for test isolation.
func resetRecovery() {
	recoveryDone = sync.Map{}
}

// --- CaptainActivationHook tests ---

// TestRunCycle_CaptainActivationNotCalledOnCycle1 verifies that the
// CaptainActivationHook is NOT called during the first cycle (recovery is
// responsible for startup activation). Only per-cycle hooks after recovery
// call it.
func TestRunCycle_CaptainActivationNotCalledOnCycle1(t *testing.T) {
	tmp := setupRunCycleTest(t)

	callCount := 0
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{activate: func(homeDir string) {
		callCount++
	}}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Cycle 1 — recovery guard should prevent hook call.
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("first runCycle: %v", err)
	}
	_ = emitted

	if callCount != 0 {
		t.Errorf("expected 0 CaptainActivationHook calls on cycle 1, got %d", callCount)
	}

	// Cycle 2 — after recovery, hook should be called.
	emitted2, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("second runCycle: %v", err)
	}
	_ = emitted2

	if callCount != 1 {
		t.Errorf("expected 1 CaptainActivationHook call on cycle 2, got %d", callCount)
	}
}

// TestRunCycle_CaptainActivationEveryCycleAfterRecovery verifies that after
// recovery, the CaptainActivationHook is called exactly once per cycle.
func TestRunCycle_CaptainActivationEveryCycleAfterRecovery(t *testing.T) {
	tmp := setupRunCycleTest(t)

	callCount := 0
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{activate: func(homeDir string) {
		callCount++
	}}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Cycle 1 — recovery, no hook call.
	testRunCycle(tmp)
	if callCount != 0 {
		t.Fatalf("expected 0 after cycle 1, got %d", callCount)
	}

	// Cycle 2 — hook called once.
	testRunCycle(tmp)
	if callCount != 1 {
		t.Fatalf("expected 1 after cycle 2, got %d", callCount)
	}

	// Cycle 3 — hook called once more.
	testRunCycle(tmp)
	if callCount != 2 {
		t.Fatalf("expected 2 after cycle 3, got %d", callCount)
	}

	// Cycle 4 — hook called once more.
	testRunCycle(tmp)
	if callCount != 3 {
		t.Fatalf("expected 3 after cycle 4, got %d", callCount)
	}
}

// TestDeadStaleWatcher_PendingWakeDetectsDeadWatcher proves that when the
// watcher beat is stale and material wakes are pending, a scan produces
// actionable condition codes for recovery.
func TestDeadStaleWatcher_PendingWakeDetectsDeadWatcher(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Set up a stale watcher beat.
	old := time.Now().Add(-(StaleThreshold() + time.Minute))
	beatContent := fmt.Sprintf("%d %d", old.Unix(), 99999)
	os.WriteFile(mhome.WatcherBeatPath(tmp), []byte(beatContent), 0644)

	// Add an in-flight task.
	mhome.WriteMeta(tmp, "task-stale", map[string]string{"window": "@test"})
	os.WriteFile(filepath.Join(stateDir, "task-stale.status"), []byte("working: started\n"), 0644)

	// Enqueue a material wake.
	EnqueueWake(tmp, "signal", "task-stale", "done: PR merged")

	// Evaluate guard — should detect stale + pending.
	result := EvaluateGuard(tmp, 1, time.Now())
	if !result.BeatStatus.Stale {
		t.Fatal("expected stale beat status")
	}
	if !HasQueuedWakes(tmp) {
		t.Error("material wake not queued")
	}
}

// TestArmBackground_ClearsStaleIdentity verifies that ArmBackground clears
// stale identity before invoking the process starter. This tests the real
// production contract without spawning a daemon.
func TestArmBackground_ClearsStaleIdentity(t *testing.T) {
	home := t.TempDir()

	// Write a stale identity with known CommitSHA.
	id := NewIdentity(home)
	id.BuildVersion = "0.1.0-dev+stalecommit"
	id.CommitSHA = "stalecommit"
	WriteIdentity(home, id)

	// Substitute the lower-level starter to capture when it would be invoked.
	started := make(chan struct{}, 1)
	savedStarter := startWatcherProcess
	startWatcherProcess = func(dir string) (int, error) {
		// Verify identity is already cleared before the starter runs.
		if remaining := ReadIdentity(dir); remaining != nil {
			if remaining.CommitSHA == "stalecommit" {
				t.Error("stale identity should have been cleared before starter")
			}
		}
		started <- struct{}{}
		return 0, nil // don't actually start a daemon
	}
	defer func() { startWatcherProcess = savedStarter }()

	if err := ArmBackground(home, false); err != nil {
		t.Fatalf("ArmBackground: %v", err)
	}

	// The seam must have been reached (starter called).
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("process starter was never invoked")
	}

	// After ArmBackground returns, identity must be gone.
	if remaining := ReadIdentity(home); remaining != nil {
		t.Errorf("identity should be nil after ArmBackground, got %+v", remaining)
	}
}

// A watcher whose home is deleted under it exits on the failed beat instead of
// resurrecting the state directory.
func TestRunExitsOnABeatFailureAfterTheStateDirectoryIsRemoved(t *testing.T) {
	homeDir := t.TempDir()
	removeState := func(d time.Duration) *time.Ticker {
		if err := os.RemoveAll(filepath.Join(homeDir, "state")); err != nil {
			t.Fatal(err)
		}
		return time.NewTicker(time.Millisecond)
	}

	reason, err := run(homeDir, removeState, nil, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, nil, nil, NoopActiveDeliveryPort{})
	if err == nil || !strings.Contains(err.Error(), "writing watcher beat") || reason != nil {
		t.Fatalf("run = %+v, %v, want a writing-watcher-beat error and no wake", reason, err)
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, "state")); !os.IsNotExist(statErr) {
		t.Fatalf("the state directory was recreated: %v", statErr)
	}
}

// activeDeliveryReply is one scripted ActiveDeliveries result.
type activeDeliveryReply struct {
	active []ActiveDelivery
	err    error
}

// scriptedActiveDeliveryPort answers one reply per call and records whether a
// watcher beat existed at its first call. Its last reply runs last, which ends
// the daemon after the cycle in progress.
type scriptedActiveDeliveryPort struct {
	replies    []activeDeliveryReply
	calls      int
	beatAtCall bool
	last       func()
}

func (p *scriptedActiveDeliveryPort) ActiveDeliveries(homeDir string) ([]ActiveDelivery, error) {
	p.calls++
	if p.calls == 1 {
		_, _, p.beatAtCall = ReadBeat(homeDir)
	}
	if p.calls > len(p.replies) {
		return nil, fmt.Errorf("unscripted call %d", p.calls)
	}
	if p.calls == len(p.replies) {
		p.last()
	}
	r := p.replies[p.calls-1]
	return r.active, r.err
}

// The daemon reads the active delivery journals at each cycle boundary, after
// the cycle's beat, and wakes the General once per journal or read failure. It
// resumes nothing: the port can only list, and fleet's
// TestActiveDeliveryJournalsReadsWithoutResuming pins that the listing leaves
// the journal active.
func TestRunWakesOnActiveDeliveryJournals(t *testing.T) {
	stuck := []ActiveDelivery{{ID: "dj-1", TaskID: "task-1", Stage: "authorized"}}
	readErr := fmt.Errorf("corrupt delivery journal index: unexpected end of JSON input")
	held := fmt.Errorf("home: %w", mhome.ErrLockTimeout)
	stuckMsg := "delivery journal dj-1 of task task-1 is still active at stage authorized after an interrupted delivery; run `munsu watch run` or pr-merge to resume it"
	readErrMsg := "delivery journals could not be read: corrupt delivery journal index: unexpected end of JSON input; run `munsu watch run` to retry recovery"
	cases := []struct {
		name    string
		replies []activeDeliveryReply
		want    []string // "key|payload" of every queued wake, in order
	}{
		{"active journal wakes once", []activeDeliveryReply{{active: stuck}, {active: stuck}},
			[]string{"delivery:dj-1|" + stuckMsg}},
		{"no journals", []activeDeliveryReply{{}, {}}, nil},
		{"read error wakes once per failure", []activeDeliveryReply{{err: readErr}, {err: readErr}, {}, {err: readErr}},
			[]string{deliveryRecoveryKey + "|" + readErrMsg, deliveryRecoveryKey + "|" + readErrMsg}},
		{"scope held by a live delivery", []activeDeliveryReply{{err: held}, {err: held}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			resetRecovery()

			sigCh := make(chan os.Signal, 1)
			stop := func() {
				select {
				case sigCh <- syscall.SIGTERM:
				default:
				}
			}
			// Backstop for a daemon that never reaches the port.
			backstop := time.AfterFunc(3*time.Second, stop)
			defer backstop.Stop()
			var ticker *time.Ticker
			newTicker := func(time.Duration) *time.Ticker {
				// Clear run's startup beat, so only the cycle's own beat can
				// exist when the port is first called.
				ClearBeat(homeDir)
				ticker = time.NewTicker(10 * time.Millisecond)
				return ticker
			}
			port := &scriptedActiveDeliveryPort{replies: tc.replies, last: func() {
				ticker.Stop()
				stop()
			}}

			reason, err := run(homeDir, newTicker, sigCh, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, nil, nil, port)
			if err != nil || reason == nil || reason.Kind != "signal" {
				t.Fatalf("run = %+v, %v, want the daemon to keep running until signalled", reason, err)
			}
			if port.calls != len(tc.replies) {
				t.Fatalf("active deliveries read %d times, want once per cycle (%d)", port.calls, len(tc.replies))
			}
			if !port.beatAtCall {
				t.Fatal("the daemon read the active deliveries before the cycle wrote its beat")
			}
			wakes, err := mhome.PeekWakes(homeDir)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, w := range wakes {
				if w.Kind != "check" {
					t.Fatalf("wake %#v, want only check wakes", w)
				}
				got = append(got, w.Key+"|"+w.Payload)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("wakes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// A status line `munsu report` filed is owned by its report wake, settled or
// not; a status line that came any other way still raises a signal wake.
func TestRunCycleWakesOnceForAReportedStatus(t *testing.T) {
	wakesOfKind := func(t *testing.T, generalHome, kind string) int {
		t.Helper()
		wakes, err := mhome.PeekWakes(generalHome)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, w := range wakes {
			if w.Kind == kind {
				n++
			}
		}
		return n
	}
	cases := []struct {
		name            string
		setup           func(t *testing.T, generalHome string)
		signal, reports int
	}{
		{"open report", func(t *testing.T, g string) {
			reportDone(t, g, "direct-task", "failed")
		}, 0, 1},
		{"settled report", func(t *testing.T, g string) {
			reportDone(t, g, "direct-task", "failed")
			settleAll(t, g)
		}, 0, 0},
		{"status after the report", func(t *testing.T, g string) {
			reportDone(t, g, "direct-task", "failed")
			settleAll(t, g)
			if err := mhome.AppendStatus(g, "direct-task", "failed: a different failure [key=default]"); err != nil {
				t.Fatal(err)
			}
		}, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			generalHome, _ := directGeneralHome(t)
			tc.setup(t, generalHome)
			if _, err := testRunCycle(generalHome); err != nil {
				t.Fatal(err)
			}
			if got := wakesOfKind(t, generalHome, "signal"); got != tc.signal {
				t.Errorf("signal wakes = %d, want %d", got, tc.signal)
			}
			if got := wakesOfKind(t, generalHome, mhome.ReportWakeKind); got != tc.reports {
				t.Errorf("report wakes = %d, want %d", got, tc.reports)
			}
		})
	}
}
