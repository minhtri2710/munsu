package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

type windowSubmitPort struct{ window, prompt string }

func (p *windowSubmitPort) Submit(window, prompt string) SubmitResult {
	p.window, p.prompt = window, prompt
	return SubmitResult{Acknowledged: true}
}

// A captain home's soldier reports reach its captain's own pane: the watcher
// pushes them there when the captain runs claude on herdr, and any other
// captain claims them at turn end. Neither the General's runtime pane nor a
// general-pane file routes a captain home's wakes.
func TestCaptainHomeWakesTargetTheCaptainPane(t *testing.T) {
	for _, tc := range []struct {
		name, harness, backend string
		metaHome               string // the home the captain task meta names; empty is this captain home
		wantMode               WakeDeliveryMode
		wantSource             TargetSource
	}{
		{name: "claude captain on herdr", harness: "claude", backend: "herdr", wantMode: WakeDeliveryHerdr, wantSource: CaptainSource},
		{name: "pi captain on herdr", harness: "pi", backend: "herdr", wantMode: WakeDeliveryNative, wantSource: CaptainSource},
		{name: "claude captain on tmux", harness: "claude", backend: "tmux", wantMode: WakeDeliveryNative, wantSource: Unsupported},
		{name: "meta names another home", harness: "claude", backend: "herdr", metaHome: "/elsewhere/cap1", wantMode: WakeDeliveryNative, wantSource: Unsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SESSION", "lab")
			t.Setenv("HERDR_PANE_ID", "w1:p1")
			general := testutil.TempHome(t)
			captain := filepath.Join(general, "captains", "cap1")
			if err := os.MkdirAll(filepath.Join(captain, "config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := home.SeedCaptainProvenance(captain, "cap1"); err != nil {
				t.Fatal(err)
			}
			if err := config.Set(captain, "parent-home", general); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(captain, generalPaneConfig), []byte("lab:w1:p1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			canonical, err := home.CanonicalCaptainHome(captain)
			if err != nil {
				t.Fatal(err)
			}
			if tc.metaHome != "" {
				canonical = tc.metaHome
			}
			if err := home.WriteMeta(general, "captain:cap1", map[string]string{
				"kind": "captain", "home": canonical, "window": "lab:w9:p9", "backend": tc.backend,
				"harness": tc.harness, "sm_id": "cap1", "session_owner": "lab",
			}); err != nil {
				t.Fatal(err)
			}

			mode, err := ResolveWakeDeliveryMode(captain)
			if err != nil || mode != tc.wantMode {
				t.Fatalf("mode = %q, %v; want %q", mode, err, tc.wantMode)
			}
			target, err := ResolveTargetWithSource(captain)
			if err != nil || target.Source != tc.wantSource {
				t.Fatalf("target = %+v, %v; want source %s", target, err, tc.wantSource)
			}
			if tc.wantSource == CaptainSource && (target.Handle != "lab:w9:p9" || target.Session != "lab") {
				t.Fatalf("target = %+v, want the captain pane lab:w9:p9 in session lab", target)
			}
			if mode != WakeDeliveryHerdr {
				return
			}

			if err := home.AppendStatus(captain, "soldier-1", domain.ReportStatusLine("done", "shipped the fix", "default")); err != nil {
				t.Fatal(err)
			}
			submit := &windowSubmitPort{}
			res, err := DispatchWake(DispatchWakeRequest{
				HomeDir: captain, Mode: mode, Target: target,
				Probe: aliveProbe(), Submit: submit, Busy: idleBusy(), Composer: claudeIdleComposer(t),
			})
			if err != nil || res.Outcome != WakeSubmitted {
				t.Fatalf("dispatch = %+v, %v; want submitted", res, err)
			}
			if submit.window != "lab:w9:p9" || !strings.Contains(submit.prompt, "done: shipped the fix") {
				t.Fatalf("submitted to %q: %q; want the soldier report in the captain pane", submit.window, submit.prompt)
			}
		})
	}
}

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
