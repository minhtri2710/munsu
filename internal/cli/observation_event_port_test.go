// Tests for the CLI-side native observation event port (BEO-17/P1b): endpoint
// scanning, backend resolution, and source wiring. Wire behavior is exercised
// with a fake herdr executable; endpoints without an event surface must be
// omitted so the orchestrator keeps pure polling.
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
)

func TestObservationEventPort_Sources_HerdrEndpoint(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteCLIMeta(t, homeDir, "task-1", map[string]string{
		"backend":              "herdr",
		"window":               "w:p",
		"endpoint_incarnation": "inc-1",
	})

	// Fake herdr on PATH with a ready protocol so resolution succeeds.
	fakeDir := t.TempDir()
	writeFakeHerdrCLI(t, fakeDir)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+":"+oldPath)

	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("Sources = %d, want 1", len(sources))
	}
	if sources[0].Endpoint.Backend != "herdr" || sources[0].Endpoint.Handle != "w:p" {
		t.Errorf("endpoint = %+v, want herdr w:p", sources[0].Endpoint)
	}
	if sources[0].Incarnation != "inc-1" {
		t.Errorf("incarnation = %q, want inc-1", sources[0].Incarnation)
	}
	if sources[0].Source == nil {
		t.Error("source must be non-nil for a herdr endpoint")
	}
	if _, ok := sources[0].Source.(backend.ObservationEventSource); !ok {
		t.Errorf("source type %T does not implement ObservationEventSource", sources[0].Source)
	}
}

func TestObservationEventPort_Sources_BackendWithoutEventsOmitted(t *testing.T) {
	homeDir := t.TempDir()
	// tmux has no native event surface; the port must omit it entirely.
	mustWriteCLIMeta(t, homeDir, "task-tmux", map[string]string{
		"backend": "tmux",
		"window":  "sess:1",
	})
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 (tmux has no event surface)", len(sources))
	}
}

func TestObservationEventPort_Sources_NoMeta(t *testing.T) {
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(t.TempDir())
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 for empty home", len(sources))
	}
}

func TestObservationEventPort_Sources_CaptainSkipped(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteCLIMeta(t, homeDir, "cap-1", map[string]string{
		"backend": "herdr",
		"window":  "w:cap",
		"kind":    "captain",
	})
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 (captain endpoints are not event-wait targets)", len(sources))
	}
}

func TestObservationEventPort_Sources_UnresolvableSkipped(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteCLIMeta(t, homeDir, "task-1", map[string]string{
		"backend": "herdr",
		"window":  "w:p",
	})
	// No herdr on PATH → BackendForTask fails → endpoint omitted → pure polling.
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	port := observationEventPort{resolve: backend.BackendForTask}
	sources, err := port.Sources(homeDir)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("Sources = %d, want 0 (unresolvable backend → poll)", len(sources))
	}
	_ = oldPath
}

func mustWriteCLIMeta(t *testing.T, homeDir, id string, kv map[string]string) {
	t.Helper()
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for k, v := range kv {
		b = append(b, []byte(k+"="+v+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(stateDir, id+".meta"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

// writeFakeHerdrCLI creates a minimal herdr binary responding to the schema
// probe (protocol 17) so backend resolution succeeds.
func writeFakeHerdrCLI(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "herdr 0.7.5"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  echo '{"protocol":17,"schema_version":1,"schemas":{}}'` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}
