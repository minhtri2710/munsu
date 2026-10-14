//go:build windows

package orchestrator

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestCollectStatusIDsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := home.AppendStatus(tmp, "captain:foo", "done: shipped"); err != nil {
		t.Fatal(err)
	}
	ids := collectStatusIDs(home.StateDir(tmp))
	if _, ok := ids["captain:foo"]; !ok {
		t.Errorf("collectStatusIDs missing logical id captain:foo (got %v)", ids)
	}
	if _, ok := ids["captain%3Afoo"]; ok {
		t.Errorf("collectStatusIDs surfaced encoded stem captain%%3Afoo (got %v)", ids)
	}
}

func TestScanFleetWithProbeCheckPath(t *testing.T) {
	tmp := t.TempDir()
	seedBoundSoldier(t, tmp, "ship-1", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@win"})
	reasons := scanFleetWithProbe(tmp, false, nil, testTaskStatePort{}, testSeatLiveness, nil)
	if len(reasons) == 0 {
		t.Fatalf("scanFleetWithProbe returned no reasons for bound ship-1")
	}
	if reasons[0].Kind != "check" || len(reasons[0].TaskIDs) != 1 || reasons[0].TaskIDs[0] != "ship-1" {
		t.Errorf("scanFleetWithProbe reason = %+v, want check reason for ship-1", reasons[0])
	}
}
