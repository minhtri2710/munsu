package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// init supplies a deterministic Pi harness lookup for every recovery test.
// Recovery paths that relaunch a captain resolve the "pi" binary through the
// package-level captainLookPath seam; CI runners do not have pi on PATH, so
// without a fixture the launch never reaches the endpoint under test. Only
// "pi" is intercepted — every other binary delegates to the real lookup — so
// tests asserting absence failures for other harnesses are unaffected, and
// production lookup behavior is never touched. Per-test overrides of
// captainLookPath save/restore this wrapper and keep working unchanged.
func init() {
	lookPath := captainLookPath
	captainLookPath = func(name string) (string, error) {
		if name == "pi" {
			return "/test/bin/pi", nil
		}
		return lookPath(name)
	}
}

// --- RecoverTransaction tests ---

func TestRecoverTransaction_ProvenanceFailureSkipsAll(t *testing.T) {
	parent := t.TempDir()
	// Home dir exists but has no provenance marker.
	smHome := filepath.Join(parent, "captains", "sm-bad")
	os.MkdirAll(smHome, 0755)

	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}}}
	res := tx.Recover(parent, Info{ID: "sm-bad", Home: smHome})

	if len(res.Steps) != 11 {
		t.Fatalf("expected 11 steps, got %d", len(res.Steps))
	}

	// Provenance must fail.
	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepFailed {
		t.Errorf("provenance step expected failed, got %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}

	// All subsequent steps must be skipped.
	for i := 1; i < len(res.Steps); i++ {
		if res.Steps[i].State != StepSkipped {
			t.Errorf("step %d (%s) expected skipped, got %s", i, res.Steps[i].Name, res.Steps[i].State)
		}
	}
}

func TestRecoverTransaction_EmptyHomeFailed(t *testing.T) {
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}}}
	res := tx.Recover(t.TempDir(), Info{ID: "empty", Home: ""})

	if len(res.Steps) != 11 {
		t.Fatalf("expected 10 steps, got %d", len(res.Steps))
	}

	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepFailed {
		t.Errorf("provenance should fail for empty home: %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}
	if !strings.Contains(res.Steps[0].Detail, "missing home path") {
		t.Errorf("detail should mention missing home: %s", res.Steps[0].Detail)
	}
}

func TestRecoverTransaction_StepsString(t *testing.T) {
	res := &RecoverResult{Steps: []StepResult{
		{Name: "provenance", State: StepOk, Detail: "valid"},
		{Name: "config-validation", State: StepFailed, Detail: "missing config"},
		{Name: "relaunch-pane", State: StepSkipped, Detail: "not launched"},
	}}
	s := res.StepsString()
	if !strings.Contains(s, "provenance: ok (valid)") {
		t.Errorf("StepsString missing ok step: %s", s)
	}
	if !strings.Contains(s, "config-validation: FAILED (missing config)") {
		t.Errorf("StepsString missing failed step: %s", s)
	}
	if !strings.Contains(s, "relaunch-pane: skipped (not launched)") {
		t.Errorf("StepsString missing skipped step: %s", s)
	}
}

func TestRecoverTransaction_EmptyStepsString(t *testing.T) {
	empty := (&RecoverResult{}).StepsString()
	if empty != "no recovery steps" {
		t.Errorf("empty StepsString() = %q", empty)
	}
}

func TestRecoverIntegrationStatus_MissingCanonicalIntegrationFailsClosed(t *testing.T) {
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "absent", Message: "no integration manifest found — not installed"}}}}
	step := tx.stepIntegrationStatus(Info{Home: captainHomeWithHarness(t, "pi")})
	if step.State != StepFailed {
		t.Fatalf("state = %s, want %s", step.State, StepFailed)
	}
	if !strings.Contains(step.Detail, "integration absent") || !strings.Contains(step.Detail, "munsu integrate repair --harness pi --scope project") {
		t.Fatalf("detail = %q, want typed actionable repair", step.Detail)
	}
}

func TestRecoverIntegrationStatus_HealthyCanonicalIntegrationPermitsRelaunch(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "healthy-integration")
	writeCanonicalPiIntegration(t, home)
	writeCaptainMeta(t, parent, "healthy-integration", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{results: []CaptainProbeResult{{}, {PaneAlive: true, AgentAlive: true}}},
	}}

	result := tx.Recover(parent, Info{ID: "healthy-integration", Home: home})
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepOk || !strings.Contains(step.Detail, "relaunched") {
		t.Fatalf("relaunch step = %+v, want successful relaunch", step)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("healthy-integration"))
	if err != nil {
		t.Fatalf("read relaunched meta: %v", err)
	}
	if meta["window"] != "window" || meta["backend"] != "test" {
		t.Fatalf("relaunched meta = %#v, want fresh endpoint metadata", meta)
	}
}

func TestRecoverRelaunchFailsWhenPostLaunchLivenessIsUnproven(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "post-launch-dead")
	writeCanonicalPiIntegration(t, home)
	writeCaptainMeta(t, parent, "post-launch-dead", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{
		Capabilities: RecoverCapabilities{
			Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
			Launch:      launch,
			Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
		},
		sleep: func(time.Duration) {},
	}

	result := tx.Recover(parent, Info{ID: "post-launch-dead", Home: home})
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepFailed || !strings.Contains(step.Detail, "post-launch liveness") {
		t.Fatalf("relaunch step = %+v, want post-launch liveness failure", step)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("post-launch-dead"))
	if err != nil {
		t.Fatalf("read recovery guard meta: %v", err)
	}
	if meta["relaunch_liveness"] != "unproven" {
		t.Fatalf("relaunch_liveness = %q, want unproven", meta["relaunch_liveness"])
	}

	second := tx.Recover(parent, Info{ID: "post-launch-dead", Home: home})
	secondStep := findStep(second.Steps, "relaunch-pane")
	if launch.calls != 1 {
		t.Fatalf("second recovery launch calls = %d, want 1", launch.calls)
	}
	if secondStep == nil || secondStep.State != StepFailed || !strings.Contains(secondStep.Detail, "duplicate launch") {
		t.Fatalf("second relaunch step = %+v, want duplicate-launch refusal", secondStep)
	}
}

func TestRecoverIntegrationStatus_MissingCanonicalIntegrationDoesNotRelaunch(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "missing-integration")
	writeCaptainMeta(t, parent, "missing-integration", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "absent"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
	}}

	result := tx.Recover(parent, Info{ID: "missing-integration", Home: home})
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepSkipped || !strings.Contains(step.Detail, "integration") {
		t.Fatalf("relaunch step = %+v, want integration-blocked skip", step)
	}
}

func TestRecoverIntegrationStatus_DigestInvalidCanonicalIntegrationFailsClosed(t *testing.T) {
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "drifted", Message: "one or more integration artifacts are missing or modified"}}}}
	step := tx.stepIntegrationStatus(Info{Home: captainHomeWithHarness(t, "pi")})
	if step.State != StepFailed {
		t.Fatalf("state = %s, want %s", step.State, StepFailed)
	}
	if !strings.Contains(step.Detail, "integration drifted") || !strings.Contains(step.Detail, "munsu integrate repair --harness pi --scope project") {
		t.Fatalf("detail = %q, want typed actionable repair", step.Detail)
	}
}

func TestRecoverIntegrationStatus_DriftedCanonicalIntegrationDoesNotRelaunch(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "drifted-integration")
	writeCaptainMeta(t, parent, "drifted-integration", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "drifted", Message: "digest mismatch"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
	}}

	result := tx.Recover(parent, Info{ID: "drifted-integration", Home: home})
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepSkipped || !strings.Contains(step.Detail, "integration") {
		t.Fatalf("relaunch step = %+v, want integration-blocked skip", step)
	}
}

// captainHomeWithHarness creates a captain home whose published snapshot
// carries the given CaptainProfile harness. The snapshot is the ONLY captain
// harness identity source for recovery operations; no flat pins are consulted.
func captainHomeWithHarness(t *testing.T, name string) string {
	t.Helper()
	return captainHomeWithSnapshot(t, config.CaptainProfile{Harness: name})
}

type staticIntegrationPort struct{ status IntegrationStatus }

func (p staticIntegrationPort) EnsureCaptain(string) error { return nil }
func (p staticIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	return p.status, nil
}

type countingStatusIntegrationPort struct{ calls int }

func (p *countingStatusIntegrationPort) EnsureCaptain(string) error { return nil }
func (p *countingStatusIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	p.calls++
	return IntegrationStatus{State: "installed"}, nil
}

type countingLaunchEndpoint struct{ calls int }

func (e *countingLaunchEndpoint) Launch(string, LaunchRequest) (LaunchResult, error) {
	e.calls++
	return LaunchResult{Backend: "test", Window: "window"}, nil
}
func (e *countingLaunchEndpoint) Cleanup(string, LaunchResult) error { return nil }

// findStep locates a step by name in the result slice.
func findStep(steps []StepResult, name string) *StepResult {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

// writeRelaunchGuard seeds a set relaunch guard on a captain task, using the
// given raw relaunch_guard_until value (empty leaves the field absent).
func writeRelaunchGuard(t *testing.T, parent, id, untilRaw string) {
	t.Helper()
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain(id))
	if err != nil {
		t.Fatal(err)
	}
	meta["relaunch_liveness"] = "unproven"
	if untilRaw != "" {
		meta[relaunchGuardUntilField] = untilRaw
	}
	if err := mhome.WriteMeta(parent, taskIDForCaptain(id), meta); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverRelaunchSlowBootProvesLivenessBeyondOldWindow(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "slow-boot")
	writeCanonicalPiIntegration(t, home)
	writeCaptainMeta(t, parent, "slow-boot", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	// Pre-launch probe dead, then five dead post-launch probes, then alive on
	// the sixth post-launch probe: the old five-probe window would fail this.
	results := make([]CaptainProbeResult, 7)
	results[6] = CaptainProbeResult{PaneAlive: true, AgentAlive: true}
	tx := &RecoverTransaction{
		Capabilities: RecoverCapabilities{
			Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
			Launch:      launch,
			Probe:       &testProbeEndpoint{results: results},
		},
		sleep: func(time.Duration) {},
	}

	result := tx.Recover(parent, Info{ID: "slow-boot", Home: home})
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepOk || !strings.Contains(step.Detail, "relaunched") {
		t.Fatalf("relaunch step = %+v, want successful relaunch after slow boot", step)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("slow-boot"))
	if err != nil {
		t.Fatalf("read relaunched meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared after proven liveness", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared after proven liveness", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}

func TestRecoverRelaunchExpiredGuardAllowsOneFreshAttempt(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	fixed := time.Unix(1_700_000_000, 0)
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "expired-guard")
	writeCanonicalPiIntegration(t, home)
	writeCaptainMeta(t, parent, "expired-guard", home, "dead-window")
	writeRelaunchGuard(t, parent, "expired-guard", strconv.FormatInt(fixed.Add(-time.Hour).Unix(), 10))

	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{
		Capabilities: RecoverCapabilities{
			Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
			Launch:      launch,
			Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
		},
		now:   func() time.Time { return fixed },
		sleep: func(time.Duration) {},
	}

	result := tx.Recover(parent, Info{ID: "expired-guard", Home: home})
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1 fresh attempt after guard expiry", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepFailed || !strings.Contains(step.Detail, "post-launch liveness") {
		t.Fatalf("relaunch step = %+v, want failed proof after fresh attempt", step)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("expired-guard"))
	if err != nil {
		t.Fatalf("read re-armed guard meta: %v", err)
	}
	if meta["relaunch_liveness"] != "unproven" {
		t.Fatalf("relaunch_liveness = %q, want unproven re-armed", meta["relaunch_liveness"])
	}
	wantUntil := strconv.FormatInt(fixed.Add(relaunchGuardTTL).Unix(), 10)
	if meta[relaunchGuardUntilField] != wantUntil {
		t.Fatalf("%s = %q, want re-armed %q", relaunchGuardUntilField, meta[relaunchGuardUntilField], wantUntil)
	}

	second := tx.Recover(parent, Info{ID: "expired-guard", Home: home})
	secondStep := findStep(second.Steps, "relaunch-pane")
	if launch.calls != 1 {
		t.Fatalf("second recovery launch calls = %d, want 1 (refused while guard fresh)", launch.calls)
	}
	if secondStep == nil || secondStep.State != StepFailed || !strings.Contains(secondStep.Detail, "duplicate launch") {
		t.Fatalf("second relaunch step = %+v, want fresh-guard refusal", secondStep)
	}
}

func TestRecoverRelaunchMalformedGuardDeadlineIsNormalizedAndBounded(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	fixed := time.Unix(1_700_000_000, 0)
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "missing", raw: ""},
		{name: "malformed", raw: "not-a-time"},
		{name: "implausibly-future", raw: strconv.FormatInt(fixed.Add(24*time.Hour).Unix(), 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			home := seedCaptainForTest(t, parent, "malformed-guard")
			writeCanonicalPiIntegration(t, home)
			writeCaptainMeta(t, parent, "malformed-guard", home, "dead-window")
			writeRelaunchGuard(t, parent, "malformed-guard", tc.raw)

			launch := &countingLaunchEndpoint{}
			cur := fixed
			tx := &RecoverTransaction{
				Capabilities: RecoverCapabilities{
					Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
					Launch:      launch,
					Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
				},
				now:   func() time.Time { return cur },
				sleep: func(time.Duration) {},
			}

			result := tx.Recover(parent, Info{ID: "malformed-guard", Home: home})
			if launch.calls != 0 {
				t.Fatalf("launch calls = %d, want 0 (normalized guard refuses once)", launch.calls)
			}
			step := findStep(result.Steps, "relaunch-pane")
			if step == nil || step.State != StepFailed || !strings.Contains(step.Detail, "duplicate launch") {
				t.Fatalf("relaunch step = %+v, want bounded refusal", step)
			}
			meta, err := mhome.ReadMeta(parent, taskIDForCaptain("malformed-guard"))
			if err != nil {
				t.Fatalf("read normalized guard meta: %v", err)
			}
			wantUntil := strconv.FormatInt(fixed.Add(relaunchGuardTTL).Unix(), 10)
			if meta[relaunchGuardUntilField] != wantUntil {
				t.Fatalf("%s = %q, want normalized %q", relaunchGuardUntilField, meta[relaunchGuardUntilField], wantUntil)
			}

			cur = fixed.Add(relaunchGuardTTL + time.Second)
			tx.Recover(parent, Info{ID: "malformed-guard", Home: home})
			if launch.calls != 1 {
				t.Fatalf("launch calls after deadline = %d, want 1 bounded retry", launch.calls)
			}
		})
	}
}

func TestRecoverRelaunchAliveEndpointClearsGuard(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "alive-clears-guard")
	writeCanonicalPiIntegration(t, home)
	writeCaptainMeta(t, parent, "alive-clears-guard", home, "dead-window")
	writeRelaunchGuard(t, parent, "alive-clears-guard", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}},
	}}

	result := tx.Recover(parent, Info{ID: "alive-clears-guard", Home: home})
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepOk || !strings.Contains(step.Detail, "alive") {
		t.Fatalf("relaunch step = %+v, want alive no-action", step)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("alive-clears-guard"))
	if err != nil {
		t.Fatalf("read cleared guard meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}
