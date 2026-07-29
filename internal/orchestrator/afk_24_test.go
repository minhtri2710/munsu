package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Fake backends for injector tests ---

// fakeSendKeysBackend records SendKeys calls for test inspection.
type fakeSendKeysBackend struct {
	mu     sync.Mutex
	calls  []sendKeysCall
	fail   bool // if true, SendKeys returns an error
	record bool // if true, record calls
}

type sendKeysCall struct {
	windowID string
	text     string
}

func (f *fakeSendKeysBackend) SendKeys(windowID, text string) error {
	if f.fail {
		return os.ErrInvalid
	}
	if f.record {
		f.mu.Lock()
		f.calls = append(f.calls, sendKeysCall{windowID: windowID, text: text})
		f.mu.Unlock()
	}
	return nil
}

func (f *fakeSendKeysBackend) Calls() []sendKeysCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make([]sendKeysCall, len(f.calls))
	copy(c, f.calls)
	return c
}

// --- Util to set up test fixtures ---

// setupInjectorTest creates a temp home with config/general-pane, state/.afk,
// and a fake capture that returns Empty (safe). Returns the home dir, injector,
// fake backend, and a cleanup function.
func setupInjectorTest(t *testing.T) (homeDir string, inj *Injector, fakeBk *fakeSendKeysBackend, cleanup func()) {
	t.Helper()

	tmp := t.TempDir()

	// Create config/general-pane.
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("test-session:test-pane\n"), 0644); err != nil {
		t.Fatalf("writing general-pane config: %v", err)
	}

	// Create state/.afk consent flag.
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".afk"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		t.Fatalf("writing consent flag: %v", err)
	}

	// Create a default BatchedEscalation file (needed for tryInject path).
	// The injector doesn't need it, but keep the state tidy.

	fakeBk = &fakeSendKeysBackend{record: true}
	fakeCap := &fakePaneCapture{content: "\u276F \n"} // Claude prompt = Empty = safe
	inj = NewInjector(fakeBk, fakeCap, tmp)

	return tmp, inj, fakeBk, func() {}
}

// buildTestEscalation creates a simple BatchedEscalation with one entry.
func buildTestEscalation(entryKind, entryKey, payload string, etype EscalationType) *BatchedEscalation {
	return &BatchedEscalation{
		Entries: []BatchedEntry{
			{
				Kind:    entryKind,
				Key:     entryKey,
				Payload: payload,
				Type:    etype,
				At:      time.Now(),
			},
		},
		EscalatedCount: 1,
		FirstAt:        time.Now(),
		LastAt:         time.Now(),
	}
}

// --- InjectIfSafe tests ---

func TestInjectIfSafe_SafeTarget_SendsKeys(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	be := buildTestEscalation("afk", "task-1", "PR merged", EscalationReviewReady)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe: %v", err)
	}
	if !injected {
		t.Fatal("InjectIfSafe returned false, want true")
	}

	calls := fakeBk.Calls()
	if len(calls) != 1 {
		t.Fatalf("SendKeys calls = %d, want 1", len(calls))
	}

	// Verify sentinel prefix.
	if !strings.HasPrefix(calls[0].text, FM_INJECT_MARK) {
		t.Errorf("SendKeys text does not start with sentinel mark: %q", calls[0].text)
	}

	// Verify payload contains the escalation info.
	if !strings.Contains(calls[0].text, "PR merged") {
		t.Errorf("SendKeys text missing payload: %q", calls[0].text)
	}

	// Verify target pane.
	if calls[0].windowID != "test-session:test-pane" {
		t.Errorf("SendKeys windowID = %q, want test-session:test-pane", calls[0].windowID)
	}
}

func TestInjectIfSafe_PendingTarget_NoSendKeys(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("s:p\n"), 0644)
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("now\n"), 0644)

	fakeBk := &fakeSendKeysBackend{record: true}
	// Pending: typed text in composer.
	fakeCap := &fakePaneCapture{content: "git status\n"}
	inj := NewInjector(fakeBk, fakeCap, tmp)

	be := buildTestEscalation("afk", "task-1", "needs decision", EscalationDecision)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe: %v", err)
	}
	if injected {
		t.Fatal("InjectIfSafe returned true for pending target, want false")
	}

	calls := fakeBk.Calls()
	if len(calls) != 0 {
		t.Fatalf("SendKeys calls = %d, want 0 for pending target", len(calls))
	}
}

func TestInjectIfSafe_UnknownTarget_NoSendKeys(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("s:p\n"), 0644)
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("now\n"), 0644)

	fakeBk := &fakeSendKeysBackend{record: true}
	// Unknown: bare shell prompt.
	fakeCap := &fakePaneCapture{content: "> \n"}
	inj := NewInjector(fakeBk, fakeCap, tmp)

	be := buildTestEscalation("afk", "task-1", "PR merged", EscalationReviewReady)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe: %v", err)
	}
	if injected {
		t.Fatal("InjectIfSafe returned true for unknown target, want false")
	}

	calls := fakeBk.Calls()
	if len(calls) != 0 {
		t.Fatalf("SendKeys calls = %d, want 0 for unknown target", len(calls))
	}
}

func TestInjectIfSafe_NoConsentFlag_NoSendKeys(t *testing.T) {
	tmp := t.TempDir()
	// Set up config but NOT the consent flag (state/.afk).
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("s:p\n"), 0644)

	fakeBk := &fakeSendKeysBackend{record: true}
	fakeCap := &fakePaneCapture{content: "\u276F \n"} // safe if it were checked
	inj := NewInjector(fakeBk, fakeCap, tmp)

	be := buildTestEscalation("afk", "task-2", "build broken", EscalationFailure)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe: %v", err)
	}
	if injected {
		t.Fatal("InjectIfSafe returned true without consent flag, want false")
	}

	calls := fakeBk.Calls()
	if len(calls) != 0 {
		t.Fatalf("SendKeys calls = %d, want 0 without consent flag", len(calls))
	}
}

func TestInjectIfSafe_NoTargetConfig_NoSendKeys(t *testing.T) {
	tmp := t.TempDir()
	// Create consent flag but NO config/general-pane.
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("now\n"), 0644)

	fakeBk := &fakeSendKeysBackend{record: true}
	fakeCap := &fakePaneCapture{content: "\u276F \n"} // safe
	inj := NewInjector(fakeBk, fakeCap, tmp)

	be := buildTestEscalation("afk", "task-3", "review ready", EscalationReviewReady)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe: %v", err)
	}
	if injected {
		t.Fatal("InjectIfSafe returned true without target config, want false")
	}

	calls := fakeBk.Calls()
	if len(calls) != 0 {
		t.Fatalf("SendKeys calls = %d, want 0 without target config", len(calls))
	}
}

func TestInjectIfSafe_DuplicateEscalation_NoReInject(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	be := buildTestEscalation("afk", "task-dup", "same content", EscalationRoutine)

	// First inject should succeed.
	injected1, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("first InjectIfSafe: %v", err)
	}
	if !injected1 {
		t.Fatal("first InjectIfSafe returned false, want true")
	}

	// Captain inject with same entries should be deduped.
	injected2, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("captain InjectIfSafe: %v", err)
	}
	if injected2 {
		t.Fatal("captain InjectIfSafe returned true for duplicate, want false (dedup)")
	}

	calls := fakeBk.Calls()
	if len(calls) != 1 {
		t.Fatalf("SendKeys calls = %d after dedup, want 1", len(calls))
	}
}

func TestInjectIfSafe_DifferentEntries_NotDeduped(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	be1 := buildTestEscalation("afk", "task-a", "payload one", EscalationReviewReady)
	be2 := buildTestEscalation("afk", "task-b", "payload two", EscalationDecision)

	injected1, err := inj.InjectIfSafe(be1)
	if err != nil {
		t.Fatalf("first InjectIfSafe: %v", err)
	}
	if !injected1 {
		t.Fatal("first InjectIfSafe returned false, want true")
	}

	// Captain escalation has different key/payload -> should inject.
	injected2, err := inj.InjectIfSafe(be2)
	if err != nil {
		t.Fatalf("captain InjectIfSafe: %v", err)
	}
	if !injected2 {
		t.Fatal("captain InjectIfSafe returned false for different entries, want true")
	}

	calls := fakeBk.Calls()
	if len(calls) != 2 {
		t.Fatalf("SendKeys calls = %d, want 2 for different entries", len(calls))
	}
}

func TestInjectIfSafe_SentinelPrefixAlwaysPresent(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	// Test with various escalation types — sentinel must always be there.
	payloads := []string{
		"needs-decision: which branch",
		"done: build green",
		"failed: tests broken",
		"credential expired",
	}

	for _, p := range payloads {
		be := buildTestEscalation("afk", "test", p, EscalationRoutine)
		injected, err := inj.InjectIfSafe(be)
		if err != nil {
			t.Fatalf("InjectIfSafe(%q): %v", p, err)
		}
		if !injected {
			t.Fatalf("InjectIfSafe(%q) returned false, want true", p)
		}

		calls := fakeBk.Calls()
		lastCall := calls[len(calls)-1]
		if !strings.HasPrefix(lastCall.text, FM_INJECT_MARK) {
			t.Errorf("payload %q: SendKeys text missing sentinel: %q", p, lastCall.text)
		}
	}
}

func TestInjectIfSafe_EmptyEntries(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	// Escalation with no entries and no wedge alarm.
	be := &BatchedEscalation{
		Entries:        nil,
		EscalatedCount: 0,
	}

	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe on empty escalation: %v", err)
	}
	if injected {
		t.Fatal("InjectIfSafe returned true for empty escalation, want false")
	}

	calls := fakeBk.Calls()
	if len(calls) != 0 {
		t.Fatalf("SendKeys calls = %d for empty escalation, want 0", len(calls))
	}
}

func TestInjectIfSafe_SendKeysError(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("s:p\n"), 0644)
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("now\n"), 0644)

	// Backend that fails.
	fakeBk := &fakeSendKeysBackend{fail: true, record: true}
	fakeCap := &fakePaneCapture{content: "\u276F \n"}
	inj := NewInjector(fakeBk, fakeCap, tmp)

	be := buildTestEscalation("afk", "task-e", "error test", EscalationRoutine)
	_, err := inj.InjectIfSafe(be)
	if err == nil {
		t.Fatal("InjectIfSafe expected error from failed SendKeys, got nil")
	}
}

// --- formatPayload tests ---

func TestFormatPayload_Basic(t *testing.T) {
	be := buildTestEscalation("afk", "t1", "PR merged", EscalationReviewReady)
	payload := formatPayload(be)

	if !strings.Contains(payload, "1 escalated") {
		t.Errorf("payload missing escalation count: %q", payload)
	}
	if !strings.Contains(payload, "PR merged") {
		t.Errorf("payload missing entry payload: %q", payload)
	}
	if !strings.Contains(payload, "review-ready") {
		t.Errorf("payload missing type: %q", payload)
	}
}

func TestFormatPayload_WedgeAlarm(t *testing.T) {
	be := &BatchedEscalation{
		WedgeAlarm: &WedgeAlarm{
			Reason:     "watcher beat stale",
			DetectedAt: time.Now(),
		},
		EscalatedCount: 0,
	}
	payload := formatPayload(be)

	if !strings.Contains(payload, "wedge") {
		t.Errorf("payload missing wedge indicator: %q", payload)
	}
	if !strings.Contains(payload, "watcher beat stale") {
		t.Errorf("payload missing wedge reason: %q", payload)
	}
}

func TestFormatPayload_RoutineOnly(t *testing.T) {
	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "check", Key: "health", Payload: "all green", Type: EscalationRoutine, At: time.Now()},
		},
		RoutineCount:   1,
		EscalatedCount: 0,
	}
	payload := formatPayload(be)

	// Routine entries should NOT appear in the detail parts (only escalated).
	if strings.Contains(payload, "all green") {
		t.Errorf("payload should not include routine details: %q", payload)
	}
}

// --- Injector LastEvents tests ---

func TestInjector_LastEvents(t *testing.T) {
	_, inj, _, cleanup := setupInjectorTest(t)
	defer cleanup()

	events := inj.LastEvents()
	if len(events) != 0 {
		t.Fatalf("initial LastEvents = %d, want 0", len(events))
	}

	be := buildTestEscalation("afk", "t1", "event test", EscalationReviewReady)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe: %v", err)
	}
	if !injected {
		t.Fatal("InjectIfSafe returned false")
	}

	events = inj.LastEvents()
	if len(events) != 1 {
		t.Fatalf("LastEvents after inject = %d, want 1", len(events))
	}
	if events[0].Target != "test-session:test-pane" {
		t.Errorf("event target = %q, want test-session:test-pane", events[0].Target)
	}
	if events[0].Entries != 1 {
		t.Errorf("event entries = %d, want 1", events[0].Entries)
	}
	if events[0].Verdict == "" {
		t.Error("event verdict is empty")
	}
	if events[0].Timestamp.IsZero() {
		t.Error("event timestamp is zero")
	}
}

// --- Daemon tryInject integration test ---

func TestDaemonTryInject_NoDigestFile(t *testing.T) {
	tmp := t.TempDir()
	d := &Daemon{homeDir: tmp}

	// tryInject without a digest file should be a no-op.
	err := d.tryInject()
	if err != nil {
		t.Fatalf("tryInject without digest file: %v", err)
	}
}

func TestDaemonTryInject_EmptyDigestFile(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write an empty BatchedEscalation (no entries, no wedge).
	be := &BatchedEscalation{Entries: nil}
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	d := &Daemon{homeDir: tmp}
	err := d.tryInject()
	if err != nil {
		t.Fatalf("tryInject with empty digest: %v", err)
	}
}

func TestDaemonTryInject_WithBackend(t *testing.T) {
	tmp := t.TempDir()

	// Set up config, consent flag.
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("s:p\n"), 0644)
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("now\n"), 0644)

	// Write a non-empty digest file.
	be := buildTestEscalation("afk", "task-x", "digest inject test", EscalationReviewReady)
	data, _ := json.Marshal(be)
	os.WriteFile(filepath.Join(stateDir, ".afk-digest"), data, 0644)

	fakeBk := &fakeSendKeysBackend{record: true}
	fakeCap := &fakePaneCapture{content: "\u276F \n"} // safe

	d := &Daemon{
		homeDir:  tmp,
		injector: NewInjector(fakeBk, fakeCap, tmp),
	}

	err := d.tryInject()
	if err != nil {
		t.Fatalf("tryInject: %v", err)
	}

	calls := fakeBk.Calls()
	if len(calls) != 1 {
		t.Fatalf("SendKeys calls = %d, want 1", len(calls))
	}
	if !strings.HasPrefix(calls[0].text, FM_INJECT_MARK) {
		t.Errorf("SendKeys text missing sentinel: %q", calls[0].text)
	}
}

// --- Edge cases ---

func TestInjectIfSafe_CaptureError(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("s:p\n"), 0644)
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("now\n"), 0644)

	fakeBk := &fakeSendKeysBackend{record: true}
	// Capture returns an error.
	fakeCap := &fakePaneCapture{err: os.ErrInvalid}
	inj := NewInjector(fakeBk, fakeCap, tmp)

	be := buildTestEscalation("afk", "t-err", "capture error", EscalationRoutine)
	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		// Error is expected, but injection should not happen.
		t.Logf("InjectIfSafe returned error: %v", err)
	}
	if injected {
		t.Fatal("InjectIfSafe returned true on capture error, want false")
	}

	calls := fakeBk.Calls()
	if len(calls) != 0 {
		t.Fatalf("SendKeys calls = %d on capture error, want 0", len(calls))
	}
}

func TestInjectIfSafe_WedgeAlarmOnly(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	// Escalation with only a wedge alarm (no entries).
	be := &BatchedEscalation{
		WedgeAlarm: &WedgeAlarm{
			Reason:     "repeated identical stale wake",
			DetectedAt: time.Now(),
		},
	}

	injected, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("InjectIfSafe with wedge alarm: %v", err)
	}
	if !injected {
		t.Fatal("InjectIfSafe returned false for wedge alarm, want true")
	}

	calls := fakeBk.Calls()
	if len(calls) != 1 {
		t.Fatalf("SendKeys calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0].text, "repeated identical") {
		t.Errorf("SendKeys text missing wedge reason: %q", calls[0].text)
	}
}

func TestInjectIfSafe_CooldownRespectsPerEntryDedup(t *testing.T) {
	_, inj, fakeBk, cleanup := setupInjectorTest(t)
	defer cleanup()

	// Two entries in one escalation.
	be := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "afk", Key: "t1", Payload: "PR merged", Type: EscalationReviewReady, At: time.Now()},
			{Kind: "afk", Key: "t2", Payload: "build broken", Type: EscalationFailure, At: time.Now()},
		},
		EscalatedCount: 2,
		FirstAt:        time.Now(),
		LastAt:         time.Now(),
	}

	// First inject should send both.
	injected1, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("first InjectIfSafe: %v", err)
	}
	if !injected1 {
		t.Fatal("first InjectIfSafe returned false, want true")
	}

	// Captain inject with same entries -> deduped.
	injected2, err := inj.InjectIfSafe(be)
	if err != nil {
		t.Fatalf("captain InjectIfSafe: %v", err)
	}
	if injected2 {
		t.Fatal("captain InjectIfSafe should be deduped", err)
	}

	// Third inject with one new entry should not be fully deduped.
	be2 := &BatchedEscalation{
		Entries: []BatchedEntry{
			{Kind: "afk", Key: "t1", Payload: "PR merged", Type: EscalationReviewReady, At: time.Now()}, // dup
			{Kind: "afk", Key: "t3", Payload: "new event", Type: EscalationDecision, At: time.Now()},    // new
		},
		EscalatedCount: 2,
		FirstAt:        time.Now(),
		LastAt:         time.Now(),
	}

	injected3, err := inj.InjectIfSafe(be2)
	if err != nil {
		t.Fatalf("third InjectIfSafe: %v", err)
	}
	if !injected3 {
		t.Fatal("third InjectIfSafe should inject (has new entry)")
	}

	calls := fakeBk.Calls()
	if len(calls) != 2 {
		t.Fatalf("SendKeys calls = %d, want 2 (first + third)", len(calls))
	}
}
