package taskauthority

import (
	"os"
	"path/filepath"
	"testing"
)

// This file characterizes the current enumeration behavior of the canonical
// Task Authority over the filesystem-level task-authority layout (A-02 seam).
// It locks the existing semantics so the Phase-1 home.ReadDir refactor can
// prove it preserves them: missing directory => empty, non-directory and
// symlinked entries are skipped, and malformed committed documents fail closed.

// tasksQueryPath returns the physical on-disk path for a sub-path under the
// canonical task-authority tasks directory.
func (c *Canonical) tasksQueryPath(t *testing.T, sub ...string) string {
	t.Helper()
	full := tasksDir
	for _, s := range sub {
		full += "/" + s
	}
	p, err := c.h.Path(canonicalRoot, full)
	if err != nil {
		t.Fatalf("Path(%s): %v", full, err)
	}
	return p
}

// TestListEmptyTasksDir characterizes List with no task-authority/tasks dir.
func TestListEmptyTasksDir(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	got, err := c.List()
	if err != nil {
		t.Fatalf("List with missing tasks dir: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List with missing tasks dir = %d tasks, want 0", len(got))
	}
}

// TestListSkipsNonDirectoryTasksEntries characterizes that a regular file
// directly under tasks/ (not a task directory) is ignored by enumeration.
func TestListSkipsNonDirectoryTasksEntries(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	// A real task for baseline.
	mustCreate(t, c, "t1")

	// A stray file inside tasks/ that is not a directory.
	dir := c.tasksQueryPath(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-task"), []byte("junk"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].TaskID != "t1" {
		t.Fatalf("List with stray file = %+v, want only [t1]", taskIDsOf(got))
	}
}

// TestListSkipsSymlinkedTasksEntries characterizes that a tasks/ entry that is
// a symlink (not a real directory) is ignored by enumeration.
func TestListSkipsSymlinkedTasksEntries(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// An outside directory pointed at by a symlinked tasks/ entry.
	outside := t.TempDir()
	dir := c.tasksQueryPath(t)
	if err := os.Symlink(outside, filepath.Join(dir, "sneaky")); err != nil {
		t.Fatal(err)
	}

	got, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].TaskID != "t1" {
		t.Fatalf("List with symlinked entry = %+v, want only [t1]", taskIDsOf(got))
	}
}

// TestListMalformedCurrentFailsClosed characterizes that a task directory with
// a malformed current.json makes List fail closed rather than skip.
func TestListMalformedCurrentFailsClosed(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	path, err := c.h.Path(canonicalRoot, taskCurrentKey("t1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := c.List(); err == nil {
		t.Fatalf("List on malformed current.json = nil error, want failure")
	}
}

// TestListHoldsSkipsNonHoldFiles characterizes ListHolds ignoring stray files
// in the holds directory that do not end in .json.
func TestListHoldsSkipsNonHoldFiles(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	// Baseline: a real, committed dispatch hold.
	mustCreate(t, c, "t1")
	hold := CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "hold-1",
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart},
		Reason:  "characterization",
	}
	if _, err := c.AddHold(mustOperation(t, "op-hold-1", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}

	// A stray non-json file in holds/ must be skipped.
	p, err := c.h.Path(canonicalRoot, holdsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "notjson.txt"), []byte("junk"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := c.ListHolds()
	if err != nil {
		t.Fatalf("ListHolds: %v", err)
	}
	if len(got) != 1 || got[0].ID != "hold-1" {
		t.Fatalf("ListHolds with stray file = %+v, want only [hold-1]", holdIDsOf(got))
	}
}

// TestListHoldsMissingHoldsDir characterizes ListHolds with no holds directory.
func TestListHoldsMissingHoldsDir(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	got, err := c.ListHolds()
	if err != nil {
		t.Fatalf("ListHolds with missing holds dir: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListHolds with missing holds dir = %d holds, want 0", len(got))
	}
}

func taskIDsOf(aggs []Aggregate) []string {
	out := make([]string, 0, len(aggs))
	for _, a := range aggs {
		out = append(out, a.TaskID)
	}
	return out
}

func holdIDsOf(holds []DispatchHold) []string {
	out := make([]string, 0, len(holds))
	for _, h := range holds {
		out = append(out, h.ID)
	}
	return out
}
