//go:build integration

package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeHerdrPrune creates a fake herdr executable in dir that responds to
// workspace list and workspace close commands using the given workspace JSON.
// When workspaceJSON is empty, a default single-workspace response is used.
func writeFakeHerdrPrune(t *testing.T, dir string, workspaceJSON string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	if workspaceJSON == "" {
		workspaceJSON = `[{"label":"testtag","workspace_id":"wTest","tab_count":0,"agent_status":"none"}]`
	}
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  SESSION="$2"` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`if [ -z "$SESSION" ]; then` + "\n" +
		`  >&2 echo 'fake herdr: --session missing'` + "\n" +
		`  exit 1` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		`  workspace)` + "\n" +
		`    if [ "$2" = "list" ]; then` + "\n" +
		`      cat <<'JSON'` + "\n" +
		`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":` +
		workspaceJSON + `}}` + "\n" +
		"JSON\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "close" ]; then` + "\n" +
		`      echo "$3" >> "` + dir + `/closed-workspaces"` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunPrune_DryRunListsCandidates(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdrPrune(t, tmp, `[{"label":"a1b2c3","workspace_id":"wEmpty","tab_count":0,"agent_status":"none"},{"label":"a1b2c3","workspace_id":"wBusy","tab_count":2,"agent_status":"none"}]`)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   false,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	// The hometag for homeDir will be some hex string. Since we use "a1b2c3" in the
	// fake, it won't match the actual hometag, so both workspaces are "keep".
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
	if result.ToClose != 0 {
		t.Errorf("ToClose = %d, want 0 (no hometag match)", result.ToClose)
	}

	// Verify no close was attempted (dry-run).
	if _, err := os.Stat(filepath.Join(tmp, "closed-workspaces")); err == nil {
		t.Error("workspace close was called in dry-run mode")
	}
}

func TestRunPrune_ApplyClosesHometagMatch(t *testing.T) {
	tmp := t.TempDir()

	// Create a home directory and compute its hometag.
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)

	// Compute the actual hometag, then inject it into the fake.
	tag := Hometag(homeDir)
	wsJSON := `[{"label":"` + tag + `","workspace_id":"wMatch","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   true,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
	if result.Closed != 1 {
		t.Errorf("Closed = %d, want 1", result.Closed)
	}
	if len(result.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1", len(result.Workspaces))
	}
	if result.Workspaces[0].Action != "closed" {
		t.Errorf("Action = %q, want closed", result.Workspaces[0].Action)
	}

	// Verify close was called.
	data, err := os.ReadFile(filepath.Join(tmp, "closed-workspaces"))
	if err != nil {
		t.Fatalf("reading closed-workspaces trace: %v", err)
	}
	if strings.TrimSpace(string(data)) != "wMatch" {
		t.Errorf("closed workspace id = %q, want wMatch", strings.TrimSpace(string(data)))
	}
}

func TestRunPrune_LiveAgentSkipped(t *testing.T) {
	for _, status := range []string{"working", "idle", "blocked"} {
		t.Run("agent_"+status, func(t *testing.T) {
			subTmp := t.TempDir()
			homeDir := filepath.Join(subTmp, "home")
			os.MkdirAll(homeDir, 0755)
			tag := Hometag(homeDir)
			wsJSON := `[{"label":"` + tag + `","workspace_id":"wLive","tab_count":0,"agent_status":"` + status + `"}]`
			fakePath := writeFakeHerdrPrune(t, subTmp, wsJSON)
			oldPath := os.Getenv("PATH")
			t.Setenv("PATH", fakePath+":"+oldPath)

			result, err := RunPrune(PruneOptions{
				Session: "test-session",
				Apply:   true,
				HomeDir: homeDir,
			})
			if err != nil {
				t.Fatalf("RunPrune failed: %v", err)
			}

			if len(result.Workspaces) != 1 {
				t.Fatalf("len(Workspaces) = %d, want 1", len(result.Workspaces))
			}
			if result.Workspaces[0].Action != "skip" {
				t.Errorf("Action for agent_status=%q = %q, want skip", status, result.Workspaces[0].Action)
			}
			if !strings.Contains(result.Workspaces[0].Reason, status) {
				t.Errorf("Reason = %q, should mention status %q", result.Workspaces[0].Reason, status)
			}
			if result.Closed != 0 {
				t.Errorf("Closed = %d, want 0", result.Closed)
			}
		})
	}
}

func TestRunPrune_NonHometagLabelKept(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)

	// Workspace with a label that doesn't match the hometag.
	wsJSON := `[{"label":"non-hometag-label","workspace_id":"wForeign","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   true,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if len(result.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1", len(result.Workspaces))
	}
	if result.Workspaces[0].Action != "keep" {
		t.Errorf("Action = %q, want keep", result.Workspaces[0].Action)
	}
	if !strings.Contains(result.Workspaces[0].Reason, "not owned by this home") {
		t.Errorf("Reason = %q, should mention not owned", result.Workspaces[0].Reason)
	}
	if result.Closed != 0 {
		t.Errorf("Closed = %d, want 0", result.Closed)
	}
}

func TestRunPrune_TabCountGtZeroKept(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)
	tag := Hometag(homeDir)
	wsJSON := `[{"label":"` + tag + `","workspace_id":"wBusy","tab_count":2,"agent_status":"none"}]
`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   true,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if len(result.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1", len(result.Workspaces))
	}
	if result.Workspaces[0].Action != "keep" {
		t.Errorf("Action = %q, want keep", result.Workspaces[0].Action)
	}
	if !strings.Contains(result.Workspaces[0].Reason, "tab") {
		t.Errorf("Reason = %q, should mention tab count", result.Workspaces[0].Reason)
	}
	if result.Closed != 0 {
		t.Errorf("Closed = %d, want 0", result.Closed)
	}
}

func TestRunPrune_ApplyNoMatchIsNoop(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)

	// Workspace with a label that won't match the hometag.
	wsJSON := `[{"label":"not-matching","workspace_id":"wNope","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   true,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if result.Closed != 0 {
		t.Errorf("Closed = %d, want 0 (no matching workspace)", result.Closed)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}

	// Verify no close was called.
	if _, err := os.Stat(filepath.Join(tmp, "closed-workspaces")); err == nil {
		t.Error("workspace close was called but no workspace matched")
	}
}

func TestRunPrune_MetaReferencedSkipped(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)
	tag := Hometag(homeDir)

	// Write a meta file that references a workspace.
	metaContent := "herdr_workspace_id=wReferenced\nbackend=herdr\n"
	if err := os.WriteFile(filepath.Join(stateDir, "task-1.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	wsJSON := `[{"label":"` + tag + `","workspace_id":"wReferenced","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   true,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if len(result.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1", len(result.Workspaces))
	}
	if result.Workspaces[0].Action != "skip" {
		t.Errorf("Action = %q, want skip (meta-referenced)", result.Workspaces[0].Action)
	}
	if !strings.Contains(result.Workspaces[0].Reason, "task meta") {
		t.Errorf("Reason = %q, should mention task meta reference", result.Workspaces[0].Reason)
	}
	if result.Closed != 0 {
		t.Errorf("Closed = %d, want 0", result.Closed)
	}
}

func TestRunPrune_MixedWorkspaces(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)
	tag := Hometag(homeDir)

	// A mix of workspaces: matching+empty, matching+live, non-matching, matching+not-empty.
	ws := `[{"label":"protected-legacy","workspace_id":"w1","tab_count":0,"agent_status":"none"},` +
		`{"label":"` + tag + `","workspace_id":"w2","tab_count":0,"agent_status":"none"},` +
		`{"label":"` + tag + `","workspace_id":"w3","tab_count":0,"agent_status":"working"},` +
		`{"label":"` + tag + `","workspace_id":"w4","tab_count":3,"agent_status":"none"},` +
		`{"label":"other-tag","workspace_id":"w5","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, ws)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   true,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if result.Total != 5 {
		t.Errorf("Total = %d, want 5", result.Total)
	}

	// Check specific actions.
	actions := make(map[string]string)
	for _, w := range result.Workspaces {
		actions[w.WorkspaceID] = w.Action
	}

	// w1: label "protected-legacy", doesn't match hometag → keep
	if a := actions["w1"]; a != "keep" {
		t.Errorf("w1 action = %q, want keep (non-matching label)", a)
	}

	// w2: hometag match, empty, no agent → closed
	if a := actions["w2"]; a != "closed" {
		t.Errorf("w2 action = %q, want closed", a)
	}

	// w3: hometag match, empty, live agent → skip
	if a := actions["w3"]; a != "skip" {
		t.Errorf("w3 action = %q, want skip (live agent)", a)
	}

	// w4: hometag match, has tabs → keep
	if a := actions["w4"]; a != "keep" {
		t.Errorf("w4 action = %q, want keep (has tabs)", a)
	}

	// w5: non-matching label → keep
	if a := actions["w5"]; a != "keep" {
		t.Errorf("w5 action = %q, want keep (non-matching label)", a)
	}

	if result.Closed != 1 {
		t.Errorf("Closed = %d, want 1", result.Closed)
	}
}

func TestDenyListedLabel(t *testing.T) {
	tests := []struct {
		label  string
		denied bool
	}{
		{"default", true},
		{"default-something", false}, // exact match only
		{"legacy-home", false},       // not a deny-listed label
		{"captain-abc", false},
		{"captain-", false},
		{"general", false}, // needs hyphen prefix
		{"other", false},
		{"", false},
	}
	for _, tt := range tests {
		got := denyListedLabel(tt.label)
		if got != tt.denied {
			t.Errorf("denyListedLabel(%q) = %v, want %v", tt.label, got, tt.denied)
		}
	}
}

func TestIsLiveAgent(t *testing.T) {
	tests := []struct {
		status string
		live   bool
	}{
		{"working", true},
		{"idle", true},
		{"done", true},
		{"blocked", true},
		{"unknown", true},
		{"none", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isLiveAgent(tt.status)
		if got != tt.live {
			t.Errorf("isLiveAgent(%q) = %v, want %v", tt.status, got, tt.live)
		}
	}
}

func TestRunPrune_DryRunDoesNotClose(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)
	tag := Hometag(homeDir)
	wsJSON := `[{"label":"` + tag + `","workspace_id":"wDry","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	// Dry-run
	result, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   false,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}

	if len(result.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1", len(result.Workspaces))
	}

	// Should be "would_close", not "closed"
	if result.Workspaces[0].Action != "would_close" {
		t.Errorf("Action in dry-run = %q, want would_close", result.Workspaces[0].Action)
	}
	if result.Closed != 0 {
		t.Errorf("Closed = %d, want 0 in dry-run", result.Closed)
	}
	if result.ToClose != 1 {
		t.Errorf("ToClose = %d, want 1 in dry-run", result.ToClose)
	}

	// Verify no close was actually called.
	if _, err := os.Stat(filepath.Join(tmp, "closed-workspaces")); err == nil {
		t.Error("workspace close was called in dry-run mode")
	}
}

func TestRunPrune_WithSession(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, "home")
	os.MkdirAll(homeDir, 0755)

	wsJSON := `[{"label":"some-tag","workspace_id":"wDef","tab_count":0,"agent_status":"none"}]`
	fakePath := writeFakeHerdrPrune(t, tmp, wsJSON)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	_, err := RunPrune(PruneOptions{
		Session: "test-session",
		Apply:   false,
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("RunPrune failed: %v", err)
	}
}

func TestDenyListedLabelWithMatchingTag(t *testing.T) {
	// Test the deny list functions directly since we can't force a specific hometag.
	if !denyListedLabel("default") {
		t.Error("denyListedLabel('default') = false")
	}
	if denyListedLabel("captain-anything") {
		t.Error("denyListedLabel('captain-anything') = true; captain labels are owned+live-meta protected")
	}
	if denyListedLabel("some-other-label") {
		t.Error("denyListedLabel('some-other-label') = true")
	}
}
