package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// StepState is the outcome of one recovery step.
type StepState string

const (
	StepOk      StepState = "ok"
	StepFailed  StepState = "failed"
	StepSkipped StepState = "skipped"
)

// StepResult describes one step's outcome.
type StepResult struct {
	Name        string             `json:"name"`
	State       StepState          `json:"state"`
	Detail      string             `json:"detail"`
	Diagnostics []ConfigDiagnostic `json:"diagnostics,omitempty"`
}

// ConfigDiagnostic describes the result of checking one config item.
type ConfigDiagnostic struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Required bool   `json:"required"`
	Present  bool   `json:"present"`
}

// RecoverTransaction sequences a structured captain recovery for one captain.
// Each step runs in order and produces a StepResult with ok/failed/skipped,
// so partial failures never block the entire recovery.
type RecoverTransaction struct{ Capabilities RecoverCapabilities }

// Recover runs the full recovery transaction for a single captain.
func (tx *RecoverTransaction) Recover(parentHome string, sm Info) *RecoverResult {
	res := &RecoverResult{}

	// Step a: provenance check
	res.Steps = append(res.Steps, tx.stepProvenance(sm))

	// If provenance failed, all subsequent steps are skipped.
	if res.Steps[0].State == StepFailed {
		res.Steps = append(res.Steps,
			StepResult{Name: "config-validation", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "integration-status", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "charter-refresh", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "config-push", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "launch-readiness", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "relaunch-pane", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "watcher-ensure", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "legacy-guard", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "terminal-reconcile", State: StepSkipped, Detail: "skipped: provenance failed"},
			StepResult{Name: "nudge-retry", State: StepSkipped, Detail: "skipped: provenance failed"},
		)
		return res
	}

	// Step b: config validation (parent-home registry + required/optional)
	b := tx.stepConfigValidation(parentHome, sm)
	res.Steps = append(res.Steps, b)
	configOk := b.State == StepOk

	// Step c: integration status
	integration := tx.stepIntegrationStatus(parentHome, sm)
	res.Steps = append(res.Steps, integration)
	integrationAllowsRelaunch := integration.State != StepFailed

	// Step c2: charter refresh — re-generate .captain-charter.md idempotently
	res.Steps = append(res.Steps, tx.stepCharterRefresh(parentHome, sm))

	// Step c3: config inheritance push — ensures config/parent-home and other
	// inheritable config are up to date from the authoritative General home.
	// This must run before launch-readiness and watcher-ensure so those steps
	// see the latest parent-home contract.
	res.Steps = append(res.Steps, tx.stepConfigPush(parentHome, sm, configOk))

	// Step d: launch readiness
	res.Steps = append(res.Steps, tx.stepLaunchReadiness(parentHome, sm))

	// Step e: relaunch pane if needed
	if integrationAllowsRelaunch {
		res.Steps = append(res.Steps, tx.stepRelaunch(parentHome, sm))
	} else {
		res.Steps = append(res.Steps, StepResult{Name: "relaunch-pane", State: StepSkipped, Detail: "skipped: canonical integration is not healthy"})
	}

	// Step f: watcher ensure — only when config is OK
	res.Steps = append(res.Steps, tx.stepWatcherEnsure(sm, configOk))

	// Step g: stale legacy transport guard — only when config is OK
	res.Steps = append(res.Steps, tx.stepLegacyGuard(parentHome, sm, configOk))

	// Step h: terminal receipt reconciliation — relay pending soldier
	// terminal reports to General. Runs only when config is OK.
	res.Steps = append(res.Steps, tx.stepTerminalReconcile(parentHome, sm, configOk))

	// Step i: nudge retry — only when config is OK
	res.Steps = append(res.Steps, tx.stepNudgeRetry(parentHome, sm, configOk))

	return res
}

func (tx *RecoverTransaction) stepProvenance(sm Info) StepResult {
	if sm.Home == "" {
		return StepResult{Name: "provenance", State: StepFailed, Detail: "missing home path"}
	}
	markerID, err := ValidateProvenance(sm.Home)
	if err != nil {
		return StepResult{Name: "provenance", State: StepFailed, Detail: err.Error()}
	}
	if markerID != sm.ID {
		return StepResult{Name: "provenance", State: StepFailed,
			Detail: fmt.Sprintf("marker id %q does not match registry id %q", markerID, sm.ID)}
	}
	return StepResult{Name: "provenance", State: StepOk, Detail: fmt.Sprintf("valid provenance for %s", markerID)}
}

func (tx *RecoverTransaction) stepConfigValidation(parentHome string, sm Info) StepResult {
	// Check key config files. Registry is checked from the parent (General) home, not the captain home.
	// All items are optional — harness.Captain() handles the full fallback chain (captain-harness →
	// soldier-harness → Detect()). Missing items produce diagnostics but don't block watcher/outbox.
	checks := []struct {
		path string
		desc string
	}{
		{filepath.Join(sm.Home, "config", "captain-harness"), "captain-harness config"},
		{filepath.Join(sm.Home, "config", "soldier-harness"), "soldier-harness config"},
		{filepath.Join(parentHome, "state", "fleet-registry", "captains.json"), "fleet captain registry"},
	}
	var diags []ConfigDiagnostic
	for _, c := range checks {
		_, err := os.Stat(c.path)
		diags = append(diags, ConfigDiagnostic{
			Name:     c.desc,
			Path:     c.path,
			Required: false,
			Present:  err == nil,
		})
	}
	var missing []string
	for _, d := range diags {
		if !d.Present {
			missing = append(missing, d.Name)
		}
	}
	detail := "all config files present"
	if len(missing) > 0 {
		detail = fmt.Sprintf("missing (optional): %s", strings.Join(missing, ", "))
	}
	return StepResult{
		Name:        "config-validation",
		State:       StepOk,
		Detail:      detail,
		Diagnostics: diags,
	}
}

func (tx *RecoverTransaction) stepIntegrationStatus(parentHome string, sm Info) StepResult {
	h, err := harness.Captain(parentHome)
	if err != nil || h == "" {
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("cannot resolve harness: %v", err)}
	}
	if h != harness.Pi {
		return StepResult{Name: "integration-status", State: StepSkipped, Detail: fmt.Sprintf("canonical Pi integration not required for %s", h)}
	}
	if tx.Capabilities.Integration == nil {
		return StepResult{Name: "integration-status", State: StepFailed, Detail: "canonical Pi integration status capability is required"}
	}
	result, err := tx.Capabilities.Integration.Status(sm.Home, h)
	if err != nil {
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("integration check error: %v", err)}
	}
	switch result.State {
	case "installed":
		return StepResult{Name: "integration-status", State: StepOk,
			Detail: fmt.Sprintf("integrated with %s (%s)", result.Harness, result.Scope)}
	case "absent":
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("integration absent for %s; repair with: munsu integrate repair --harness %s --scope project", result.Harness, result.Harness)}
	case "drifted":
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("integration drifted for %s: %s; repair with: munsu integrate repair --harness %s --scope project", result.Harness, result.Message, result.Harness)}
	default:
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("integration state %q", result.State)}
	}
}

func (tx *RecoverTransaction) stepCharterRefresh(parentHome string, sm Info) StepResult {
	if err := RefreshCharter(sm.Home, parentHome); err != nil {
		return StepResult{Name: "charter-refresh", State: StepFailed,
			Detail: fmt.Sprintf("charter refresh failed: %v", err)}
	}
	return StepResult{Name: "charter-refresh", State: StepOk,
		Detail: "versioned .captain-charter.md refreshed"}
}

// stepConfigPush pushes inheritable config from the General home to the
// captain, including config/parent-home. This ensures every recovery path
// (including state-only alive captains) picks up the authoritative General
// home reference for watcher relay and terminal receipt routing.
// Uses PropagateConfig with a noop sender; notification is deferred for
// converge to retry when the captain is alive.
func (tx *RecoverTransaction) stepConfigPush(parentHome string, sm Info, configOk bool) StepResult {
	if !configOk {
		return StepResult{Name: "config-push", State: StepSkipped,
			Detail: "skipped: config validation failed"}
	}
	if _, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parentHome,
		CaptainHome: sm.Home,
		Mailbox:     &noopBoundSender{},
	}); err != nil {
		return StepResult{Name: "config-push", State: StepFailed,
			Detail: fmt.Sprintf("config-push failed: %v", err)}
	}
	return StepResult{Name: "config-push", State: StepOk, Detail: "inheritable config pushed including parent-home"}
}

func (tx *RecoverTransaction) stepLaunchReadiness(parentHome string, sm Info) StepResult {
	profile, err := harness.CaptainProfileFromHome(parentHome)
	if err != nil {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("cannot resolve captain profile: %v", err)}
	}
	h := profile.Harness
	if h == "" {
		h, err = harness.Captain(sm.Home)
	}
	if err != nil || h == "" {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("cannot resolve harness: %v", err)}
	}
	a, ok := harness.GetAdapter(h)
	if !ok {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("harness %q not in adapter registry", h)}
	}
	// Check harness binary on PATH.
	binPath, err := captainLookPath(a.Name)
	if err != nil {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("harness binary %q not found on PATH: %v", a.Name, err)}
	}
	// Share the soldier/captain launch validation seam: a denied model — or an
	// unresolved identity under an active policy — makes the captain not
	// launchable (a runtime default cannot bypass the policy).
	if err := harness.CheckModelAllowed(parentHome, h, profile.Model); err != nil {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("harness %q ready at %s but model allowlist denies %s:%s: %v", h, binPath, h, profile.Model, err)}
	}
	if profile.Model == "" {
		return StepResult{Name: "launch-readiness", State: StepOk,
			Detail: fmt.Sprintf("harness %q ready at %s (no model override)", h, binPath)}
	}
	return StepResult{Name: "launch-readiness", State: StepOk,
		Detail: fmt.Sprintf("harness %q ready at %s (model %q configured)", h, binPath, profile.Model)}
}

func (tx *RecoverTransaction) stepRelaunch(parentHome string, sm Info) StepResult {
	alive, aliveErr := checkAliveWithProbe(parentHome, sm, tx.Capabilities.Probe)
	if aliveErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: fmt.Sprintf("alive check failed: %v", aliveErr)}
	}
	if alive {
		return StepResult{Name: "relaunch-pane", State: StepOk, Detail: "endpoint alive, no action needed"}
	}

	// Not alive. Distinguish launched-but-dead from seeded-never-launched.
	taskID := taskIDForCaptain(sm.ID)
	meta, mErr := mhome.ReadMeta(parentHome, taskID)
	launched := false
	if mErr == nil && meta["kind"] == "captain" && meta["sm_id"] == sm.ID && meta["window"] != "" {
		launched = true
	}
	if !launched {
		return StepResult{Name: "relaunch-pane", State: StepSkipped,
			Detail: "seeded but not launched"}
	}

	// Launched-but-dead: relaunch.
	if lErr := Launch(sm.Home, parentHome, tx.Capabilities.Launch); lErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: fmt.Sprintf("relaunch failed: %v", lErr)}
	}
	return StepResult{Name: "relaunch-pane", State: StepOk, Detail: "relaunched successfully"}
}

func (tx *RecoverTransaction) stepWatcherEnsure(sm Info, configOk bool) StepResult {
	if !configOk {
		return StepResult{Name: "watcher-ensure", State: StepSkipped,
			Detail: "skipped: config validation failed"}
	}

	hasChildWork := inFlightSoldierPath(sm.Home)
	if tx.Capabilities.Watcher == nil {
		return StepResult{Name: "watcher-ensure", State: StepFailed, Detail: "captain watcher capability is required"}
	}
	status := tx.Capabilities.Watcher.Status(sm.Home)

	if hasChildWork {
		if status == WatcherRunning {
			return StepResult{Name: "watcher-ensure", State: StepOk,
				Detail: "watcher already running (child work in flight)"}
		}
		if err := tx.Capabilities.Watcher.Ensure(sm.Home, true); err != nil {
			return StepResult{Name: "watcher-ensure", State: StepFailed,
				Detail: fmt.Sprintf("starting watcher: %v", err)}
		}
		return StepResult{Name: "watcher-ensure", State: StepOk,
			Detail: "watcher started (child work in flight)"}
	}

	// No child work — idle policy: stop watcher if running.
	if status == WatcherRunning {
		if err := tx.Capabilities.Watcher.Ensure(sm.Home, false); err != nil {
			return StepResult{Name: "watcher-ensure", State: StepFailed,
				Detail: fmt.Sprintf("stopping watcher: %v", err)}
		}
		return StepResult{Name: "watcher-ensure", State: StepOk,
			Detail: "watcher stopped (no child work)"}
	}

	return StepResult{Name: "watcher-ensure", State: StepOk,
		Detail: "watcher not needed (no child work)"}
}

func (tx *RecoverTransaction) stepLegacyGuard(parentHome string, sm Info, configOk bool) StepResult {
	if !configOk {
		return StepResult{Name: "legacy-guard", State: StepSkipped,
			Detail: "skipped: config validation failed"}
	}
	if err := checkStaleLegacyRecords(parentHome, sm.ID); err != nil {
		return StepResult{Name: "legacy-guard", State: StepFailed,
			Detail: err.Error()}
	}
	return StepResult{Name: "legacy-guard", State: StepOk, Detail: "no stale legacy records"}
}

func (tx *RecoverTransaction) stepTerminalReconcile(parentHome string, sm Info, configOk bool) StepResult {
	if !configOk {
		return StepResult{Name: "terminal-reconcile", State: StepSkipped, Detail: "skipped: config validation failed"}
	}
	if tx.Capabilities.Continuity == nil {
		return StepResult{Name: "terminal-reconcile", State: StepFailed, Detail: "captain continuity capability is required"}
	}
	result, err := tx.Capabilities.Continuity.ReconcileTerminal(parentHome, CaptainEndpoint{ID: sm.ID, Home: sm.Home, Scope: sm.Scope, Project: sm.Project})
	if err != nil {
		return StepResult{Name: "terminal-reconcile", State: StepFailed, Detail: err.Error()}
	}
	if result.Relayed > 0 {
		return StepResult{Name: "terminal-reconcile", State: StepOk, Detail: fmt.Sprintf("relayed %d receipt(s) to General", result.Relayed)}
	}
	return StepResult{Name: "terminal-reconcile", State: StepSkipped, Detail: "no pending receipts"}
}

func (tx *RecoverTransaction) stepNudgeRetry(parentHome string, sm Info, configOk bool) StepResult {
	if !configOk {
		return StepResult{Name: "nudge-retry", State: StepSkipped,
			Detail: "skipped: config validation failed"}
	}
	if err := retryNudge(parentHome, sm, tx.Capabilities.Nudge); err != nil {
		return StepResult{Name: "nudge-retry", State: StepFailed,
			Detail: err.Error()}
	}
	return StepResult{Name: "nudge-retry", State: StepOk, Detail: "nudge sent or no pending"}
}
