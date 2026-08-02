package taskauthorityfs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// writeDocAt writes data to homeDir/rel, creating parents with private modes.
func writeDocAt(t *testing.T, homeDir, rel string, data []byte) {
	t.Helper()
	abs := filepath.Join(homeDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeAggregateDoc encodes and writes one aggregate document.
func writeAggregateDoc(t *testing.T, homeDir, taskID string, generation, revision uint64, current bool) {
	t.Helper()
	agg, err := taskauthority.NewAggregate(taskID, "owner", "work", "ship", "", "")
	if err != nil {
		t.Fatal(err)
	}
	agg.Generation = taskauthority.Generation(generation)
	agg.Revision = taskauthority.Revision(revision)
	agg.Current = current
	data, err := EncodeAggregate(agg)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := AggregateRelPath(taskID, agg.Generation)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

// writeCurrentPointer encodes and writes one current generation pointer file.
func writeCurrentPointer(t *testing.T, homeDir, taskID string, generation uint64) {
	t.Helper()
	data, err := EncodeCurrentPointer(taskauthority.Generation(generation))
	if err != nil {
		t.Fatal(err)
	}
	rel, err := CurrentPointerRelPath(taskID)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

func writeHoldDoc(t *testing.T, homeDir string, hold taskauthority.DispatchHold) {
	t.Helper()
	data, err := EncodeHold(hold)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := HoldRelPath(hold.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

func writeInterpretationDoc(t *testing.T, homeDir string, rec taskauthority.DispatchInterpretation) {
	t.Helper()
	data, err := EncodeInterpretation(rec)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := InterpretationRelPath(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

func writeDecisionDoc(t *testing.T, homeDir string, dec taskauthority.DispatchDecision) {
	t.Helper()
	data, err := EncodeDecision(dec)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := DecisionRelPath(dec.Key)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

func writeReceiptDoc(t *testing.T, homeDir string, receipt taskauthority.Receipt) {
	t.Helper()
	data, err := EncodeReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := ReceiptRelPath(receipt.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

func writeAuditDoc(t *testing.T, homeDir string, ev taskauthority.AuditEvent) {
	t.Helper()
	data, err := EncodeAudit(ev)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := AuditRelPath(ev.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

func writeManifestDoc(t *testing.T, homeDir string, manifest TransactionManifest) {
	t.Helper()
	data, err := EncodeTransactionManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := TransactionManifestRelPath(manifest.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	writeDocAt(t, homeDir, rel, data)
}

// openStore constructs a Store for homeDir, failing the test on error.
func openStore(t *testing.T, homeDir string) *Store {
	t.Helper()
	s, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mustView loads the View, failing the test on error.
func mustView(t *testing.T, s *Store) taskauthority.View {
	t.Helper()
	v, err := s.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// assertCorrupt asserts View fails closed with a typed corrupt-document error.
func assertCorrupt(t *testing.T, home string) {
	t.Helper()
	_, err := openStore(t, home).View()
	if !errors.Is(err, ErrCorruptDocument) {
		t.Fatalf("View error = %v, want ErrCorruptDocument", err)
	}
	var ce *CorruptDocumentError
	if !errors.As(err, &ce) {
		t.Fatalf("View error %v is not a *CorruptDocumentError", err)
	}
}

func TestStoreConstruction(t *testing.T) {
	t.Run("empty home rejected", func(t *testing.T) {
		if _, err := NewStore(""); err == nil {
			t.Fatal("NewStore(\"\") error = nil, want error")
		}
	})

	t.Run("file home rejected", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(file); err == nil {
			t.Fatal("NewStore(file) error = nil, want error")
		}
	})

	t.Run("missing home yields empty view", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "missing")
		s := openStore(t, home)
		if v := mustView(t, s); len(v.Aggregates) != 0 || len(v.Holds) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
			t.Fatalf("missing home view = %+v, want empty", v)
		}
	})

	t.Run("construction creates nothing", func(t *testing.T) {
		home := t.TempDir()
		if _, err := NewStore(home); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("NewStore created %d entries, want none (read-only construction)", len(entries))
		}
	})
}

// TestViewEmptyHome proves a read on a home with no authority state returns
// the empty committed view and leaves the home untouched: no state/
// directory and no state/.dispatch.lock are created. A pure read must never
// mutate the home it reads (the fleet snapshot contract depends on this).
func TestViewEmptyHome(t *testing.T) {
	home := t.TempDir()
	v := mustView(t, openStore(t, home))
	if len(v.Aggregates) != 0 || len(v.Holds) != 0 || len(v.Interpretations) != 0 || len(v.Decisions) != 0 || len(v.Receipts) != 0 || len(v.Audit) != 0 {
		t.Fatalf("empty home view = %+v, want all record families empty", v)
	}
	if _, err := os.Lstat(filepath.Join(home, "state")); !os.IsNotExist(err) {
		t.Fatalf("read on empty home created state/ (err=%v), want no state directory", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("read on empty home mutated it: %v", entries)
	}
}

// buildCanonicalHome writes a representative committed v2 state: two tasks
// (one with a historical generation), holds, interpretations, decisions,
// receipts, and audit.
func buildCanonicalHome(t *testing.T, homeDir string) {
	t.Helper()
	writeCurrentPointer(t, homeDir, "t1", 1)
	writeAggregateDoc(t, homeDir, "t1", 1, 3, true)
	writeAggregateDoc(t, homeDir, "t1", 2, 1, false)
	writeCurrentPointer(t, homeDir, "t2", 1)
	writeAggregateDoc(t, homeDir, "t2", 1, 1, true)

	hold := fixtureHold()
	writeHoldDoc(t, homeDir, hold)
	secondHold := fixtureHold()
	secondHold.ID = "hold-2"
	secondHold.CreatedAt = 2
	writeHoldDoc(t, homeDir, secondHold)

	writeInterpretationDoc(t, homeDir, fixtureInterpretation())

	writeDecisionDoc(t, homeDir, fixtureDecision())

	writeReceiptDoc(t, homeDir, fixtureReceipt())
	holdOnly := taskauthority.Receipt{OperationID: "op-2", Digest: testDigest(), CommittedAt: 2}
	writeReceiptDoc(t, homeDir, holdOnly)

	writeAuditDoc(t, homeDir, fixtureAuditLifecycle())
	writeAuditDoc(t, homeDir, fixtureAuditDispatch())
}

func findReceipt(receipts []taskauthority.Receipt, id string) (taskauthority.Receipt, bool) {
	for _, r := range receipts {
		if r.OperationID == id {
			return r, true
		}
	}
	return taskauthority.Receipt{}, false
}

func TestViewLoadsCanonicalRecords(t *testing.T) {
	home := t.TempDir()
	buildCanonicalHome(t, home)
	v := mustView(t, openStore(t, home))

	cur, ok := v.Current("t1")
	if !ok {
		t.Fatal("v.Current(t1) not found")
	}
	if cur.Generation != 1 || cur.Revision != 3 || !cur.Current || cur.Phase != taskauthority.PhaseQueued {
		t.Fatalf("t1 current aggregate = %+v, want generation 1 revision 3", cur)
	}
	historical, ok := v.Aggregate("t1", 2)
	if !ok || historical.Current {
		t.Fatalf("t1 generation 2 must load as historical: %+v", historical)
	}
	if _, ok := v.Aggregate("t1", 3); ok {
		t.Fatal("t1 generation 3 must not exist")
	}
	if _, ok := v.Current("nope"); ok {
		t.Fatal("v.Current(nope) must not be found")
	}

	cur2, ok := v.Current("t2")
	if !ok || cur2.Generation != 1 || cur2.Revision != 1 {
		t.Fatalf("t2 current = %+v", cur2)
	}

	hold, ok := v.Hold("hold-1")
	if !ok || hold.ID != "hold-1" || hold.ReleasedAt != 0 {
		t.Fatalf("hold-1 = %+v", hold)
	}
	if _, ok := v.Hold("missing"); ok {
		t.Fatal("missing hold must not be found")
	}

	if len(v.Interpretations) != 1 || v.Interpretations[0].ID != "interp-1" {
		t.Fatalf("interpretations = %+v", v.Interpretations)
	}
	if len(v.Decisions) != 1 || v.Decisions[0].Key != "decision-1" {
		t.Fatalf("decisions = %+v", v.Decisions)
	}
	if len(v.Receipts) != 2 {
		t.Fatalf("receipts = %d, want 2", len(v.Receipts))
	}
	op1, ok := findReceipt(v.Receipts, "op-1")
	if !ok || op1.TaskID != "t1" || op1.Revision != 1 || op1.Phase != taskauthority.PhaseQueued {
		t.Fatalf("op-1 receipt = %+v", op1)
	}
	if op1.Replayed {
		t.Fatalf("replayed is computed metadata and must not load: %+v", op1)
	}
	if len(v.Audit) != 2 {
		t.Fatalf("audit = %d events, want 2", len(v.Audit))
	}
	if v.Audit[0].OperationID != "op-1" || v.Audit[1].OperationID != "op-2" {
		t.Fatalf("audit order = %+v, want op-1 then op-2", v.Audit)
	}

	// Aggregates load deterministically sorted by task id then generation.
	want := []string{"t1/1", "t1/2", "t2/1"}
	got := make([]string, 0, len(v.Aggregates))
	for _, agg := range v.Aggregates {
		got = append(got, agg.TaskID+"/"+agg.Generation.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregate order = %v, want %v", got, want)
	}
}

func TestViewMigrationRequired(t *testing.T) {
	v1Doc := []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"t1","current":true}`)
	locations := []struct {
		rel  string
		want string
	}{
		{"state/.task-authority/aggregates/t1/1.json", "state/.task-authority/aggregates"},
		{"state/.task-authority/worktree-leases/t1.json", "state/.task-authority/worktree-leases"},
		{"state/.dispatch/holds/hold-1.json", "state/.dispatch"},
	}
	for _, tc := range locations {
		t.Run(tc.want, func(t *testing.T) {
			home := t.TempDir()
			writeDocAt(t, home, tc.rel, v1Doc)
			_, err := openStore(t, home).View()
			if !errors.Is(err, ErrMigrationRequired) {
				t.Fatalf("View error = %v, want ErrMigrationRequired", err)
			}
			var me *MigrationRequiredError
			if !errors.As(err, &me) {
				t.Fatalf("View error %v is not a *MigrationRequiredError", err)
			}
			if me.V1Location != tc.want {
				t.Fatalf("MigrationRequiredError.V1Location = %q, want %q", me.V1Location, tc.want)
			}
		})
	}

	t.Run("v1 wins over v2 state", func(t *testing.T) {
		home := t.TempDir()
		buildCanonicalHome(t, home)
		writeDocAt(t, home, "state/.task-authority/aggregates/t1/1.json", v1Doc)
		if _, err := openStore(t, home).View(); !errors.Is(err, ErrMigrationRequired) {
			t.Fatalf("View error = %v, want ErrMigrationRequired with v1 present", err)
		}
	})

	t.Run("v1 wins over pending manifest", func(t *testing.T) {
		home := t.TempDir()
		writeManifestDoc(t, home, fixtureManifest(t))
		writeDocAt(t, home, "state/.dispatch/holds/hold-1.json", v1Doc)
		if _, err := openStore(t, home).View(); !errors.Is(err, ErrMigrationRequired) {
			t.Fatalf("View error = %v, want ErrMigrationRequired with v1 present", err)
		}
	})

	t.Run("v2-only home serves", func(t *testing.T) {
		home := t.TempDir()
		buildCanonicalHome(t, home)
		if _, err := openStore(t, home).View(); err != nil {
			t.Fatalf("v2-only home View error = %v, want nil", err)
		}
	})
}

func TestViewRecoveryRequired(t *testing.T) {
	// Automatic recovery completes interrupted transactions before canonical
	// reads; recovery_test.go proves convergence at every journal stage.
	// Recovery itself still fails closed on corruption and identity mismatch:
	// those are not recoverable states.

	t.Run("corrupt manifest fails closed as corruption", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, relPath(t, func() (string, error) { return TransactionManifestRelPath("op-1") }), []byte("{not json"))
		assertCorrupt(t, home)
	})

	t.Run("identity-mismatched manifest fails closed", func(t *testing.T) {
		home := t.TempDir()
		m := fixtureManifest(t)
		m.OperationID = "op-2"
		audit := *m.Audit
		audit.OperationID = "op-2"
		m.Audit = &audit
		data, err := EncodeTransactionManifest(m)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, relPath(t, func() (string, error) { return TransactionManifestRelPath("op-1") }), data)
		_, err = openStore(t, home).View()
		if !errors.Is(err, ErrCorruptDocument) {
			t.Fatalf("View error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("no manifests serves", func(t *testing.T) {
		home := t.TempDir()
		buildCanonicalHome(t, home)
		if _, err := openStore(t, home).View(); err != nil {
			t.Fatalf("View error = %v, want nil", err)
		}
	})
}

func TestViewFailsClosedOnCorruption(t *testing.T) {
	t.Run("invalid aggregate json", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/1.json", []byte("{not json"))
		assertCorrupt(t, home)
	})

	t.Run("invalid current pointer", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/current", []byte("not-a-number\n"))
		assertCorrupt(t, home)
	})

	t.Run("invalid hold json", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/holds/686f6c642d31.json", []byte("{not json"))
		assertCorrupt(t, home)
	})

	t.Run("invalid receipt json", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/receipts/6f703a31.json", []byte("{not json"))
		assertCorrupt(t, home)
	})

	t.Run("invalid audit json", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/audit/6f703a31.json", []byte("{not json"))
		assertCorrupt(t, home)
	})

	t.Run("legacy v1 document inside v2 namespace", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/1.json", []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"t1","current":true}`))
		_, err := openStore(t, home).View()
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("View error = %v, want ErrUnsupportedVersion", err)
		}
	})

	t.Run("non-json visible file in record dir", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/holds/note.txt", []byte("x"))
		assertCorrupt(t, home)
	})

	t.Run("directory in record dir", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/holds/sub/1.json", []byte("{}"))
		assertCorrupt(t, home)
	})

	t.Run("non-hex record filename", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/holds/zzz.json", []byte("{}"))
		assertCorrupt(t, home)
	})

	t.Run("stray file in aggregates root", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/stray.json", []byte("{}"))
		assertCorrupt(t, home)
	})

	t.Run("unexpected entry in task dir", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/README", []byte("x"))
		assertCorrupt(t, home)
	})
}

func TestViewFailsClosedOnIdentityMismatch(t *testing.T) {
	t.Run("hold document id differs from filename", func(t *testing.T) {
		home := t.TempDir()
		other := fixtureHold()
		other.ID = "hold-2"
		data, err := EncodeHold(other)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, relPath(t, func() (string, error) { return HoldRelPath("hold-1") }), data)
		assertCorrupt(t, home)
	})

	t.Run("interpretation id differs from filename", func(t *testing.T) {
		home := t.TempDir()
		other := fixtureInterpretation()
		other.ID = "interp-2"
		data, err := EncodeInterpretation(other)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, relPath(t, func() (string, error) { return InterpretationRelPath("interp-1") }), data)
		assertCorrupt(t, home)
	})

	t.Run("decision key differs from filename", func(t *testing.T) {
		home := t.TempDir()
		other := fixtureDecision()
		other.Key = "decision-2"
		data, err := EncodeDecision(other)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, relPath(t, func() (string, error) { return DecisionRelPath("decision-1") }), data)
		assertCorrupt(t, home)
	})

	t.Run("receipt operation id differs from filename", func(t *testing.T) {
		home := t.TempDir()
		other := fixtureReceipt()
		other.OperationID = "op-2"
		data, err := EncodeReceipt(other)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, relPath(t, func() (string, error) { return ReceiptRelPath("op-1") }), data)
		assertCorrupt(t, home)
	})

	t.Run("audit operation id differs from filename", func(t *testing.T) {
		home := t.TempDir()
		other := fixtureAuditLifecycle()
		other.OperationID = "op-2"
		data, err := EncodeAudit(other)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, relPath(t, func() (string, error) { return AuditRelPath("op-1") }), data)
		assertCorrupt(t, home)
	})

	t.Run("aggregate in wrong task directory", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		agg, err := taskauthority.NewAggregate("t2", "owner", "work", "ship", "", "")
		if err != nil {
			t.Fatal(err)
		}
		agg.Revision = 1
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/1.json", data)
		assertCorrupt(t, home)
	})

	t.Run("aggregate generation differs from filename", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
		if err != nil {
			t.Fatal(err)
		}
		agg.Generation = 2
		agg.Revision = 1
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/1.json", data)
		assertCorrupt(t, home)
	})
}

func TestViewFailsClosedOnCurrentContradictions(t *testing.T) {
	t.Run("pointed-to document is not current", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		writeAggregateDoc(t, home, "t1", 1, 1, false)
		assertCorrupt(t, home)
	})

	t.Run("task directory without pointer", func(t *testing.T) {
		home := t.TempDir()
		writeAggregateDoc(t, home, "t1", 1, 1, true)
		assertCorrupt(t, home)
	})

	t.Run("empty task directory fails closed", func(t *testing.T) {
		home := t.TempDir()
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/.keep", []byte("x"))
		assertCorrupt(t, home)
	})

	t.Run("pointer with no matching document", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		writeAggregateDoc(t, home, "t1", 2, 1, false)
		assertCorrupt(t, home)
	})

	t.Run("pointer with no documents at all", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		assertCorrupt(t, home)
	})

	t.Run("multiple current documents", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		writeAggregateDoc(t, home, "t1", 1, 1, true)
		writeAggregateDoc(t, home, "t1", 2, 1, true)
		assertCorrupt(t, home)
	})

	t.Run("leading-zero generation alias", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "", "")
		if err != nil {
			t.Fatal(err)
		}
		agg.Revision = 1
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		writeDocAt(t, home, "state/.task-authority/v2/aggregates/t1/01.json", data)
		assertCorrupt(t, home)
	})
}

func TestViewDeterministic(t *testing.T) {
	home := t.TempDir()
	buildCanonicalHome(t, home)
	first := mustView(t, openStore(t, home))
	second := mustView(t, openStore(t, home))
	if !reflect.DeepEqual(first, second) {
		t.Fatal("repeated View calls differ")
	}
}
