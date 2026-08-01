package taskauthorityfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// --- v1 fixture helpers -----------------------------------------------------

const v1AggSchema = "munsu.task-aggregate/v1"
const v1DispatchSchema = "munsu.dispatch-control/v1"

func writeV1Agg(t *testing.T, home, taskID, gen string, current bool, fields map[string]any) string {
	t.Helper()
	doc := map[string]any{
		"schema_version": v1AggSchema,
		"task_id":        taskID,
		"generation":     gen,
		"current":        current,
		"owner":          "captain:api",
		"state":          "queued",
	}
	if fields != nil {
		for k, v := range fields {
			doc[k] = v
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rel := "state/.task-authority/aggregates/" + taskID + "/" + gen + ".json"
	writeDocAt(t, home, rel, append(data, '\n'))
	if current {
		writeDocAt(t, home, "state/.task-authority/aggregates/"+taskID+"/current", []byte(gen+"\n"))
	}
	return rel
}

func writeV1Pointer(t *testing.T, home, taskID, gen string) {
	t.Helper()
	writeDocAt(t, home, "state/.task-authority/aggregates/"+taskID+"/current", []byte(gen+"\n"))
}

func writeV1Hold(t *testing.T, home, id, reason string, actions []string) string {
	t.Helper()
	doc := map[string]any{
		"schema_version": v1DispatchSchema,
		"id":             id,
		"scope":          map[string]any{"tasks": []string{"t1"}},
		"actions":        actions,
		"reason":         reason,
		"created_at":     1700000000,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rel := "state/.dispatch/holds/" + id + ".json"
	writeDocAt(t, home, rel, append(data, '\n'))
	return rel
}

func writeV1Interpretation(t *testing.T, home, id string) string {
	t.Helper()
	doc := map[string]any{
		"schema_version":             v1DispatchSchema,
		"id":                         id,
		"requested_order":            []string{"t1"},
		"selected_tasks":             []string{"t1"},
		"dependency_snapshot_digest": strings.Repeat("a", 64),
		"outcome":                    "accepted",
		"created_at":                 1700000000,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rel := "state/.dispatch/interpretations/" + id + ".json"
	writeDocAt(t, home, rel, append(data, '\n'))
	return rel
}

func writeV1Decision(t *testing.T, home, key, interpretationID string) string {
	t.Helper()
	doc := map[string]any{
		"schema_version":    v1DispatchSchema,
		"key":               key,
		"interpretation_id": interpretationID,
		"reason":            "material dispatch ambiguity",
		"created_at":        1700000000,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rel := "state/.dispatch/decisions/" + key + ".json"
	writeDocAt(t, home, rel, append(data, '\n'))
	return rel
}

func writeV1Lease(t *testing.T, home, taskID, gen, leaseID, fenceToken string) string {
	t.Helper()
	doc := map[string]any{
		"task_id":         taskID,
		"task_generation": gen,
		"lease_id":        leaseID,
		"fence_token":     fenceToken,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	rel := "state/.task-authority/worktree-leases/" + taskID + "/" + gen + "/" + leaseID + ".json"
	writeDocAt(t, home, rel, append(data, '\n'))
	return rel
}

func v1WorktreeBinding(gen, leaseID, fenceToken string) map[string]any {
	return map[string]any{
		"task_generation":     gen,
		"repository_identity": "repo",
		"path":                "/tmp/worktree",
		"git_dir":             "/tmp/worktree/.git",
		"common_dir":          "/tmp/worktree/.git",
		"head":                "0123456789abcdef",
		"lease_id":            leaseID,
		"fence_token":         fenceToken,
		"bound_at_unix":       1700000000,
	}
}

func v1EndpointBinding(gen string) map[string]any {
	return map[string]any{
		"task_generation": gen,
		"backend":         "pi",
		"handle":          "handle-1",
		"lease_id":        "lease-1",
		"fence_token":     "tok-1",
		"bound_at_unix":   1700000000,
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func v2RootExists(home string) bool {
	_, err := os.Stat(filepath.Join(home, "state", ".task-authority", "v2"))
	return err == nil
}

func planHome(t *testing.T, home string) *MigrationPlan {
	t.Helper()
	plan, err := PlanMigration(home)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func mustApply(t *testing.T, plan *MigrationPlan) *MigrationReceipt {
	t.Helper()
	receipt, err := ApplyMigration(plan)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

// --- plan tests -------------------------------------------------------------

func TestMigrationPlanIsReadOnlyAndDeterministic(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, nil)
	writeV1Hold(t, home, "hold-1", "dispatch decision required", []string{"handoff", "start", "spawn"})
	writeV1Interpretation(t, home, "interpretation-1")
	writeV1Decision(t, home, "decision-1", "interpretation-1")

	before, err := snapshotTree(t, home)
	if err != nil {
		t.Fatal(err)
	}
	plan1 := planHome(t, home)
	after, err := snapshotTree(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("plan wrote under home:\nbefore=%v\nafter=%v", before, after)
	}

	plan2 := planHome(t, home)
	data1, err := json.MarshalIndent(plan1, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data2, err := json.MarshalIndent(plan2, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(data1, data2) {
		t.Fatalf("plan is not deterministic: %s != %s", data1, data2)
	}
}

func TestMigrationPlanFilePrivateAndConflict(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, nil)
	plan := planHome(t, home)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := WriteMigrationPlan(planPath, plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plan file mode = %v, want 0600", info.Mode().Perm())
	}
	// Identical content is accepted without rewrite.
	if err := WriteMigrationPlan(planPath, plan); err != nil {
		t.Fatalf("rewriting identical plan: %v", err)
	}
	// Differing content conflicts.
	other := *plan
	other.HomeIdentity = "different"
	if err := WriteMigrationPlan(planPath, &other); err == nil {
		t.Fatal("conflicting plan write succeeded, want error")
	}
}

func TestMigrationPlanConsumesV1AggregateOutputNotRawProjections(t *testing.T) {
	home := t.TempDir()
	// Raw projections only: the older task-aggregates migration owns these.
	writeDocAt(t, home, "state/t1.meta", []byte("description=raw meta\nowner=captain:api\n"))
	writeDocAt(t, home, "state/t1.status", []byte("working: from status\n"))
	writeDocAt(t, home, "data/backlog.md", []byte("- [ ] t1 - raw backlog\n"))
	// The older migration's own state must be left untouched too.
	writeDocAt(t, home, "state/.task-aggregate-migration/receipt.json", []byte(`{"schema_version":"x"}`))
	writeDocAt(t, home, "state/.task-authority/quarantine/q1.json", []byte(`{"schema_version":"x"}`))

	plan := planHome(t, home)
	if plan.RecordCount != 0 || len(plan.Aggregates) != 0 {
		t.Fatalf("plan consumed raw projections: records=%d aggregates=%d", plan.RecordCount, len(plan.Aggregates))
	}

	// A v1 aggregate produced by `munsu migrate task-aggregates` IS the source.
	writeV1Agg(t, home, "t1", "1", true, map[string]any{"definition": "resolved aggregate", "state": "working"})
	plan = planHome(t, home)
	if plan.RecordCount != 1 || len(plan.Aggregates) != 1 {
		t.Fatalf("plan did not consume v1 aggregate output: records=%d aggregates=%d", plan.RecordCount, len(plan.Aggregates))
	}
	if plan.Aggregates[0].Definition.Description != "resolved aggregate" {
		t.Fatalf("aggregate definition = %q, want resolved aggregate", plan.Aggregates[0].Definition.Description)
	}
	if plan.Aggregates[0].Phase != taskauthority.PhaseWorking {
		t.Fatalf("aggregate phase = %q, want working", plan.Aggregates[0].Phase)
	}
}

func TestMigrationPlanConvertsAllRecordFamilies(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, map[string]any{
		"definition":    "task one",
		"kind":          "ship",
		"project":       "munsu",
		"state":         "working",
		"endpoint":      v1EndpointBinding("1"),
		"worktree":      v1WorktreeBinding("1", "lease-1", "tok-1"),
		"audit_sources": []map[string]any{{"kind": "meta", "path": "state/t1.meta", "field": "state", "value": "working"}},
	})
	writeV1Agg(t, home, "t1", "2", false, map[string]any{"state": "done"})
	writeV1Lease(t, home, "t1", "1", "lease-1", "tok-1")
	writeV1Hold(t, home, "hold-1", "dispatch decision required", []string{"handoff", "start"})
	writeV1Interpretation(t, home, "interpretation-1")
	writeV1Decision(t, home, "decision-1", "interpretation-1")

	plan := planHome(t, home)
	if plan.RecordCount != 5 {
		t.Fatalf("record count = %d, want 5 (2 aggregates, hold, interpretation, decision)", plan.RecordCount)
	}
	if len(plan.Aggregates) != 2 || len(plan.Holds) != 1 || len(plan.Interpretations) != 1 || len(plan.Decisions) != 1 {
		t.Fatalf("targets = agg:%d hold:%d interp:%d dec:%d", len(plan.Aggregates), len(plan.Holds), len(plan.Interpretations), len(plan.Decisions))
	}
	if plan.Aggregates[0].TaskID != "t1" || plan.Aggregates[0].Generation != 1 || !plan.Aggregates[0].Current {
		t.Fatalf("agg[0] = %+v, want t1/1 current", plan.Aggregates[0])
	}
	if plan.Aggregates[0].Revision != taskauthority.FirstRevision {
		t.Fatalf("agg[0] revision = %d, want FirstRevision", plan.Aggregates[0].Revision)
	}
	if plan.Aggregates[0].Endpoint == nil || plan.Aggregates[0].Endpoint.Backend != "pi" {
		t.Fatalf("agg[0] endpoint = %+v, want pi binding", plan.Aggregates[0].Endpoint)
	}
	if plan.Aggregates[0].Worktree == nil || plan.Aggregates[0].Worktree.LeaseID != "lease-1" {
		t.Fatalf("agg[0] worktree = %+v, want lease-1 binding", plan.Aggregates[0].Worktree)
	}
	if plan.Aggregates[1].Generation != 2 || plan.Aggregates[1].Current {
		t.Fatalf("agg[1] = %+v, want t1/2 historical", plan.Aggregates[1])
	}
	if plan.Aggregates[1].Phase != taskauthority.PhaseDone {
		t.Fatalf("agg[1] phase = %q, want done", plan.Aggregates[1].Phase)
	}
	if plan.Holds[0].ID != "hold-1" || len(plan.Holds[0].Actions) != 2 {
		t.Fatalf("hold = %+v", plan.Holds[0])
	}
	if plan.Interpretations[0].ID != "interpretation-1" || plan.Decisions[0].Key != "decision-1" {
		t.Fatalf("dispatch records not converted: %+v %+v", plan.Interpretations[0], plan.Decisions[0])
	}
	// Deterministic source digest covers every source file: t1/1.json,
	// t1/2.json, t1/current, one lease, and three dispatch records.
	if len(plan.Sources) != 7 {
		t.Fatalf("source files = %d, want 7", len(plan.Sources))
	}
	if len(plan.SourceDigest) != 64 {
		t.Fatalf("source digest = %q", plan.SourceDigest)
	}
	// One lifecycle audit per converted aggregate plus one dispatch audit.
	if len(plan.Audits) != 3 {
		t.Fatalf("planned audits = %d, want 3", len(plan.Audits))
	}
	if plan.Command != "munsu migrate task-authority apply --plan <plan.json>" {
		t.Fatalf("command = %q", plan.Command)
	}
}

func TestMigrationPlanNormalizesReopenedPointer(t *testing.T) {
	// home.ReopenTask can leave the pointer-named document with current=false;
	// the pointer is authoritative for v2 conversion.
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", false, map[string]any{"state": "done"})
	writeV1Agg(t, home, "t1", "2", false, map[string]any{"state": "queued"})
	writeV1Pointer(t, home, "t1", "2")

	plan := planHome(t, home)
	if len(plan.Aggregates) != 2 {
		t.Fatalf("aggregates = %d, want 2", len(plan.Aggregates))
	}
	if !plan.Aggregates[1].Current || plan.Aggregates[1].Generation != 2 {
		t.Fatalf("pointer-named generation not normalized to current: %+v", plan.Aggregates[1])
	}
	if plan.Aggregates[0].Current {
		t.Fatalf("historical generation marked current: %+v", plan.Aggregates[0])
	}
	if len(plan.Quarantined) != 0 {
		t.Fatalf("quarantined = %v, want none", plan.Quarantined)
	}
}

func TestMigrationPlanQuarantinesUnconvertibleSource(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, home string)
		wantQ int
	}{
		{
			name: "unsupported v1 state",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", true, map[string]any{"state": "paused"})
			},
			wantQ: 1,
		},
		{
			name: "missing owner",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", true, map[string]any{"owner": ""})
			},
			wantQ: 1,
		},
		{
			name: "corrupt json",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", []byte("{not json"))
			},
			wantQ: 1,
		},
		{
			name: "pointer divergence",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", false, nil)
				writeV1Pointer(t, home, "t1", "9")
			},
			wantQ: 1,
		},
		{
			name: "conflicting current claims",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", true, nil)
				writeV1Agg(t, home, "t1", "2", true, nil)
			},
			wantQ: 1,
		},
		{
			name: "historical only without pointer",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", false, nil)
				writeV1Agg(t, home, "t1", "2", false, nil)
			},
			wantQ: 1,
		},
		{
			name: "current flag without pointer",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"t1","generation":"1","current":true,"owner":"captain:api"}`))
			},
			wantQ: 1,
		},
		{
			name: "endpoint generation mismatch",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", true, map[string]any{"endpoint": v1EndpointBinding("2")})
			},
			wantQ: 1,
		},
		{
			name: "duplicate generation aliases",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"t1","generation":"1","current":true,"owner":"c"}`))
				writeDocAt(t, home, "state/.task-authority/aggregates/t1/01.json", []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"t1","generation":"01","current":false,"owner":"c"}`))
			},
			wantQ: 1,
		},
		{
			name: "identity mismatch",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"other","generation":"1","current":true,"owner":"c"}`))
			},
			wantQ: 1,
		},
		{
			name: "unsupported document schema",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", []byte(`{"schema_version":"munsu.task-authority/v2","task_id":"t1","generation":1,"revision":1,"phase":"queued","current":true}`))
			},
			wantQ: 1,
		},
		{
			name: "unrecognized aggregate entry",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.task-authority/aggregates/stray.json", []byte("{}"))
			},
			wantQ: 1,
		},
		{
			name: "worktree lease without binding",
			setup: func(t *testing.T, home string) {
				writeV1Agg(t, home, "t1", "1", true, nil)
				writeV1Lease(t, home, "t1", "1", "orphan-lease", "tok")
			},
			wantQ: 1,
		},
		{
			name: "unrecognized dispatch entry",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.dispatch/stray.json", []byte("{}"))
			},
			wantQ: 1,
		},
		{
			name: "corrupt dispatch record",
			setup: func(t *testing.T, home string) {
				writeDocAt(t, home, "state/.dispatch/holds/hold-1.json", []byte("{not json"))
			},
			wantQ: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			tc.setup(t, home)
			plan := planHome(t, home)
			if len(plan.Quarantined) != tc.wantQ {
				t.Fatalf("quarantined = %d, want %d: %+v", len(plan.Quarantined), tc.wantQ, plan.Quarantined)
			}
			for _, q := range plan.Quarantined {
				if q.SourcePath == "" || q.Reason == "" {
					t.Fatalf("quarantine missing evidence: %+v", q)
				}
			}
			if len(plan.SourceDigest) != 64 {
				t.Fatalf("source digest = %q", plan.SourceDigest)
			}
		})
	}
}

func TestMigrationPlanEmptyHome(t *testing.T) {
	home := t.TempDir()
	plan := planHome(t, home)
	if plan.RecordCount != 0 || len(plan.Quarantined) != 0 || len(plan.Sources) != 0 {
		t.Fatalf("empty home plan = %+v", plan)
	}
	if plan.HomeIdentity == "" || plan.HomeDir == "" {
		t.Fatalf("empty home plan missing identity: %+v", plan)
	}
}

// --- apply tests ------------------------------------------------------------

func TestMigrationApplyHappyPath(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, map[string]any{"definition": "one", "state": "working"})
	writeV1Agg(t, home, "t1", "2", false, map[string]any{"state": "done", "worktree": v1WorktreeBinding("2", "lease-2", "tok-2")})
	writeV1Lease(t, home, "t1", "2", "lease-2", "tok-2")
	writeV1Hold(t, home, "hold-1", "dispatch decision required", []string{"handoff", "start", "spawn"})
	writeV1Interpretation(t, home, "interpretation-1")
	writeV1Decision(t, home, "decision-1", "interpretation-1")

	plan := planHome(t, home)
	sourceDigest := plan.SourceDigest
	receipt := mustApply(t, plan)

	if receipt.HomeIdentity != plan.HomeIdentity || receipt.SourceDigest != sourceDigest || receipt.RecordCount != plan.RecordCount {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.CompletedAt <= 0 || receipt.ArchivePath == "" || receipt.TargetManifestDigest == "" {
		t.Fatalf("receipt missing fields: %+v", receipt)
	}
	if v1SourcesRemain(home) {
		t.Fatal("v1 sources still visible after migration")
	}
	archive := filepath.Join(home, filepath.FromSlash(receipt.ArchivePath))
	if info, err := os.Stat(archive); err != nil || !info.IsDir() {
		t.Fatalf("archive %s: err=%v", receipt.ArchivePath, err)
	}

	// Store serves the converted v2 state.
	store, err := NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.View()
	if err != nil {
		t.Fatalf("View after migration: %v", err)
	}
	if len(view.Aggregates) != 2 || len(view.Holds) != 1 || len(view.Interpretations) != 1 || len(view.Decisions) != 1 {
		t.Fatalf("view = agg:%d hold:%d interp:%d dec:%d", len(view.Aggregates), len(view.Holds), len(view.Interpretations), len(view.Decisions))
	}
	if !reflect.DeepEqual(view.Aggregates, plan.Aggregates) {
		t.Fatalf("view aggregates != plan aggregates:\n%+v\n%+v", view.Aggregates, plan.Aggregates)
	}
	if !reflect.DeepEqual(view.Holds, plan.Holds) || !reflect.DeepEqual(view.Interpretations, plan.Interpretations) || !reflect.DeepEqual(view.Decisions, plan.Decisions) {
		t.Fatalf("view dispatch records != plan dispatch records")
	}
	// Initial typed audit evidence was written.
	if len(view.Audit) != len(plan.Audits) {
		t.Fatalf("audit = %d events, want %d", len(view.Audit), len(plan.Audits))
	}
	for _, want := range plan.Audits {
		found := false
		for _, ev := range view.Audit {
			if ev.OperationID == want.OperationID && ev.Kind == want.Kind && ev.After == want.After {
				found = true
			}
		}
		if !found {
			t.Fatalf("audit intent %+v not installed", want)
		}
	}

	// Manifest pins installed file digests.
	manifestPath := filepath.Join(home, "state", ".task-authority-migration", "manifest.json")
	manifestData := readFileBytes(t, manifestPath)
	if sha256Hex(manifestData) != receipt.TargetManifestDigest {
		t.Fatalf("receipt target manifest digest does not match manifest.json")
	}
	var manifest migrationManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) == 0 {
		t.Fatal("empty migration manifest")
	}
	for _, f := range manifest.Files {
		data := readFileBytes(t, filepath.Join(home, filepath.FromSlash(f.Path)))
		if sha256Hex(data) != f.SHA256 {
			t.Fatalf("installed file %s digest mismatch", f.Path)
		}
	}
}

func TestMigrationApplyRefusesQuarantinedPlan(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, map[string]any{"state": "paused"})
	plan := planHome(t, home)
	if len(plan.Quarantined) == 0 {
		t.Fatal("expected quarantine")
	}
	if _, err := ApplyMigration(plan); err == nil {
		t.Fatal("apply succeeded on quarantined plan, want refusal")
	}
	if v2RootExists(home) {
		t.Fatal("apply created v2 state on quarantined plan")
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority-migration", "receipt.json")); !os.IsNotExist(err) {
		t.Fatal("apply wrote receipt on quarantined plan")
	}
}

func TestMigrationApplySourceChangedFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, nil)
	plan := planHome(t, home)
	// Source changes after planning.
	writeV1Agg(t, home, "t1", "2", true, nil)
	if _, err := ApplyMigration(plan); err == nil {
		t.Fatal("apply succeeded on changed source, want failure")
	}
	if v2RootExists(home) {
		t.Fatal("apply created v2 state on changed source")
	}
	if v1SourcesRemain(home) != true {
		t.Fatal("v1 source was touched by failed apply")
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority-migration", "receipt.json")); !os.IsNotExist(err) {
		t.Fatal("apply wrote receipt on changed source")
	}
}

func TestMigrationApplyHomeIdentityChangedFailsClosed(t *testing.T) {
	home := t.TempDir()
	// Captain identity via marker.
	writeDocAt(t, home, ".munsu-captain-home", []byte("munsu-v2\ncaptain-alpha\n"))
	writeV1Agg(t, home, "t1", "1", true, nil)
	plan := planHome(t, home)
	if plan.HomeIdentity != "captain-alpha" {
		t.Fatalf("home identity = %q, want captain-alpha", plan.HomeIdentity)
	}
	// Identity marker changes after planning.
	writeDocAt(t, home, ".munsu-captain-home", []byte("munsu-v2\ncaptain-beta\n"))
	if _, err := ApplyMigration(plan); err == nil {
		t.Fatal("apply succeeded on changed home identity, want failure")
	}
	if v2RootExists(home) {
		t.Fatal("apply created v2 state on changed identity")
	}
}

func TestMigrationApplyTargetConflictFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, nil)
	plan := planHome(t, home)
	// A conflicting target already exists in the v2 namespace.
	writeDocAt(t, home, "state/.task-authority/v2/aggregates/other/1.json", []byte(`{"schema_version":"munsu.task-authority/v2","task_id":"other","generation":1,"revision":1,"current":true,"definition":{"owner":"x"},"phase":"queued"}`))
	if _, err := ApplyMigration(plan); err == nil {
		t.Fatal("apply succeeded over conflicting target, want failure")
	}
	if v1SourcesRemain(home) != true {
		t.Fatal("v1 source was touched by conflicted apply")
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority-migration", "receipt.json")); !os.IsNotExist(err) {
		t.Fatal("apply wrote receipt on target conflict")
	}
}

func TestMigrationApplyAlreadyMigratedDoesNotRewrite(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, map[string]any{"definition": "one"})
	writeV1Hold(t, home, "hold-1", "dispatch decision required", []string{"start"})
	plan := planHome(t, home)
	mustApply(t, plan)

	// Record installed mtimes.
	store, err := NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	mtimes := map[string]int64{}
	holdRel, err := HoldRelPath("hold-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"state/.task-authority/v2/aggregates/t1/1.json",
		"state/.task-authority/v2/aggregates/t1/current",
		holdRel,
	} {
		abs := filepath.Join(home, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			t.Fatal(err)
		}
		mtimes[rel] = info.ModTime().UnixNano()
	}
	_ = view

	// Re-run must be typed already_migrated and must not rewrite anything.
	if _, err := ApplyMigration(plan); !errors.Is(err, ErrAlreadyMigrated) {
		t.Fatalf("re-run error = %v, want ErrAlreadyMigrated", err)
	}
	for rel, before := range mtimes {
		info, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if info.ModTime().UnixNano() != before {
			t.Fatalf("re-run rewrote %s (mtime changed)", rel)
		}
	}
	if v1SourcesRemain(home) {
		t.Fatal("v1 sources re-appeared")
	}
}

func TestMigrationApplyResumesAfterCrash(t *testing.T) {
	crashes := []struct {
		crashAfter string
		verify     func(t *testing.T, home string, plan *MigrationPlan)
	}{
		{
			crashAfter: "install",
			verify: func(t *testing.T, home string, plan *MigrationPlan) {
				// v2 installed, journal not yet committed: retry re-installs.
				if !v2RootExists(home) {
					t.Fatal("expected installed v2 after crash at install")
				}
			},
		},
		{
			crashAfter: "committed",
			verify: func(t *testing.T, home string, plan *MigrationPlan) {
				// v2 committed, v1 not yet archived.
				if v1SourcesRemain(home) != true {
					t.Fatal("expected v1 still present after crash at committed")
				}
			},
		},
		{
			crashAfter: "archive",
			verify: func(t *testing.T, home string, plan *MigrationPlan) {
				// v1 archived, receipt not yet written.
				if v1SourcesRemain(home) {
					t.Fatal("expected v1 archived after crash at archive")
				}
			},
		},
		{
			crashAfter: "receipt",
			verify: func(t *testing.T, home string, plan *MigrationPlan) {
				// Receipt written, journal completion pending.
				receiptPath := filepath.Join(home, "state", ".task-authority-migration", "receipt.json")
				if _, err := os.Stat(receiptPath); err != nil {
					t.Fatalf("expected receipt after crash at receipt: %v", err)
				}
			},
		},
	}
	for _, tc := range crashes {
		t.Run(tc.crashAfter, func(t *testing.T) {
			home := t.TempDir()
			writeV1Agg(t, home, "t1", "1", true, map[string]any{"definition": "one"})
			writeV1Hold(t, home, "hold-1", "dispatch decision required", []string{"start"})
			plan := planHome(t, home)

			migrationCrashAfter = tc.crashAfter
			_, err := ApplyMigration(plan)
			migrationCrashAfter = ""
			if err == nil {
				t.Fatal("crash injection did not fail apply")
			}
			tc.verify(t, home, plan)

			// Retry must converge to a durable terminal state: a completed
			// receipt returned, or the typed already_migrated when the receipt
			// was already durable before the crash.
			receipt, err := ApplyMigration(plan)
			if err != nil && !errors.Is(err, ErrAlreadyMigrated) {
				t.Fatalf("retry apply: %v", err)
			}
			if err == nil && (receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != plan.RecordCount) {
				t.Fatalf("retry receipt = %+v", receipt)
			}
			if _, statErr := os.Stat(filepath.Join(home, "state", ".task-authority-migration", "receipt.json")); statErr != nil {
				t.Fatalf("no durable receipt after retry: %v", statErr)
			}
			if v1SourcesRemain(home) {
				t.Fatal("v1 sources remain after retry")
			}
			store, err := NewStore(home)
			if err != nil {
				t.Fatal(err)
			}
			view, err := store.View()
			if err != nil {
				t.Fatalf("View after retry: %v", err)
			}
			if !reflect.DeepEqual(view.Aggregates, plan.Aggregates) {
				t.Fatalf("view aggregates != plan aggregates after retry")
			}
		})
	}
}

func TestMigrationViewUpdateDetectOnlyNoSideEffects(t *testing.T) {
	home := t.TempDir()
	writeV1Agg(t, home, "t1", "1", true, nil)

	store, err := NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.View(); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("View error = %v, want ErrMigrationRequired", err)
	}
	op := taskauthority.Operation{ID: "op-1", Digest: strings.Repeat("a", 64)}
	if _, err := store.Update(op, func(tx *taskauthority.Tx) error { return errors.New("must not run") }); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("Update error = %v, want ErrMigrationRequired", err)
	}
	if v2RootExists(home) {
		t.Fatal("detect-only View/Update created v2 state")
	}

	// After explicit migration the same store serves the converted state.
	plan := planHome(t, home)
	mustApply(t, plan)
	view, err := NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	viewed, err := view.View()
	if err != nil {
		t.Fatalf("View after migration: %v", err)
	}
	if len(viewed.Aggregates) != 1 {
		t.Fatalf("view aggregates = %d, want 1", len(viewed.Aggregates))
	}
}

func TestMigrationApplyZeroRecordPlan(t *testing.T) {
	home := t.TempDir()
	plan := planHome(t, home)
	receipt, err := ApplyMigration(plan)
	if err != nil {
		t.Fatalf("apply empty plan: %v", err)
	}
	if receipt.RecordCount != 0 {
		t.Fatalf("receipt record count = %d", receipt.RecordCount)
	}
	// Re-run reports already migrated.
	if _, err := ApplyMigration(plan); !errors.Is(err, ErrAlreadyMigrated) {
		t.Fatalf("re-run error = %v, want ErrAlreadyMigrated", err)
	}
}

// --- helpers ----------------------------------------------------------------

func snapshotTree(t *testing.T, root string) (map[string]string, error) {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sha256Hex(data)
		return nil
	})
	return out, err
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
