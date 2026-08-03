package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestTaskReconcileRepairsTamperedProjections proves `task reconcile`
// rewrites authoritative .meta fields and derives .status from the typed
// audit history, pinning the untouched Generation and Revision in the output.
func TestTaskReconcileRepairsTamperedProjections(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "ship beta", "--kind", "ship", "--repo", "munsu", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	if err := home.WriteMeta(homeDir, "beta", map[string]string{"kind": "scout", "runtime": "keep-me"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.StateDir(homeDir), "beta.status"), []byte{0x00, 0x01}, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runTaskCommand(t, []string{"task", "reconcile", "--output", "json", "--home", homeDir})
	if err != nil {
		t.Fatalf("task reconcile: %v\n%s", err, out)
	}
	var resp Response[[]TaskProjectionRow]
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if resp.Kind != "task.reconcile" || resp.Status != "success" || len(resp.Data) != 1 {
		t.Fatalf("response = %+v\n%s", resp, out)
	}
	row := resp.Data[0]
	if row.TaskID != "beta" || row.Generation != "1" || row.Revision != 1 {
		t.Fatalf("row = %+v", row)
	}
	if row.Meta != "repaired" || row.Status != "repaired" {
		t.Fatalf("row = %+v", row)
	}
	meta := readCLIMeta(t, homeDir, "beta")
	if meta["kind"] != "ship" || meta["description"] != "ship beta" || meta["project"] != "munsu" {
		t.Fatalf("repaired meta = %v", meta)
	}
	if meta["owner"] != filepath.Base(homeDir) || meta["generation"] != "1" || meta["state"] != "queued" {
		t.Fatalf("repaired meta = %v", meta)
	}
	if meta["runtime"] != "keep-me" {
		t.Fatalf("runtime projection field lost: %v", meta)
	}
	lines, err := home.ReadStatus(homeDir, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !reflectDeepEqualStrings(lines, []string{"queued: cli task add"}) {
		t.Fatalf("derived status = %q", lines)
	}
	agg, err := testAuthorityFor(t, homeDir).Get("beta")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != taskauthority.FirstRevision || agg.Generation != 1 {
		t.Fatalf("reconcile changed authoritative record: %+v", agg)
	}
}

func readCLIMeta(t *testing.T, homeDir, id string) map[string]string {
	t.Helper()
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func reflectDeepEqualStrings(a, b []string) bool {
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

// TestTaskReconcileSingleTask proves `task reconcile <id>` repairs only the
// named task.
func TestTaskReconcileSingleTask(t *testing.T) {
	homeDir := t.TempDir()
	for _, args := range [][]string{
		{"task", "add", "alpha", "first", "--home", homeDir},
		{"task", "add", "beta", "second", "--home", homeDir},
	} {
		if out, err := runTaskCommand(t, args); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := home.WriteMeta(homeDir, "beta", map[string]string{"kind": "scout"}); err != nil {
		t.Fatal(err)
	}
	// Tamper alpha so an accidental whole-home reconcile would visibly repair
	// it: task add now fully derives the projection, so a single-task
	// reconcile must leave the tampered alpha projection untouched.
	if err := home.WriteMeta(homeDir, "alpha", map[string]string{"kind": "scout", "generation": "99"}); err != nil {
		t.Fatal(err)
	}
	out, err := runTaskCommand(t, []string{"task", "reconcile", "beta", "--output", "json", "--home", homeDir})
	if err != nil {
		t.Fatalf("task reconcile beta: %v\n%s", err, out)
	}
	var resp Response[[]TaskProjectionRow]
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(resp.Data) != 1 || resp.Data[0].TaskID != "beta" || resp.Data[0].Meta != "repaired" {
		t.Fatalf("response = %+v\n%s", resp, out)
	}
	alphaMeta := readCLIMeta(t, homeDir, "alpha")
	if alphaMeta["generation"] != "99" || alphaMeta["kind"] != "scout" {
		t.Fatalf("single-task reconcile touched alpha: %v", alphaMeta)
	}
}

// TestTaskReconcileIsIdempotent proves a second `task reconcile` pass
// reports every projection unchanged and rewrites nothing.
func TestTaskReconcileIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "work", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	if out, err := runTaskCommand(t, []string{"task", "reconcile", "--output", "json", "--home", homeDir}); err != nil {
		t.Fatalf("first reconcile: %v\n%s", err, out)
	}
	metaBefore := readFileForTest(t, filepath.Join(homeDir, "state", "beta.meta"))
	statusBefore := readFileForTest(t, filepath.Join(homeDir, "state", "beta.status"))
	out, err := runTaskCommand(t, []string{"task", "reconcile", "--output", "json", "--home", homeDir})
	if err != nil {
		t.Fatalf("second reconcile: %v\n%s", err, out)
	}
	var resp Response[[]TaskProjectionRow]
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(resp.Data) != 1 || resp.Data[0].Meta != "unchanged" || resp.Data[0].Status != "unchanged" {
		t.Fatalf("second pass = %+v\n%s", resp.Data, out)
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", "beta.meta")); got != metaBefore {
		t.Fatal("second pass rewrote meta projection")
	}
	if got := readFileForTest(t, filepath.Join(homeDir, "state", "beta.status")); got != statusBefore {
		t.Fatal("second pass rewrote status projection")
	}
}

// TestTaskReconcileTypedPartialOutcome proves a projection that cannot be
// repaired surfaces a typed retryable partial error while the authoritative
// record stays intact.
func TestTaskReconcileTypedPartialOutcome(t *testing.T) {
	homeDir := t.TempDir()
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "work", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	metaPath := filepath.Join(home.StateDir(homeDir), "beta.meta")
	if err := os.Remove(metaPath); err != nil {
		t.Fatal(err)
	}
	// A directory where the meta file belongs makes the atomic rename fail.
	if err := os.Mkdir(metaPath, 0o700); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	args := []string{"task", "reconcile", "--output", "json", "--home", homeDir}
	root.SetArgs(args)
	err := root.Execute()
	var partial *ProjectionPartialError
	if !errors.As(err, &partial) || len(partial.Failed) != 1 || partial.Failed[0].TaskID != "beta" {
		t.Fatalf("error = %T %v, want typed projection partial", err, err)
	}
	WriteContractError(&out, err, args)
	if !strings.Contains(out.String(), `"error_code": "projection_failed"`) || !strings.Contains(out.String(), `"retryable": true`) {
		t.Fatalf("contract error = %s", out.String())
	}
	agg, getErr := testAuthorityFor(t, homeDir).Get("beta")
	if getErr != nil {
		t.Fatalf("authoritative record must survive projection failure: %v", getErr)
	}
	if agg.Revision != taskauthority.FirstRevision || agg.Generation != 1 {
		t.Fatalf("projection failure changed authoritative record: %+v", agg)
	}
}

// TestTaskReconcileEmptyHomeSucceeds proves reconciling a home with no
// canonical tasks is a success no-op.
func TestTaskReconcileEmptyHomeSucceeds(t *testing.T) {
	homeDir := t.TempDir()
	out, err := runTaskCommand(t, []string{"task", "reconcile", "--output", "json", "--home", homeDir})
	if err != nil {
		t.Fatalf("task reconcile: %v\n%s", err, out)
	}
	var resp Response[[]TaskProjectionRow]
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if resp.Status != "success" || len(resp.Data) != 0 {
		t.Fatalf("response = %+v\n%s", resp, out)
	}
}
