package soldierstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
func TestReadWithProbePreservesBoundMetadata(t *testing.T) {
	h := t.TempDir()
	os.MkdirAll(filepath.Join(h, "state"), 0700)
	os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte("window=pane-1\nbackend=herdr\nherdr_session=session-1\nherdr_workspace_id=workspace-1\nherdr_tab_id=tab-1\n"), 0600)
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
	os.MkdirAll(filepath.Join(h, "state"), 0700)
	os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte("window=pane-1\nbackend=herdr\n"), 0600)
	state, err := ReadWithProbe(h, "task", &stateProbe{alive: true, err: errors.New("probe failed")})
	if err != nil {
		t.Fatal(err)
	}
	if state.PaneAlive {
		t.Fatal("probe error must fail closed")
	}
}
