// Package session provides the session backend interface and resolution.
package backend

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// HerdrCapabilityState describes the readiness of the installed herdr CLI.
type HerdrCapabilityState string

const (
	// HerdrAbsent means the herdr binary is not on PATH.
	HerdrAbsent HerdrCapabilityState = "ABSENT"
	// HerdrUnsupported means the herdr binary was found but its protocol
	// version is outside the supported range or returned a protocol_mismatch.
	HerdrUnsupported HerdrCapabilityState = "UNSUPPORTED"
	// HerdrReady means the herdr binary is on PATH, schema is valid, and
	// protocol version is in the supported range.
	HerdrReady HerdrCapabilityState = "READY"
	// HerdrFailed means herdr was found but the schema/probe command failed
	// or returned malformed JSON.
	HerdrFailed HerdrCapabilityState = "FAILED"
)

// HerdrCapabilityFlag identifies optional features the herdr version supports.
type HerdrCapabilityFlag string

const (
	// CapAgentFacade indicates the version supports agent start/prompt/send-keys/wait.
	CapAgentFacade HerdrCapabilityFlag = "agent_facade"
	// CapAgentWait indicates the version supports agent wait.
	CapAgentWait HerdrCapabilityFlag = "agent_wait"
	// CapPaneWaitOutput indicates the version supports pane wait-output.
	CapPaneWaitOutput HerdrCapabilityFlag = "pane_wait_output"
)

// HerdrCapabilityFlags is a set of capability flags.
type HerdrCapabilityFlags map[HerdrCapabilityFlag]bool

// Has returns true if the given flag is present and true.
func (f HerdrCapabilityFlags) Has(flag HerdrCapabilityFlag) bool {
	return f != nil && f[flag]
}

// CapabilityInfo carries the result of probing the installed herdr CLI.
type CapabilityInfo struct {
	State         HerdrCapabilityState `json:"state"`
	CLIPath       string               `json:"cli_path,omitempty"`
	CLIVersion    string               `json:"cli_version,omitempty"`
	Protocol      int                  `json:"protocol,omitempty"`
	SchemaVersion int                  `json:"schema_version,omitempty"`
	MinProtocol   int                  `json:"min_protocol,omitempty"`
	MaxProtocol   int                  `json:"max_protocol,omitempty"`
	Flags         HerdrCapabilityFlags `json:"flags,omitempty"`
	Err           string               `json:"err,omitempty"`
}

// SupportedProtocolRange defines the minimum and maximum protocol versions
// that the munsu backend is verified to support.
const (
	MinSupportedProtocol = 16
	MaxSupportedProtocol = 17
)

// ProbeHerdrCapability probes the installed herdr CLI and returns capability info.
// It runs herdr api schema --json to determine protocol version and capabilities.
// The probe is injectable via the cliPath parameter: use "" to resolve from PATH.
func ProbeHerdrCapability(cliPath string) CapabilityInfo {
	info := CapabilityInfo{
		Flags:       make(HerdrCapabilityFlags),
		MinProtocol: MinSupportedProtocol,
		MaxProtocol: MaxSupportedProtocol,
	}

	// Resolve herdr binary.
	bin := cliPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath("herdr")
		if err != nil {
			info.State = HerdrAbsent
			info.Err = fmt.Sprintf("herdr not found on PATH: %v", err)
			return info
		}
	}
	info.CLIPath = bin

	// Get version string.
	if ver, err := exec.Command(bin, "--version").Output(); err == nil {
		info.CLIVersion = strings.TrimSpace(string(ver))
	}

	// Run herdr api schema --json.
	// This command does not require an active session — it emits the bundled
	// schema document the server uses.
	out, err := exec.Command(bin, "api", "schema", "--json").Output()
	if err != nil {
		info.State = HerdrFailed
		info.Err = fmt.Sprintf("schema probe failed: %v", err)
		return info
	}

	// Parse schema JSON.
	var schema struct {
		Protocol      int `json:"protocol"`
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(out, &schema); err != nil {
		info.State = HerdrFailed
		info.Err = fmt.Sprintf("parsing schema JSON: %v", err)
		return info
	}

	info.Protocol = schema.Protocol
	info.SchemaVersion = schema.SchemaVersion

	// Check protocol range.
	if info.Protocol < MinSupportedProtocol || info.Protocol > MaxSupportedProtocol {
		info.State = HerdrUnsupported
		info.Err = fmt.Sprintf("protocol %d outside supported range [%d, %d]",
			info.Protocol, MinSupportedProtocol, MaxSupportedProtocol)
		return info
	}

	// Determine capability flags from protocol version.
	// agent_facade and agent_wait are available in protocol 17+ (herdr 0.7.5+).
	if info.Protocol >= 17 {
		info.Flags[CapAgentFacade] = true
		info.Flags[CapAgentWait] = true
	}
	// pane_wait_output is available in protocol 16+ (herdr 0.7.4+).
	if info.Protocol >= 16 {
		info.Flags[CapPaneWaitOutput] = true
	}

	info.State = HerdrReady
	return info
}

// HerdrCLIError represents a typed error from the herdr CLI JSON error envelope.
type HerdrCLIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *HerdrCLIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("herdr error: %s (%s)", e.Code, e.Message)
	}
	return fmt.Sprintf("herdr error: %s", e.Code)
}

// Unwrap is provided for potential future wrapping.
func (e *HerdrCLIError) Unwrap() error { return nil }

// Known Herdr CLI error codes.
const (
	HerdrErrProtocolMismatch  = "protocol_mismatch"
	HerdrErrPaneNotFound      = "pane_not_found"
	HerdrErrWorkspaceNotFound = "workspace_not_found"
	HerdrErrTabNotFound       = "tab_not_found"
	HerdrErrUnknownCommand    = "unknown_command"
	HerdrErrInternal          = "internal_error"
	HerdrErrTimeout           = "timeout"
)

// parseHerdrError attempts to parse an exec error from herdr CLI as a typed
// HerdrCLIError. Returns nil if no structured error is found (caller should
// fall back to textual matching for legacy compatibility).
func parseHerdrError(err error) *HerdrCLIError {
	if err == nil {
		return nil
	}
	// Try to extract JSON from stderr (herdr CLI outputs JSON error envelopes).
	// The typical format is: {"error":{"code":"pane_not_found","message":"..."}}
	// Textual errors are also possible for non-JSON failures.
	msg := err.Error()

	// Look for a JSON error envelope in the error string.
	// The exec.ExitError stderr is typically included in the message.
	start := strings.Index(msg, `{"error":`)
	if start < 0 {
		return nil
	}

	// Find the enclosing JSON object. Walk braces to find the end.
	braceCount := 0
	end := -1
	for i := start; i < len(msg); i++ {
		switch msg[i] {
		case '{':
			braceCount++
		case '}':
			braceCount--
			if braceCount == 0 {
				end = i + 1
				goto found
			}
		}
	}
found:
	if end <= start {
		return nil
	}

	var envelope struct {
		Error HerdrCLIError `json:"error"`
	}
	if err := json.Unmarshal([]byte(msg[start:end]), &envelope); err != nil {
		return nil
	}
	if envelope.Error.Code == "" {
		return nil
	}
	return &envelope.Error
}

// isHerdrProtocolMismatch returns true if the error indicates a protocol_mismatch.
func isHerdrProtocolMismatch(err error) bool {
	if err == nil {
		return false
	}
	if herr := parseHerdrError(err); herr != nil {
		return herr.Code == HerdrErrProtocolMismatch
	}
	return strings.Contains(err.Error(), HerdrErrProtocolMismatch)
}

// parseHerdrProtocolMismatch extracts expected_protocol from a protocol_mismatch
// error when available. Returns 0 if not parseable.
func parseHerdrProtocolMismatch(err error) int {
	if herr := parseHerdrError(err); herr != nil && herr.Code == HerdrErrProtocolMismatch {
		// The message might contain "expected_protocol: 17" or similar.
		// Extract numeric value.
		msg := herr.Message
		if idx := strings.LastIndex(msg, " "); idx >= 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(msg[idx+1:])); err == nil {
				return n
			}
		}
	}
	return 0
}
