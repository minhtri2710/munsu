package fleet

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
)

// CapabilityAttestation is a signed snapshot of the capabilities available at
// mode resolution time. It binds project, home, harness, gate agent, executable
// identity, resolved config, capabilities, and expiry into a single record that
// is used to detect late capability loss before soldier launch.
type CapabilityAttestation struct {
	Project        string            `json:"project"`
	Home           string            `json:"home"`
	Harness        string            `json:"harness"`
	GateAgent      string            `json:"gateAgent"`
	ExecutableID   string            `json:"executableId"`
	ResolvedConfig map[string]string `json:"resolvedConfig"`
	Capabilities   []CapabilityEntry `json:"capabilities"`
	Expiry         string            `json:"expiry"`
	CreatedAt      string            `json:"createdAt"`
	RequestedMode  string            `json:"requestedMode"`
	EffectiveMode  string            `json:"effectiveMode"`
	FallbackReason string            `json:"fallbackReason,omitempty"`
	FallbackPolicy *FallbackPolicy   `json:"fallbackPolicy,omitempty"`
}

// CapabilityEntry captures a single capability's state at attestation time.
type CapabilityEntry struct {
	Name    string        `json:"name"`
	State   backend.State `json:"state"`
	Version string        `json:"version,omitempty"`
	Path    string        `json:"path,omitempty"`
	Detail  string        `json:"detail,omitempty"`
}

// FallbackPolicy authorizes a specific fallback mode when the attested
// capabilities are no longer available. This is the "exact pre-authorization"
// that allows late capability loss to transition without a parent Decision.
type FallbackPolicy struct {
	AuthorizedMode string `json:"authorizedMode"`
	Reason         string `json:"reason"`
}

// CreateCapabilityAttestation builds a point-in-time attestation snapshot
// capturing the current state of all delivery capabilities. It probes each
// capability and records the result alongside the binding fields.
//
// Parameters:
//   - project: the project name
//   - homeDir: the munsu home directory
//   - harness: the resolved soldier harness
//   - gateAgent: the gate agent exe name (e.g. "pi", "codex", "claude")
//   - requestedMode: what the user/registry/config asked for
//   - effectiveMode: what was resolved (possibly after fallback)
//   - fallbackReason: why the modes differ (empty if same)
//   - fallbackPolicy: optional pre-authorized fallback; nil = no pre-auth
func CreateCapabilityAttestation(
	project, homeDir, harness, gateAgent string,
	requestedMode, effectiveMode, fallbackReason string,
	fallbackPolicy *FallbackPolicy,
) *CapabilityAttestation {
	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour)

	// Resolve executable identity (binary path + version of the gate agent).
	execID := resolveExecutableID(gateAgent)

	// Resolved config: capture the effective mode and harness.
	resolvedConfig := map[string]string{
		"mode":    effectiveMode,
		"harness": harness,
	}

	// Probe all delivery capabilities.
	caps := probeDeliveryCapabilities()

	return &CapabilityAttestation{
		Project:        project,
		Home:           homeDir,
		Harness:        harness,
		GateAgent:      gateAgent,
		ExecutableID:   execID,
		ResolvedConfig: resolvedConfig,
		Capabilities:   caps,
		Expiry:         expiry.Format(time.RFC3339),
		CreatedAt:      now.Format(time.RFC3339),
		RequestedMode:  requestedMode,
		EffectiveMode:  effectiveMode,
		FallbackReason: fallbackReason,
		FallbackPolicy: fallbackPolicy,
	}
}

// resolveExecutableID returns the path and version of the named binary.
// Returns "binary:version" if found, or "binary:unknown" if not on PATH.
func resolveExecutableID(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return name + ":unknown"
	}
	// Try to get version from the binary.
	out, verr := exec.Command(name, "--version").Output()
	if verr != nil {
		return path + ":unknown"
	}
	ver := strings.TrimSpace(string(out))
	// Strip common version output prefixes.
	ver = strings.TrimPrefix(ver, "no-mistakes version ")
	ver = strings.TrimPrefix(ver, "v")
	if idx := strings.IndexAny(ver, " (\n"); idx > 0 {
		ver = ver[:idx]
	}
	if ver == "" {
		return path + ":unknown"
	}
	return path + ":" + ver
}

// probeDeliveryCapabilities probes all delivery-relevant capabilities and
// returns their current state as a slice of CapabilityEntry.
func probeDeliveryCapabilities() []CapabilityEntry {
	var caps []CapabilityEntry

	// no-mistakes capability probe.
	nmProbe := NoMistakesProbe()
	caps = append(caps, CapabilityEntry{
		Name:    "no-mistakes",
		State:   nmProbe.State,
		Version: nmProbe.Version,
		Path:    nmProbe.Path,
		Detail:  nmProbe.Detail,
	})

	// gh-axi capability probe.
	ghState := probeBinary("gh-axi")
	ghPath, _ := exec.LookPath("gh-axi")
	caps = append(caps, CapabilityEntry{
		Name:  "gh-axi",
		State: ghState,
		Path:  ghPath,
	})

	// gh CLI capability probe (fallback path).
	ghCliState := probeBinary("gh")
	ghCliPath, _ := exec.LookPath("gh")
	caps = append(caps, CapabilityEntry{
		Name:  "gh",
		State: ghCliState,
		Path:  ghCliPath,
	})

	// git is always required.
	gitState := probeBinary("git")
	gitPath, _ := exec.LookPath("git")
	caps = append(caps, CapabilityEntry{
		Name:  "git",
		State: gitState,
		Path:  gitPath,
	})

	return caps
}

// probeBinary checks if a binary is on PATH and reachable.
func probeBinary(name string) backend.State {
	_, err := exec.LookPath(name)
	if err != nil {
		return backend.Absent
	}
	return backend.Ready
}

// CheckCapabilityAttestation verifies that the attested capabilities are still
// valid. It re-probes each capability and compares against the attestation.
//
// Returns:
//   - changed: true if any capability state changed significantly
//   - detail: a description of what changed, empty if unchanged
func CheckCapabilityAttestation(att *CapabilityAttestation) (changed bool, detail string) {
	if att == nil {
		return true, "no attestation to check"
	}

	// Check expiry first.
	if att.Expiry != "" {
		expiry, err := time.Parse(time.RFC3339, att.Expiry)
		if err == nil && time.Now().UTC().After(expiry) {
			return true, fmt.Sprintf("attestation expired at %s", att.Expiry)
		}
	}

	// Re-probe and compare each attested capability.
	current := probeDeliveryCapabilities()
	for _, attested := range att.Capabilities {
		for _, cur := range current {
			if cur.Name != attested.Name {
				continue
			}
			// A capability that was Ready but is now Absent or Failed
			// is a significant loss.
			if attested.State == backend.Ready && (cur.State == backend.Absent || cur.State == backend.Failed) {
				detail := fmt.Sprintf("capability %q changed from %s to %s", attested.Name, attested.State, cur.State)
				if attested.Path != "" && cur.Path != attested.Path {
					detail += fmt.Sprintf(" (path: %s -> %s)", attested.Path, cur.Path)
				}
				return true, detail
			}
			break
		}
	}

	return false, ""
}

// LateCapabilityLossResult captures the outcome of a late capability loss check.
type LateCapabilityLossResult struct {
	Changed      bool   `json:"changed"`
	Detail       string `json:"detail"`
	CanProceed   bool   `json:"canProceed"`
	FallbackMode string `json:"fallbackMode,omitempty"`
	BlockReason  string `json:"blockReason,omitempty"`
}

// HandleLateCapabilityLoss checks whether a late capability loss can be
// tolerated. It preserves work and either:
//   - Proceeds with the pre-authorized fallback mode (FallbackPolicy matches)
//   - Blocks and requires a parent Decision to transition
//
// Late capability loss occurs when capabilities change between attestation
// creation (during mode resolution) and soldier launch. Work is preserved
// (worktree, brief, etc.) and the caller decides whether to proceed or block.
func HandleLateCapabilityLoss(att *CapabilityAttestation) *LateCapabilityLossResult {
	if att == nil {
		return &LateCapabilityLossResult{
			Changed:     true,
			Detail:      "no attestation to check",
			CanProceed:  false,
			BlockReason: "late capability loss: no attestation; requires pre-authorization or a parent Decision to transition",
		}
	}

	changed, detail := CheckCapabilityAttestation(att)
	if !changed {
		return &LateCapabilityLossResult{Changed: false, Detail: "", CanProceed: true}
	}

	// Capability lost. Check for pre-authorized fallback.
	if att.FallbackPolicy != nil && att.FallbackPolicy.AuthorizedMode != "" {
		return &LateCapabilityLossResult{
			Changed:      true,
			Detail:       detail,
			CanProceed:   true,
			FallbackMode: att.FallbackPolicy.AuthorizedMode,
		}
	}

	// No pre-authorization. Block and require parent Decision.
	return &LateCapabilityLossResult{
		Changed:     true,
		Detail:      detail,
		CanProceed:  false,
		BlockReason: fmt.Sprintf("late capability loss: %s; requires pre-authorization or a parent Decision to transition", detail),
	}
}
