package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/integrate"
	"github.com/minhtri2710/munsu/internal/task"
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
	res.Steps = append(res.Steps, tx.stepIntegrationStatus(sm))

	// Step c2: charter refresh — re-generate .captain-charter.md idempotently
	res.Steps = append(res.Steps, tx.stepCharterRefresh(parentHome, sm))

	// Step c3: config inheritance push — ensures config/parent-home and other
	// inheritable config are up to date from the authoritative General home.
	// This must run before launch-readiness and watcher-ensure so those steps
	// see the latest parent-home contract.
	res.Steps = append(res.Steps, tx.stepConfigPush(parentHome, sm, configOk))

	// Step d: launch readiness
	res.Steps = append(res.Steps, tx.stepLaunchReadiness(sm))

	// Step e: relaunch pane if needed
	res.Steps = append(res.Steps, tx.stepRelaunch(parentHome, sm))

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
		{RegistryPath(parentHome), "parent captains.md registry"},
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

func (tx *RecoverTransaction) stepIntegrationStatus(sm Info) StepResult {
	h, err := harness.Captain(sm.Home)
	if err != nil || h == "" {
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("cannot resolve harness: %v", err)}
	}
	result, err := integrate.Status(sm.Home, sm.Home, h, integrate.ScopeProject)
	if err != nil {
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("integration check error: %v", err)}
	}
	switch result.State {
	case "installed":
		return StepResult{Name: "integration-status", State: StepOk,
			Detail: fmt.Sprintf("integrated with %s (%s)", result.Harness, result.Scope)}
	case "absent":
		return StepResult{Name: "integration-status", State: StepSkipped,
			Detail: fmt.Sprintf("integration absent for %s", result.Harness)}
	case "drifted":
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("integration drifted for %s: %s", result.Harness, result.Message)}
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
func (tx *RecoverTransaction) stepConfigPush(parentHome string, sm Info, configOk bool) StepResult {
	if !configOk {
		return StepResult{Name: "config-push", State: StepSkipped,
			Detail: "skipped: config validation failed"}
	}
	if err := ConfigPush(parentHome, sm.Home); err != nil {
		return StepResult{Name: "config-push", State: StepFailed,
			Detail: fmt.Sprintf("config-push failed: %v", err)}
	}
	return StepResult{Name: "config-push", State: StepOk, Detail: "inheritable config pushed including parent-home"}
}

func (tx *RecoverTransaction) stepLaunchReadiness(sm Info) StepResult {
	h, err := harness.Captain(sm.Home)
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
	binPath, err := lookPath(a.Name)
	if err != nil {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("harness binary %q not found on PATH: %v", a.Name, err)}
	}
	// Check model config.
	modelPath := filepath.Join(sm.Home, "config", "model")
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		// Model is optional — harness may have a default.
		return StepResult{Name: "launch-readiness", State: StepOk,
			Detail: fmt.Sprintf("harness %q ready at %s (no model override)", h, binPath)}
	}
	return StepResult{Name: "launch-readiness", State: StepOk,
		Detail: fmt.Sprintf("harness %q ready at %s (model configured)", h, binPath)}
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
	meta, mErr := task.ReadMeta(parentHome, taskID)
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
	status := WatcherStatusSummary(sm.Home)

	if hasChildWork {
		if status == WatcherRunning {
			return StepResult{Name: "watcher-ensure", State: StepOk,
				Detail: "watcher already running (child work in flight)"}
		}
		if err := EnsureWatcher(sm.Home, true); err != nil {
			return StepResult{Name: "watcher-ensure", State: StepFailed,
				Detail: fmt.Sprintf("starting watcher: %v", err)}
		}
		return StepResult{Name: "watcher-ensure", State: StepOk,
			Detail: "watcher started (child work in flight)"}
	}

	// No child work — idle policy: stop watcher if running.
	if status == WatcherRunning {
		if err := EnsureWatcher(sm.Home, false); err != nil {
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
		return StepResult{Name: "terminal-reconcile", State: StepSkipped,
			Detail: "skipped: config validation failed"}
	}
	result, err := ReconcileTerminalReceipts(sm.Home, parentHome)
	if err != nil {
		return StepResult{Name: "terminal-reconcile", State: StepFailed,
			Detail: err.Error()}
	}
	relayed := result.Relayed()
	if relayed > 0 {
		var diags []string
		for _, o := range result.Outcomes {
			if o.Outcome != OutcomeRelayed {
				diags = append(diags, fmt.Sprintf("%s/%s: %s (%v)", o.TaskID, o.TermKey, o.Outcome, o.Err))
			}
		}
		detail := fmt.Sprintf("relayed %d receipt(s) to General", relayed)
		if len(diags) > 0 {
			detail += "; partial failures: " + strings.Join(diags, ", ")
		}
		return StepResult{Name: "terminal-reconcile", State: StepOk, Detail: detail}
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
