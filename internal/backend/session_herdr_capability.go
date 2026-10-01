// Package session provides the session backend interface and resolution.
package backend

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
	if ver, _, err := runBackendCommand(bin, []string{"--version"}, "", nil); err == nil {
		info.CLIVersion = strings.TrimSpace(string(ver))
	}

	// Run herdr api schema --json.
	// This command does not require an active session — it emits the bundled
	// schema document the server uses.
	out, stderr, err := runBackendCommand(bin, []string{"api", "schema", "--json"}, "", nil)
	if err != nil {
		info.State = HerdrFailed
		detail := commandOutput(out, stderr)
		if detail == "" {
			detail = err.Error()
		}
		info.Err = fmt.Sprintf("schema probe failed: %s", detail)
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

// herdrErrorEnvelope is the {"error":{"code","message"}} body herdr prints
// when a CLI call fails.
type herdrErrorEnvelope struct {
	Error *HerdrCLIError `json:"error,omitempty"`
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

// parseHerdrError extracts the typed HerdrCLIError from a herdr JSON error
// envelope embedded in err's text, whatever the key order ({"error":...,"id":...}
// and {"id":...,"error":...} both parse). It returns nil when no envelope with
// a code is present; callers must treat that as an unknown failure, never as a
// classified one.
func parseHerdrError(err error) *HerdrCLIError {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for i := 0; i < len(msg); {
		j := strings.IndexByte(msg[i:], '{')
		if j < 0 {
			return nil
		}
		i += j
		dec := json.NewDecoder(strings.NewReader(msg[i:]))
		var envelope herdrErrorEnvelope
		if dec.Decode(&envelope) != nil {
			i++
			continue
		}
		if envelope.Error != nil && envelope.Error.Code != "" {
			return envelope.Error
		}
		i += int(dec.InputOffset())
	}
	return nil
}

// isHerdrProtocolMismatch returns true if the error indicates a protocol_mismatch.
func isHerdrProtocolMismatch(err error) bool {
	if err == nil {
		return false
	}
	herr := parseHerdrError(err)
	return herr != nil && herr.Code == HerdrErrProtocolMismatch
}
