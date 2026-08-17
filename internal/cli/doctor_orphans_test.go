package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
)

// The exit-code contract `munsu doctor --orphans` publishes to member scripts:
// 0 nothing to look at, 1 leftovers found, 2 nothing conclusive but a human
// should look. These are literals on purpose. Asserting against
// orphanExitGarbage / orphanExitNeedsMember instead makes both sides of the
// comparison move together, so the assertion restates the implementation and
// guards nothing — the identity BEO-75 was opened for.
const (
	documentedOrphanExitClean       = 0
	documentedOrphanExitGarbage     = 1
	documentedOrphanExitNeedsMember = 2
)

// orphanShellExit is the number a member's script actually sees: it reproduces
// cmd/munsu/main.go, which turns the scan's error into an exit status through
// WriteContractError. Tests assert on this rather than on contractError.status
// so the whole published path — error kind, contract wrapping, status — stays
// under the contract, not just the constant.
func orphanShellExit(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	code := WriteContractError(io.Discard, err, nil)
	if code == 0 {
		code = 1
	}
	return code
}

type stubMarkerInventory struct {
	scan fleet.MarkerScan
	err  error
}

func (s stubMarkerInventory) ListMarked() (fleet.MarkerScan, error) { return s.scan, s.err }

type stubRunOracle struct{ liveness fleet.RunLiveness }

func (s stubRunOracle) RunLiveness(string, fleet.MarkedProcess) (fleet.RunLiveness, string) {
	return s.liveness, "stub oracle"
}

func stubFence(scan fleet.MarkerScan, err error, liveness fleet.RunLiveness) fleet.CompositeWriterFence {
	return fleet.CompositeWriterFence{
		Marked: stubMarkerInventory{scan: scan, err: err},
		Oracle: stubRunOracle{liveness: liveness},
	}
}

func markedAt(pid int) fleet.MarkedProcess {
	return fleet.MarkedProcess{PID: pid, Markers: map[string]string{fleet.MarkerMulticaTask: "run-a"}}
}

func TestDoctorRegistersOrphanFlag(t *testing.T) {
	flag := newDoctorCmd().Flags().Lookup("orphans")
	if flag == nil {
		t.Fatal("doctor must expose --orphans")
	}
	if flag.Value.String() != "false" {
		t.Fatalf("the orphan scan must be opt-in, got default %q", flag.Value.String())
	}
	if !strings.Contains(flag.Usage, fmt.Sprintf("exit %d", documentedOrphanExitGarbage)) ||
		!strings.Contains(flag.Usage, fmt.Sprintf("%d nothing conclusive", documentedOrphanExitNeedsMember)) {
		t.Fatalf("the flag must document what its exit codes mean, got %q", flag.Usage)
	}
}

// TestDoctorOrphansFlagRunsTheScan drives `doctor --orphans` through cobra, the
// way a member does. Without it the flag can be registered and wired to
// nothing: the command would accept it, fall through to the ordinary doctor
// path, and never scan — silently.
func TestDoctorOrphansFlagRunsTheScan(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())

	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--orphans"})

	err := cmd.Execute()
	report := out.String()
	if !strings.Contains(report, "Orphan scan (home:") {
		t.Fatalf("`doctor --orphans` did not run the orphan scan, got:\n%s", report)
	}
	if strings.Contains(report, "Integration status:") {
		t.Fatalf("`doctor --orphans` fell through to the ordinary doctor path, got:\n%s", report)
	}
	// The machine running this test may hold real leftovers, so a non-nil
	// error is legitimate — it must still be a scan verdict and not a failure.
	if err != nil {
		var contract *contractError
		if !errors.As(err, &contract) {
			t.Fatalf("expected the scan verdict as a contract error, got %T: %v", err, err)
		}
		if got := orphanShellExit(t, err); got != documentedOrphanExitGarbage && got != documentedOrphanExitNeedsMember {
			t.Fatalf("a scan verdict must exit %d or %d, got %d",
				documentedOrphanExitGarbage, documentedOrphanExitNeedsMember, got)
		}
	}
}

// TestOrphanScanExitCodes pins all three exit paths, including the one a
// failing scan takes: a script must not read a broken scan as "garbage found".
// Every wantExit below is the documented number, not the constant the
// implementation happens to hold, so moving a constant turns this test red.
func TestOrphanScanExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		fence      fleet.CompositeWriterFence
		wantExit   int
		wantCode   string
		wantReport bool
	}{
		{
			name:       "no marked process exits clean",
			fence:      stubFence(fleet.MarkerScan{Total: 7}, nil, fleet.RunUnresolved),
			wantExit:   documentedOrphanExitClean,
			wantReport: true,
		},
		{
			name:       "marked processes whose runs are alive exit clean",
			fence:      stubFence(fleet.MarkerScan{Total: 7, Marked: []fleet.MarkedProcess{markedAt(10)}}, nil, fleet.RunAlive),
			wantExit:   documentedOrphanExitClean,
			wantReport: true,
		},
		{
			name:       "a leftover exits 1",
			fence:      stubFence(fleet.MarkerScan{Total: 7, Marked: []fleet.MarkedProcess{markedAt(11)}}, nil, fleet.RunEnded),
			wantExit:   documentedOrphanExitGarbage,
			wantCode:   "orphans_found",
			wantReport: true,
		},
		{
			name:       "only unresolved processes exit 2",
			fence:      stubFence(fleet.MarkerScan{Total: 7, Marked: []fleet.MarkedProcess{markedAt(12)}}, nil, fleet.RunUnresolved),
			wantExit:   documentedOrphanExitNeedsMember,
			wantCode:   "orphans_unresolved",
			wantReport: true,
		},
		{
			name:     "a failed scan exits 2, not 1",
			fence:    stubFence(fleet.MarkerScan{}, errors.New("process table unreadable"), fleet.RunUnresolved),
			wantExit: documentedOrphanExitNeedsMember,
			wantCode: "orphan_scan_failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runOrphanScanWith(&out, tc.fence, t.TempDir())
			if got := orphanShellExit(t, err); got != tc.wantExit {
				t.Fatalf("a member's script must see exit %d, got %d (err %v)", tc.wantExit, got, err)
			}
			if tc.wantCode != "" {
				var contract *contractError
				if !errors.As(err, &contract) {
					t.Fatalf("expected a contract error, got %T: %v", err, err)
				}
				if contract.value.Error.ErrorCode != tc.wantCode {
					t.Fatalf("expected error code %q, got %q", tc.wantCode, contract.value.Error.ErrorCode)
				}
			}
			if got := strings.Contains(out.String(), "Orphan scan (home:"); got != tc.wantReport {
				t.Fatalf("report written = %v, want %v; output:\n%s", got, tc.wantReport, out.String())
			}
		})
	}
}

// TestOrphanScanExitPrioritisesGarbageOverUnknown pins the priority decision so
// it stays a decision: a machine holding real leftovers signals 1 even when the
// same report also lists unresolved processes, and the report still names them.
func TestOrphanScanExitPrioritisesGarbageOverUnknown(t *testing.T) {
	scan := fleet.MarkerScan{Total: 9, Marked: []fleet.MarkedProcess{
		{PID: 21, Markers: map[string]string{fleet.MarkerMulticaTask: "run-a", fleet.MarkerTmpdir: "/nonexistent/multica-task-gone"}},
		{PID: 22, Markers: map[string]string{fleet.MarkerMulticaTask: "run-b"}},
	}}
	fence := fleet.CompositeWriterFence{Marked: stubMarkerInventory{scan: scan}, Oracle: fleet.OSRunOracle{}}

	var out bytes.Buffer
	err := runOrphanScanWith(&out, fence, t.TempDir())

	var contract *contractError
	if !errors.As(err, &contract) || orphanShellExit(t, err) != documentedOrphanExitGarbage {
		t.Fatalf("a report holding both groups must exit %d, got %v", documentedOrphanExitGarbage, err)
	}
	report := out.String()
	if !strings.Contains(report, "GARBAGE (1)") || !strings.Contains(report, "UNKNOWN (1)") {
		t.Fatalf("both groups must stay visible in the report, got:\n%s", report)
	}
	if !strings.Contains(contract.value.Error.Message, "1 unresolved") {
		t.Fatalf("the exit-1 message must still mention the unresolved ones, got %q", contract.value.Error.Message)
	}
}
