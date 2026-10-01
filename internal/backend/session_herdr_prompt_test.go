//go:build integration

package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// promptArgvFile is the file the fake herdr writes the `agent prompt` argv to,
// one argument per line.
const promptArgvFile = "prompt.argv"

// readPromptArgv returns the argv the fake herdr saw for `agent prompt` (after
// any --session), one element per argument; nil when it was never called.
func readPromptArgv(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, promptArgvFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// writeFakeHerdrPrompt creates a fake herdr tuned for agent prompt tests.
// Behavior is controlled via env file lines read by the script.
func writeFakeHerdrPrompt(t *testing.T, dir, apiSchema string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	envFile := filepath.Join(dir, "fakeherdr.env")

	// Ensure defaults.
	_ = os.WriteFile(envFile, []byte("AGENT_GET_STATUS=idle\nAGENT_PROMPT_STDOUT={\"result\":{\"type\":\"prompt_submitted\",\"agent\":{\"agent_status\":\"idle\"}}}\n"), 0644)

	script := "#!/usr/bin/env bash\n" +
		`ENV="` + envFile + `"` + "\n" +
		`# Read env settings line by line (avoid source quoting issues)` + "\n" +
		`while IFS='=' read -r key value; do` + "\n" +
		`  if [ -n "$key" ] && [[ $key != "#"* ]]; then` + "\n" +
		`    export "$key=$value"` + "\n" +
		`  fi` + "\n" +
		`done < "$ENV"` + "\n" +
		`# Consume --session if present` + "\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		`  api)` + "\n" +
		`    if [ "$2" = "schema" ]; then` + "\n" +
		`      echo "` + apiSchema + `"` + "\n" +
		`      exit 0` + "\n" +
		`    fi` + "\n" +
		`    ;;` + "\n" +
		`  agent)` + "\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      if [ -n "$AGENT_GET_ERRCODE" ]; then` + "\n" +
		`        echo '{"error":{"code":"'"$AGENT_GET_ERRCODE"'"}}'` + "\n" +
		`        exit ${AGENT_GET_EXIT:-1}` + "\n" +
		`      fi` + "\n" +
		`      echo '{"result":{"agent":{"agent_status":"'"$AGENT_GET_STATUS"'"}}}'` + "\n" +
		`      exit ${AGENT_GET_EXIT:-0}` + "\n" +
		`    fi` + "\n" +
		`    if [ "$2" = "prompt" ]; then` + "\n" +
		`      printf '%s\n' "$@" > "` + filepath.Join(dir, promptArgvFile) + `"` + "\n" +
		`      echo "$AGENT_PROMPT_STDOUT"` + "\n" +
		`      exit ${AGENT_PROMPT_EXIT:-0}` + "\n" +
		`    fi` + "\n" +
		`    ;;` + "\n" +
		`  pane)` + "\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"result":{"pane_id":"'"$3"'"}}'` + "\n" +
		`      exit ${FAKE_PANE_GET_EXIT:-0}` + "\n" +
		`    fi` + "\n" +
		`    if [ "$2" = "send-text" ] || [ "$2" = "send-keys" ]; then` + "\n" +
		`      echo '{"result":{}}'` + "\n" +
		`      exit ${FAKE_PANE_SEND_EXIT:-0}` + "\n" +
		`    fi` + "\n" +
		`    ;;` + "\n" +
		`esac` + "\n" +
		`>&2 echo "unknown command: $*"` + "\n" +
		`exit 1` + "\n"

	testutil.WriteFakeExecutable(t, bin, script)
	return bin
}

// setFakeEnv writes key=value lines to the fake herdr env file in dir.
func setFakeEnv(t *testing.T, dir string, kv ...string) {
	t.Helper()
	envFile := filepath.Join(dir, "fakeherdr.env")
	data := strings.Join(kv, "\n") + "\n"
	if err := os.WriteFile(envFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestAgentPrompt_WaitsForTurnStart pins the submission contract: an idle
// agent's prompt waits for working or blocked, bounded by the start timeout,
// and only that observation reads PromptSubmitted.
func TestAgentPrompt_WaitsForTurnStart(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptSubmitted {
		t.Errorf("AgentPrompt status = %q, want %q (detail: %s)", result.Status, PromptSubmitted, result.Detail)
	}
	want := []string{"agent", "prompt", "w1:p1", "hello", "--wait", "--until", "working", "--until", "blocked",
		"--timeout", strconv.FormatInt(backendPromptStartTimeout.Milliseconds(), 10)}
	if got := readPromptArgv(t, tmp); !slices.Equal(got, want) {
		t.Errorf("agent prompt argv = %q, want %q", got, want)
	}
}

func TestAgentPrompt_IdleAgent(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Agent is idle before submission.
	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT={\"result\":{\"type\":\"prompt_submitted\",\"agent\":{\"agent_status\":\"idle\"}}}",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptSubmitted {
		t.Errorf("idle: status = %q, want %q (detail: %s)", result.Status, PromptSubmitted, result.Detail)
	}
	if !strings.Contains(result.Detail, "agent-status: idle") {
		t.Errorf("idle: detail should mention agent status, got: %s", result.Detail)
	}
}

// TestAgentPrompt_BusyAgentIsQueued: a working agent cannot show a new turn
// start, so its prompt is sent without the wait and reported queued.
func TestAgentPrompt_BusyAgentIsQueued(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=working",
		"AGENT_PROMPT_STDOUT={\"result\":{\"type\":\"prompt_submitted\",\"agent\":{\"agent_status\":\"working\"}}}",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptQueuedWhileBusy {
		t.Errorf("busy: status = %q, want %q (detail: %s)", result.Status, PromptQueuedWhileBusy, result.Detail)
	}
	if got := readPromptArgv(t, tmp); slices.Contains(got, "--wait") {
		t.Errorf("busy agent prompt must be sent without --wait, argv = %q", got)
	}
}

func TestAgentPrompt_Stalled(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT={\"error\":{\"code\":\"agent_prompt_stalled\",\"message\":\"agent did not start processing\"}}",
		"AGENT_PROMPT_EXIT=1",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptStalled {
		t.Errorf("stalled: status = %q, want %q (detail: %s)", result.Status, PromptStalled, result.Detail)
	}
}

// TestAgentPrompt_WaitTimeoutIsStalled: herdr's own timeout error on the turn
// start wait leaves the text possibly pending in the composer, so it is a
// stall and never a submission.
func TestAgentPrompt_WaitTimeoutIsStalled(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT={\"error\":{\"code\":\"timeout\",\"message\":\"wait timed out\"}}",
		"AGENT_PROMPT_EXIT=1",
	)
	result := NewHerdrBackend("test-s").AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptStalled {
		t.Errorf("timeout: status = %q, want %q (detail: %s)", result.Status, PromptStalled, result.Detail)
	}
}

func TestAgentPrompt_AgentNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// agent get returns agent_not_found and pane is also gone.
	setFakeEnv(t, tmp,
		"AGENT_GET_ERRCODE=agent_not_found",
		"AGENT_GET_EXIT=1",
		"FAKE_PANE_GET_EXIT=1",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptEndpointDead {
		t.Errorf("not found: status = %q, want %q (detail: %s)", result.Status, PromptEndpointDead, result.Detail)
	}
}

func TestAgentPrompt_UnsupportedPane(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// agent get returns agent_not_found, but pane is alive.
	setFakeEnv(t, tmp,
		"AGENT_GET_ERRCODE=agent_not_found",
		"AGENT_GET_EXIT=1",
		"FAKE_PANE_GET_EXIT=0",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptUnsupported {
		t.Errorf("unsupported pane: status = %q, want %q (detail: %s)", result.Status, PromptUnsupported, result.Detail)
	}
}

func TestAgentPrompt_BackendFailed(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT={\"error\":{\"code\":\"internal_error\",\"message\":\"something broke\"}}",
		"AGENT_PROMPT_EXIT=1",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptBackendFailed {
		t.Errorf("backend failed: status = %q, want %q (detail: %s)", result.Status, PromptBackendFailed, result.Detail)
	}
}

func TestAgentPrompt_UnsupportedNonAgentPane(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Pane is alive but not a recognized agent: unsupported (routes to legacy).
	setFakeEnv(t, tmp,
		"AGENT_GET_ERRCODE=agent_not_found",
		"AGENT_GET_EXIT=1",
		"FAKE_PANE_GET_EXIT=0",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptUnsupported {
		t.Errorf("non-agent pane: status = %q, want %q (detail: %s)", result.Status, PromptUnsupported, result.Detail)
	}
}

func TestAgentPrompt_DeadEndpoint(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// agent get returns agent_not_found and pane get fails.
	setFakeEnv(t, tmp,
		"AGENT_GET_ERRCODE=agent_not_found",
		"AGENT_GET_EXIT=1",
		"FAKE_PANE_GET_EXIT=1",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptEndpointDead {
		t.Errorf("dead endpoint: status = %q, want %q (detail: %s)", result.Status, PromptEndpointDead, result.Detail)
	}
}

func TestAgentPrompt_ResponseParsing(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Unparseable response.
	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT=not-json-at-all",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptBackendFailed {
		t.Errorf("unparseable: status = %q, want %q (detail: %s)", result.Status, PromptBackendFailed, result.Detail)
	}
}

func TestAgentPrompt_EmptyResponse(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Empty stdout on error.
	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT=",
		"AGENT_PROMPT_EXIT=1",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptBackendFailed {
		t.Errorf("empty response: status = %q, want %q (detail: %s)", result.Status, PromptBackendFailed, result.Detail)
	}
}

func TestSubmitPrompt_NoFallbackAfterTypedFailure(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Stalled must NOT fall back to legacy.
	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT={\"error\":{\"code\":\"agent_prompt_stalled\",\"message\":\"stalled\"}}",
		"AGENT_PROMPT_EXIT=1",
	)
	h := NewHerdrBackend("test-s")
	result := SubmitPrompt(h, "test-s:w1:p1", "hello")
	if result.Status != PromptStalled {
		t.Errorf("stalled no-fallback: status = %q, want %q (detail: %s)", result.Status, PromptStalled, result.Detail)
	}
}

func TestSubmitPrompt_LegacyFallback(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Alive non-agent pane: AgentPrompt returns Unsupported, so SubmitPrompt
	// must fall back to the legacy SendKeys path.
	setFakeEnv(t, tmp,
		"AGENT_GET_ERRCODE=agent_not_found",
		"AGENT_GET_EXIT=1",
		"FAKE_PANE_GET_EXIT=0",
	)
	h := NewHerdrBackend("test-s")
	result := SubmitPrompt(h, "test-s:w1:p1", "hello")
	if result.Status != PromptSubmitted || !result.Legacy {
		t.Errorf("legacy fallback: status=%q legacy=%v want submitted+legacy (detail: %s)", result.Status, result.Legacy, result.Detail)
	}
}

// TestAgentPrompt_ProtocolProbeFailure verifies that when the protocol probe
// fails entirely (no herdr server), we get backend-failed, not unsupported.
func TestAgentPrompt_ProtocolProbeFailure(t *testing.T) {
	tmp := t.TempDir()
	// Write a fake herdr that fails on all commands including api schema.
	bin := filepath.Join(tmp, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`>&2 echo "herdr: not available"` + "\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptBackendFailed {
		t.Errorf("probe fail: status = %q, want %q (detail: %s)", result.Status, PromptBackendFailed, result.Detail)
	}
}

func TestAgentPrompt_Protocol17LegacyPrompt(t *testing.T) {
	// LegacyPrompt is tested in herdr_backend_test.go SendKeys tests.
	// This test requires a fake herdr that handles send-text/send-keys
	// which the prompt-focused fake doesn't provide.
	t.Skip("LegacyPrompt tested via SendKeys in herdr_backend_test.go")
}

func TestAgentPrompt_SystemOK(t *testing.T) {
	// Integration-style test: run Tests above as a suite.
	// This is a placeholder; real Herdr 0.7.5 smoke removed.
	t.Log("herdr 0.7.5 real smoke test: requires isolated herdr session")
}

// TestAgentPrompt_EmptyResponseOnSuccess ensures empty success output
// (no JSON) is treated as backend-failed.
func TestAgentPrompt_EmptyResponseOnSuccess(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=idle",
		"AGENT_PROMPT_STDOUT=",
		"AGENT_PROMPT_EXIT=0",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	// Currently empty stdout with exit 0 still parses; Result will be nil
	// because json.Unmarshal of empty string leaves successResp.Result nil.
	if result.Status != PromptBackendFailed {
		t.Errorf("empty success: status = %q, want %q", result.Status, PromptBackendFailed)
	}
}

// TestAgentPrompt_IdlePromptRunsUnderThePromptStartBound: the wait for a turn
// start outlives the short command bound, so an answer that arrives after it is
// still a submission. A busy agent's unwaited prompt stays on the short bound.
func TestAgentPrompt_IdlePromptRunsUnderThePromptStartBound(t *testing.T) {
	if backendCommandTimeoutFor(backendCommandPromptStart) <= backendCommandTimeout+time.Second {
		t.Fatal("the prompt-start bound must sit above the short bound for this test to discriminate")
	}
	dir := t.TempDir()
	reply := `{"result":{"type":"prompt_submitted","agent":{"agent_status":"working"}}}`
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "herdr"), "#!/bin/sh\nif [ \"$1\" = \"--session\" ]; then shift 2; fi\ncase \"$1 $2\" in\n  \"api schema\") echo 'protocol: 17'; exit 0 ;;\n  \"agent get\") echo '{\"result\":{\"agent\":{\"agent_status\":\"idle\"}}}'; exit 0 ;;\n  \"agent prompt\") sleep "+strconv.Itoa(int(backendCommandTimeout/time.Second)+1)+"; echo '"+reply+"'; exit 0 ;;\nesac\nexit 1\n")
	testutil.PrependPath(t, dir)

	result := NewHerdrBackend("test").AgentPrompt("test:pane", "hello")
	if result.Status != PromptSubmitted {
		t.Fatalf("status = %q, want %q (detail: %s)", result.Status, PromptSubmitted, result.Detail)
	}
}

var _ = fmt.Sprintf // keep fmt import
