package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGuardExecutable(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

func TestGuardBurnDownHerdrEventWaitRejectsEmptyEndpoint(t *testing.T) {
	src := &HerdrEventSource{}
	src.once.Do(func() {
		src.info = CapabilityInfo{
			State: HerdrReady,
			Flags: HerdrCapabilityFlags{CapAgentWait: true},
		}
	})

	_, err := src.Wait(context.Background(), EndpointRef{}, "")
	if err == nil || !errors.Is(err, ErrEventUnavailable) || !strings.Contains(err.Error(), "endpoint handle") {
		t.Fatalf("Wait error = %v, want empty-endpoint refusal", err)
	}
}

func TestGuardBurnDownCmuxEmptyWorkspaceIdentity(t *testing.T) {
	c := newCmuxBackend()
	_, err := c.checkWorkspaceAlive("")
	if err == nil || !strings.Contains(err.Error(), "empty workspace identity") {
		t.Fatalf("checkWorkspaceAlive error = %v, want empty-workspace refusal", err)
	}
}

func TestGuardBurnDownCmuxNewWindowRefusals(t *testing.T) {
	tests := []struct {
		name       string
		scriptJSON string
		existing   bool
		want       string
	}{
		{
			name:       "split surface",
			scriptJSON: `{"result":{"surface_id":""}}`,
			existing:   true,
			want:       "empty surface_id",
		},
		{
			name:       "new workspace id",
			scriptJSON: `{"result":{"workspace_id":"","surface_id":"surface"}}`,
			want:       "empty workspace_id",
		},
		{
			name:       "new workspace surface",
			scriptJSON: `{"result":{"workspace_id":"workspace","surface_id":""}}`,
			want:       "empty surface_id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeGuardExecutable(t, "cmux", "#!/bin/sh\ncase \"$1\" in\nselect-workspace|new-split|new-workspace) exit 0 ;;\nidentify) printf '%s\\n' '"+tt.scriptJSON+"' ;;\nesac\n")
			c := newCmuxBackend()
			if tt.existing {
				c.sessions["session"] = "workspace"
			}
			_, err := c.NewWindow("session", "task")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewWindow error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGuardBurnDownHerdrFindOrCreateRefusesPaneWithoutIdentity(t *testing.T) {
	writeGuardExecutable(t, "herdr", `#!/bin/sh
if [ "$1" = "--session" ]; then shift 2; fi
case "$1 $2" in
"workspace list") echo '{"result":{"workspaces":[{"label":"workspace","workspace_id":"ws-1"}]}}' ;;
"tab list") echo '{"result":{"tabs":[{"label":"task","tab_id":"tab-1"}]}}' ;;
"pane list") echo '{"result":{"panes":[{"tab_id":"tab-1","pane_id":""}]}}' ;;
esac
`)

	h := NewHerdrBackend("session")
	_, err := h.FindOrCreateWindow("workspace", "task")
	if err == nil || !strings.Contains(err.Error(), "has no pane") {
		t.Fatalf("FindOrCreateWindow error = %v, want empty-pane refusal", err)
	}
}

func TestGuardBurnDownHerdrNewWindowRefusesEmptyPane(t *testing.T) {
	writeGuardExecutable(t, "herdr", `#!/bin/sh
if [ "$1" = "--session" ]; then shift 2; fi
case "$1 $2" in
"workspace list") echo '{"result":{"workspaces":[{"label":"workspace","workspace_id":"ws-1"}]}}' ;;
"tab create") echo '{"result":{"root_pane":{"pane_id":""},"tab":{"tab_id":"tab-1"}}}' ;;
esac
`)

	h := NewHerdrBackend("session")
	_, err := h.NewWindow("workspace", "task")
	if err == nil || !strings.Contains(err.Error(), "empty pane_id") {
		t.Fatalf("NewWindow error = %v, want empty-pane refusal", err)
	}
}

func TestGuardBurnDownHerdrProtocolVersionCachedFailure(t *testing.T) {
	h := &HerdrBackend{protocolCache: -1}
	_, err := h.protocolVersion()
	if err == nil || !strings.Contains(err.Error(), "probe failed previously") {
		t.Fatalf("protocolVersion error = %v, want cached-failure refusal", err)
	}
}

func TestGuardBurnDownOrcaNewWindowRefusals(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"container", `{"container_id":"","terminal_id":"terminal"}`, "empty container_id"},
		{"terminal", `{"container_id":"container","terminal_id":""}`, "empty terminal_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeGuardExecutable(t, "orca", "#!/bin/sh\nprintf '%s\\n' '"+tt.json+"'\n")
			_, err := NewOrcaBackend().NewWindow("session", "task")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewWindow error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGuardBurnDownGetWorktreeReservedRejectsEmptyReservation(t *testing.T) {
	_, err := GetWorktreeReserved("", "", false, "  ", false)
	if err == nil || !strings.Contains(err.Error(), "requires a reservation identity") {
		t.Fatalf("GetWorktreeReserved error = %v, want empty-reservation refusal", err)
	}
}

func TestGuardBurnDownGitWorktreeReturnRejectsMalformedGitFile(t *testing.T) {
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("not a gitdir\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := (&gitWorktreeProvider{}).Return(path); err == nil || !strings.Contains(err.Error(), "unexpected .git file format") {
		t.Fatalf("Return error = %v, want malformed-git-file refusal", err)
	}
}

func TestGuardBurnDownSelectProviderRejectsEmptyHome(t *testing.T) {
	t.Setenv("PATH", "/dev/null")
	_, err := selectProvider("")
	if err == nil || !strings.Contains(err.Error(), "homeDir is required") {
		t.Fatalf("selectProvider error = %v, want empty-home refusal", err)
	}
}

func TestGuardBurnDownTreehouseRecoveryRefusesReplacement(t *testing.T) {
	_, err := (&treehouseProvider{}).GetReserved("", false, "reservation", true)
	if err == nil || !errors.Is(err, ErrWorktreeReservationRecoveryUnsupported) {
		t.Fatalf("GetReserved error = %v, want unsupported-recovery refusal", err)
	}
}

func TestGuardBurnDownTreehouseGetRefusesEmptyPath(t *testing.T) {
	writeGuardExecutable(t, "treehouse", "#!/bin/sh\nexit 0\n")
	_, err := (&treehouseProvider{}).GetReserved(t.TempDir(), false, "reservation", false)
	if err == nil || !strings.Contains(err.Error(), "returned empty path") {
		t.Fatalf("GetReserved error = %v, want empty-path refusal", err)
	}
}
