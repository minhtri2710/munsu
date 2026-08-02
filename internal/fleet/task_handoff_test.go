package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestHandoffCrashAfterJournalResumesWithoutPrecommitMutation(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		boundary := os.Getenv("MUNSU_HANDOFF_CRASH_AFTER")
		handoffCrashHook = func(got string) {
			if got == boundary {
				os.Exit(92)
			}
		}
		keys := []string{"TASK-1"}
		if os.Getenv("MUNSU_HANDOFF_CRASH_AFTER") != "journal" {
			keys = []string{"TASK-1", "TASK-2"}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), keys); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	writeHandoffTaskFixture(t, parent, "TASK-1")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	srcBacklog := filepath.Join(parent, "data", "backlog.md")
	dstBacklog := filepath.Join(sm, "data", "backlog.md")
	if err := os.WriteFile(srcBacklog, []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstBacklog, []byte("# Backlog\\n\\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(parent, "tasks-axi")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
if [ "$1" = show ]; then echo 'state: queued'; exit 0; fi
if [ "$1" = mv ]; then
  to=""; file=""
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
exit 2
`), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := captainLookPath
	oldBackend := isTasksAxiBackend
	captainLookPath = func(string) (string, error) { return fake, nil }
	isTasksAxiBackend = func(string) bool { return true }
	t.Cleanup(func() { captainLookPath = oldPath; isTasksAxiBackend = oldBackend })

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashAfterJournalResumesWithoutPrecommitMutation$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_CRASH_AFTER=journal",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+sm,
		"PATH="+filepath.Dir(fake)+":"+os.Getenv("PATH"),
		"MUNSU_HANDOFF_TASKS_AXI="+fake,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper subprocess to crash")
	} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 92 {
		t.Fatalf("helper exited at wrong boundary: %v\n%s", err, output)
	}

	journalRoot := filepath.Join(parent, "state", ".task-handoff")
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("reading durable handoff journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one crash-resumable journal", len(entries))
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1"); err != nil || !ok {
		t.Fatalf("source authority after precommit crash ok=%v err=%v", ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || ok {
		t.Fatalf("destination authority after precommit crash ok=%v err=%v", ok, err)
	}

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatalf("resume handoff: %v", err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1"); err != nil || ok {
		t.Fatalf("source authority after resume ok=%v err=%v", ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || !ok {
		t.Fatalf("destination authority after resume ok=%v err=%v", ok, err)
	}
	if entries, err := os.ReadDir(journalRoot); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("journal entries after resume = %d, want none", len(entries))
	}
}

func TestHandoffRecoveryRejectsCorruptStageAndRetainsJournal(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captain")
	if err := os.MkdirAll(filepath.Join(parent, "state", taskHandoffDirName, "tx", "stage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	stagePath := filepath.Join(parent, "state", taskHandoffDirName, "tx", "stage", "post")
	if err := os.WriteFile(stagePath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	journal := &taskHandoffJournal{
		Version: 2, ID: "tx", Phase: "commit-decided", SourceHome: parent, DestinationHome: captain,
		SourceBacklogPost: handoffFile{Target: "data/backlog.md", Stage: "stage/post", Exists: true, Mode: 0644, Size: 5, SHA256: digestHandoff([]byte("valid"))},
		DestBacklogPost:   handoffFile{Target: "data/backlog.md", Stage: "stage/post", Exists: true, Mode: 0644, Size: 5, SHA256: digestHandoff([]byte("valid"))},
	}
	if err := writeHandoffJournal(filepath.Join(parent, "state", taskHandoffDirName, "tx"), journal); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected corrupt stage recovery failure")
	}
	if _, err := os.Stat(filepath.Join(parent, "state", taskHandoffDirName, "tx", "journal.json")); err != nil {
		t.Fatalf("corrupt journal was not retained: %v", err)
	}
}

func TestHandoffRecoveryRejectsStaleSourceBeforeCommit(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captain")
	backlog := filepath.Join(parent, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlog), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlog, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	pre, err := inventoryHandoffFile(parent, backlog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlog, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	journal := &taskHandoffJournal{Version: 2, ID: "stale", Phase: "prepared", SourceHome: parent, DestinationHome: captain, SourceBacklogPre: pre}
	if err := os.MkdirAll(filepath.Join(parent, "state", taskHandoffDirName, "stale", "stage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeHandoffJournal(filepath.Join(parent, "state", taskHandoffDirName, "stale"), journal); err != nil {
		t.Fatal(err)
	}
	if err := revalidateHandoff(journal); err == nil {
		t.Fatal("expected stale source rejection")
	}
	if _, err := os.Stat(backlog); err != nil {
		t.Fatal(err)
	}
}

func TestHandoffLocksOppositeOrdersFailFastWithoutDeadlock(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	results := make(chan error, 2)
	go func() {
		unlock, err := acquireHandoffLocks(a, b)
		if unlock != nil {
			defer unlock()
		}
		results <- err
	}()
	go func() {
		unlock, err := acquireHandoffLocks(b, a)
		if unlock != nil {
			defer unlock()
		}
		results <- err
	}()
	for range 2 {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("opposite ordered handoff locks deadlocked")
		}
	}
}

func TestPublicDestinationWorkflowsRecoverCommittedHandoff(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captain, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set(captain, "parent-home", parent); err != nil {
		t.Fatal(err)
	}
	writeHandoffTaskFixture(t, parent, "TASK-1")
	writeHandoffTaskFixture(t, parent, "TASK-2")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captain, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	sourceBacklog := filepath.Join(parent, "data", "backlog.md")
	destinationBacklog := filepath.Join(captain, "data", "backlog.md")
	if err := os.WriteFile(sourceBacklog, []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n- [ ] TASK-2 - TASK-2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationBacklog, []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake := fakeMovingTasksAxi(t, parent)
	oldPath := captainLookPath
	oldBackend := isTasksAxiBackend
	captainLookPath = func(string) (string, error) { return fake, nil }
	isTasksAxiBackend = func(string) bool { return true }
	t.Cleanup(func() { captainLookPath = oldPath; isTasksAxiBackend = oldBackend })
	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashAfterJournalResumesWithoutPrecommitMutation$", "--")
	cmd.Env = append(os.Environ(), "MUNSU_HANDOFF_CRASH_HELPER=1", "MUNSU_HANDOFF_CRASH_AFTER=commit-decided", "MUNSU_HANDOFF_SOURCE="+parent, "MUNSU_HANDOFF_DEST="+captain, "MUNSU_HANDOFF_TASKS_AXI="+fake)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected committed handoff helper to crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	if err := Scaffold(ScaffoldOptions{HomeDir: captain, ID: "TASK-1", Repo: "munsu", Mode: "local-only"}); err != nil {
		t.Fatalf("scaffold recovery: %v", err)
	}
	runner := NewRunner(Args{ID: "TASK-1", ProjectName: "munsu", HomeDir: captain, Mode: "local-only"})
	runner.homeDir = captain
	runner.effectiveMode = "local-only"
	if err := runner.preflightBrief(); err != nil {
		t.Fatalf("brief preflight recovery: %v", err)
	}
	if err := runner.checkBacklogAuthority(); err != nil {
		t.Fatalf("backlog readiness recovery: %v", err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(captain, "TASK-1"); err != nil || !ok {
		t.Fatalf("destination authority after public recovery ok=%v err=%v", ok, err)
	}
}

func TestRecoverTaskHandoffsRejectsMalformedParentAndHeldLock(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "config", "parent-home"), []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTaskHandoffs(homeDir); err == nil {
		t.Fatal("expected malformed parent-home rejection")
	}
	parent := t.TempDir()
	captain := t.TempDir()
	unlock, err := acquireHandoffLocks(parent, captain)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := RecoverTaskHandoffs(parent); err == nil {
		t.Fatal("expected recovery to fail while source lock is held")
	}
}

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

func TestHandoffCrashBoundariesResumeToSingleDestinationAuthority(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		boundary := os.Getenv("MUNSU_HANDOFF_CRASH_AFTER")
		handoffCrashHook = func(got string) {
			if got == boundary {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1", "TASK-2"}); err == nil {
			os.Exit(0)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(91)
	}
	for _, boundary := range []string{"staging", "prepared", "commit-decided", "destination-receipt", "source-authority", "artifacts", "between-authority", "completed"} {
		t.Run(boundary, func(t *testing.T) {
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
			defer func() {
				captainLookPath = origPath
				isTasksAxiBackend = origBackend
			}()
			isTasksAxiBackend = func(string) bool { return true }
			fake := fakeMovingTasksAxi(t, parent)
			captainLookPath = func(name string) (string, error) { return fake, nil }
			cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffCrashBoundariesResumeToSingleDestinationAuthority$", "--")
			cmd.Env = append(os.Environ(), "MUNSU_HANDOFF_CRASH_HELPER=1", "MUNSU_HANDOFF_CRASH_AFTER="+boundary, "MUNSU_HANDOFF_SOURCE="+parent, "MUNSU_HANDOFF_DEST="+sm, "MUNSU_HANDOFF_TASKS_AXI="+fake)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatal("expected crash helper to exit abruptly")
			} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
				t.Fatalf("boundary %s exited unexpectedly: %v\\n%s", boundary, err, output)
			}
			if boundary == "artifacts" {
				for _, taskID := range []string{"TASK-1", "TASK-2"} {
					if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, taskID); err != nil || ok {
						t.Fatalf("destination authority published before artifact completion for %s: ok=%v err=%v", taskID, ok, err)
					}
				}
			}
			if boundary == "staging" || boundary == "prepared" {
				if got := mustReadFleetTestFile(t, srcBacklog); !bytes.Equal(got, beforeSrcBacklog) {
					t.Fatalf("precommit source backlog changed at %s", boundary)
				}
				if got := mustReadFleetTestFile(t, dstBacklog); !bytes.Equal(got, beforeDstBacklog) {
					t.Fatalf("precommit destination backlog changed at %s", boundary)
				}
				if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
					t.Fatalf("resume at %s: %v", boundary, err)
				}
			} else if err := RecoverTaskHandoffs(parent); err != nil {
				t.Fatalf("recover at %s: %v", boundary, err)
			}
			if got := mustReadFleetTestFile(t, dstBacklog); len(got) == 0 || bytes.Equal(got, beforeDstBacklog) {
				t.Fatalf("destination backlog did not converge at %s", boundary)
			}
			for _, taskID := range []string{"TASK-1", "TASK-2"} {
				if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, taskID); err != nil || ok {
					t.Fatalf("source aggregate %s remains at %s: ok=%v err=%v", taskID, boundary, ok, err)
				}
				if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, taskID); err != nil || !ok {
					t.Fatalf("destination aggregate %s missing at %s: ok=%v err=%v", taskID, boundary, ok, err)
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
	if got.DispatchInterpretationID == "" || got.DispatchInterpretationDigest == "" {
		t.Fatalf("destination aggregate lost dispatch interpretation binding: %+v", got)
	}
	interpretation, err := mhome.LoadDispatchInterpretation(sm, got.DispatchInterpretationID)
	if err != nil || interpretation.DependencySnapshotDigest != got.DispatchInterpretationDigest {
		t.Fatalf("destination interpretation = %+v err=%v", interpretation, err)
	}
	got.DispatchInterpretationID = ""
	got.DispatchInterpretationDigest = ""
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

// TestHandoffChildWithExistingParentDependencySucceeds pins reviewer probe E:
// handing off a child alone whose backlog blocked-by references an
// in-source parent must succeed. The parent exists in canonical source state
// but is outside the requested set, so the dependency edge is not material
// ambiguity.
func TestHandoffChildWithExistingParentDependencySucceeds(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	writeHandoffTaskFixture(t, parent, "TASK-1")
	writeHandoffTaskFixture(t, parent, "TASK-PARENT")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
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
	captainLookPath = func(name string) (string, error) { return fakeBlockedTasksAxi(t, parent), nil }

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatalf("handoff of child with in-source parent dependency failed: %v", err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || !ok {
		t.Fatalf("destination child ok=%v err=%v, want moved", ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-PARENT"); err != nil || !ok {
		t.Fatalf("source parent ok=%v err=%v, want retained", ok, err)
	}
}

// fakeBlockedTasksAxi reports a queued child whose backlog entry is blocked by
// TASK-PARENT, and moves like fakeMovingTasksAxi.
func fakeBlockedTasksAxi(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-blocked-tasks-axi")
	script := `#!/bin/sh
if [ "$1" = show ]; then
  echo 'state: queued'
  echo 'blocked-by: TASK-PARENT'
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

// TestHandoffIntentBindsTaskIdentityDurably crashes immediately after the
// durable journal (now carrying one validated transfer intent per task) is
// written and proves the intent binds source/destination home identity, Task
// ID, exact Generation, request digest, and both stable Operation IDs
// (ADR-0007 §10, Task 6.1 acceptance 1).
func TestHandoffIntentBindsTaskIdentityDurably(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		handoffCrashHook = func(got string) {
			if got == "journal" {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1", "TASK-2"}); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

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
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n- [ ] TASK-2 - TASK-2\n"), 0644); err != nil {
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
	fake := fakeMovingTasksAxi(t, parent)
	captainLookPath = func(name string) (string, error) { return fake, nil }

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffIntentBindsTaskIdentityDurably$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_CRASH_AFTER=journal",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+sm,
		"MUNSU_HANDOFF_TASKS_AXI="+fake,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper subprocess to crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	journalRoot := filepath.Join(parent, "state", ".task-handoff")
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatalf("reading durable handoff journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one crash-resumable journal", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(journalRoot, entries[0].Name(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal taskHandoffJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.Version != 2 {
		t.Fatalf("journal version = %d, want 2", journal.Version)
	}
	if len(journal.Tasks) != 2 {
		t.Fatalf("journal tasks = %d, want 2", len(journal.Tasks))
	}
	canonicalSource, err := canonicalHandoffHome(parent)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDest, err := canonicalHandoffHome(sm)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range journal.Tasks {
		intent := task.Intent
		if err := intent.Validate(); err != nil {
			t.Fatalf("intent for %s invalid: %v", task.ID, err)
		}
		if intent.SourceHome != canonicalSource || intent.DestinationHome != canonicalDest {
			t.Fatalf("intent homes = %s -> %s, want %s -> %s", intent.SourceHome, intent.DestinationHome, canonicalSource, canonicalDest)
		}
		if intent.TaskID != task.ID {
			t.Fatalf("intent task = %s, want %s", intent.TaskID, task.ID)
		}
		if intent.Generation.String() != "7" || intent.Generation.String() != task.Generation {
			t.Fatalf("intent generation = %s, want 7", intent.Generation)
		}
		wantDigest, err := taskauthority.TransferRequest{SourceHome: canonicalSource, DestinationHome: canonicalDest, TaskID: task.ID, Generation: 7}.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if intent.RequestDigest != wantDigest {
			t.Fatalf("intent digest = %s, want %s", intent.RequestDigest, wantDigest)
		}
		if !strings.HasPrefix(intent.SourceOperationID, "handoff-source-") || !strings.HasPrefix(intent.DestinationOperationID, "handoff-dest-") {
			t.Fatalf("intent operation ids = %s / %s, want handoff-source-/handoff-dest- prefixes", intent.SourceOperationID, intent.DestinationOperationID)
		}
		if intent.SourceOperationID == intent.DestinationOperationID {
			t.Fatal("source and destination operation ids must differ")
		}
	}

	// The crash happened before any source mutation: source ownership stays
	// current and the destination owns nothing.
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1"); err != nil || !ok {
		t.Fatalf("source TASK-1 after intent crash ok=%v err=%v", ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || ok {
		t.Fatalf("destination TASK-1 after intent crash ok=%v err=%v, want none", ok, err)
	}

	if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
		t.Fatalf("resume handoff: %v", err)
	}
	for _, taskID := range []string{"TASK-1", "TASK-2"} {
		if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, taskID); err != nil || ok {
			t.Fatalf("source %s remains ok=%v err=%v", taskID, ok, err)
		}
		if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, taskID); err != nil || !ok {
			t.Fatalf("destination %s missing ok=%v err=%v", taskID, ok, err)
		}
	}
}

// TestHandoffSourcePreservedUntilDestinationReceipt crashes after the
// destination receipt is durably committed and before any source mutation,
// and proves source ownership remains current until the destination receipt
// is durable (ADR-0007 §10, Task 6.1 acceptance 3).
func TestHandoffSourcePreservedUntilDestinationReceipt(t *testing.T) {
	if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
		captainLookPath = func(string) (string, error) { return os.Getenv("MUNSU_HANDOFF_TASKS_AXI"), nil }
		handoffCrashHook = func(got string) {
			if got == "destination-receipt" {
				os.Exit(92)
			}
		}
		if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1"}); err == nil {
			os.Exit(0)
		}
		os.Exit(91)
	}

	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	writeHandoffTaskFixture(t, parent, "TASK-1")
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sm, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - TASK-1\n"), 0644); err != nil {
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
	fake := fakeMovingTasksAxi(t, parent)
	captainLookPath = func(name string) (string, error) { return fake, nil }

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffSourcePreservedUntilDestinationReceipt$", "--")
	cmd.Env = append(os.Environ(),
		"MUNSU_HANDOFF_CRASH_HELPER=1",
		"MUNSU_HANDOFF_CRASH_AFTER=destination-receipt",
		"MUNSU_HANDOFF_SOURCE="+parent,
		"MUNSU_HANDOFF_DEST="+sm,
		"MUNSU_HANDOFF_TASKS_AXI="+fake,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper subprocess to crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	// Source ownership is still current: the destination receipt is durable
	// but the source aggregate has not been mutated.
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1"); err != nil || !ok {
		t.Fatalf("source ownership lost before destination receipt: ok=%v err=%v", ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || ok {
		t.Fatalf("destination authority published before its receipt: ok=%v err=%v", ok, err)
	}
	journalRoot := filepath.Join(parent, "state", ".task-handoff")
	entries, err := os.ReadDir(journalRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want one", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(journalRoot, entries[0].Name(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal taskHandoffJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.Phase != "commit-decided" {
		t.Fatalf("journal phase = %q, want commit-decided", journal.Phase)
	}
	if len(journal.Tasks) != 1 {
		t.Fatalf("journal tasks = %d, want 1", len(journal.Tasks))
	}
	intent := journal.Tasks[0].Intent
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	receiptPath := taskTransferReceiptPath(sm, intent.DestinationOperationID)
	receiptData, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("destination receipt not durable: %v", err)
	}
	var receipt taskTransferReceipt
	if err := json.Unmarshal(receiptData, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.OperationID != intent.DestinationOperationID || receipt.Digest != intent.RequestDigest ||
		receipt.TaskID != "TASK-1" || receipt.Generation != "7" {
		t.Fatalf("receipt = %+v, want binding to destination operation and request digest", receipt)
	}

	// Recovery re-commits the same receipt idempotently and completes the
	// transfer to a single destination owner.
	if err := RecoverTaskHandoffs(parent); err != nil {
		t.Fatalf("recover after destination receipt: %v", err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1"); err != nil || ok {
		t.Fatalf("source TASK-1 remains after recovery ok=%v err=%v", ok, err)
	}
	if _, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1"); err != nil || !ok {
		t.Fatalf("destination TASK-1 missing after recovery ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("destination receipt lost after recovery: %v", err)
	}
}

// TestHandoffReceiptIdempotentReplayAndTypedConflict proves the fleet-durable
// destination receipt mirrors the Authority Store receipt semantics: the
// same Operation ID with the same digest is an idempotent no-op returning the
// original receipt, and a changed digest is a non-retryable typed conflict
// (Task 6.1 acceptance 2).
func TestHandoffReceiptIdempotentReplayAndTypedConflict(t *testing.T) {
	dest := t.TempDir()
	intent := taskauthority.TransferIntent{
		SourceHome:             "/homes/source",
		DestinationHome:        dest,
		TaskID:                 "TASK-1",
		Generation:             7,
		RequestDigest:          strings.Repeat("a", 64),
		SourceOperationID:      "handoff-source-TASK-1-7",
		DestinationOperationID: "handoff-dest-TASK-1-7",
	}
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt := taskTransferReceiptFromIntent(intent)

	replayed, err := commitHandoffReceipt(dest, receipt)
	if err != nil || replayed {
		t.Fatalf("first commit = %v err=%v, want fresh receipt", replayed, err)
	}
	path := taskTransferReceiptPath(dest, intent.DestinationOperationID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("receipt not durable: %v", err)
	}

	replayed, err = commitHandoffReceipt(dest, receipt)
	if err != nil || !replayed {
		t.Fatalf("repeat = %v err=%v, want idempotent no-op", replayed, err)
	}

	conflict := receipt
	conflict.Digest = strings.Repeat("b", 64)
	if _, err := commitHandoffReceipt(dest, conflict); !errors.Is(err, taskauthority.ErrOperationConflict) {
		t.Fatalf("changed digest = %v, want ErrOperationConflict", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored taskTransferReceipt
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Digest != intent.RequestDigest {
		t.Fatalf("conflict overwrote the original receipt: digest = %s", stored.Digest)
	}

	other := receipt
	other.OperationID = "handoff-dest-TASK-2-7"
	other.TaskID = "TASK-2"
	replayed, err = commitHandoffReceipt(dest, other)
	if err != nil || replayed {
		t.Fatalf("different operation = %v err=%v, want fresh receipt", replayed, err)
	}
	if _, err := os.Stat(taskTransferReceiptPath(dest, other.OperationID)); err != nil {
		t.Fatalf("second receipt not durable: %v", err)
	}
}

// TestHandoffConflictDestinationOwnerFailsClosed proves a destination that
// already owns the same Task ID/Generation (or a newer generation) quarantines
// the transfer with a typed conflict and never overwrites destination truth
// (Task 6.1 acceptance 4).
func TestHandoffConflictDestinationOwnerFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name           string
		destGeneration string
	}{
		{"same generation", "7"},
		{"newer generation", "8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			// The destination must sit outside the source's captains/ tree:
			// ResolveCurrentTaskID scans captains/ and would report the two
			// TASK-1 owners as ambiguous before the conflict check runs.
			sm := filepath.Join(parent, "test-sm")
			if err := os.MkdirAll(sm, 0755); err != nil {
				t.Fatal(err)
			}
			if err := SeedProvenance(sm, "test-sm"); err != nil {
				t.Fatal(err)
			}
			if err := mhome.WriteTaskAggregate(parent, mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: "TASK-1", Generation: "7", Current: true, Owner: "general", Definition: "source truth", State: "queued", Kind: "ship", Project: "munsu"}); err != nil {
				t.Fatal(err)
			}
			if err := mhome.WriteMeta(parent, "TASK-1", map[string]string{"description": "source truth", "generation": "7"}); err != nil {
				t.Fatal(err)
			}
			if err := mhome.WriteTaskAggregate(sm, mhome.TaskAggregate{SchemaVersion: "munsu.task-aggregate/v1", TaskID: "TASK-1", Generation: tc.destGeneration, Current: true, Owner: "captain:test-sm", Definition: "destination truth", State: "queued", Kind: "ship", Project: "munsu"}); err != nil {
				t.Fatal(err)
			}
			dstAggPath := filepath.Join(sm, "state", ".task-authority", "aggregates", "TASK-1", tc.destGeneration+".json")
			beforeDestAgg := mustReadFleetTestFile(t, dstAggPath)

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
				t.Fatal("expected typed destination conflict")
			}
			if de, ok := err.(*domain.Error); !ok || de.Category != domain.ErrorConflict {
				t.Fatalf("error = %v, want typed conflict", err)
			}
			srcAgg, ok, err := mhome.ReadCurrentTaskAggregate(parent, "TASK-1")
			if err != nil || !ok || srcAgg.Generation != "7" || srcAgg.Owner != "general" {
				t.Fatalf("source aggregate = %+v ok=%v err=%v, want untouched", srcAgg, ok, err)
			}
			dstAgg, ok, err := mhome.ReadCurrentTaskAggregate(sm, "TASK-1")
			if err != nil || !ok || dstAgg.Generation != tc.destGeneration || dstAgg.Owner != "captain:test-sm" {
				t.Fatalf("destination aggregate = %+v ok=%v err=%v, want untouched", dstAgg, ok, err)
			}
			if got := mustReadFleetTestFile(t, dstAggPath); !bytes.Equal(got, beforeDestAgg) {
				t.Fatal("destination truth overwritten by conflicting transfer")
			}
		})
	}
}
