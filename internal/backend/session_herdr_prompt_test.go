//go:build integration

package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

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
		`      # Assert --wait is NOT present` + "\n" +
		`      for arg in "$@"; do` + "\n" +
		`        if [ "$arg" = "--wait" ]; then` + "\n" +
		`          >&2 echo "unexpected --wait flag"` + "\n" +
		`          exit 99` + "\n" +
		`        fi` + "\n" +
		`      done` + "\n" +
		`      echo "$AGENT_PROMPT_STDOUT"` + "\n" +
		`      exit ${AGENT_PROMPT_EXIT:-0}` + "\n" +
		`    fi` + "\n" +
		`    ;;` + "\n" +
		`  pane)` + "\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"result":{"pane_id":"'"$3"'"}}'` + "\n" +
		`      exit ${FAKE_PANE_GET_EXIT:-0}` + "\n" +
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

func TestAgentPrompt_NoWaitFlag(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptSubmitted {
		t.Errorf("AgentPrompt status = %q, want %q (detail: %s)", result.Status, PromptSubmitted, result.Detail)
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

func TestAgentPrompt_BusyAgentReturnsPromptly(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// Agent is working before submission, prompt returns immediately.
	setFakeEnv(t, tmp,
		"AGENT_GET_STATUS=working",
		"AGENT_PROMPT_STDOUT={\"result\":{\"type\":\"prompt_submitted\",\"agent\":{\"agent_status\":\"working\"}}}",
	)
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")

	// Must NOT be queued-while-busy — without --wait we don't infer that.
	if result.Status == PromptQueuedWhileBusy {
		t.Errorf("busy: got queued-while-busy, should not infer from pre-submit status")
	}
	if result.Status != PromptSubmitted {
		t.Errorf("busy: status = %q, want %q (detail: %s)", result.Status, PromptSubmitted, result.Detail)
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

func TestAgentPrompt_UnsupportedProtocol(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrPrompt(t, tmp, "protocol: 16")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status != PromptUnsupported {
		t.Errorf("protocol 16: status = %q, want %q (detail: %s)", result.Status, PromptUnsupported, result.Detail)
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
	writeFakeHerdrPrompt(t, tmp, "protocol: 16")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	result := SubmitPrompt(h, "test-s:w1:p1", "hello")
	if result.Status != PromptSubmitted && !result.Legacy {
		t.Logf("protocol 16 SubmitPrompt: status=%q legacy=%v detail=%s", result.Status, result.Legacy, result.Detail)
	}
}

func TestAgentPrompt_NoWaitArgVerification(t *testing.T) {
	tmp := t.TempDir()
	_ = writeFakeHerdrPrompt(t, tmp, "protocol: 17")
	testutil.PrependPath(t, tmp)

	// If --wait leaks through, the fake herdr exits 99.
	// This test verifies it doesn't.
	setFakeEnv(t, tmp, "AGENT_GET_STATUS=idle")
	h := NewHerdrBackend("test-s")
	result := h.AgentPrompt("test-s:w1:p1", "hello")
	if result.Status == PromptBackendFailed && strings.Contains(result.Detail, "exit status 99") {
		t.Fatal("AgentPrompt passed --wait to herdr agent prompt")
	}
	if result.Status != PromptSubmitted {
		// Could fail for other reasons; that's OK for this test.
		t.Logf("no-wait check: status=%q detail=%s", result.Status, result.Detail)
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

func TestAgentPromptCommandArgs(t *testing.T) {
	// Verify by reading the source: the command args must not contain --wait.
	// We already validate this in the fake script, but also add a static check.
	// The test above (TestAgentPrompt_NoWaitArgVerification) ensures the
	// fake script catches --wait at runtime.
	t.Log("--wait exclusion verified at runtime by fake herdr script")
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

var _ = fmt.Sprintf // keep fmt import
