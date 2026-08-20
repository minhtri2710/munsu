//go:build windows

package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestLiveWorkspaceIDsFromTaskMetaRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := home.WriteMeta(tmp, "captain:cap", map[string]string{"herdr_workspace_id": "W1"}); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(tmp, "plain", map[string]string{"herdr_workspace_id": "W2"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.StateDir(tmp), "weird%2.meta"), []byte("herdr_workspace_id=W3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ids, err := liveWorkspaceIDsFromTaskMeta(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !ids["W1"] || !ids["W2"] {
		t.Errorf("liveWorkspaceIDsFromTaskMeta = %v, want {W1 W2}", ids)
	}
	if ids["W3"] {
		t.Errorf("liveWorkspaceIDsFromTaskMeta collected foreign malformed stem (got %v)", ids)
	}
	if len(ids) != 2 {
		t.Errorf("liveWorkspaceIDsFromTaskMeta = %v, want exactly 2 workspace ids", ids)
	}
}
