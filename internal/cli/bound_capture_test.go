package cli

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type captureBackend struct {
	handle string
	lines  int
	output string
	err    error
}

func (b *captureBackend) NewWindow(string, string) (string, error)          { return "", nil }
func (b *captureBackend) FindOrCreateWindow(string, string) (string, error) { return "", nil }
func (b *captureBackend) SendKeys(string, string) error                     { return nil }
func (b *captureBackend) Capture(handle string, lines int) (string, error) {
	b.handle, b.lines = handle, lines
	return b.output, b.err
}
func (b *captureBackend) Alive(string) bool     { return true }
func (b *captureBackend) Teardown(string) error { return nil }

func TestSessionBoundCapturePreservesBoundRequest(t *testing.T) {
	b := &captureBackend{output: "captured"}
	c := sessionBoundCapture{resolve: func(home string, ep backend.EndpointRef) (backend.Backend, string, error) {
		if home != "/home" || ep.WorkspaceID != "workspace-1" || ep.TabID != "tab-1" {
			t.Fatalf("home=%q endpoint=%+v", home, ep)
		}
		return b, "herdr", nil
	}}
	out, err := c.Capture("/home", taskauthority.EndpointBinding{Backend: "herdr", Handle: "session-1:pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1"}, 42)
	if err != nil || out != "captured" || b.handle != "session-1:pane-1" || b.lines != 42 {
		t.Fatalf("out=%q err=%v backend=%+v", out, err, b)
	}
}
func TestSessionBoundCaptureRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		home     string
		ep       taskauthority.EndpointBinding
		resolved string
		want     string
	}{
		{"no home", "", taskauthority.EndpointBinding{Backend: "tmux", Handle: "p"}, "tmux", "bound capture identity is incomplete"},
		{"no window", "/home", taskauthority.EndpointBinding{Backend: "tmux"}, "tmux", "bound capture identity is incomplete"},
		{"backend outside the supported set", "/home", taskauthority.EndpointBinding{Backend: "unknown", Handle: "p"}, "unknown", `unsupported bound backend "unknown"`},
		{"zellij blocks", "/home", taskauthority.EndpointBinding{Backend: "zellij", Handle: "p"}, "zellij", `unsupported bound backend "zellij"`},
		{"cmux blocks", "/home", taskauthority.EndpointBinding{Backend: "cmux", Handle: "p"}, "cmux", `unsupported bound backend "cmux"`},
		{"orca blocks", "/home", taskauthority.EndpointBinding{Backend: "orca", Handle: "p"}, "orca", `unsupported bound backend "orca"`},
		{"herdr allows", "/home", taskauthority.EndpointBinding{Backend: "herdr", Handle: "p"}, "herdr", ""},
		{"tmux allows", "/home", taskauthority.EndpointBinding{Backend: "tmux", Handle: "p"}, "tmux", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := sessionBoundCapture{resolve: func(string, backend.EndpointRef) (backend.Backend, string, error) {
				return &captureBackend{output: "captured"}, tc.resolved, nil
			}}
			out, err := c.Capture(tc.home, tc.ep, 1)
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
	c := sessionBoundCapture{resolve: func(string, backend.EndpointRef) (backend.Backend, string, error) {
		return &captureBackend{err: want}, "tmux", nil
	}}
	_, err := c.Capture("/home", taskauthority.EndpointBinding{Backend: "tmux", Handle: "p"}, 1)
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
