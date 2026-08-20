package home

import (
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// TestCaptainDurableKeyRoundTrip exercises the full taskmeta and classification
// surface with soldier-slug and captain:<id> style logical keys on every
// platform. It never asserts a specific persisted filename, so it passes both
// on Unix (durable key is the identity) and on Windows (durable key is the
// NTFS-safe encoding), while proving that the write path, read path, status
// path, and enumeration agree on the logical id.
func TestCaptainDurableKeyRoundTrip(t *testing.T) {
	tmp := t.TempDir()

	ids := []string{
		"task-1",
		"captain:test",
		"captain:domain-alpha",
		"captain:my cap",
		"cap:t.",
	}
	for _, id := range ids {
		if err := WriteMeta(tmp, id, map[string]string{"kind": "captain", "home": "/h"}); err != nil {
			t.Fatalf("WriteMeta(%q): %v", id, err)
		}
		got, err := ReadMeta(tmp, id)
		if err != nil {
			t.Fatalf("ReadMeta(%q): %v", id, err)
		}
		if got["kind"] != "captain" {
			t.Errorf("ReadMeta(%q) kind = %q, want captain", id, got["kind"])
		}

		line := "working: test " + id
		if err := AppendStatus(tmp, id, line); err != nil {
			t.Fatalf("AppendStatus(%q): %v", id, err)
		}
		lines, err := ReadStatus(tmp, id)
		if err != nil {
			t.Fatalf("ReadStatus(%q): %v", id, err)
		}
		if len(lines) != 1 || lines[0] != line {
			t.Errorf("ReadStatus(%q) = %v, want [%q]", id, lines, line)
		}
	}
}

// TestListMetaReturnsLogicalIDs verifies enumeration reverses the durable key:
// ListMeta reports the logical ids (including captain:<id>) exactly as they
// were written, not the persisted stems.
func TestListMetaReturnsLogicalIDs(t *testing.T) {
	tmp := t.TempDir()

	ids := []string{"task-a", "captain:test", "captain:alpha"}
	for _, id := range ids {
		if err := WriteMeta(tmp, id, map[string]string{"kind": "captain"}); err != nil {
			t.Fatalf("WriteMeta(%q): %v", id, err)
		}
	}

	got, err := ListMeta(tmp)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range got {
		seen[e.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("ListMeta missing logical id %q (got %v)", id, got)
		}
	}
}

// TestScanGeneralRelevantReturnsLogicalIDs verifies the status enumeration
// reverses the durable key so callers receive the logical task id.
func TestScanGeneralRelevantReturnsLogicalIDs(t *testing.T) {
	tmp := t.TempDir()
	sd := StateDir(tmp)

	for _, id := range []string{"captain:domain", "captain:infra"} {
		if err := AppendStatus(tmp, id, "done: shipped "+id); err != nil {
			t.Fatalf("AppendStatus(%q): %v", id, err)
		}
	}

	matches := ScanGeneralRelevant(sd)
	// "done:" lines are general-relevant.
	found := map[string]bool{}
	for _, m := range matches {
		found[m.TaskID] = true
	}
	for _, id := range []string{"captain:domain", "captain:infra"} {
		if !found[id] {
			t.Errorf("ScanGeneralRelevant missing logical id %q (got %v)", id, matches)
		}
	}
}

func TestDurableKeyRejectsInvalidIDs(t *testing.T) {
	for _, id := range []string{"", ".", "..", "a/b", "a\\b"} {
		if _, err := DurableKey(id); err == nil {
			t.Errorf("DurableKey(%q) succeeded, want invalid-id error", id)
		}
	}
}

// TestAbsorbClassReadsDurableKey verifies AbsorbClass resolves the logical id
// to the durable status file.
func TestAbsorbClassReadsDurableKey(t *testing.T) {
	tmp := t.TempDir()
	if err := AppendStatus(tmp, "captain:absorb", "working: actively working"); err != nil {
		t.Fatal(err)
	}
	if got := AbsorbClass("captain:absorb", filepath.Join(tmp, "state")); got != domain.Working {
		t.Errorf("AbsorbClass = %v, want Working", got)
	}
}
