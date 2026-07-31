package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskAggregateRequiresOneAuthoritativeOwner(t *testing.T) {
	agg, quarantine := ResolveTaskAggregate([]TaskAggregateCandidate{
		{TaskID: "ship-1", Generation: "7", Owner: "captain:api", Definition: "Implement API", State: "working", Current: true, Source: TaskAggregateSource{Kind: "meta", Path: "state/ship-1.meta", Field: "owner"}},
		{TaskID: "ship-1", Generation: "7", Definition: "Implement API", State: "working", Current: true, Projection: true, Source: TaskAggregateSource{Kind: "backlog", Path: "data/backlog.md", Field: "description"}},
	})
	if quarantine != nil {
		t.Fatalf("unexpected quarantine: %+v", quarantine)
	}
	if agg == nil || agg.Owner != "captain:api" || agg.Generation != "7" || !agg.Current || agg.State != "working" || len(agg.Projections) != 2 || len(agg.AuditSources) != 3 {
		t.Fatalf("aggregate = %+v", agg)
	}
}

func TestTaskAggregateQuarantinesConflictingActiveOwnersWithEvidence(t *testing.T) {
	agg, quarantine := ResolveTaskAggregate([]TaskAggregateCandidate{
		{TaskID: "ship-1", Generation: "7", Owner: "captain:api", Definition: "Implement API", Current: true, Source: TaskAggregateSource{Kind: "meta", Path: "state/ship-1.meta", Field: "owner"}},
		{TaskID: "ship-1", Generation: "7", Owner: "captain:web", Definition: "Implement API", Current: true, Source: TaskAggregateSource{Kind: "meta", Path: "captains/web/state/ship-1.meta", Field: "owner"}},
	})
	if agg != nil {
		t.Fatalf("conflicting owners produced aggregate: %+v", agg)
	}
	if quarantine == nil || quarantine.Reason != "conflicting active owners" || quarantine.TaskID != "ship-1" || quarantine.Generation != "7" {
		t.Fatalf("quarantine = %+v", quarantine)
	}
	want := []TaskAggregateEvidence{
		{Kind: "meta", Path: "captains/web/state/ship-1.meta", Field: "owner", Value: "captain:web"},
		{Kind: "meta", Path: "state/ship-1.meta", Field: "owner", Value: "captain:api"},
	}
	assertEvidence(t, quarantine.Evidence, want)
}

func TestTaskAggregateQuarantinesConflictingDefinitionsAndStatesIndependently(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []TaskAggregateCandidate
		reason     string
		want       []TaskAggregateEvidence
	}{
		{
			name: "definitions",
			candidates: []TaskAggregateCandidate{
				{TaskID: "ship-1", Generation: "7", Owner: "captain:api", Definition: "Implement API", Current: true, Source: TaskAggregateSource{Kind: "meta", Path: "state/ship-1.meta", Field: "description"}},
				{TaskID: "ship-1", Generation: "7", Definition: "Implement UI", Current: true, Projection: true, Source: TaskAggregateSource{Kind: "backlog", Path: "data/backlog.md", Field: "description"}},
			},
			reason: "conflicting active definitions",
			want: []TaskAggregateEvidence{
				{Kind: "backlog", Path: "data/backlog.md", Field: "description", Value: "Implement UI"},
				{Kind: "meta", Path: "state/ship-1.meta", Field: "description", Value: "Implement API"},
			},
		},
		{
			name: "states",
			candidates: []TaskAggregateCandidate{
				{TaskID: "ship-1", Generation: "7", Owner: "captain:api", Definition: "Implement API", State: "working", Current: true, Source: TaskAggregateSource{Kind: "meta", Path: "state/ship-1.meta", Field: "state"}},
				{TaskID: "ship-1", Generation: "7", Owner: "captain:api", Definition: "Implement API", State: "done", Current: true, Source: TaskAggregateSource{Kind: "meta", Path: "captains/api/state/ship-1.meta", Field: "state"}},
			},
			reason: "conflicting active states",
			want: []TaskAggregateEvidence{
				{Kind: "meta", Path: "captains/api/state/ship-1.meta", Field: "state", Value: "done"},
				{Kind: "meta", Path: "state/ship-1.meta", Field: "state", Value: "working"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agg, quarantine := ResolveTaskAggregate(tc.candidates)
			if agg != nil || quarantine == nil || quarantine.Reason != tc.reason {
				t.Fatalf("agg=%+v quarantine=%+v", agg, quarantine)
			}
			assertEvidence(t, quarantine.Evidence, tc.want)
		})
	}
}

func TestTaskAggregateMigrationPreservesHistoricalTaskGenerationsAndCurrentPointer(t *testing.T) {
	homeDir := t.TempDir()
	if err := WriteTaskAggregate(homeDir, TaskAggregate{SchemaVersion: taskAggregateSchema, TaskID: "ship-1", Generation: "6", Owner: "captain:old", Definition: "Old incarnation", State: "done"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Implement API\nowner=captain:api\ngeneration=7\nkind=ship\n")
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.status"), "working: from status projection\n")
	writeFile(t, filepath.Join(homeDir, "data", "backlog.md"), "# Backlog\n\n- [-] ship-1 - Implement API (repo: munsu) (kind: ship)\n")
	beforeBacklog := string(mustReadFile(t, filepath.Join(homeDir, "data", "backlog.md")))
	beforeStatus := string(mustReadFile(t, filepath.Join(homeDir, "state", "ship-1.status")))
	beforeOld := string(mustReadFile(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "ship-1", "6.json")))

	plan, err := PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RecordCount != 2 || len(plan.Aggregates) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Aggregates[0].Generation != "6" || plan.Aggregates[0].Current || plan.Aggregates[1].Generation != "7" || !plan.Aggregates[1].Current {
		t.Fatalf("generations/current = %+v", plan.Aggregates)
	}
	if string(mustReadFile(t, filepath.Join(homeDir, "data", "backlog.md"))) != beforeBacklog {
		t.Fatal("planning mutated backlog source")
	}

	receipt, err := ApplyTaskAggregateMigration(plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RecordCount != 2 || receipt.SourceDigest != plan.SourceDigest {
		t.Fatalf("receipt = %+v plan=%+v", receipt, plan)
	}
	if got := string(mustReadFile(t, filepath.Join(homeDir, "data", "backlog.md"))); got != beforeBacklog {
		t.Fatalf("legacy backlog changed: %q", got)
	}
	if got := string(mustReadFile(t, filepath.Join(homeDir, "state", "ship-1.status"))); got != beforeStatus {
		t.Fatalf("status projection changed: %q", got)
	}
	current, ok, err := ReadCurrentTaskAggregate(homeDir, "ship-1")
	if err != nil || !ok {
		t.Fatalf("ReadCurrentTaskAggregate ok=%v err=%v", ok, err)
	}
	if current.Generation != "7" || current.Owner != "captain:api" || current.State != "working" {
		t.Fatalf("current aggregate = %+v", current)
	}
	old, err := ReadTaskAggregate(homeDir, "ship-1", "6")
	if err != nil || old.Owner != "captain:old" || old.Current {
		t.Fatalf("old aggregate = %+v err=%v", old, err)
	}
	if got := string(mustReadFile(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "ship-1", "6.json"))); got != beforeOld {
		t.Fatalf("historical canonical aggregate changed: %q", got)
	}
	again, err := ApplyTaskAggregateMigration(plan)
	if err != nil || again.CompletedAt != receipt.CompletedAt {
		t.Fatalf("idempotent apply = %+v err=%v", again, err)
	}
}

func TestTaskAggregateMigrationCaptainHomeOwnerAndPreservesExistingCurrent(t *testing.T) {
	t.Run("captain owner", func(t *testing.T) {
		homeDir := t.TempDir()
		if err := WriteHomeIdentity(homeDir, "api", RankCaptain); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Implement API\ngeneration=1\n")
		plan, err := PlanTaskAggregateMigration(homeDir)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Aggregates[0].Owner != "captain:api" {
			t.Fatalf("owner = %q", plan.Aggregates[0].Owner)
		}
	})
	t.Run("existing current only", func(t *testing.T) {
		homeDir := t.TempDir()
		if err := WriteTaskAggregate(homeDir, TaskAggregate{SchemaVersion: taskAggregateSchema, TaskID: "ship-1", Generation: "6", Current: true, Owner: "captain:old", State: "done"}); err != nil {
			t.Fatal(err)
		}
		plan, err := PlanTaskAggregateMigration(homeDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Quarantined) != 0 || len(plan.Aggregates) != 1 || !plan.Aggregates[0].Current {
			t.Fatalf("plan = %+v", plan)
		}
	})
}

func TestTaskAggregateMigrationQuarantinesCollectedConflictsAndCorruptSources(t *testing.T) {
	homeDir := t.TempDir()
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Implement API\nowner=captain:api\ngeneration=7\n")
	writeFile(t, filepath.Join(homeDir, "captains", "web", "state", "ship-1.meta"), "description=Implement API\nowner=captain:web\ngeneration=7\n")
	writeFile(t, filepath.Join(homeDir, "state", "ship-2.meta"), "description=Implement API\nowner=captain:api\ngeneration=1\n")
	writeFile(t, filepath.Join(homeDir, "data", "backlog.md"), "# Backlog\n\n- [-] ship-2 - Implement UI (repo: munsu) (kind: ship)\n- [?] broken no separator\n")
	corruptPath := filepath.Join(homeDir, "state", "bad.meta")
	corrupt := "description=Broken\nowner=captain:bad\ngeneration=not-a-number\n"
	writeFile(t, corruptPath, corrupt)

	plan, err := PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Quarantined) != 4 {
		t.Fatalf("quarantines = %+v", plan.Quarantined)
	}
	if plan.Quarantined[0].TaskID != "bad" || plan.Quarantined[0].Reason != "corrupt source" {
		t.Fatalf("first quarantine = %+v", plan.Quarantined[0])
	}
	if got := string(mustReadFile(t, corruptPath)); got != corrupt {
		t.Fatalf("corrupt source changed during plan: %q", got)
	}
	if _, err := ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if got := string(mustReadFile(t, corruptPath)); got != corrupt {
		t.Fatalf("corrupt source changed during apply: %q", got)
	}
	if _, ok, err := ReadCurrentTaskAggregate(homeDir, "ship-1"); err != nil || ok {
		t.Fatalf("conflicting task current aggregate ok=%v err=%v", ok, err)
	}
}

func TestTaskAggregateMigrationResumesAfterInstallBeforeReceipt(t *testing.T) {
	homeDir := t.TempDir()
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Implement API\nowner=captain:api\ngeneration=7\n")
	plan, err := PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	taskAggregateMigrationCrashAfter = "install"
	_, err = ApplyTaskAggregateMigration(plan)
	taskAggregateMigrationCrashAfter = ""
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("ApplyTaskAggregateMigration err=%v, want injected crash", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".task-aggregate-migration", "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("receipt should not exist after injected crash: %v", err)
	}
	receipt, err := ApplyTaskAggregateMigration(plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RecordCount != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestTaskAggregateMigrationIdempotentReapplyRejectsUnexpectedInstalledFiles(t *testing.T) {
	homeDir := t.TempDir()
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Implement API\nowner=captain:api\ngeneration=7\n")
	plan, err := PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(homeDir, "state", ".task-authority", "aggregates", "ship-1", "unexpected.json"), "{}\n")
	if _, err := ApplyTaskAggregateMigration(plan); err == nil || !strings.Contains(err.Error(), "unexpected task aggregate file") {
		t.Fatalf("idempotent reapply err=%v, want unexpected manifest file", err)
	}
}

func TestTaskAggregateMigrationRejectsSymlinkRetarget(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	link := filepath.Join(root, "link")
	writeFile(t, filepath.Join(a, "state", "ship-1.meta"), "description=A\nowner=general\ngeneration=1\n")
	writeFile(t, filepath.Join(b, "state", "ship-1.meta"), "description=B\nowner=general\ngeneration=1\n")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanTaskAggregateMigration(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b, link); err != nil {
		t.Fatal(err)
	}
	plan.HomeDir = link
	if _, err := ApplyTaskAggregateMigration(plan); err == nil || !strings.Contains(err.Error(), "home identity changed") {
		t.Fatalf("ApplyTaskAggregateMigration err=%v, want home identity changed", err)
	}
}

func TestTaskAggregateMigrationRejectsStalePlanWithoutPartialRewrite(t *testing.T) {
	homeDir := t.TempDir()
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Implement API\nowner=captain:api\ngeneration=7\n")
	plan, err := PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=Changed\nowner=captain:api\ngeneration=7\n")
	if _, err := ApplyTaskAggregateMigration(plan); err == nil || !strings.Contains(err.Error(), "source digest changed") {
		t.Fatalf("ApplyTaskAggregateMigration err=%v, want source digest changed", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".task-authority", "aggregates")); !os.IsNotExist(err) {
		t.Fatalf("stale apply wrote aggregates: %v", err)
	}
}

func assertEvidence(t *testing.T, got, want []TaskAggregateEvidence) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("evidence = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("evidence[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTaskAggregateRequiresPersistedEndpointBindingBeforeWorking(t *testing.T) {
	homeDir := t.TempDir()
	agg, err := CreateTaskAggregate(homeDir, "task-bind", "general", "bind endpoint", "ship", "munsu")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateCurrentTaskAggregateState(homeDir, agg.TaskID, "working", "spawned"); err == nil {
		t.Fatal("working state without endpoint binding should fail")
	}
	binding := TaskEndpointBinding{
		Backend:     "herdr",
		Handle:      "session:pane",
		LeaseID:     "lease-1",
		FenceToken:  "fence-1",
		BoundAtUnix: 123,
	}
	if err := BindTaskEndpoint(homeDir, agg.TaskID, agg.Generation, binding); err != nil {
		t.Fatalf("BindTaskEndpoint: %v", err)
	}
	updated, ok, err := UpdateCurrentTaskAggregateState(homeDir, agg.TaskID, "working", "spawned")
	if err != nil || !ok {
		t.Fatalf("UpdateCurrentTaskAggregateState: ok=%v err=%v", ok, err)
	}
	reloaded, err := ReadTaskAggregate(homeDir, agg.TaskID, agg.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Endpoint == nil || reloaded.Endpoint == nil {
		t.Fatalf("endpoint binding not persisted: updated=%+v reloaded=%+v", updated.Endpoint, reloaded.Endpoint)
	}
	if reloaded.Endpoint.TaskGeneration != agg.Generation || reloaded.Endpoint.LeaseID != "lease-1" || reloaded.Endpoint.FenceToken != "fence-1" {
		t.Fatalf("binding = %+v", reloaded.Endpoint)
	}
}

func TestTaskEndpointBindingIsGenerationScopedAndImmutable(t *testing.T) {
	homeDir := t.TempDir()
	agg, err := CreateTaskAggregate(homeDir, "task-immut", "general", "bind endpoint", "ship", "munsu")
	if err != nil {
		t.Fatal(err)
	}
	binding := TaskEndpointBinding{Backend: "tmux", Handle: "munsu:@1", LeaseID: "lease-1", FenceToken: "fence-1", BoundAtUnix: 1}
	if err := BindTaskEndpoint(homeDir, agg.TaskID, agg.Generation, binding); err != nil {
		t.Fatal(err)
	}
	if err := BindTaskEndpoint(homeDir, agg.TaskID, agg.Generation, TaskEndpointBinding{Backend: "tmux", Handle: "munsu:@2", LeaseID: "lease-2", FenceToken: "fence-2", BoundAtUnix: 2}); err == nil {
		t.Fatal("rebinding same task generation should fail")
	}
	if err := BindTaskEndpoint(homeDir, agg.TaskID, "2", binding); err == nil {
		t.Fatal("binding stale/non-current generation should fail")
	}
}

func TestTaskWorktreeBindingRecordsExactIdentityAndIsGenerationScoped(t *testing.T) {
	homeDir := t.TempDir()
	agg, err := CreateTaskAggregate(homeDir, "task-wt", "general", "bind worktree", "ship", "munsu")
	if err != nil {
		t.Fatal(err)
	}
	binding := TaskWorktreeBinding{
		RepositoryIdentity: "repo-identity",
		Path:               "/tmp/wt",
		GitDir:             "/repo/.git/worktrees/wt",
		CommonDir:          "/repo/.git",
		Head:               "0123456789abcdef0123456789abcdef01234567",
		LeaseID:            "lease-1",
		FenceToken:         "fence-1",
		BoundAtUnix:        123,
	}
	if err := BindTaskWorktree(homeDir, agg.TaskID, agg.Generation, binding); err != nil {
		t.Fatal(err)
	}
	reloaded, err := ReadTaskAggregate(homeDir, agg.TaskID, agg.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Worktree == nil || reloaded.Worktree.TaskGeneration != agg.Generation || reloaded.Worktree.RepositoryIdentity != "repo-identity" || reloaded.Worktree.Path != "/tmp/wt" || reloaded.Worktree.GitDir == reloaded.Worktree.CommonDir || reloaded.Worktree.Head == "" || reloaded.Worktree.LeaseID != "lease-1" || reloaded.Worktree.FenceToken != "fence-1" {
		t.Fatalf("worktree binding = %+v", reloaded.Worktree)
	}
	if err := BindTaskWorktree(homeDir, agg.TaskID, agg.Generation, TaskWorktreeBinding{RepositoryIdentity: "repo-identity", Path: "/tmp/other", GitDir: "/repo/.git/worktrees/other", CommonDir: "/repo/.git", Head: binding.Head, LeaseID: "lease-2", FenceToken: "fence-2", BoundAtUnix: 124}); err == nil {
		t.Fatal("rebinding same task generation should fail")
	}
	if err := BindTaskWorktree(homeDir, agg.TaskID, "2", binding); err == nil {
		t.Fatal("binding stale/non-current generation should fail")
	}
}
