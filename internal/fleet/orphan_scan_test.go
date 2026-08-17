package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeMarkerInventory struct {
	scan MarkerScan
	err  error
}

func (f fakeMarkerInventory) ListMarked() (MarkerScan, error) { return f.scan, f.err }

type fakeRunOracle struct {
	liveness map[int]RunLiveness
}

func (f fakeRunOracle) RunLiveness(_ string, process MarkedProcess) (RunLiveness, string) {
	return f.liveness[process.PID], "fake oracle"
}

func orphanFence(t *testing.T, scan MarkerScan, liveness map[int]RunLiveness, artifacts []WriterArtifact, processes []WriterProcess) CompositeWriterFence {
	t.Helper()
	return CompositeWriterFence{
		Artifacts: &fakeArtifactScanner{snapshots: [][]WriterArtifact{artifacts}},
		Processes: &fakeProcessInventory{snapshots: [][]WriterProcess{processes}},
		Verifier:  &fakeProcessVerifier{dead: true},
		Marked:    fakeMarkerInventory{scan: scan},
		Oracle:    fakeRunOracle{liveness: liveness},
	}
}

func TestInspectOrphansClassifiesAndNeverTerminates(t *testing.T) {
	home := t.TempDir()
	scan := MarkerScan{Total: 300, Marked: []MarkedProcess{
		{PID: 11, PPID: 1, PGID: 11, ExecutablePath: "/bin/dead", Markers: map[string]string{MarkerMulticaTask: "run-a"}},
		{PID: 12, PPID: 1, PGID: 12, ExecutablePath: "/bin/live", Markers: map[string]string{MarkerMulticaTask: "run-b"}},
		{PID: 13, PPID: 1, PGID: 13, ExecutablePath: "/bin/murky", Markers: map[string]string{MarkerMunsuTask: "task-c"}},
	}}
	liveness := map[int]RunLiveness{11: RunEnded, 12: RunAlive, 13: RunUnresolved}
	// "never terminates" is structural since ADR-0012: the fence carries no
	// process controller or endpoint drainer at all, only evidence sources.
	fence := orphanFence(t, scan, liveness, nil, nil)

	report, err := fence.InspectOrphans(home)
	if err != nil {
		t.Fatalf("InspectOrphans: %v", err)
	}
	if report.Scanned != 300 || report.Marked != 3 {
		t.Fatalf("expected 300 scanned and 3 marked, got %d and %d", report.Scanned, report.Marked)
	}
	if len(report.Garbage) != 1 || report.Garbage[0].PID != 11 || report.Garbage[0].Layer != LayerRuntimeRun {
		t.Fatalf("expected PID 11 as the only L0 garbage, got %+v", report.Garbage)
	}
	if len(report.Owned) != 1 || report.Owned[0].PID != 12 {
		t.Fatalf("expected PID 12 owned, got %+v", report.Owned)
	}
	if len(report.Unknown) != 1 || report.Unknown[0].PID != 13 || report.Unknown[0].Layer != LayerMunsuTask {
		t.Fatalf("an unresolved process must stay UNKNOWN and never fold into GARBAGE, got %+v", report.Unknown)
	}
	if report.Unknown[0].TaskID != "task-c" {
		t.Fatalf("expected the munsu task id on the L1 entry, got %q", report.Unknown[0].TaskID)
	}
}

func TestInspectOrphansKeepsMarkerVerdictOverWriterClaim(t *testing.T) {
	home := t.TempDir()
	claimed := artifact(home)
	running := process(home)
	scan := MarkerScan{Total: 10, Marked: []MarkedProcess{
		{PID: running.PID, PPID: 1, PGID: running.PID, Markers: map[string]string{MarkerMulticaTask: "run-a"}},
	}}
	fence := orphanFence(t, scan, map[int]RunLiveness{running.PID: RunEnded}, []WriterArtifact{claimed}, []WriterProcess{running})

	report, err := fence.InspectOrphans(home)
	if err != nil {
		t.Fatalf("InspectOrphans: %v", err)
	}
	if len(report.Garbage) != 1 || report.Garbage[0].PID != running.PID {
		t.Fatalf("a writer identity claim must not overturn the owning run's verdict, got %+v", report)
	}
	if report.Garbage[0].Kind != "watcher" {
		t.Fatalf("expected the writer kind folded onto the entry, got %q", report.Garbage[0].Kind)
	}
}

func TestInspectOrphansReportsUnclaimedWriterAndStaleArtifact(t *testing.T) {
	home := t.TempDir()
	stale := artifact(home)
	stale.PID = 99
	unclaimed := process(home)

	fence := orphanFence(t, MarkerScan{Total: 4}, nil, []WriterArtifact{stale}, []WriterProcess{unclaimed})
	report, err := fence.InspectOrphans(home)
	if err != nil {
		t.Fatalf("InspectOrphans: %v", err)
	}
	if len(report.Unknown) != 1 || report.Unknown[0].PID != unclaimed.PID || report.Unknown[0].Layer != LayerWriter {
		t.Fatalf("a writer process no artifact claims must be UNKNOWN, got %+v", report.Unknown)
	}
	if len(report.StaleArtifacts) != 1 || report.StaleArtifacts[0] != stale.Path {
		t.Fatalf("expected the verified-dead artifact reported as stale, got %v", report.StaleArtifacts)
	}
}

func TestInspectOrphansRequiresScanDependencies(t *testing.T) {
	if _, err := (CompositeWriterFence{}).InspectOrphans(t.TempDir()); err == nil {
		t.Fatal("expected a fence without a marker inventory or run oracle to fail closed")
	}
}

func TestNewRuntimeWriterFenceWiresOrphanScan(t *testing.T) {
	fence := NewRuntimeWriterFence()
	if fence.Marked == nil || fence.Oracle == nil {
		t.Fatal("the runtime fence must carry the marker inventory and run oracle the orphan scan needs")
	}
}

func TestRuntimeTmpdirOracleResolvesOnlyRunScopedDirectories(t *testing.T) {
	live := filepath.Join(t.TempDir(), "multica-task-live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		tmpdir string
		want   RunLiveness
	}{
		{"existing run directory", live, RunAlive},
		{"removed run directory", filepath.Join(t.TempDir(), "multica-task-gone"), RunEnded},
		{"directory outside the run-scoped convention", t.TempDir(), RunUnresolved},
		{"no tmpdir at all", "", RunUnresolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := runtimeTmpdirLiveness(MarkedProcess{Markers: map[string]string{MarkerMulticaTask: "run-a", MarkerTmpdir: tc.tmpdir}})
			if got != tc.want {
				t.Fatalf("expected %v, got %v (%s)", tc.want, got, reason)
			}
		})
	}
}
