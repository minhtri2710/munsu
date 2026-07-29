package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
)

type captureBackend struct {
	handle string
	lines  int
	output string
	err    error
}

func (b *captureBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *captureBackend) SendKeys(string, string) error            { return nil }
func (b *captureBackend) Capture(handle string, lines int) (string, error) {
	b.handle, b.lines = handle, lines
	return b.output, b.err
}
func (b *captureBackend) Alive(string) bool     { return true }
func (b *captureBackend) Teardown(string) error { return nil }

func TestSessionBoundCapturePreservesBoundRequest(t *testing.T) {
	b := &captureBackend{output: "captured"}
	c := sessionBoundCapture{resolve: func(home string, meta map[string]string) (backend.Backend, string, error) {
		if home != "/home" || meta["herdr_workspace_id"] != "workspace-1" || meta["herdr_tab_id"] != "tab-1" {
			t.Fatalf("home=%q meta=%v", home, meta)
		}
		return b, "herdr", nil
	}}
	out, err := c.Capture("/home", map[string]string{"backend": "herdr", "window": "session-1:pane-1", "herdr_session": "session-1", "herdr_workspace_id": "workspace-1", "herdr_tab_id": "tab-1"}, 42)
	if err != nil || out != "captured" || b.handle != "session-1:pane-1" || b.lines != 42 {
		t.Fatalf("out=%q err=%v backend=%+v", out, err, b)
	}
}
func TestSessionBoundCaptureRejectsIdentityAndMismatch(t *testing.T) {
	c := sessionBoundCapture{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captureBackend{}, "tmux", nil
	}}
	cases := []map[string]string{{}, {"backend": "unknown", "window": "p"}, {"backend": "herdr", "window": "session-a:p", "herdr_session": "session-b"}, {"backend": "herdr", "window": "session:p"}}
	for i, meta := range cases {
		if _, err := c.Capture("/home", meta, 1); err == nil {
			t.Fatalf("case %d expected error", i)
		}
	}
}
func TestSessionBoundCaptureRejectsResolvedBackendMismatch(t *testing.T) {
	c := sessionBoundCapture{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captureBackend{}, "tmux", nil
	}}
	_, err := c.Capture("/home", map[string]string{"backend": "herdr", "window": "session:p"}, 1)
	if err == nil {
		t.Fatal("expected mismatch")
	}
}
func TestSessionBoundCaptureReturnsAdapterError(t *testing.T) {
	want := errors.New("capture failed")
	c := sessionBoundCapture{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captureBackend{err: want}, "tmux", nil
	}}
	_, err := c.Capture("/home", map[string]string{"backend": "tmux", "window": "p"}, 1)
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
