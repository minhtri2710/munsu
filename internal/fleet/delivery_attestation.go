package fleet

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// CapabilityAttestation is a point-in-time snapshot of the capabilities
// available at mode resolution time. It binds project, home, harness, gate
// agent, executable identity, resolved config, the configured review and forge
// steps, capabilities, and expiry into a single record that is used to detect
// late capability loss before soldier launch.
type CapabilityAttestation struct {
	Project        string                     `json:"project"`
	Home           string                     `json:"home"`
	Harness        string                     `json:"harness"`
	GateAgent      string                     `json:"gateAgent"`
	ExecutableID   string                     `json:"executableId"`
	ResolvedConfig map[string]string          `json:"resolvedConfig"`
	Capabilities   []CapabilityEntry          `json:"capabilities"`
	Expiry         time.Time                  `json:"expiry"`
	Review         taskauthority.DeliveryStep `json:"review"`
	Forge          taskauthority.DeliveryStep `json:"forge"`
}

// CapabilityEntry captures a single capability's state at attestation time.
type CapabilityEntry struct {
	Name    string        `json:"name"`
	State   backend.State `json:"state"`
	Version string        `json:"version,omitempty"`
	Path    string        `json:"path,omitempty"`
	Detail  string        `json:"detail,omitempty"`
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
//   - review, forge: the generation's captured delivery steps
func CreateCapabilityAttestation(
	project, homeDir, harness, gateAgent string,
	review, forge taskauthority.DeliveryStep,
) *CapabilityAttestation {
	now := time.Now().UTC()
	expiry := now.Add(24 * time.Hour)

	// Resolve executable identity (binary path + version of the gate agent).
	execID := resolveExecutableID(gateAgent)

	// Resolved config: capture the derived mode and harness.
	resolvedConfig := map[string]string{
		"mode":    taskauthority.DeliveryModeForSteps(review, forge),
		"harness": harness,
	}

	return &CapabilityAttestation{
		Project:        project,
		Home:           homeDir,
		Harness:        harness,
		GateAgent:      gateAgent,
		ExecutableID:   execID,
		ResolvedConfig: resolvedConfig,
		Capabilities:   probeDeliveryCapabilities(review, forge),
		Expiry:         expiry,
		Review:         review,
		Forge:          forge,
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

// probeDeliveryCapabilities probes the generation's configured review and
// forge steps and returns their current state. A baseline step has no tool to
// probe, and PATH is never searched for an adapter that has no entry.
func probeDeliveryCapabilities(review, forge taskauthority.DeliveryStep) []CapabilityEntry {
	var caps []CapabilityEntry
	if !review.Baseline {
		probe := ProbeNoMistakesTool(toolEntryOf(review))
		caps = append(caps, CapabilityEntry{
			Name:    review.Adapter,
			State:   probe.State,
			Version: probe.Version,
			Path:    probe.Path,
			Detail:  probe.Detail,
		})
	}
	if !forge.Baseline {
		caps = append(caps, CapabilityEntry{
			Name:  forge.Adapter,
			State: probeConfiguredForge(toolEntryOf(forge)),
			Path:  forge.Path,
		})
	}

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

	// Check expiry first. A zero Expiry means no expiry was recorded, and
	// time.Now().After(zero) is true, so it reads as expired and fails closed.
	if time.Now().UTC().After(att.Expiry) {
		return true, fmt.Sprintf("attestation expired at %s", att.Expiry.Format(time.RFC3339))
	}

	// Re-probe and compare each attested capability.
	current := probeDeliveryCapabilities(att.Review, att.Forge)
	for _, attested := range att.Capabilities {
		for _, cur := range current {
			if cur.Name != attested.Name {
				continue
			}
			// A capability that was Ready but is now non-Ready is a significant loss.
			if attested.State == backend.Ready && cur.State != backend.Ready {
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
	Changed     bool   `json:"changed"`
	Detail      string `json:"detail"`
	CanProceed  bool   `json:"canProceed"`
	BlockReason string `json:"blockReason,omitempty"`
}

// HandleLateCapabilityLoss checks whether a late capability loss can be
// tolerated. Any loss blocks and requires a parent Decision to transition;
// there is no pre-authorized fallback that lets a launch proceed in a mode
// whose capability is gone.
//
// Late capability loss occurs when capabilities change between attestation
// creation (during mode resolution) and soldier launch. Work is preserved
// (worktree, brief, etc.) and the caller blocks for the Decision.
func HandleLateCapabilityLoss(att *CapabilityAttestation) *LateCapabilityLossResult {
	if att == nil {
		return &LateCapabilityLossResult{
			Changed:     true,
			Detail:      "no attestation to check",
			CanProceed:  false,
			BlockReason: "late capability loss: no attestation; requires a parent Decision to transition",
		}
	}

	changed, detail := CheckCapabilityAttestation(att)
	if !changed {
		return &LateCapabilityLossResult{Changed: false, Detail: "", CanProceed: true}
	}

	// Capability lost. Block and require a parent Decision.
	return &LateCapabilityLossResult{
		Changed:     true,
		Detail:      detail,
		CanProceed:  false,
		BlockReason: fmt.Sprintf("late capability loss: %s; requires a parent Decision to transition", detail),
	}
}
