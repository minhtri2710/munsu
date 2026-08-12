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
	setupProbeHome(t, h, "window=pane-1\nbackend=herdr\nherdr_session=session-1\nherdr_workspace_id=workspace-1\nherdr_tab_id=tab-1\n")
	probe := &stateProbe{alive: true}
	state, err := ReadWithProbe(h, "task", probe)
	if err != nil {
		t.Fatal(err)
	}
	if !state.PaneAlive {
		t.Fatal("expected alive")
	}
	if probe.meta["herdr_session"] != "session-1" || probe.meta["herdr_workspace_id"] != "workspace-1" || probe.meta["herdr_tab_id"] != "tab-1" {
		t.Fatalf("meta=%v", probe.meta)
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
