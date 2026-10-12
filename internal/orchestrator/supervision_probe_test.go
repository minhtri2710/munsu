package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type watcherProbe struct {
	alive bool
	err   error
	meta  map[string]string
}

func (p *watcherProbe) Probe(_ string, meta map[string]string) (bool, error) {
	p.meta = meta
	return p.alive, p.err
}
func TestScanTaskWithProbePreservesHerdrIdentity(t *testing.T) {
	h := t.TempDir()
	os.MkdirAll(filepath.Join(h, "state"), 0700)
	os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte("kind=ship\nwindow=session-1:pane-1\nbackend=herdr\nsession_owner=session-1\nworkspace_id=workspace-1\ntab_id=tab-1\n"), 0600)
	p := &watcherProbe{alive: true}
	if reason := scanTaskWithProbe(h, "task", p, testTaskStatePort{}, testSeatLiveness); reason != nil {
		t.Fatalf("reason=%+v", reason)
	}
	if p.meta["session_owner"] != "session-1" || p.meta["workspace_id"] != "workspace-1" || p.meta["tab_id"] != "tab-1" {
		t.Fatalf("meta=%v", p.meta)
	}
}
func TestScanTaskWithProbeSurfacesProbeError(t *testing.T) {
	h := t.TempDir()
	os.MkdirAll(filepath.Join(h, "state"), 0700)
	os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte("kind=ship\nwindow=pane\nbackend=herdr\n"), 0600)
	reason := scanTaskWithProbe(h, "task", &watcherProbe{err: errors.New("backend unavailable")}, testTaskStatePort{}, testSeatLiveness)
	if reason == nil || reason.Kind != "check" {
		t.Fatalf("reason=%+v", reason)
	}
}
