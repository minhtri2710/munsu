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
		if home != "/home" || meta["workspace_id"] != "workspace-1" || meta["tab_id"] != "tab-1" {
			t.Fatalf("home=%q meta=%v", home, meta)
		}
		return b, "herdr", nil
	}}
	out, err := c.Capture("/home", map[string]string{"backend": "herdr", "window": "session-1:pane-1", "session_owner": "session-1", "workspace_id": "workspace-1", "tab_id": "tab-1"}, 42)
	if err != nil || out != "captured" || b.handle != "session-1:pane-1" || b.lines != 42 {
		t.Fatalf("out=%q err=%v backend=%+v", out, err, b)
	}
}
func TestSessionBoundCaptureRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		home     string
		meta     map[string]string
		resolved string
		want     string
	}{
		{"no home", "", map[string]string{"backend": "tmux", "window": "p"}, "tmux", "bound capture identity is incomplete"},
		{"no window", "/home", map[string]string{"backend": "tmux"}, "tmux", "bound capture identity is incomplete"},
		{"backend outside the supported set", "/home", map[string]string{"backend": "unknown", "window": "p"}, "unknown", `unsupported bound backend "unknown"`},
		{"zellij blocks", "/home", map[string]string{"backend": "zellij", "window": "p"}, "zellij", `unsupported bound backend "zellij"`},
		{"cmux blocks", "/home", map[string]string{"backend": "cmux", "window": "p"}, "cmux", `unsupported bound backend "cmux"`},
		{"orca blocks", "/home", map[string]string{"backend": "orca", "window": "p"}, "orca", `unsupported bound backend "orca"`},
		{"herdr allows", "/home", map[string]string{"backend": "herdr", "window": "p"}, "herdr", ""},
		{"tmux allows", "/home", map[string]string{"backend": "tmux", "window": "p"}, "tmux", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sessionBoundCapture{resolve: func(string, map[string]string) (backend.Backend, string, error) {
				return &captureBackend{output: "captured"}, tc.resolved, nil
			}}
			out, err := c.Capture(tc.home, tc.meta, 1)
			if tc.want == "" {
				if err != nil || out != "captured" {
					t.Fatalf("Capture = %q, %v, want acceptance", out, err)
				}
				return
			}
			wantErrContains(t, err, tc.want, "Capture with "+tc.name)
		})
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
