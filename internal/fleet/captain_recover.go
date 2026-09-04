package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
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
type RecoverTransaction struct {
	Capabilities RecoverCapabilities

	now   func() time.Time
	sleep func(time.Duration)
}

const (
	relaunchProofAttempts = 20
	relaunchProofInterval = 500 * time.Millisecond
	relaunchGuardTTL      = 5 * time.Minute
)

const relaunchGuardUntilField = "relaunch_guard_until"

func (tx *RecoverTransaction) nowTime() time.Time {
	if tx.now != nil {
		return tx.now()
	}
	return time.Now()
}

func (tx *RecoverTransaction) sleepFor(d time.Duration) {
	if tx.sleep != nil {
		tx.sleep(d)
		return
	}
	time.Sleep(d)
}

// relaunchGuardDeadline resolves the persisted guard deadline. Missing,
// malformed, or implausibly distant deadlines are normalized to now+ttl so
// the guard always stays bounded; the second return reports whether the
// caller must persist the normalized deadline.
func relaunchGuardDeadline(meta map[string]string, ttl time.Duration, now time.Time) (time.Time, bool) {
	if raw := meta[relaunchGuardUntilField]; raw != "" {
		if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
			until := time.Unix(secs, 0)
			if !until.After(now.Add(ttl)) {
				return until, false
			}
		}
	}
	return now.Add(ttl), true
}

// clearRelaunchGuard removes an armed relaunch guard from the captain task
// meta when liveness has been proven by observation. A missing or unarmed
// guard is a no-op. Meta read failures are ignored; write failures are
// returned so callers can surface the persistence failure.
func clearRelaunchGuard(parentHome, taskID string) error {
	meta, err := mhome.ReadMeta(parentHome, taskID)
	if err != nil {
		return nil
	}
	if meta["relaunch_liveness"] != "unproven" {
		return nil
	}
	delete(meta, "relaunch_liveness")
	delete(meta, relaunchGuardUntilField)
	return mhome.WriteMeta(parentHome, taskID, meta)
}

// consultRelaunchGuard evaluates the persisted relaunch guard for a
// launched-but-dead captain before any relaunch. It reports whether a
// duplicate relaunch is refused because a prior relaunch's liveness is
// still unproven within the guard window, along with the remaining window.
// Malformed or implausibly distant deadlines are normalized and persisted;
// an expired guard is cleared and persisted. meta is the caller's task-meta
// snapshot and is written back through parentHome/taskID as needed.
func consultRelaunchGuard(parentHome, taskID string, meta map[string]string, now time.Time) (refused bool, remaining time.Duration, err error) {
	if meta["relaunch_liveness"] != "unproven" {
		return false, 0, nil
	}
	until, normalized := relaunchGuardDeadline(meta, relaunchGuardTTL, now)
	if normalized {
		meta[relaunchGuardUntilField] = strconv.FormatInt(until.Unix(), 10)
		if err := mhome.WriteMeta(parentHome, taskID, meta); err != nil {
			return false, 0, fmt.Errorf("normalizing relaunch guard failed: %w", err)
		}
	}
	if remaining := until.Sub(now); remaining > 0 {
		return true, remaining, nil
	}
	delete(meta, "relaunch_liveness")
	delete(meta, relaunchGuardUntilField)
	if err := mhome.WriteMeta(parentHome, taskID, meta); err != nil {
		return false, 0, fmt.Errorf("clearing expired relaunch guard failed: %w", err)
	}
	return false, 0, nil
}

// armRelaunchGuard records an unproven relaunch with a bounded deadline so
// every recovery path refuses a duplicate relaunch until the guard expires.
func armRelaunchGuard(meta map[string]string, now time.Time) {
	meta["relaunch_liveness"] = "unproven"
	meta[relaunchGuardUntilField] = strconv.FormatInt(now.Add(relaunchGuardTTL).Unix(), 10)
}

// proveRelaunch polls the captain endpoint after a relaunch until liveness
// is proven or the proof window elapses. Proven liveness clears the relaunch
// guard; an elapsed window arms the bounded guard so every recovery path
// refuses a duplicate relaunch until it expires. Probe errors are retried
// within the window and reported only if the window elapses without proven
// liveness. Returns proven=false when the window elapsed with the guard
// armed. sleep is the pause between probes and now is the clock used to set
// the armed guard deadline; both must be non-nil.
func proveRelaunch(parentHome string, sm Info, probe ProbeEndpoint, sleep func(time.Duration), now func() time.Time) (proven bool, err error) {
	taskID := taskIDForCaptain(sm.ID)
	meta, err := mhome.ReadMeta(parentHome, taskID)
	if err != nil {
		return false, fmt.Errorf("post-launch metadata read failed: %w", err)
	}
	var lastProbeErr error
	for attempt := 0; attempt < relaunchProofAttempts; attempt++ {
		state, stateErr := checkAliveWithProbe(parentHome, sm, probe)
		if stateErr != nil {
			lastProbeErr = stateErr
		} else if state == CaptainAlive {
			delete(meta, "relaunch_liveness")
			delete(meta, relaunchGuardUntilField)
			if err := mhome.WriteMeta(parentHome, taskID, meta); err != nil {
				return false, fmt.Errorf("clearing resolved relaunch guard failed: %w", err)
			}
			return true, nil
		}
		if attempt+1 < relaunchProofAttempts {
			sleep(relaunchProofInterval)
		}
	}
	armRelaunchGuard(meta, now())
	if err := mhome.WriteMeta(parentHome, taskID, meta); err != nil {
		if lastProbeErr != nil {
			return false, fmt.Errorf("post-launch liveness could not be proven: %w (recording recovery guard failed: %v)", lastProbeErr, err)
		}
		return false, fmt.Errorf("post-launch liveness could not be proven; recording recovery guard failed: %w", err)
	}
	if lastProbeErr != nil {
		return false, fmt.Errorf("post-launch liveness could not be proven; last probe error: %w", lastProbeErr)
	}
	return false, nil
}

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
	integration := tx.stepIntegrationStatus(sm)
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

	// Step h: nudge retry — only when config is OK
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
	// Check key config artifacts. The registry is checked from the parent
	// (General) home, not the captain home. All items are optional — the captain
	// resolves its harness from the published snapshot, and missing items produce
	// diagnostics but don't block watcher/outbox.
	checks := []struct {
		path string
		desc string
	}{
		{filepath.Join(sm.Home, config.PublishedSnapshotPath), "published config snapshot"},
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

func (tx *RecoverTransaction) stepIntegrationStatus(sm Info) StepResult {
	snapshot, err := config.LoadPublishedSnapshot(sm.Home)
	if err != nil {
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("cannot load captain published snapshot: %v", err)}
	}
	h, err := harness.ResolveCaptainFromSnapshot(snapshot.Config())
	if err != nil {
		return StepResult{Name: "integration-status", State: StepFailed,
			Detail: fmt.Sprintf("cannot resolve harness: %v", err)}
	}
	if tx.Capabilities.Integration == nil {
		return StepResult{Name: "integration-status", State: StepFailed, Detail: "captain integration status capability is required"}
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
	snapshot, err := config.LoadPublishedSnapshot(sm.Home)
	if err != nil {
		return StepResult{Name: "launch-readiness", State: StepFailed,
			Detail: fmt.Sprintf("cannot load captain published snapshot: %v", err)}
	}
	profile := snapshot.Config().CaptainProfile
	h, err := harness.ResolveCaptainFromSnapshot(snapshot.Config())
	if err != nil {
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
	state, stateErr := checkAliveWithProbe(parentHome, sm, tx.Capabilities.Probe)
	if stateErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: fmt.Sprintf("alive check failed: %v", stateErr)}
	}
	taskID := taskIDForCaptain(sm.ID)
	switch state {
	case CaptainAlive:
		if err := clearRelaunchGuard(parentHome, taskID); err != nil {
			return StepResult{Name: "relaunch-pane", State: StepFailed,
				Detail: fmt.Sprintf("clearing resolved relaunch guard failed: %v", err)}
		}
		return StepResult{Name: "relaunch-pane", State: StepOk, Detail: "endpoint alive, no action needed"}
	case CaptainSeeded:
		return StepResult{Name: "relaunch-pane", State: StepSkipped,
			Detail: "seeded but not launched"}
	case CaptainUnproven:
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: "endpoint evidence is not authoritatively absent; strict-dead-only refuses relaunch"}
	}

	// CaptainDead: launched-but-dead (binding already validated by
	// checkAliveWithProbe). Refuse a duplicate relaunch while the guard is
	// armed, then relaunch and prove post-launch liveness.
	meta, mErr := mhome.ReadMeta(parentHome, taskID)
	if mErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: fmt.Sprintf("re-reading task meta for relaunch guard: %v", mErr)}
	}
	refused, remaining, gErr := consultRelaunchGuard(parentHome, taskID, meta, tx.nowTime())
	if gErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: gErr.Error()}
	}
	if refused {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: fmt.Sprintf("prior relaunch liveness remains unproven; duplicate launch refused (guard expires in %s)", remaining.Round(time.Second))}
	}

	// Launched-but-dead: relaunch.
	if lErr := Launch(sm.Home, parentHome, tx.Capabilities.Launch); lErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: fmt.Sprintf("relaunch failed: %v", lErr)}
	}
	proven, pErr := proveRelaunch(parentHome, sm, tx.Capabilities.Probe, tx.sleepFor, tx.nowTime)
	if pErr != nil {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: pErr.Error()}
	}
	if !proven {
		return StepResult{Name: "relaunch-pane", State: StepFailed,
			Detail: "post-launch liveness could not be proven; duplicate relaunch guarded"}
	}
	return StepResult{Name: "relaunch-pane", State: StepOk, Detail: "relaunched successfully and liveness proven"}
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
