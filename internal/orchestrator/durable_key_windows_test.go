//go:build windows

package orchestrator

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
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
	if err := home.WriteMeta(tmp, "ship:1", map[string]string{"kind": "ship", "window": "@win"}); err != nil {
		t.Fatal(err)
	}
	reasons := scanFleetWithProbe(tmp, false, nil, testTaskStatePort{}, nil)
	if len(reasons) == 0 {
		t.Fatalf("scanFleetWithProbe returned no reasons for encoded ship:1 meta")
	}
	if reasons[0].Kind != "check" || len(reasons[0].TaskIDs) != 1 || reasons[0].TaskIDs[0] != "ship:1" {
		t.Errorf("scanFleetWithProbe reason = %+v, want check reason for logical id ship:1", reasons[0])
	}
}

func TestPeekFleetRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := home.WriteMeta(tmp, "ship:1", map[string]string{"kind": "ship", "window": "@win"}); err != nil {
		t.Fatal(err)
	}
	peek, err := peekFleet(tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if peek.InFlight != 1 || peek.Alive != 1 {
		t.Errorf("peekFleet counts = %+v, want in_flight 1 alive 1", peek)
	}
	if len(peek.Phases) != 1 || peek.Phases[0].ID != "ship:1" {
		t.Errorf("peekFleet phases = %+v, want one phase for logical id ship:1", peek.Phases)
	}
}
