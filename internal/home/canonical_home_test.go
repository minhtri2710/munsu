package home

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// newTestHome returns a fresh canonical home on an isolated real temp dir.
func newTestHome(t *testing.T) *Home {
	t.Helper()
	h, err := Init(t.TempDir())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return h
}

func TestInitCreatesVerifiedV1Home(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	id := h.Identity()
	if id.LayoutVersion != LayoutVersion {
		t.Errorf("LayoutVersion = %d, want %d", id.LayoutVersion, LayoutVersion)
	}
	if id.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", id.SchemaVersion)
	}
	if id.ID == "" {
		t.Error("ID is empty")
	}
	if id.CanonicalRoot == "" {
		t.Error("CanonicalRoot is empty")
	}
	// identity file exists and is private to owner
	testutil.AssertOwnerPrivate(t, filepath.Join(root, IdentityFileName))
	// stable identity across Opens
	h2, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if h2.Identity() != id {
		t.Errorf("identity changed across Open: %+v != %+v", h2.Identity(), id)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	root := t.TempDir()
	h1, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	h2, err := Init(root)
	if err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if h1.Identity() != h2.Identity() {
		t.Errorf("re-Init changed identity: %+v != %+v", h1.Identity(), h2.Identity())
	}
	// still readable as current v1
	if _, err := Open(root); err != nil {
		t.Fatalf("Open after re-init: %v", err)
	}
}

func TestInitEmptyDirOk(t *testing.T) {
	root := t.TempDir() // exists and is empty
	if _, err := Init(root); err != nil {
		t.Fatalf("Init on empty dir: %v", err)
	}
}

func TestInitNonEmptyNotHomeFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "junk.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Init(root)
	if !errors.Is(err, ErrNonEmptyHome) {
		t.Fatalf("Init non-empty stray dir: got %v, want ErrNonEmptyHome", err)
	}
}

func TestInitRootNotDirectoryFails(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Init(file)
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Init non-dir: got %v, want ErrNotDirectory", err)
	}
}

func TestInitEmptyRootFails(t *testing.T) {
	if _, err := Init(""); !errors.Is(err, ErrEmptyRoot) {
		t.Fatalf("Init empty root: got %v, want ErrEmptyRoot", err)
	}
}

func TestOpenNotInitializedFails(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Open uninitialized: got %v, want ErrNotInitialized", err)
	}
}

func TestMalformedIdentityFailsClosed(t *testing.T) {
	root := t.TempDir()
	h := newTestHome(t)
	_ = h
	// Corrupt the identity file.
	if err := os.WriteFile(filepath.Join(root, IdentityFileName), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	// Recreate through Init: identity exists but is malformed -> fail closed.
	if _, err := Init(root); !errors.Is(err, ErrMalformedIdentity) {
		t.Fatalf("Init malformed identity: got %v, want ErrMalformedIdentity", err)
	}
	if _, err := Open(root); !errors.Is(err, ErrMalformedIdentity) {
		t.Fatalf("Open malformed identity: got %v, want ErrMalformedIdentity", err)
	}
}

func TestUnsupportedLayoutVersionFailsClosed(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	id := h.Identity()
	id.LayoutVersion = 999
	data, _ := json.Marshal(id)
	if err := os.WriteFile(filepath.Join(root, IdentityFileName), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); !errors.Is(err, ErrUnsupportedLayout) {
		t.Fatalf("Open unsupported layout: got %v, want ErrUnsupportedLayout", err)
	}
}

func TestUnsupportedSchemaVersionFailsClosed(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	id := h.Identity()
	id.SchemaVersion = 7
	data, _ := json.Marshal(id)
	if err := os.WriteFile(filepath.Join(root, IdentityFileName), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open unsupported schema: got %v, want ErrUnsupportedSchema", err)
	}
}

func TestIdentityRootMismatchFailsClosed(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	id := h.Identity()
	id.CanonicalRoot = "/somewhere/else"
	data, _ := json.Marshal(id)
	if err := os.WriteFile(filepath.Join(root, IdentityFileName), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("Open identity mismatch: got %v, want ErrIdentityMismatch", err)
	}
}

func TestLayoutAndPermissionsPrivate(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{RootState, RootData, RootConfig, RootProjects} {
		dir := filepath.Join(root, name)
		testutil.AssertOwnerPrivate(t, dir)
	}
}

func TestRootFor(t *testing.T) {
	h := newTestHome(t)
	state, err := h.RootFor(RootState)
	if err != nil {
		t.Fatal(err)
	}
	if state != filepath.Join(h.Root(), RootState) {
		t.Errorf("RootFor(state) = %q, want %q", state, filepath.Join(h.Root(), RootState))
	}
	if _, err := h.RootFor("nonexistent"); !errors.Is(err, ErrUnknownRoot) {
		t.Fatalf("RootFor(unknown): got %v, want ErrUnknownRoot", err)
	}
}

func TestPathContainment(t *testing.T) {
	h := newTestHome(t)
	valid, err := h.Path(RootState, "a/b/c")
	if err != nil {
		t.Fatalf("Path valid: %v", err)
	}
	if valid != filepath.Join(h.Root(), RootState, "a", "b", "c") {
		t.Errorf("Path = %q", valid)
	}
	bad := []string{"", "..", "../x", "../../etc", "/abs", "a/../../etc", "./x", "a/../b"}
	for _, key := range bad {
		if _, err := h.Path(RootState, key); err == nil {
			t.Errorf("Path(%q) unexpectedly succeeded", key)
		}
	}
}

func TestJoinContainedLogicalKeys(t *testing.T) {
	h := newTestHome(t)
	root, err := h.RootFor(RootState)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"a/b/c",
		"single",
		"nested/sub/dir/file.txt",
		"tasks/abc",
	} {
		got, err := joinContained(root, key)
		if err != nil {
			t.Errorf("joinContained(%q) unexpected error: %v", key, err)
		}
		want := filepath.Join(root, filepath.FromSlash(key))
		if got != want {
			t.Errorf("joinContained(%q) = %q, want %q", key, got, want)
		}
	}

	for _, key := range []string{
		"",
	} {
		if _, err := joinContained(root, key); !errors.Is(err, ErrEmptyKey) {
			t.Errorf("joinContained(%q) = %v, want ErrEmptyKey", key, err)
		}
	}

	for _, key := range []string{
		"/abs",
		"/a/b/c",
		"\\abs",
		"\\a\\b",
		"C:foo",
		"C:/foo",
	} {
		if _, err := joinContained(root, key); !errors.Is(err, ErrAbsoluteKey) {
			t.Errorf("joinContained(%q) = %v, want ErrAbsoluteKey", key, err)
		}
	}

	for _, key := range []string{
		"..",
		"../x",
		"../../etc",
		"a/../../etc",
		"./x",
		"a/../b",
		"a//b",
		"a/b/.",
		"a/b/..",
		"a/b/",
		"a\\b",
		"a\\..\\b",
		"tasks/foo:bar",
		"CON",
		"con.txt",
		"CON .txt",
		"COM1",
		"LPT9.log",
		"NUL...",
	} {
		if _, err := joinContained(root, key); !errors.Is(err, ErrKeyEscapes) {
			t.Errorf("joinContained(%q) = %v, want ErrKeyEscapes", key, err)
		}
	}
}

func TestPathSymlinkEscapeFailsClosed(t *testing.T) {
	h := newTestHome(t)
	state, _ := h.RootFor(RootState)
	outside := t.TempDir()
	link := filepath.Join(state, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := h.Path(RootState, "escape/secret"); !errors.Is(err, ErrSymlinkEscapes) {
		t.Fatalf("Path through escaping symlink: got %v, want ErrSymlinkEscapes", err)
	}
}

func TestReadAndPathAccessors(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("test-read")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(lk, "txn-read", 0, []ChangeItem{{Root: RootData, Key: "k/v", Data: []byte("hello")}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	data, err := h.Read(RootData, "k/v")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("Read = %q, want hello", data)
	}
	// Read of a symlink-escaped key fails closed.
	state, _ := h.RootFor(RootState)
	outside := t.TempDir()
	link := filepath.Join(state, "esc")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := h.Read(RootState, "esc/x"); !errors.Is(err, ErrSymlinkEscapes) {
			t.Errorf("Read escaping symlink: got %v, want ErrSymlinkEscapes", err)
		}
	}
}

func TestLockScopedExclusiveAndFence(t *testing.T) {
	h := newTestHome(t)
	lk1, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if lk1.FenceToken() == 0 {
		t.Error("fence token is zero")
	}
	if err := lk1.Release(); err != nil {
		t.Fatal(err)
	}
	lk2, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if lk2.FenceToken() <= lk1.FenceToken() {
		t.Errorf("fence token did not advance: %d <= %d", lk2.FenceToken(), lk1.FenceToken())
	}
	if err := lk2.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestLockInvalidScope(t *testing.T) {
	h := newTestHome(t)
	for _, scope := range []string{"", "a/b", "a\\b", "..", "."} {
		if _, err := h.Lock(scope); !errors.Is(err, ErrInvalidScope) {
			t.Errorf("Lock(%q): got %v, want ErrInvalidScope", scope, err)
		}
	}
}

func TestCommitConflictFailsClosed(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(lk, "txn-1", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("a")}}); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	// Expected revision now 1; a stale expected 0 must conflict.
	if _, err := h.Commit(lk, "txn-2", 0, []ChangeItem{{Root: RootData, Key: "k2", Data: []byte("b")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale commit: got %v, want ErrConflict", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestCommitFenceRequired(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}}); !errors.Is(err, ErrFenced) {
		t.Fatalf("commit after release: got %v, want ErrFenced", err)
	}
}

func TestCommitRejectsStaleFence(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	if rev, err := h.Commit(lk, "happy", 0, []ChangeItem{{Root: RootData, Key: "happy", Data: []byte("ok")}}); err != nil {
		t.Fatalf("happy-path commit: %v", err)
	} else if rev != 1 {
		t.Fatalf("happy-path revision = %d, want 1", rev)
	}
	if err := canonicalAtomicWrite(h.fencePath("scope"), []byte("999\n")); err != nil {
		t.Fatalf("write newer fence: %v", err)
	}
	if _, err := h.Commit(lk, "stale", 1, []ChangeItem{{Root: RootData, Key: "stale", Data: []byte("no")}}); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale-fence commit: got %v, want ErrFenced", err)
	}
}

func TestCommitRejectsMalformedFence(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	if err := canonicalAtomicWrite(h.fencePath("scope"), []byte("not-a-fence\n")); err != nil {
		t.Fatalf("write malformed fence: %v", err)
	}
	if _, err := h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}}); err == nil || !strings.Contains(err.Error(), "home: parse lock fence") {
		t.Fatalf("malformed-fence commit: got %v, want a parse lock fence error", err)
	}
}

func TestCommitEmptyChangesetFails(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(lk, "txn", 0, nil); !errors.Is(err, ErrEmptyChangeset) {
		t.Fatalf("empty changeset: got %v, want ErrEmptyChangeset", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestCommitInvalidTxnFails(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	if _, err := h.Commit(lk, "../evil", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("x")}}); !errors.Is(err, ErrInvalidTxnID) {
		t.Fatalf("invalid txn: got %v, want ErrInvalidTxnID", err)
	}
}

func TestCommitEscapingItemFails(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	if _, err := h.Commit(lk, "txn", 0, []ChangeItem{{Root: RootData, Key: "../../etc/passwd", Data: []byte("x")}}); !errors.Is(err, ErrKeyEscapes) {
		t.Fatalf("escaping item: got %v, want ErrKeyEscapes", err)
	}
}

func TestCommitAtomicReadAndRevision(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := h.Commit(lk, "txn-1", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("v1")}})
	if err != nil {
		t.Fatal(err)
	}
	if rev != 1 {
		t.Errorf("rev = %d, want 1", rev)
	}
	rev, err = h.Commit(lk, "txn-2", rev, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("v2")}})
	if err != nil {
		t.Fatal(err)
	}
	if rev != 2 {
		t.Errorf("rev = %d, want 2", rev)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	data, _ := h.Read(RootData, "k")
	if string(data) != "v2" {
		t.Errorf("Read = %q, want v2", data)
	}
}

func TestJournalRecoveryReplaysInterruptedCommit(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted commit: write a prepared journal record whose
	// items were only partially applied, then re-open and expect recovery.
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	rec := journalRecord{
		TxnID:            "interrupted",
		Scope:            "scope",
		FenceToken:       uint64(lk.token),
		ExpectedRevision: 0,
		NewRevision:      1,
		Items: []ChangeItem{
			{Root: RootData, Key: "a", Data: []byte("A")},
			{Root: RootData, Key: "b", Data: []byte("B")},
		},
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}

	h2, err := Open(root)
	if err != nil {
		t.Fatalf("Open after interrupted commit: %v", err)
	}
	// Items were replayed.
	if data, err := h2.Read(RootData, "a"); err != nil || string(data) != "A" {
		t.Errorf("recovered a = %q, err=%v", data, err)
	}
	if data, err := h2.Read(RootData, "b"); err != nil || string(data) != "B" {
		t.Errorf("recovered b = %q, err=%v", data, err)
	}
	// Revision advanced to the journal's new revision.
	lk2, err := h2.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h2.Commit(lk2, "txn-next", 1, []ChangeItem{{Root: RootData, Key: "c", Data: []byte("C")}}); err != nil {
		t.Fatalf("commit after recovery: %v", err)
	}
	if err := lk2.Release(); err != nil {
		t.Fatal(err)
	}
	// Journal dir should be empty of records.
	entries, err := os.ReadDir(filepath.Join(root, JournalDirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("journal record not cleaned up: %s", e.Name())
		}
	}
}

func TestJournalRecoverySkipsSupersededRecord(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	// Advance the scope to revision 2 with the current value.
	if _, err := h.Commit(lk, "t1", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("V1")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(lk, "t2", 1, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("NEW")}}); err != nil {
		t.Fatal(err)
	}
	// Leave a superseded record on disk: a well-formed record for revision 1
	// whose items carry the stale value. Recovery must not re-apply it.
	stale := journalRecord{
		TxnID:            "stale",
		Scope:            "scope",
		FenceToken:       uint64(lk.token),
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            []ChangeItem{{Root: RootData, Key: "k", Data: []byte("OLD")}},
	}
	if err := h.writeJournalRecord(stale); err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}

	h2, err := Open(root)
	if err != nil {
		t.Fatalf("Open with superseded record: %v", err)
	}
	if data, err := h2.Read(RootData, "k"); err != nil || string(data) != "NEW" {
		t.Errorf("k = %q err=%v, want NEW (superseded record must not clobber)", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, JournalDirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("superseded record not cleaned up: %s", e.Name())
		}
	}
}

func TestJournalRecoveryRejectsRevisionGap(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	// NewRevision is not one past ExpectedRevision: only corruption produces this.
	rec := journalRecord{
		TxnID:            "gap",
		Scope:            "scope",
		FenceToken:       uint64(lk.token),
		ExpectedRevision: 0,
		NewRevision:      5,
		Items:            []ChangeItem{{Root: RootData, Key: "k", Data: []byte("X")}},
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("Open must fail on a journal record with a revision gap")
	}
}

func TestJournalRecoveryRejectsRegressedRevision(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	// A well-formed record whose expected revision is ahead of the actual scope
	// revision (0): the scope revision regressed, e.g. a lost .rev file.
	rec := journalRecord{
		TxnID:            "ahead",
		Scope:            "scope",
		FenceToken:       uint64(lk.token),
		ExpectedRevision: 3,
		NewRevision:      4,
		Items:            []ChangeItem{{Root: RootData, Key: "k", Data: []byte("X")}},
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("Open must fail when the scope revision is behind the record's expected revision")
	}
}

func TestJournalRecoveryRejectsEmptyScope(t *testing.T) {
	root := t.TempDir()
	h, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	rec := journalRecord{
		TxnID:            "blank",
		Scope:            "",
		FenceToken:       uint64(lk.token),
		ExpectedRevision: 0,
		NewRevision:      1,
		Items:            []ChangeItem{{Root: RootData, Key: "k", Data: []byte("X")}},
	}
	if err := h.writeJournalRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("Open must fail on a journal record with an empty scope")
	}
}

func TestCommitOverwritesAtomically(t *testing.T) {
	h := newTestHome(t)
	lk, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		rev, err := h.Commit(lk, "txn", uint64(i-1), []ChangeItem{{Root: RootData, Key: "k", Data: []byte(strings.Repeat("x", i))}})
		if err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
		if rev != uint64(i) {
			t.Errorf("rev = %d, want %d", rev, i)
		}
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	// Data file perms are private.
	path, _ := h.Path(RootData, "k")
	testutil.AssertOwnerPrivate(t, path)
}

func TestCommitBetweenTwoHomesOnSameRoot(t *testing.T) {
	root := t.TempDir()
	h1, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lk1, err := h1.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h1.Commit(lk1, "txn-1", 0, []ChangeItem{{Root: RootData, Key: "k", Data: []byte("one")}}); err != nil {
		t.Fatal(err)
	}
	if err := lk1.Release(); err != nil {
		t.Fatal(err)
	}
	if data, err := h2.Read(RootData, "k"); err != nil || string(data) != "one" {
		t.Errorf("h2 read = %q, err=%v", data, err)
	}
}

// TestContainedJoin exercises the isolated refusal decision that joinContained
// delegates to, so the branch is entered directly rather than only implicitly
// through withinRoot. A refusal returns the zero path and ErrKeyEscapes; an
// acceptance returns the joined path unchanged with a nil error.
func TestContainedJoin(t *testing.T) {
	if p, err := containedJoin("/root/sub", false); err == nil || !errors.Is(err, ErrKeyEscapes) || p != "" {
		t.Fatalf("containedJoin(joined, false) = %q, %v; want %q, ErrKeyEscapes", p, err, "")
	}
	if p, err := containedJoin("/root/sub", true); err != nil || p != "/root/sub" {
		t.Fatalf("containedJoin(joined, true) = %q, %v; want %q, nil", p, err, "/root/sub")
	}
}
