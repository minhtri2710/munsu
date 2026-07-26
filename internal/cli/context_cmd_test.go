package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextCmd_SyncsAndPrintsManual(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_HOME", tmp)
	// Stale home AGENTS must be overwritten by seed.
	if err := os.MkdirAll(filepath.Join(tmp, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(stale, []byte("# stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data", "projects.md"), []byte("- demo - /tmp/demo (added 2026-01-01)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"context"})
	if err := root.Execute(); err != nil {
		t.Fatalf("context: %v\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "munsu context: orchestrator doctrine") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "never do project work yourself") {
		t.Fatalf("missing role line:\n%s", out)
	}
	if !strings.Contains(out, "Registered projects") {
		t.Fatalf("missing projects section:\n%s", out)
	}
	if !strings.Contains(out, "demo") {
		t.Fatalf("expected demo project in list:\n%s", out)
	}
	if !strings.Contains(out, "munsu session-start") {
		t.Fatalf("missing session-start next step:\n%s", out)
	}
	// Manual body from seed
	if !strings.Contains(out, "soldiers") && !strings.Contains(out, "orchestrator") {
		snippet := out
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		t.Fatalf("expected manual body keywords:\n%s", snippet)
	}
	data, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "# stale\n" {
		t.Fatal("home AGENTS.md was not refreshed from seed")
	}
	if string(data) != orchestratorManual {
		t.Fatal("home AGENTS.md does not match embedded seed")
	}
}

func TestContextCmd_SyncOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_HOME", tmp)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"context", "--sync-only"})
	if err := root.Execute(); err != nil {
		t.Fatalf("context --sync-only: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "=== munsu context") {
		t.Fatalf("sync-only should not dump full doctrine:\n%s", out)
	}
	if !strings.Contains(out, "orchestrator manual") {
		t.Fatalf("expected sync status line:\n%s", out)
	}
	path := filepath.Join(tmp, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != orchestratorManual {
		t.Fatal("AGENTS.md not written on sync-only")
	}
}

func TestSyncOrchestratorManual_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	path, wrote, err := syncOrchestratorManual(tmp)
	if err != nil || !wrote {
		t.Fatalf("first sync wrote=%v err=%v path=%s", wrote, err, path)
	}
	_, wrote2, err := syncOrchestratorManual(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if wrote2 {
		t.Fatal("second sync should be no-op when content matches seed")
	}
}
