package fleet

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestHandoffFailureLeavesTaskAuthorityUnchanged(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteTaskAggregate(parent, mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: "TASK-1", Generation: "7", Current: true, Owner: "general", Definition: "Preserve authority", State: "queued", Kind: "ship", Project: "munsu"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(parent, "TASK-1", map[string]string{"description": "Preserve authority", "kind": "ship", "project": "munsu", "issue": "#384", "delivery_plan": "no-mistakes", "context_hints": "read AGENTS.md", "generation": "7"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(parent, "TASK-1", "queued: ready for handoff"); err != nil {
		t.Fatal(err)
	}
	beforeAgg := mustReadFleetTestFile(t, filepath.Join(parent, "state", ".task-authority", "aggregates", "TASK-1", "7.json"))
	beforeCurrent := mustReadFleetTestFile(t, filepath.Join(parent, "state", ".task-authority", "aggregates", "TASK-1", "current"))
	beforeMeta := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.meta"))
	beforeStatus := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.status"))

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) {
		return filepath.Join(parent, "missing-tasks-axi"), nil
	}

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected handoff failure")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", ".task-authority", "aggregates", "TASK-1", "7.json")); !bytes.Equal(got, beforeAgg) {
		t.Fatal("source aggregate changed after failed handoff")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", ".task-authority", "aggregates", "TASK-1", "current")); !bytes.Equal(got, beforeCurrent) {
		t.Fatal("source current pointer changed after failed handoff")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.meta")); !bytes.Equal(got, beforeMeta) {
		t.Fatal("source meta changed after failed handoff")
	}
	if got := mustReadFleetTestFile(t, filepath.Join(parent, "state", "TASK-1.status")); !bytes.Equal(got, beforeStatus) {
		t.Fatal("source status changed after failed handoff")
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || ok {
		t.Fatalf("destination aggregate ok=%v err=%v, want none", ok, err)
	}
	if _, err := mhome.ReadMeta(sm, "TASK-1"); err == nil {
		t.Fatal("destination meta exists after failed handoff")
	}
}

func TestHandoffProjectionPreflightFailureLeavesTaskAuthorityUnchanged(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteTaskAggregate(parent, mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: "TASK-1", Generation: "7", Current: true, Owner: "general", Definition: "Preserve authority", State: "queued", Kind: "ship", Project: "munsu"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(parent, "TASK-1", map[string]string{"description": "Preserve authority", "generation": "7"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(sm, "TASK-1", map[string]string{"description": "stale destination projection"}); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) { return fakeQueuedTasksAxi(t, parent), nil }

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected projection collision failure")
	}
	sourceAgg, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1")
	if err != nil || !ok || sourceAgg.Owner != "general" || sourceAgg.Generation != "7" {
		t.Fatalf("source aggregate = %+v ok=%v err=%v", sourceAgg, ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || ok {
		t.Fatalf("destination aggregate ok=%v err=%v, want none", ok, err)
	}
	sourceMeta, err := mhome.ReadMeta(parent, "TASK-1")
	if err != nil || sourceMeta["description"] != "Preserve authority" {
		t.Fatalf("source meta = %+v err=%v", sourceMeta, err)
	}
}

func TestHandoffFailpointsRestoreBacklogsAndAllTaskAuthority(t *testing.T) {
	for _, failpoint := range []string{"after-backlog", "after-first-projection", "between-tasks"} {
		t.Run(failpoint, func(t *testing.T) {
			parent := t.TempDir()
			sm := filepath.Join(parent, "captains", "test-sm")
			if err := os.MkdirAll(sm, 0755); err != nil {
				t.Fatal(err)
			}
			if err := SeedProvenance(sm, "test-sm"); err != nil {
				t.Fatal(err)
			}
			writeHandoffTaskFixture(t, parent, "TASK-1")
			writeHandoffTaskFixture(t, parent, "TASK-2")
			if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
				t.Fatal(err)
			}
			srcBacklog := filepath.Join(parent, "data", "backlog.md")
			dstBacklog := filepath.Join(sm, "data", "backlog.md")
			if err := os.WriteFile(srcBacklog, []byte("# Backlog\n\n- [ ] TASK-1 - work 1\n- [ ] TASK-2 - work 2\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dstBacklog, []byte("# Backlog\n\n"), 0644); err != nil {
				t.Fatal(err)
			}
			beforeSrcBacklog := mustReadFleetTestFile(t, srcBacklog)
			beforeDstBacklog := mustReadFleetTestFile(t, dstBacklog)

			origPath := captainLookPath
			origBackend := isTasksAxiBackend
			origFailpoint := handoffFailpoint
			defer func() {
				captainLookPath = origPath
				isTasksAxiBackend = origBackend
				handoffFailpoint = origFailpoint
			}()
			isTasksAxiBackend = func(string) bool { return true }
			captainLookPath = func(name string) (string, error) { return fakeMovingTasksAxi(t, parent), nil }
			handoffFailpoint = failpoint

			err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"})
			if err == nil {
				t.Fatal("expected injected handoff failure")
			}
			if got := mustReadFleetTestFile(t, srcBacklog); !bytes.Equal(got, beforeSrcBacklog) {
				t.Fatalf("source backlog not restored for %s\n%s", failpoint, got)
			}
			if got := mustReadFleetTestFile(t, dstBacklog); !bytes.Equal(got, beforeDstBacklog) {
				t.Fatalf("destination backlog not restored for %s\n%s", failpoint, got)
			}
			for _, taskID := range []string{"TASK-1", "TASK-2"} {
				sourceAgg, ok, err := mhome.ReadCurrentTaskAggregate(parent, taskID)
				if err != nil || !ok || sourceAgg.Owner != "general" || sourceAgg.Generation != "7" {
					t.Fatalf("source aggregate %s = %+v ok=%v err=%v", taskID, sourceAgg, ok, err)
				}
				if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, taskID); err != nil || ok {
					t.Fatalf("destination aggregate %s ok=%v err=%v, want none", taskID, ok, err)
				}
				if _, err := mhome.ReadMeta(parent, taskID); err != nil {
					t.Fatalf("source meta %s missing after rollback: %v", taskID, err)
				}
				if _, err := mhome.ReadMeta(sm, taskID); err == nil {
					t.Fatalf("destination meta %s visible after rollback", taskID)
				}
			}
		})
	}
}

func TestHandoffTransfersCompleteTaskGenerationAndDestinationIsReady(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	aggregate := mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: "TASK-1", Generation: "7", Current: true, Owner: "general", Definition: "Implement handoff", State: "queued", StateDetail: "ready", Kind: "ship", Project: "munsu", Projections: []mhome.TaskAggregateEvidence{{Kind: "backlog", Path: "data/backlog.md", Field: "description", Value: "Implement handoff"}}, AuditSources: []mhome.TaskAggregateEvidence{{Kind: "meta", Path: "state/TASK-1.meta", Field: "issue", Value: "#384"}}}
	if err := mhome.WriteTaskAggregate(parent, aggregate); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"description": "Implement handoff", "kind": "ship", "project": "munsu", "repo": "munsu", "issue": "#384", "issue_links": "#384,#383", "delivery_plan": "no-mistakes", "context_hints": "read AGENTS.md", "generation": "7"}
	if err := mhome.WriteMeta(parent, "TASK-1", meta); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(parent, "TASK-1", "queued: ready"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(parent, "TASK-1")), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(parent, "TASK-1"), []byte("# Implement handoff\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - Implement handoff\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sm, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origPath := captainLookPath
	origBackend := isTasksAxiBackend
	defer func() {
		captainLookPath = origPath
		isTasksAxiBackend = origBackend
	}()
	isTasksAxiBackend = func(string) bool { return true }
	captainLookPath = func(name string) (string, error) { return fakeMovingTasksAxi(t, parent), nil }

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1"); err != nil || ok {
		t.Fatalf("source aggregate ok=%v err=%v, want moved", ok, err)
	}
	got, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1")
	if err != nil || !ok {
		t.Fatalf("destination aggregate ok=%v err=%v", ok, err)
	}
	wantAggregate := aggregate
	wantAggregate.Owner = "captain:test-sm"
	if !reflect.DeepEqual(*got, wantAggregate) {
		t.Fatalf("destination aggregate = %+v, want %+v", got, wantAggregate)
	}
	dstMeta, err := mhome.ReadMeta(sm, "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range meta {
		if dstMeta[key] != want {
			t.Fatalf("destination meta[%s] = %q, want %q", key, dstMeta[key], want)
		}
	}
	status, err := mhome.ReadStatus(sm, "TASK-1")
	if err != nil || len(status) != 1 || status[0] != "queued: ready" {
		t.Fatalf("destination status = %#v err=%v", status, err)
	}
	if _, err := mhome.ReadMeta(parent, "TASK-1"); err == nil {
		t.Fatal("source meta still exists after successful handoff")
	}
	if !Exists(sm, "TASK-1") {
		t.Fatal("destination brief is not ready for spawn preflight")
	}
	shown, ok, showErr := mhome.ReadCurrentTaskAggregate(sm, "TASK-1")
	if showErr != nil || !ok || shown.Definition != "Implement handoff" {
		t.Fatalf("destination show seam aggregate = %+v ok=%v err=%v", shown, ok, showErr)
	}
	if err := Scaffold(ScaffoldOptions{HomeDir: sm, ID: "TASK-1", Repo: "munsu", Mode: "local-only"}); err != nil {
		t.Fatalf("destination brief render failed: %v", err)
	}
	runner := NewRunner(Args{ID: "TASK-1", ProjectName: "munsu", HomeDir: sm, Mode: "local-only"})
	runner.homeDir = sm
	runner.effectiveMode = "local-only"
	if err := runner.preflightBrief(); err != nil {
		t.Fatalf("destination brief preflight failed: %v", err)
	}
	if err := runner.checkBacklogAuthority(); err != nil {
		t.Fatalf("destination backlog readiness failed: %v", err)
	}
	if err := runner.preflightDelivery(); err != nil {
		t.Fatalf("destination delivery preflight failed: %v", err)
	}
}

func writeHandoffTaskFixture(t *testing.T, homeDir, taskID string) {
	t.Helper()
	if err := mhome.WriteTaskAggregate(homeDir, mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: taskID, Generation: "7", Current: true, Owner: "general", Definition: taskID, State: "queued", Kind: "ship", Project: "munsu"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(homeDir, taskID, map[string]string{"description": taskID, "generation": "7"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(homeDir, taskID, "queued: ready"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(homeDir, taskID)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(homeDir, taskID), []byte("# "+taskID+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func fakeQueuedTasksAxi(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-tasks-axi")
	script := "#!/bin/sh\nif [ \"$1\" = show ]; then echo 'state: queued'; exit 0; fi\nif [ \"$1\" = mv ]; then exit 0; fi\necho unexpected >&2\nexit 2\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeMovingTasksAxi(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-moving-tasks-axi")
	script := `#!/bin/sh
if [ "$1" = show ]; then
  echo 'state: queued'
  exit 0
fi
if [ "$1" = mv ]; then
  to=""
  file=""
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --to) to="$2"; shift 2 ;;
      --file) file="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  cp "$file" "$file.tmp"
  cp "$to" "$to.tmp"
  printf '# Backlog\n\n' > "$file"
  cat "$file.tmp" >> "$to"
  rm -f "$file.tmp" "$to.tmp"
  exit 0
fi
echo unexpected >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustReadFleetTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
