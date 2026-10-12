package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	for _, key := range []string{"TMUX_PANE", "HERDR_ENV", "HERDR_SESSION", "HERDR_PANE_ID"} {
		_ = os.Unsetenv(key)
	}
	if exitAsWatcherChild() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestResolveTargetWithSource_ConfigPrecedesRuntime(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, generalPaneConfig), []byte("lab:w1:p1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "%42")

	got, err := ResolveTargetWithSource(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != ConfigSource || got.Handle != "lab:w1:p1" || got.Session != "lab" {
		t.Fatalf("result = %+v", got)
	}
}

func TestResolveTargetWithSource_TmuxPrecedesHerdr(t *testing.T) {
	t.Setenv("TMUX_PANE", "%42")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SESSION", "lab")
	t.Setenv("HERDR_PANE_ID", "w1:p1")

	got, err := ResolveTargetWithSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != RuntimeSource || got.Handle != "%42" || got.Session != "" {
		t.Fatalf("result = %+v", got)
	}
	if !strings.Contains(got.SourceDetail, "TMUX_PANE") {
		t.Fatalf("source detail = %q", got.SourceDetail)
	}
}

func TestResolveTargetWithSource_HerdrRuntime(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SESSION", "lab")
	t.Setenv("HERDR_PANE_ID", "w1:p1")

	got, err := ResolveTargetWithSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != RuntimeSource || got.Handle != "lab:w1:p1" || got.Session != "lab" {
		t.Fatalf("result = %+v", got)
	}
	if err := ValidateTargetOwnership(&got); err != nil {
		t.Fatalf("ownership: %v", err)
	}
}

func TestResolveTargetWithSource_HerdrDefaultSession(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")

	got, err := ResolveTargetWithSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle != "default:w1:p1" || got.Session != "default" {
		t.Fatalf("result = %+v", got)
	}
}

func TestResolveTargetWithSource_Unsupported(t *testing.T) {
	got, err := ResolveTargetWithSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != Unsupported || got.Handle != "" {
		t.Fatalf("result = %+v", got)
	}
	if err := ValidateTargetOwnership(&got); err == nil {
		t.Fatal("expected unsupported ownership error")
	}
}

func TestResolveTargetWithSource_EmptyConfigIsUnsupported(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, generalPaneConfig), []byte(" \n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveTargetWithSource(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != Unsupported || !strings.Contains(got.SourceDetail, "empty") {
		t.Fatalf("result = %+v", got)
	}
}

func TestValidateTargetOwnership_HerdrSession(t *testing.T) {
	const mismatch = "does not match active herdr session"
	for _, tc := range []struct {
		name       string
		tmuxPane   string
		envSession string
		session    string
		wantErr    string
	}{
		{name: "foreign session", envSession: "general", session: "foreign", wantErr: mismatch},
		{name: "bare handle with no session", envSession: "general", session: "", wantErr: mismatch},
		{name: "bare handle against the default session", envSession: "", session: "", wantErr: mismatch},
		{name: "session other than the default", envSession: "", session: "x", wantErr: mismatch},
		{name: "matching session", envSession: "general", session: "general"},
		{name: "default session", envSession: "", session: "default"},
		{name: "tmux pane ignores the herdr session", tmuxPane: "%1", envSession: "general", session: "foreign"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMUX_PANE", tc.tmuxPane)
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SESSION", tc.envSession)
			t.Setenv("HERDR_PANE_ID", "w1:p1")
			err := ValidateTargetOwnership(&TargetResult{Source: ConfigSource, Handle: "h:w2:p2", Session: tc.session})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateTargetOwnership = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateTargetOwnership = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
