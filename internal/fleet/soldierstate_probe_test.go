//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type stateProbe struct {
	alive bool
	err   error
	meta  map[string]string
}

func (p *stateProbe) Probe(_ string, meta map[string]string) (bool, error) {
	p.meta = meta
	return p.alive, p.err
}

// setupProbeHome initializes a canonical home with a canonical task and the
// given task meta so observation's canonical Task Authority read succeeds and
// the endpoint probe receives the bound metadata.
func setupProbeHome(t *testing.T, h string, metaContent string) {
	t.Helper()
	if _, err := mhome.Init(h); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	seedCanonicalPhase(t, h, "task", taskauthority.PhaseWorking)
	if err := os.MkdirAll(filepath.Join(h, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte(metaContent), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReadWithProbePreservesBoundMetadata(t *testing.T) {
	h := t.TempDir()
	setupProbeHome(t, h, "window=pane-1\nbackend=herdr\nsession_owner=session-1\nworkspace_id=workspace-1\ntab_id=tab-1\n")
	probe := &stateProbe{alive: true}
	state, err := ReadWithProbe(h, "task", probe)
	if err != nil {
		t.Fatal(err)
	}
	if !state.PaneAlive {
		t.Fatal("expected alive")
	}
	if probe.meta["session_owner"] != "session-1" || probe.meta["workspace_id"] != "workspace-1" || probe.meta["tab_id"] != "tab-1" {
		t.Fatalf("meta=%v", probe.meta)
	}
}

// TestReadWithProbeProbesUnboundAcquiredEndpoint probes the pane a launch
// acquired but never bound, which wrote no .meta.
func TestReadWithProbeProbesUnboundAcquiredEndpoint(t *testing.T) {
	f := newLaunchFixture(t, "unbound-probe")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("launch phases = %v, want simulated pre-projection crash", err)
	}
	acquired := f.aggregate().AcquiredEndpoint
	if acquired == nil {
		t.Fatal("fixture holds no acquired endpoint")
	}
	if _, err := mhome.ReadMeta(f.homeDir, f.taskID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture meta read = %v, want none", err)
	}
	probe := &stateProbe{alive: true}
	state, err := ReadWithProbe(f.homeDir, f.taskID, probe)
	if err != nil {
		t.Fatal(err)
	}
	if !state.PaneAlive || probe.meta["window"] != acquired.Handle || probe.meta["backend"] != acquired.Backend || probe.meta["harness"] != f.aggregate().Launch.Harness {
		t.Fatalf("pane_alive=%v probed meta=%v, want the acquired endpoint %+v", state.PaneAlive, probe.meta, acquired)
	}
}

func TestReadWithProbeFailsClosedOnProbeError(t *testing.T) {
	h := t.TempDir()
	setupProbeHome(t, h, "window=pane-1\nbackend=herdr\n")
	state, err := ReadWithProbe(h, "task", &stateProbe{alive: true, err: errors.New("probe failed")})
	if err != nil {
		t.Fatal(err)
	}
	if state.PaneAlive {
		t.Fatal("probe error must fail closed")
	}
}
