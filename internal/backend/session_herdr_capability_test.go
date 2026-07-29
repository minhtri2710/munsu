//go:build integration

// Package session tests
package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeHerdrWithSchema creates a fake herdr binary that responds to
// `api schema --json` with the given schema JSON and `--version` with the
// given version string.
func writeFakeHerdrWithSchema(t *testing.T, dir, version, schemaJSON string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")

	// Escape schema for embedding in a bash heredoc.
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "` + version + `"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  cat <<'SCHEMA_EOF'` + "\n" +
		schemaJSON + "\n" +
		"SCHEMA_EOF\n" +
		"  exit 0\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"

	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFakeHerdrFailingSchema creates a fake herdr that always fails on schema.
func writeFakeHerdrFailingSchema(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "herdr 0.7.5"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  >&2 echo 'schema error: failed to load'` + "\n" +
		"  exit 1\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"

	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFakeHerdrWithMalformedSchema creates a fake herdr that returns invalid JSON.
func writeFakeHerdrWithMalformedSchema(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "herdr 0.7.5"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  echo 'not valid json {{{'` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"

	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

const schemaV074 = `{"protocol":16,"schema_version":1,"$schema":"https://json-schema.org/draft/2020-12/schema","schemas":{"error_response":{"$defs":{"ErrorBody":{"properties":{"code":{"type":"string"},"message":{"type":"string"}},"required":["code","message"],"type":"object"}},"properties":{"error":{"$ref":"#/schemas/error_response/$defs/ErrorBody"},"id":{"type":"string"}},"required":["id","error"],"type":"object"},"event":{},"request":{},"subscription_event":{},"success_response":{}}}`

const schemaV075 = `{"protocol":17,"schema_version":1,"$schema":"https://json-schema.org/draft/2020-12/schema","schemas":{"error_response":{"$defs":{"ErrorBody":{"properties":{"code":{"type":"string"},"message":{"type":"string"}},"required":["code","message"],"type":"object"}},"properties":{"error":{"$ref":"#/schemas/error_response/$defs/ErrorBody"},"id":{"type":"string"}},"required":["id","error"],"type":"object"},"event":{},"request":{},"subscription_event":{},"success_response":{}}}`

const schemaVUnknown = `{"protocol":99,"schema_version":2,"$schema":"https://json-schema.org/draft/2020-12/schema","schemas":{}}`

func TestProbeHerdrCapability_Absent(t *testing.T) {
	// Empty PATH — herdr not found.
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")

	info := ProbeHerdrCapability("")
	if info.State != HerdrAbsent {
		t.Errorf("State = %q, want %q", info.State, HerdrAbsent)
	}
	if info.CLIPath != "" {
		t.Errorf("CLIPath = %q, want empty", info.CLIPath)
	}
	_ = oldPath
}

func TestProbeHerdrCapability_ReadyV074(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrWithSchema(t, tmp, "herdr 0.7.4", schemaV074)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	info := ProbeHerdrCapability("")
	if info.State != HerdrReady {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrReady, info.Err)
	}
	if info.Protocol != 16 {
		t.Errorf("Protocol = %d, want 16", info.Protocol)
	}
	if info.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", info.SchemaVersion)
	}
	if !strings.Contains(info.CLIVersion, "0.7.4") {
		t.Errorf("CLIVersion = %q, want containing 0.7.4", info.CLIVersion)
	}
	if !info.Flags.Has(CapPaneWaitOutput) {
		t.Errorf("CapPaneWaitOutput should be true for protocol 16")
	}
	if info.Flags.Has(CapAgentFacade) {
		t.Errorf("CapAgentFacade should be false for protocol 16")
	}
	if info.Flags.Has(CapAgentWait) {
		t.Errorf("CapAgentWait should be false for protocol 16")
	}
	_ = oldPath
}

func TestProbeHerdrCapability_ReadyV075(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrWithSchema(t, tmp, "herdr 0.7.5", schemaV075)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	info := ProbeHerdrCapability("")
	if info.State != HerdrReady {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrReady, info.Err)
	}
	if info.Protocol != 17 {
		t.Errorf("Protocol = %d, want 17", info.Protocol)
	}
	if !info.Flags.Has(CapPaneWaitOutput) {
		t.Errorf("CapPaneWaitOutput should be true for protocol 17")
	}
	if !info.Flags.Has(CapAgentFacade) {
		t.Errorf("CapAgentFacade should be true for protocol 17")
	}
	if !info.Flags.Has(CapAgentWait) {
		t.Errorf("CapAgentWait should be true for protocol 17")
	}
}

func TestProbeHerdrCapability_UnsupportedProtocol(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrWithSchema(t, tmp, "herdr 0.99.0", schemaVUnknown)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	info := ProbeHerdrCapability("")
	if info.State != HerdrUnsupported {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrUnsupported, info.Err)
	}
	if info.Protocol != 99 {
		t.Errorf("Protocol = %d, want 99", info.Protocol)
	}
	if info.Err == "" {
		t.Error("Err should be non-empty for unsupported protocol")
	}
}

func TestProbeHerdrCapability_FailedSchema(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrFailingSchema(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	info := ProbeHerdrCapability("")
	if info.State != HerdrFailed {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrFailed, info.Err)
	}
}

func TestProbeHerdrCapability_MalformedSchema(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrWithMalformedSchema(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	info := ProbeHerdrCapability("")
	if info.State != HerdrFailed {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrFailed, info.Err)
	}
}

func TestProbeHerdrCapability_ExplicitPath(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrWithSchema(t, tmp, "herdr 0.7.5", schemaV075)
	binPath := filepath.Join(fakePath, "herdr")

	info := ProbeHerdrCapability(binPath)
	if info.State != HerdrReady {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrReady, info.Err)
	}
	if info.CLIPath != binPath {
		t.Errorf("CLIPath = %q, want %q", info.CLIPath, binPath)
	}
}

func TestProbeHerdrCapability_ExplicitPathNotFound(t *testing.T) {
	info := ProbeHerdrCapability("/nonexistent/herdr")
	// With an explicit path, the probe attempts to run it; since the binary
	// doesn't exist, the schema command fails, producing FAILED.
	if info.State != HerdrFailed {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrFailed, info.Err)
	}
	if info.CLIPath != "/nonexistent/herdr" {
		t.Errorf("CLIPath = %q, want /nonexistent/herdr", info.CLIPath)
	}
}

// TestHerdrBackend_Capability verifies that HerdrBackend.Capability() delegates
// to ProbeHerdrCapability correctly.
func TestHerdrBackend_Capability(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrWithSchema(t, tmp, "herdr 0.7.5", schemaV075)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := NewHerdrBackend("test-s")
	info := h.Capability()
	if info.State != HerdrReady {
		t.Errorf("State = %q, want %q; err=%q", info.State, HerdrReady, info.Err)
	}
	if info.Protocol != 17 {
		t.Errorf("Protocol = %d, want 17", info.Protocol)
	}
	_ = oldPath
}

func TestParseHerdrError_PaneNotFound(t *testing.T) {
	err := parseHerdrError(execError(`{"error":{"code":"pane_not_found","message":"pane w6E:p3 not found"}}`))
	if err == nil {
		t.Fatal("parseHerdrError returned nil")
	}
	if err.Code != HerdrErrPaneNotFound {
		t.Errorf("Code = %q, want %q", err.Code, HerdrErrPaneNotFound)
	}
	if err.Message != "pane w6E:p3 not found" {
		t.Errorf("Message = %q, want %q", err.Message, "pane w6E:p3 not found")
	}
}

func TestParseHerdrError_ProtocolMismatch(t *testing.T) {
	err := parseHerdrError(execError(`{"error":{"code":"protocol_mismatch","message":"expected_protocol: 16"}}`))
	if err == nil {
		t.Fatal("parseHerdrError returned nil")
	}
	if err.Code != HerdrErrProtocolMismatch {
		t.Errorf("Code = %q, want %q", err.Code, HerdrErrProtocolMismatch)
	}
}

func TestParseHerdrError_NoJSON(t *testing.T) {
	err := parseHerdrError(execError("herdr: command not found"))
	if err != nil {
		t.Errorf("expected nil for textual error, got %v", err)
	}
}

func TestParseHerdrError_Nil(t *testing.T) {
	err := parseHerdrError(nil)
	if err != nil {
		t.Errorf("expected nil for nil input, got %v", err)
	}
}

func TestParseHerdrError_EmptyCode(t *testing.T) {
	err := parseHerdrError(execError(`{"error":{"code":"","message":"something"}}`))
	if err != nil {
		t.Errorf("expected nil for empty code, got %v", err)
	}
}

func TestParseHerdrError_WorkspaceNotFound(t *testing.T) {
	err := parseHerdrError(execError(`{"error":{"code":"workspace_not_found","message":"workspace x not found"}}`))
	if err == nil {
		t.Fatal("parseHerdrError returned nil")
	}
	if err.Code != HerdrErrWorkspaceNotFound {
		t.Errorf("Code = %q, want %q", err.Code, HerdrErrWorkspaceNotFound)
	}
}

func TestParseHerdrError_TabNotFound(t *testing.T) {
	err := parseHerdrError(execError(`{"error":{"code":"tab_not_found","message":"tab x not found"}}`))
	if err == nil {
		t.Fatal("parseHerdrError returned nil")
	}
	if err.Code != HerdrErrTabNotFound {
		t.Errorf("Code = %q, want %q", err.Code, HerdrErrTabNotFound)
	}
}

func TestIsHerdrProtocolMismatch_Structured(t *testing.T) {
	if !isHerdrProtocolMismatch(execError(`{"error":{"code":"protocol_mismatch","message":"expected_protocol: 16"}}`)) {
		t.Error("isHerdrProtocolMismatch should detect structured protocol_mismatch")
	}
}

func TestIsHerdrProtocolMismatch_NotMismatch(t *testing.T) {
	if isHerdrProtocolMismatch(execError(`{"error":{"code":"pane_not_found","message":"pane not found"}}`)) {
		t.Error("isHerdrProtocolMismatch should return false for pane_not_found")
	}
}

func TestIsHerdrProtocolMismatch_Nil(t *testing.T) {
	if isHerdrProtocolMismatch(nil) {
		t.Error("isHerdrProtocolMismatch should return false for nil")
	}
}

func TestIsNotFoundErr_StructuredCodes(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{execError(`{"error":{"code":"pane_not_found","message":"pane x not found"}}`), true},
		{execError(`{"error":{"code":"workspace_not_found","message":"ws not found"}}`), true},
		{execError(`{"error":{"code":"tab_not_found","message":"tab not found"}}`), true},
		{execError(`{"error":{"code":"protocol_mismatch","message":"expected 16"}}`), false},
		{execError(`{"error":{"code":"internal_error","message":"something broke"}}`), false},
	}
	for _, tt := range tests {
		result := isNotFoundErr(tt.err)
		if result != tt.expected {
			t.Errorf("isNotFoundErr(%v) = %v, want %v", tt.err, result, tt.expected)
		}
	}
}

func TestIsNotFoundErr_LegacyTextFallback(t *testing.T) {
	// Non-JSON textual error should still match via legacy fallback.
	err := execError("pane not found: something")
	if !isNotFoundErr(err) {
		t.Error("isNotFoundErr should match textual 'not found' as legacy fallback")
	}
}

func TestIsNotFoundErr_Nil(t *testing.T) {
	if isNotFoundErr(nil) {
		t.Error("isNotFoundErr should return false for nil")
	}
}

func TestParseHerdrProtocolMismatch_ExtractsExpected(t *testing.T) {
	expected := parseHerdrProtocolMismatch(execError(`{"error":{"code":"protocol_mismatch","message":"expected_protocol: 16"}}`))
	if expected != 16 {
		t.Errorf("expected = %d, want 16", expected)
	}
}

func TestParseHerdrProtocolMismatch_NonMismatch(t *testing.T) {
	expected := parseHerdrProtocolMismatch(execError(`{"error":{"code":"pane_not_found","message":"not found"}}`))
	if expected != 0 {
		t.Errorf("expected = %d, want 0", expected)
	}
}

func TestParseHerdrProtocolMismatch_Nil(t *testing.T) {
	expected := parseHerdrProtocolMismatch(nil)
	if expected != 0 {
		t.Errorf("expected = %d, want 0", expected)
	}
}

func TestHerdrCLIError_Error(t *testing.T) {
	e := &HerdrCLIError{Code: "test_code", Message: "test message"}
	s := e.Error()
	if !strings.Contains(s, "test_code") || !strings.Contains(s, "test message") {
		t.Errorf("Error() = %q, want both code and message", s)
	}

	e2 := &HerdrCLIError{Code: "code_only"}
	s2 := e2.Error()
	if !strings.Contains(s2, "code_only") {
		t.Errorf("Error() = %q, want code_only", s2)
	}
}

func TestHerdrCapabilityFlags_Has(t *testing.T) {
	var nilFlags HerdrCapabilityFlags
	if nilFlags.Has(CapAgentFacade) {
		t.Error("nil flags should return false")
	}

	flags := HerdrCapabilityFlags{CapAgentFacade: true}
	if !flags.Has(CapAgentFacade) {
		t.Error("flags with CapAgentFacade=true should return true")
	}
	if flags.Has(CapAgentWait) {
		t.Error("flags without CapAgentWait should return false")
	}
}

// execError wraps a string as a simulated exec.ExitError stderr output
// for testing parseHerdrError.
func execError(stderr string) error {
	return fakeExecError{msg: stderr}
}

// fakeExecError simulates the error message that exec.ExitError would produce.
type fakeExecError struct {
	msg string
}

func (e fakeExecError) Error() string {
	return e.msg
}
