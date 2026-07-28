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
	os.Exit(m.Run())
}

func TestResolveTargetWithSource_ConfigPrecedesRuntime(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, generalPaneConfig), []byte("lab:pane-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "%42")

	got, err := ResolveTargetWithSource(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != ConfigSource || got.Handle != "lab:pane-1" || got.Session != "lab" {
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

func TestValidateTargetOwnership_RejectsForeignHerdrSession(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SESSION", "general")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	result := TargetResult{Source: ConfigSource, Handle: "foreign:w2:p2", Session: "foreign"}
	if err := ValidateTargetOwnership(&result); err == nil {
		t.Fatal("expected foreign session rejection")
	}
}

func TestValidateTargetOwnership_RejectsBareHerdrHandle(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SESSION", "general")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	result := TargetResult{Source: ConfigSource, Handle: "w2:p2"}
	if err := ValidateTargetOwnership(&result); err == nil {
		t.Fatal("expected bare herdr handle rejection")
	}
}

func TestResolveTarget_BackwardCompatible(t *testing.T) {
	t.Setenv("TMUX_PANE", "%9")
	handle, session, err := ResolveTarget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if handle != "%9" || session != "" {
		t.Fatalf("ResolveTarget = %q, %q", handle, session)
	}
}
