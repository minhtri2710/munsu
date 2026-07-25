package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/event"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/turnend"
	"github.com/minhtri2710/munsu/internal/wakedelivery"
	"github.com/spf13/cobra"
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
	mu           sync.Mutex
	captureCalls []string
	sendKeysCalls []string
	captureResult string
	captureErr   error
	promptResult session.PromptResult
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
	homeDir := t.TempDir()           // no config/general-pane
	parentHome := t.TempDir()        // no config/general-pane

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

// --- Command-level injection tests ---

// deterministicInjectFn returns an injectFn that returns controlled results.
func deterministicInjectFn(outcome afk.InjectOutcome, target, errMsg string) func(string, string, string, string, string, string, uint64) afk.InjectResult {
	return func(role, homeDir, parentHome, taskID, msg, state string, syntheticID uint64) afk.InjectResult {
		return afk.InjectResult{
			Outcome: outcome,
			Target:  target,
			Error:   errMsg,
		}
	}
}

// TestReportCmd_DeterministicInjection_Success verifies that a successful
// injection in the contract output does not affect durable writes.
func TestReportCmd_DeterministicInjection_Success(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-inject-success")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	cmd := newReportCmdWithInjector(deterministicInjectFn(afk.OutcomeInjected, "captain-pane", ""))
	root := &cobra.Command{Use: "munsu"}
	root.AddCommand(cmd)
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "ring", "--output", "json", "done", "task complete"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	// Verify durable writes still happened despite deterministic injection
	receiptPath := turnend.ReceiptPath(parentHome, "test-inject-success", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist: %v", err)
	}

	statusPath := filepath.Join(homeDir, "state", "test-inject-success.status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Errorf("status should exist: %v", err)
	}

	eventPath := event.LogPath(homeDir)
	if _, err := os.Stat(eventPath); err != nil {
		t.Errorf("event log should exist: %v", err)
	}

	wakePath := lifecycle.QueuePath(homeDir)
	if _, err := os.Stat(wakePath); err != nil {
		t.Errorf("wake queue should exist in soldier home: %v", err)
	}
}

// TestReportCmd_DeterministicInjection_Failure verifies that injection failure
// does not prevent durable status/event/wake/receipt writes.
func TestReportCmd_DeterministicInjection_Failure(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-inject-fail")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	cmd := newReportCmdWithInjector(deterministicInjectFn(afk.OutcomeUnsafe, "captain-pane", "composer busy"))
	root := &cobra.Command{Use: "munsu"}
	root.AddCommand(cmd)
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "ring", "--output", "json", "done", "task complete"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success despite injection failure, got: %v", err)
	}

	// Verify durable writes still happened (injection is best-effort)
	receiptPath := turnend.ReceiptPath(parentHome, "test-inject-fail", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist despite injection failure: %v", err)
	}

	statusPath := filepath.Join(homeDir, "state", "test-inject-fail.status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Errorf("status should exist despite injection failure: %v", err)
	}

	// Verify contract output contains the injection failure details
	var resp contract.Response[contract.MessageResult]
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.Injection == nil {
		t.Fatal("injection field should be present in contract output")
	}
	if resp.Data.Injection.Outcome != string(afk.OutcomeUnsafe) {
		t.Errorf("injection outcome = %q, want %q", resp.Data.Injection.Outcome, afk.OutcomeUnsafe)
	}
	if resp.Data.Injection.Error != "composer busy" {
		t.Errorf("injection error = %q, want %q", resp.Data.Injection.Error, "composer busy")
	}
}

// TestReportCmd_DeterministicInjection_AllMaterialStates verifies that all
// material states produce durable writes regardless of injection outcome.
func TestReportCmd_DeterministicInjection_AllMaterialStates(t *testing.T) {
	for _, state := range []struct {
		name  string
		value string
	}{
		{"done", "done"},
		{"failed", "failed"},
		{"blocked", "blocked"},
		{"needs_decision", "needs-decision"},
	} {
		t.Run(state.name, func(t *testing.T) {
			homeDir := t.TempDir()
			parentHome := t.TempDir()

			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("MUNSU_TASK_ID", "test-state-"+state.value)
			t.Setenv("MUNSU_ROLE", "soldier")
			t.Setenv("MUNSU_PARENT_STATUS", parentHome)

			cmd := newReportCmdWithInjector(deterministicInjectFn(afk.OutcomeEndpointDead, "", "backend dead"))
			root := &cobra.Command{Use: "munsu"}
			root.AddCommand(cmd)
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)

			root.SetArgs([]string{"report", "--ring", "ring", "--output", "json", state.value, state.value + " message"})
			err := root.Execute()
			if err != nil {
				t.Fatalf("%s: expected success, got: %v", state.name, err)
			}

			// Verify durable writes
			receiptPath := turnend.ReceiptPath(parentHome, "test-state-"+state.value, "default")
			if _, err := os.Stat(receiptPath); err != nil {
				t.Errorf("%s: receipt should exist: %v", state.name, err)
			}

			statusPath := filepath.Join(homeDir, "state", "test-state-"+state.value+".status")
			if _, err := os.Stat(statusPath); err != nil {
				t.Errorf("%s: status should exist: %v", state.name, err)
			}

			// Verify contract shows injection failure details
			var resp contract.Response[contract.MessageResult]
			if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
				t.Fatalf("%s: unmarshal response: %v", state.name, err)
			}
			if resp.Data.Injection == nil {
				t.Fatalf("%s: injection field should be present", state.name)
			}
			if resp.Data.Injection.Outcome != string(afk.OutcomeEndpointDead) {
				t.Errorf("%s: injection outcome = %q, want %q", state.name, resp.Data.Injection.Outcome, afk.OutcomeEndpointDead)
			}
		})
	}
}

// TestReportCmd_SubmitPromptAcknowledgment_Distinct tests that SubmitPrompt
// acknowledgment (PromptSubmitted) is distinct from envelope/receipt processing
// and task completion: the durable Captain receipt is not acked just because
// the prompt was submitted to the pane.
func TestReportCmd_SubmitPromptAcknowledgment_Distinct(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	// Install config/general-pane on parentHome so target resolution succeeds.
	configDir := filepath.Join(parentHome, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir parent config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("captain-session:captain-pane\n"), 0644); err != nil {
		t.Fatalf("write parent general-pane: %v", err)
	}

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-ack-distinct")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	// Use a real injectFn but with a fake resolver that returns PromptSubmitted
	cmd := newReportCmdWithInjector(func(role, hd, ph, tid, msg, state string, sid uint64) afk.InjectResult {
		return wakedelivery.InjectToParentPaneWithResolver(role, hd, ph, tid, msg, state, sid, fakeResolver(
			&fakeInjectBackend{
				captureResult: "\u276F \n",
				promptResult:  session.PromptResult{Status: session.PromptSubmitted},
			},
		))
	})

	root := &cobra.Command{Use: "munsu"}
	root.AddCommand(cmd)
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "ring", "--output", "json", "done", "task complete"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	// Durable receipt exists but is NOT acked — PromptSubmitted to the pane
	// is NOT equivalent to envelope/receipt processing or task completion.
	receiptPath := turnend.ReceiptPath(parentHome, "test-ack-distinct", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist: %v", err)
	}
	if turnend.IsReceiptAcked(parentHome, "test-ack-distinct", "default") {
		t.Error("receipt should NOT be acked just because prompt was submitted to the pane")
	}

	// Verify contract shows injection succeeded
	var resp contract.Response[contract.MessageResult]
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.Injection == nil {
		t.Fatal("injection should be present")
	}
	if resp.Data.Injection.Outcome != string(afk.OutcomeInjected) {
		t.Errorf("injection outcome = %q, want %q", resp.Data.Injection.Outcome, afk.OutcomeInjected)
	}
}

// TestReportCmd_NoRing_SkipsInjection verifies that --ring no-ring produces
// no injection result in the contract output.
func TestReportCmd_NoRing_SkipsInjection(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "test-no-ring")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"report", "--ring", "no-ring", "--output", "json", "done", "task complete"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	var resp contract.Response[contract.MessageResult]
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.Injection != nil {
		t.Errorf("injection should be nil with --ring no-ring, got %+v", resp.Data.Injection)
	}

	// Durable writes should still exist
	receiptPath := turnend.ReceiptPath(parentHome, "test-no-ring", "default")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Errorf("receipt should exist: %v", err)
	}
}

// TestInjectToParentPaneWithResolver_SoldierMissingParent_NoPanicOnEmptyPaths
// ensures that a soldier with empty parentHome, homeDir, or other empty string
// paths does not panic and returns typed endpoint-dead.
func TestInjectToParentPaneWithResolver_SoldierMissingParent_NoPanicOnEmptyPaths(t *testing.T) {
	// Both empty — worst case
	result := wakedelivery.InjectToParentPaneWithResolver("soldier", "", "", "task-1", "done: PR", "done", 1500,
		func(_ string, _ string) (session.Backend, string, error) {
			return nil, "", errors.New("no backend")
		})

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
	// Should mention MUNSU_PARENT_STATUS in the error, not panic
	if !strings.Contains(result.Error, "MUNSU_PARENT_STATUS") {
		t.Errorf("error = %q, should mention MUNSU_PARENT_STATUS", result.Error)
	}
}

// TestInjectToParentPaneWithResolver_InvalidTargetSource verifies that an
// unsupported target source yields endpoint-dead.
func TestInjectToParentPaneWithResolver_InvalidTargetSource(t *testing.T) {
	// Home with config/general-pane pointing to empty string
	homeDir := t.TempDir()
	parentHome := t.TempDir()
	configDir := filepath.Join(parentHome, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "general-pane"), []byte(" \n"), 0644) // whitespace-only = empty

	fake := &fakeInjectBackend{
		captureResult: "\u276F \n",
		promptResult:  session.PromptResult{Status: session.PromptSubmitted},
	}

	result := wakedelivery.InjectToParentPaneWithResolver("soldier", homeDir, parentHome, "task-1", "done: PR", "done", 1600, fakeResolver(fake))

	if result.Outcome != afk.OutcomeEndpointDead {
		t.Errorf("outcome = %q, want %q", result.Outcome, afk.OutcomeEndpointDead)
	}
}
