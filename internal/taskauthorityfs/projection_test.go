package taskauthorityfs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// projectionAuthority composes the real Authority over a filesystem Store so
// projection tests read and write canonical records through the public
// interface, never through private file layout.
func projectionAuthority(t *testing.T, homeDir string) *taskauthority.Authority {
	t.Helper()
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return taskauthority.New(store)
}

// seedProjectionTask creates one canonical task through the Authority.
func seedProjectionTask(t *testing.T, auth *taskauthority.Authority, id string) {
	t.Helper()
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-" + id,
		Actor:       taskauthority.Actor{ID: "owner-x", Rank: "general"},
		TaskID:      id,
		Owner:       "owner-x",
		Description: "work on " + id,
		Kind:        "ship",
		Project:     "proj-x",
		Reason:      "cli task add",
	}); err != nil {
		t.Fatal(err)
	}
}

// seedProjectionStart transitions the task to working through the Authority.
func seedProjectionStart(t *testing.T, auth *taskauthority.Authority, id string) {
	t.Helper()
	agg, err := auth.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Start(taskauthority.StartRequest{
		OperationID:        "op-start-" + id,
		Actor:              taskauthority.Actor{ID: "owner-x", Rank: "general"},
		TaskID:             id,
		ExpectedGeneration: agg.Generation,
		Reason:             "backlog: start",
	}); err != nil {
		t.Fatal(err)
	}
}

func readMetaOrFail(t *testing.T, homeDir, id string) map[string]string {
	t.Helper()
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func readStatusOrFail(t *testing.T, homeDir, id string) []string {
	t.Helper()
	lines, err := home.ReadStatus(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// TestReconcileMetaDerivesAuthoritativeFields proves .meta authoritative
// fields are rewritten from the canonical aggregate while runtime-only
// projection fields are preserved, and that reconciliation never writes back
// into the authority (Revision and Generation are untouched).
func TestReconcileMetaDerivesAuthoritativeFields(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "beta")
	// Tamper the projection and add a runtime-only field.
	if err := home.WriteMeta(homeDir, "beta", map[string]string{
		"description": "fake description",
		"kind":        "scout",
		"project":     "fake-proj",
		"runtime":     "keep-me",
	}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ReconcileTaskProjections("beta")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Meta != ProjectionRepaired || out.Err != "" {
		t.Fatalf("outcome = %+v", out)
	}
	meta := readMetaOrFail(t, homeDir, "beta")
	if meta["description"] != "work on beta" || meta["kind"] != "ship" || meta["project"] != "proj-x" {
		t.Fatalf("derived meta = %v", meta)
	}
	if meta["owner"] != "owner-x" || meta["generation"] != "1" || meta["state"] != "queued" {
		t.Fatalf("derived meta = %v", meta)
	}
	if meta["runtime"] != "keep-me" {
		t.Fatalf("runtime projection field lost: %v", meta)
	}
	agg, err := auth.Get("beta")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != taskauthority.FirstRevision || agg.Generation != 1 {
		t.Fatalf("reconciliation changed authoritative record: %+v", agg)
	}
}

// TestReconcileRepairsDeletedAndCorruptMeta proves deleting or corrupting a
// .meta projection can be repaired without changing Revision or Generation.
func TestReconcileRepairsDeletedAndCorruptMeta(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, homeDir string)
	}{
		{
			name: "deleted",
			corrupt: func(t *testing.T, homeDir string) {
				if err := os.Remove(filepath.Join(home.StateDir(homeDir), "beta.meta")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt",
			corrupt: func(t *testing.T, homeDir string) {
				if err := os.WriteFile(filepath.Join(home.StateDir(homeDir), "beta.meta"), []byte{0x00, 0x01, 0x02}, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			auth := projectionAuthority(t, homeDir)
			seedProjectionTask(t, auth, "beta")
			// Simulate a real post-commit projection before corrupting it.
			if err := home.WriteMeta(homeDir, "beta", map[string]string{"kind": "ship"}); err != nil {
				t.Fatal(err)
			}
			tc.corrupt(t, homeDir)
			store, err := NewStore(homeDir)
			if err != nil {
				t.Fatal(err)
			}
			out, err := store.ReconcileTaskProjections("beta")
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if out.Meta != ProjectionRepaired {
				t.Fatalf("outcome = %+v", out)
			}
			meta := readMetaOrFail(t, homeDir, "beta")
			if meta["kind"] != "ship" || meta["owner"] != "owner-x" || meta["generation"] != "1" || meta["state"] != "queued" {
				t.Fatalf("repaired meta = %v", meta)
			}
			agg, err := auth.Get("beta")
			if err != nil {
				t.Fatal(err)
			}
			if agg.Revision != taskauthority.FirstRevision || agg.Generation != 1 {
				t.Fatalf("repair changed authoritative record: %+v", agg)
			}
		})
	}
}

// TestReconcileStatusDerivesFromTypedAuditHistory proves .status is derived
// from the typed lifecycle audit history in audit order.
func TestReconcileStatusDerivesFromTypedAuditHistory(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "alpha")
	seedProjectionStart(t, auth, "alpha")
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ReconcileTaskProjections("alpha")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Status != ProjectionRepaired || out.Err != "" {
		t.Fatalf("outcome = %+v", out)
	}
	want := []string{"queued: cli task add", "working: backlog: start"}
	if got := readStatusOrFail(t, homeDir, "alpha"); !reflect.DeepEqual(got, want) {
		t.Fatalf("derived status = %q, want %q", got, want)
	}
}

// TestReconcileStatusRebuildsCorruptFile proves a corrupt .status file is
// rebuilt from the typed audit history without touching the authority.
func TestReconcileStatusRebuildsCorruptFile(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "alpha")
	seedProjectionStart(t, auth, "alpha")
	if err := os.WriteFile(filepath.Join(home.StateDir(homeDir), "alpha.status"), []byte{0x00, 0xDE, 0xAD}, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ReconcileTaskProjections("alpha")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Status != ProjectionRepaired {
		t.Fatalf("outcome = %+v", out)
	}
	want := []string{"queued: cli task add", "working: backlog: start"}
	if got := readStatusOrFail(t, homeDir, "alpha"); !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt status = %q, want %q", got, want)
	}
	agg, err := auth.Get("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 || agg.Generation != 1 {
		t.Fatalf("rebuild changed authoritative record: %+v", agg)
	}
}

// TestReconcileStatusPreservesLegacyLinesAppendOnly proves reconciliation
// never removes existing .status lines (append-only compatibility): legacy
// runtime lines survive and missing derived lines are appended.
func TestReconcileStatusPreservesLegacyLinesAppendOnly(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "alpha")
	seedProjectionStart(t, auth, "alpha")
	if err := home.AppendStatus(homeDir, "alpha", "working: spawned"); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ReconcileTaskProjections("alpha")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Status != ProjectionRepaired {
		t.Fatalf("outcome = %+v", out)
	}
	lines := readStatusOrFail(t, homeDir, "alpha")
	if len(lines) != 3 || lines[0] != "working: spawned" {
		t.Fatalf("legacy line lost or reordered: %q", lines)
	}
	if lines[1] != "queued: cli task add" || lines[2] != "working: backlog: start" {
		t.Fatalf("derived lines not appended in audit order: %q", lines)
	}
}

// TestReconcileIsIdempotent proves a second reconciliation pass changes
// nothing and reports both projections unchanged.
func TestReconcileIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "alpha")
	seedProjectionStart(t, auth, "alpha")
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileTaskProjections("alpha"); err != nil {
		t.Fatal(err)
	}
	metaBefore := readFileForProjection(t, filepath.Join(home.StateDir(homeDir), "alpha.meta"))
	statusBefore := readFileForProjection(t, filepath.Join(home.StateDir(homeDir), "alpha.status"))
	second, err := store.ReconcileTaskProjections("alpha")
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second.Meta != ProjectionUnchanged || second.Status != ProjectionUnchanged {
		t.Fatalf("second pass changed projections: %+v", second)
	}
	if got := readFileForProjection(t, filepath.Join(home.StateDir(homeDir), "alpha.meta")); got != metaBefore {
		t.Fatal("second pass rewrote meta projection")
	}
	if got := readFileForProjection(t, filepath.Join(home.StateDir(homeDir), "alpha.status")); got != statusBefore {
		t.Fatal("second pass rewrote status projection")
	}
}

func readFileForProjection(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestReconcileNeverMutatesAuthority proves reconciliation is one-directional:
// the authoritative aggregate documents and current pointer are byte-identical
// after a pass that repairs both projections.
func TestReconcileNeverMutatesAuthority(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "beta")
	seedProjectionStart(t, auth, "beta")
	if err := home.WriteMeta(homeDir, "beta", map[string]string{"kind": "scout"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.StateDir(homeDir), "beta.status"), []byte{0x00}, 0o600); err != nil {
		t.Fatal(err)
	}
	aggBefore := readFileForProjection(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "beta", "1.json"))
	pointerBefore := readFileForProjection(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "beta", "current"))
	auditDir := filepath.Join(homeDir, "state", ".task-authority", "v2", "audit")
	auditBefore := map[string]string{}
	for _, name := range mustReadDir(t, auditDir) {
		auditBefore[name] = readFileForProjection(t, filepath.Join(auditDir, name))
	}
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileTaskProjections("beta"); err != nil {
		t.Fatal(err)
	}
	if got := readFileForProjection(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "beta", "1.json")); got != aggBefore {
		t.Fatal("reconciliation mutated the aggregate document")
	}
	if got := readFileForProjection(t, filepath.Join(homeDir, "state", ".task-authority", "v2", "aggregates", "beta", "current")); got != pointerBefore {
		t.Fatal("reconciliation mutated the current pointer")
	}
	for name, before := range auditBefore {
		if got := readFileForProjection(t, filepath.Join(auditDir, name)); got != before {
			t.Fatalf("reconciliation mutated audit event %s", name)
		}
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestReconcileTypedPartialOutcomeOnProjectionFailure proves a projection
// that cannot be repaired yields a typed failed state for that projection
// while the other projection still reconciles and the authority is intact.
func TestReconcileTypedPartialOutcomeOnProjectionFailure(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "beta")
	// A directory where the meta file belongs makes the atomic rename fail.
	if err := os.Mkdir(filepath.Join(home.StateDir(homeDir), "beta.meta"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ReconcileTaskProjections("beta")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Meta != ProjectionFailed || out.Err == "" {
		t.Fatalf("outcome = %+v, want typed meta failure", out)
	}
	if out.Status != ProjectionRepaired && out.Status != ProjectionUnchanged {
		t.Fatalf("status must still reconcile independently: %+v", out)
	}
	agg, err := auth.Get("beta")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != taskauthority.FirstRevision || agg.Generation != 1 {
		t.Fatalf("projection failure changed authoritative record: %+v", agg)
	}
}

// TestReconcileAllCurrentTasks proves the full pass reconciles every current
// task sorted by ID.
func TestReconcileAllCurrentTasks(t *testing.T) {
	homeDir := t.TempDir()
	auth := projectionAuthority(t, homeDir)
	seedProjectionTask(t, auth, "zeta")
	seedProjectionTask(t, auth, "alpha")
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	outs, err := store.ReconcileProjections()
	if err != nil {
		t.Fatalf("reconcile all: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("outcomes = %+v", outs)
	}
	if outs[0].TaskID != "alpha" || outs[1].TaskID != "zeta" {
		t.Fatalf("outcomes not sorted by task id: %+v", outs)
	}
	for _, out := range outs {
		if out.Meta != ProjectionRepaired || out.Status != ProjectionRepaired || out.Err != "" {
			t.Fatalf("outcome = %+v", out)
		}
	}
}

// TestReconcileFailsClosedOnLegacyV1State proves reconciliation never runs
// against v1 state and never writes projections: explicit migration is the
// only path (Task 2.5).
func TestReconcileFailsClosedOnLegacyV1State(t *testing.T) {
	homeDir := t.TempDir()
	writeDocAt(t, homeDir, "state/.task-authority/aggregates/beta/1.json", []byte("{}"))
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileTaskProjections("beta"); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("reconcile error = %v, want migration required", err)
	}
	if _, err := os.Stat(filepath.Join(home.StateDir(homeDir), "beta.meta")); !os.IsNotExist(err) {
		t.Fatal("reconciliation wrote a projection against v1 state")
	}
}

// TestReconcileUnknownTaskNotFound proves reconciling a task with no current
// canonical record returns the typed not-found error.
func TestReconcileUnknownTaskNotFound(t *testing.T) {
	homeDir := t.TempDir()
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileTaskProjections("nope"); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("reconcile error = %v, want not found", err)
	}
}

// TestDeriveStatusLinesSkipsDispatchAudit proves dispatch audit events (not
// task-bound) never become .status lines.
func TestDeriveStatusLinesSkipsDispatchAudit(t *testing.T) {
	events := []taskauthority.AuditEvent{
		{OperationID: "op-dispatch", Actor: taskauthority.Actor{ID: "owner-x"}, Kind: taskauthority.AuditDispatch, At: 1},
		{OperationID: "op-create", Actor: taskauthority.Actor{ID: "owner-x"}, Kind: taskauthority.AuditLifecycle, TaskID: "beta", Generation: 1, After: taskauthority.PhaseQueued, Reason: "cli task add", At: 2},
	}
	got := deriveStatusLines(events)
	want := []string{"queued: cli task add"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived = %q, want %q", got, want)
	}
	if strings.Join(got, "") == "" {
		t.Fatal("derived status lines unexpectedly empty")
	}
}
