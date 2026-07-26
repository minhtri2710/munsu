package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/wakedelivery"
)

// --- resolveInjectionTargetHome tests ---

func TestResolveInjectionTargetHome(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		homeDir    string
		parentHome string
		wantHome   string
		wantErr    bool
		errMsg     string
	}{
		{
			name:       "soldier with parentHome returns parentHome",
			role:       "soldier",
			homeDir:    "/soldier/home",
			parentHome: "/captain/home",
			wantHome:   "/captain/home",
			wantErr:    false,
		},
		{
			name:       "soldier without parentHome returns error",
			role:       "soldier",
			homeDir:    "/soldier/home",
			parentHome: "",
			wantErr:    true,
			errMsg:     "MUNSU_PARENT_STATUS not set for role \"soldier\"",
		},
		{
			name:       "captain with parentHome returns parentHome",
			role:       "captain",
			homeDir:    "/captain/home",
			parentHome: "/general/home",
			wantHome:   "/general/home",
			wantErr:    false,
		},
		{
			name:       "captain without parentHome returns error",
			role:       "captain",
			homeDir:    "/captain/home",
			parentHome: "",
			wantErr:    true,
			errMsg:     "MUNSU_PARENT_STATUS not set for role \"captain\"",
		},
		{
			name:       "general without parentHome returns homeDir",
			role:       "general",
			homeDir:    "/general/home",
			parentHome: "",
			wantHome:   "/general/home",
			wantErr:    false,
		},
		{
			name:       "general with parentHome ignored returns homeDir",
			role:       "general",
			homeDir:    "/general/home",
			parentHome: "/somewhere",
			wantHome:   "/general/home",
			wantErr:    false,
		},
		{
			name:       "empty role without parentHome returns homeDir",
			role:       "",
			homeDir:    "/local/home",
			parentHome: "",
			wantHome:   "/local/home",
			wantErr:    false,
		},
		{
			name:       "unknown role without parentHome returns homeDir",
			role:       "crew",
			homeDir:    "/crew/home",
			parentHome: "",
			wantHome:   "/crew/home",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wakedelivery.ResolveInjectionTargetHome(tt.role, tt.homeDir, tt.parentHome)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantHome {
				t.Errorf("resolveInjectionTargetHome = %q, want %q", got, tt.wantHome)
			}
		})
	}
}

// --- Fake backend for injection tests ---

// fakeInjectBackend implements session.Backend and session.PromptSubmitter
// for testing injectToParentPaneWithResolver. It records all calls and
// returns configurable results.
type fakeInjectBackend struct {
	mu            sync.Mutex
	captureCalls  []string
	sendKeysCalls []string
	captureResult string
	captureErr    error
	promptResult  session.PromptResult
}

func (f *fakeInjectBackend) NewWindow(session, name string) (string, error) {
	return "test-window-" + name, nil
}

func (f *fakeInjectBackend) SendKeys(windowID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendKeysCalls = append(f.sendKeysCalls, windowID+":"+text)
	return nil
}

func (f *fakeInjectBackend) Capture(windowID string, lines int) (string, error) {
	f.mu.Lock()
	f.captureCalls = append(f.captureCalls, windowID)
	f.mu.Unlock()
	return f.captureResult, f.captureErr
}

func (f *fakeInjectBackend) Alive(windowID string) bool {
	return true
}

func (f *fakeInjectBackend) Teardown(windowID string) error {
	return nil
}

func (f *fakeInjectBackend) AgentPrompt(windowID, text string) session.PromptResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendKeysCalls = append(f.sendKeysCalls, "prompt:"+windowID+":"+text)
	return f.promptResult
}

// session.Backend interface satisfaction check.
var _ session.Backend = (*fakeInjectBackend)(nil)

// fakeResolver creates a ReportSessionResolver that returns the given backend.
func fakeResolver(bk session.Backend) wakedelivery.ReportSessionResolver {
	return func(_ string, _ string) (session.Backend, string, error) {
		return bk, "test", nil
	}
}

// setupInjectionTest creates temp homes with config/general-pane and returns
// paths, a cleanup function, and a file-join helper.
func setupInjectionTest(t *testing.T) (homeDir string, parentHome string, cleanup func()) {
	t.Helper()
	homeDir = t.TempDir()
	parentHome = t.TempDir()

	// Create config/general-pane in parentHome (captain's pane config)
	configDir := filepath.Join(parentHome, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir parent config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("captain-session:captain-pane\n"), 0644); err != nil {
		t.Fatalf("write parent general-pane: %v", err)
	}

	// Create config/general-pane in homeDir (soldier's pane config — should not be used)
	soldierConfigDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(soldierConfigDir, 0755); err != nil {
		t.Fatalf("mkdir soldier config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(soldierConfigDir, "general-pane"), []byte("soldier-session:soldier-pane\n"), 0644); err != nil {
		t.Fatalf("write soldier general-pane: %v", err)
	}

	return homeDir, parentHome, func() {}
}

// --- injectToParentPaneWithResolver tests ---

func TestInjectToParentPaneWithResolver_SoldierUsesParentHome(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n", // Empty composer = safe
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR #1", "done", 100, fakeResolver(fake))

	if result.Outcome != afk.OutcomeInjected {
		t.Errorf("outcome = %q, want %q: error=%s", result.Outcome, afk.OutcomeInjected, result.Error)
	}
	// Target should be from captain's config/general-pane, not soldier's
	if !strings.Contains(result.Target, "captain-pane") {
		t.Errorf("target = %q, should contain %q (captain's pane, not soldier's)", result.Target, "captain-pane")
	}
	if strings.Contains(result.Target, "soldier-pane") {
		t.Errorf("target = %q, should NOT contain %q (must not use soldier's pane)", result.Target, "soldier-pane")
	}
}

func TestInjectToParentPaneWithResolver_SoldierUsesParentHome_AllMaterialStates(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	for i, state := range []string{"done", "failed", "blocked", "needs-decision"} {
		t.Run(state, func(t *testing.T) {
			fake := &fakeInjectBackend{
				captureResult: "\u276F \n",
				promptResult:  session.PromptResult{Status: session.PromptSubmitted},
			}
			taskID := fmt.Sprintf("task-all-states-%s", state)
			eventID := uint64(999000 + i)
			result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, taskID, state+": msg", state, eventID, fakeResolver(fake))

			if result.Outcome != afk.OutcomeInjected {
				t.Errorf("%s: outcome = %q, want %q: error=%s", state, result.Outcome, afk.OutcomeInjected, result.Error)
			}
			if !strings.Contains(result.Target, "captain-pane") {
				t.Errorf("%s: target = %q, should contain captain-pane", state, result.Target)
			}
		})
	}
}

func TestInjectToParentPaneWithResolver_CaptainUsesParentHome(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("captain", homeDir, parentHome, "task-1", "done: PR", "done", 200, fakeResolver(fake))

	if result.Outcome != afk.OutcomeInjected {
		t.Errorf("outcome = %q, want %q: error=%s", result.Outcome, afk.OutcomeInjected, result.Error)
	}
	if !strings.Contains(result.Target, "captain-pane") {
		t.Errorf("target = %q, should contain captain-pane (from parentHome)", result.Target)
	}
}

func TestInjectToParentPaneWithResolver_GeneralUsesHomeDir(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	// Create general's own config/general-pane, different from parent
	generalPane := "general-session:general-pane"
	configDir := filepath.Join(homeDir, "config")
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte(generalPane+"\n"), 0644); err != nil {
		t.Fatalf("write general-pane: %v", err)
	}

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("general", homeDir, parentHome, "task-1", "done: PR", "done", 300, fakeResolver(fake))

	if result.Outcome != afk.OutcomeInjected {
		t.Errorf("outcome = %q, want %q: error=%s", result.Outcome, afk.OutcomeInjected, result.Error)
	}
	if !strings.Contains(result.Target, "general-pane") {
		t.Errorf("target = %q, should contain general-pane (from homeDir, not parent)", result.Target)
	}
}

func TestInjectToParentPaneWithResolver_GeneralNoParent(t *testing.T) {
	homeDir, _, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("general", homeDir, "", "task-1", "done: PR", "done", 400, fakeResolver(fake))

	if result.Outcome != afk.OutcomeInjected {
		t.Errorf("outcome = %q, want %q: error=%s", result.Outcome, afk.OutcomeInjected, result.Error)
	}
	if !strings.Contains(result.Target, "soldier-pane") {
		t.Errorf("target = %q, should contain soldier-pane (from homeDir)", result.Target)
	}
}

func TestInjectToParentPaneWithResolver_SoldierMissingParent(t *testing.T) {
	homeDir, _, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, "", "task-1", "done: PR", "done", 500, fakeResolver(fake))

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
	if !strings.Contains(result.Error, "MUNSU_PARENT_STATUS") {
		t.Errorf("error = %q, should mention MUNSU_PARENT_STATUS", result.Error)
	}
	// Must never resolve from soldier's own home when parent is missing
	if strings.Contains(result.Error, "soldier-pane") || strings.Contains(result.Target, "soldier-pane") {
		t.Error("must not resolve target from soldier's own home when parent is missing")
	}
}

func TestInjectToParentPaneWithResolver_CaptainMissingParent(t *testing.T) {
	homeDir, _, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("captain", homeDir, "", "task-1", "done: PR", "done", 600, fakeResolver(fake))

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
	if !strings.Contains(result.Error, "MUNSU_PARENT_STATUS") {
		t.Errorf("error = %q, should mention MUNSU_PARENT_STATUS", result.Error)
	}
}

func TestInjectToParentPaneWithResolver_ParentTargetUnresolvable(t *testing.T) {
	homeDir := t.TempDir()    // no config/general-pane
	parentHome := t.TempDir() // no config/general-pane

	// Unset runtime env vars that could provide fallback targets
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_SESSION", "")

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 700, fakeResolver(fake))

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
}

func TestInjectToParentPaneWithResolver_DeadCapture(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "",
		captureErr:    os.ErrInvalid,
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 800, fakeResolver(fake))

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
	if result.Verdict == "" {
		t.Error("verdict should be non-empty for dead capture")
	}
}

func TestInjectToParentPaneWithResolver_UnsafeComposer(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F git push\n", // Pending content = unsafe
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 900, fakeResolver(fake))

	if result.Outcome != afk.OutcomeUnsafe {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeUnsafe)
	}
}

func TestInjectToParentPaneWithResolver_BusyComposer(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "> \n", // Shell prompt = unsafe (not bordered composer)
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 1000, fakeResolver(fake))

	if result.Outcome != afk.OutcomeUnsafe {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeUnsafe)
	}
}

func TestInjectToParentPaneWithResolver_BackendDead(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 1100,
		func(_ string, _ string) (session.Backend, string, error) {
			return nil, "", fmt.Errorf("no backend available")
		})

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
	if !strings.Contains(result.Error, "no backend available") {
		t.Errorf("error = %q, should contain 'no backend available'", result.Error)
	}
}

func TestInjectToParentPaneWithResolver_SubmitPromptStalled(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptStalled},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 1200, fakeResolver(fake))

	if result.Outcome != afk.OutcomeBackendFailed {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeBackendFailed)
	}
	if !strings.Contains(result.Error, string(session.PromptStalled)) {
		t.Errorf("error = %q, should contain %q", result.Error, session.PromptStalled)
	}
}

func TestInjectToParentPaneWithResolver_SubmitPromptQueuedWhileBusy(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptQueuedWhileBusy},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 1300, fakeResolver(fake))

	// Queued-while-busy IS acknowledged (like submitted)
	if result.Outcome != afk.OutcomeInjected {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeInjected)
	}
}

func TestInjectToParentPaneWithResolver_SubmitPromptEndpointDead(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptEndpointDead},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 1400, fakeResolver(fake))

	if result.Outcome != afk.OutcomeBackendFailed {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeBackendFailed)
	}
	if !strings.Contains(result.Error, string(session.PromptEndpointDead)) {
		t.Errorf("error = %q, should contain %q", result.Error, session.PromptEndpointDead)
	}
}

func TestInjectToParentPaneWithResolver_RetriesSameEventAfterUnsafeAttempt(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)
	eventID := uint64(99881)

	unsafe := &fakeInjectBackend{
		captureResult: "❯ git push\n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}
	first := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-retry", "done", "done", eventID, fakeResolver(unsafe))
	if first.Outcome != afk.OutcomeUnsafe {
		t.Fatalf("first attempt outcome = %q, want unsafe", first.Outcome)
	}

	safe := &fakeInjectBackend{
		captureResult: "❯ \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}
	second := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-retry", "done", "done", eventID, fakeResolver(safe))
	if second.Outcome != afk.OutcomeInjected || second.Target == "(deduped)" {
		t.Fatalf("second attempt should retry and inject, got outcome=%q target=%q", second.Outcome, second.Target)
	}
}

func TestInjectToParentPaneWithResolver_Dedup(t *testing.T) {
	homeDir, parentHome, _ := setupInjectionTest(t)

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	// Clear any previous dedup state by using a unique event ID
	eventID := uint64(99999)

	// First call should succeed
	result1 := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-dedup", "done: PR", "done", eventID, fakeResolver(fake))
	if result1.Outcome != afk.OutcomeInjected {
		t.Errorf("first call: outcome = %q, want %q", result1.Outcome, afk.OutcomeInjected)
	}

	// Second call with same event ID should be deduped
	result2 := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-dedup", "done: PR", "done", eventID, fakeResolver(fake))
	if result2.Outcome != afk.OutcomeInjected || result2.Target != "(deduped)" {
		t.Errorf("second call: outcome = %q, target = %q, want injected/(deduped)", result2.Outcome, result2.Target)
	}

	// Different event ID should succeed
	eventID3 := eventID + 1
	result3 := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-dedup", "done: PR", "done", eventID3, fakeResolver(fake))
	if result3.Outcome != afk.OutcomeInjected {
		t.Errorf("third call (different eventID): outcome = %q, want %q", result3.Outcome, afk.OutcomeInjected)
	}
	if result3.Target == "(deduped)" {
		t.Error("third call (different eventID) should not be deduped")
	}
}
