package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

// seamProbeBackend is a controllable agent-aware backend used to drive the
// production probe seam (sessionProbeEndpoint → probeCaptainBackend →
// CheckAgentAlive) in strict-dead-only causal tests.
type seamProbeBackend struct {
	paneAlive  bool
	agentAlive bool
	err        error
	calls      int
}

func (b *seamProbeBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *seamProbeBackend) SendKeys(string, string) error            { return nil }
func (b *seamProbeBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *seamProbeBackend) Alive(string) bool                        { return false }
func (b *seamProbeBackend) Teardown(string) error                    { return nil }
func (b *seamProbeBackend) CheckAgentAlive(string) (bool, bool, error) {
	b.calls++
	return b.paneAlive, b.agentAlive, b.err
}

// seamAbsentThenAliveBackend reports authoritative absence on the first probe
// and a live agent afterwards, mirroring a captain that was dead before a
// relaunch and comes up once relaunched.
type seamAbsentThenAliveBackend struct{ calls int }

func (b *seamAbsentThenAliveBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *seamAbsentThenAliveBackend) SendKeys(string, string) error            { return nil }
func (b *seamAbsentThenAliveBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *seamAbsentThenAliveBackend) Alive(string) bool                        { return false }
func (b *seamAbsentThenAliveBackend) Teardown(string) error                    { return nil }
func (b *seamAbsentThenAliveBackend) CheckAgentAlive(string) (bool, bool, error) {
	b.calls++
	if b.calls == 1 {
		return false, false, backend.ErrPaneNotFound
	}
	return true, true, nil
}

// seamLaunchEndpoint records launch invocations so causal tests can assert
// that strict-dead evidence was the only thing that authorized Launch.
type seamLaunchEndpoint struct{ calls int }

func (e *seamLaunchEndpoint) Launch(string, fleet.LaunchRequest) (fleet.LaunchResult, error) {
	e.calls++
	return fleet.LaunchResult{Backend: "herdr", Window: "fresh-window"}, nil
}
func (e *seamLaunchEndpoint) Cleanup(string, fleet.LaunchResult) error { return nil }

// seamStaticIntegrationPort reports an installed canonical Pi integration,
// mirroring the fleet test fixture; the real Launch front-end still verifies
// the canonical integration file on disk.
type seamStaticIntegrationPort struct{}

func (seamStaticIntegrationPort) EnsureCaptain(string) error { return nil }
func (seamStaticIntegrationPort) Status(string, string) (fleet.IntegrationStatus, error) {
	return fleet.IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}, nil
}

// seamProbeEndpoint wires the production CLI probe endpoint to a fake backend.
func seamProbeEndpoint(bk backend.Backend) sessionProbeEndpoint {
	return sessionProbeEndpoint{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return bk, "herdr", nil
	}}
}

// seedSeamCaptain seeds a canonical parent home, a provenance-marked captain
// home with launch meta, a Pi harness published snapshot, and the canonical
// Pi integration so fleet recovery reaches the probe/launch decision.
func seedSeamCaptain(t *testing.T, id string) (parent, captainHome string) {
	t.Helper()
	parent = t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome = filepath.Join(parent, "captains", id)
	for _, dir := range []string{"state", "config", "data"} {
		if err := os.MkdirAll(filepath.Join(captainHome, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# "+id+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := fleet.SeedProvenance(captainHome, id); err != nil {
		t.Fatal(err)
	}
	canon, err := home.CanonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(parent, "captain:"+id, map[string]string{
		"kind": "captain", "sm_id": id, "home": canon, "window": "dead-window",
		"backend": "herdr", "herdr_session": "owned",
	}); err != nil {
		t.Fatal(err)
	}
	resolved := config.ResolvedProjectConfig{
		Project: "seam", ProjectPath: captainHome, Backend: "herdr",
		CaptainProfile: config.CaptainProfile{Harness: harness.Pi},
		Digest:         "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if err := config.StorePublishedSnapshot(captainHome, resolved); err != nil {
		t.Fatal(err)
	}
	extDir := filepath.Join(captainHome, ".pi", "extensions")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "munsu-pi-integration.ts"), []byte("// munsu-owned\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return parent, captainHome
}

// putFakePiOnPath makes exec.LookPath("pi") resolve to a fake binary so the
// real fleet Launch front-end reaches the endpoint under test, mirroring the
// fake-herdr fixture pattern in the backend tests.
func putFakePiOnPath(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestProbeLivenessSeam_StrictDeadOnly drives the production probe seam into
// fleet.ProbeLiveness: only authoritative pane absence reports "dead";
// pane-present/no-agent reports "unknown" (fail closed), never "dead".
func TestProbeLivenessSeam_StrictDeadOnly(t *testing.T) {
	parent, captainHome := seedSeamCaptain(t, "seam-probe")
	registered := []fleet.Info{{ID: "seam-probe", Home: captainHome}}

	probes := fleet.ProbeLiveness(parent, registered, seamProbeEndpoint(&seamProbeBackend{err: backend.ErrPaneNotFound}))
	if len(probes) != 1 || probes[0].Status != "dead" {
		t.Fatalf("ErrPaneNotFound probes = %+v, want one dead", probes)
	}

	probes = fleet.ProbeLiveness(parent, registered, seamProbeEndpoint(&seamProbeBackend{paneAlive: true}))
	if len(probes) != 1 || probes[0].Status != "unknown" {
		t.Fatalf("no-agent probes = %+v, want unknown (never dead)", probes)
	}

	probes = fleet.ProbeLiveness(parent, registered, seamProbeEndpoint(&seamProbeBackend{paneAlive: true, agentAlive: true}))
	if len(probes) != 1 || probes[0].Status != "alive" {
		t.Fatalf("alive probes = %+v, want alive", probes)
	}
}

// TestRecoverSeam_AuthoritativeAbsenceRelaunches proves through the real CLI
// probe endpoint that only ErrPaneNotFound authorizes Launch in fleet.Recover.
func TestRecoverSeam_AuthoritativeAbsenceRelaunches(t *testing.T) {
	putFakePiOnPath(t)
	parent, captainHome := seedSeamCaptain(t, "seam-recover")
	launch := &seamLaunchEndpoint{}

	res, err := fleet.Recover(parent, []fleet.Info{{ID: "seam-recover", Home: captainHome}}, fleet.RecoverCapabilities{
		Integration: seamStaticIntegrationPort{},
		Launch:      launch,
		Probe:       seamProbeEndpoint(&seamAbsentThenAliveBackend{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1 (only authoritative absence may authorize relaunch)", launch.calls)
	}
	if res.Relaunched != 1 || res.Failed != 0 {
		t.Fatalf("result = %+v, want relaunched=1 failed=0", res)
	}
}

// TestRecoverSeam_NoAgentFailsClosed proves through the real CLI probe
// endpoint that pane-present/no-agent never authorizes Launch and fails
// closed with the strict-dead-only refusal.
func TestRecoverSeam_NoAgentFailsClosed(t *testing.T) {
	parent, captainHome := seedSeamCaptain(t, "seam-noagent")
	launch := &seamLaunchEndpoint{}

	res, err := fleet.Recover(parent, []fleet.Info{{ID: "seam-noagent", Home: captainHome}}, fleet.RecoverCapabilities{
		Launch: launch,
		Probe:  seamProbeEndpoint(&seamProbeBackend{paneAlive: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0 (pane-present/no-agent fails closed)", launch.calls)
	}
	if res.Failed != 1 || !strings.Contains(res.Entries[0].Error, "not authoritatively absent") {
		t.Fatalf("result = %+v, want fail-closed entry naming strict-dead-only", res)
	}
}
