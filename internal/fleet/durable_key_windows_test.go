//go:build windows

package fleet

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func durableContainsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestSnapshotCaptainMetaLogicalID(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(tmp, "captain:test-sm", map[string]string{"kind": "captain", "sm_id": "test-sm"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Snapshot(tmp, SnapshotDependencies{CurrentState: NewCanonicalCurrentState()})
	if err != nil {
		t.Fatalf("Snapshot with encoded captain meta: %v", err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].ID != "captain:test-sm" || snap.Tasks[0].Kind != "captain" {
		t.Fatalf("snapshot tasks = %+v, want one captain:test-sm captain entry", snap.Tasks)
	}
}

func TestInFlightSoldierIDsRoundTrip(t *testing.T) {
	captainHome := t.TempDir()
	for _, tc := range []struct {
		id   string
		kind string
	}{
		{"ship:1", "ship"},
		{"scout:2", "scout"},
		{"chore:3", "other"},
	} {
		if err := home.WriteMeta(captainHome, tc.id, map[string]string{"kind": tc.kind}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := inFlightSoldierIDs(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ship:1", "scout:2"} {
		if !durableContainsID(ids, want) {
			t.Errorf("inFlightSoldierIDs missing logical id %q (got %v)", want, ids)
		}
	}
	if durableContainsID(ids, "chore:3") {
		t.Errorf("inFlightSoldierIDs included non-ship/scout logical id chore:3 (got %v)", ids)
	}
}

func TestOtherWorkspaceRefsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := home.WriteMeta(tmp, "captain:cap", map[string]string{"herdr_workspace_id": "W1"}); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(tmp, "task-x", map[string]string{"herdr_workspace_id": "W1"}); err != nil {
		t.Fatal(err)
	}
	refs := otherWorkspaceRefs(tmp, "task-x", "W1")
	if !durableContainsID(refs, "captain:cap") {
		t.Errorf("otherWorkspaceRefs missing logical id captain:cap (got %v)", refs)
	}
	if durableContainsID(refs, "task-x") {
		t.Errorf("otherWorkspaceRefs did not exclude logical id task-x (got %v)", refs)
	}
	if durableContainsID(refs, "captain%3Acap") {
		t.Errorf("otherWorkspaceRefs surfaced encoded stem captain%%3Acap (got %v)", refs)
	}
}

func TestCurrentEndpointKindRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "P1")
	t.Setenv("TMUX_PANE", "")
	tmp := t.TempDir()
	if err := home.WriteMeta(tmp, "captain:cap", map[string]string{"kind": "captain", "herdr_pane_id": "P1"}); err != nil {
		t.Fatal(err)
	}
	kind, found, err := currentEndpointKind(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !found || kind != "captain" {
		t.Errorf("currentEndpointKind = (%q, %v), want (captain, true)", kind, found)
	}
}
