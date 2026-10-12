package backend

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func writeGuardExecutable(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	testutil.WriteFakeExecutable(t, path, content)
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

func TestGuardBurnDownGetWorktreeReservedRejectsEmptyReservation(t *testing.T) {
	_, err := GetWorktreeReserved("", "", false, "  ", false)
	if err == nil || !strings.Contains(err.Error(), "requires a reservation identity") {
		t.Fatalf("GetWorktreeReserved error = %v, want empty-reservation refusal", err)
	}
}

func TestGuardBurnDownSelectProviderRejectsEmptyHome(t *testing.T) {
	t.Setenv("PATH", "/dev/null")
	_, err := selectProvider("")
	if err == nil || !strings.Contains(err.Error(), "homeDir is required") {
		t.Fatalf("selectProvider error = %v, want empty-home refusal", err)
	}
}

func TestGuardBurnDownTreehouseGetRefusesEmptyPath(t *testing.T) {
	writeGuardExecutable(t, "treehouse", "#!/bin/sh\nexit 0\n")
	_, err := (&treehouseProvider{}).GetReserved(t.TempDir(), false, "reservation", false)
	if err == nil || !strings.Contains(err.Error(), "returned empty path") {
		t.Fatalf("GetReserved error = %v, want empty-path refusal", err)
	}
}
