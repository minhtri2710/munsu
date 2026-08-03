package taskauthorityfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func testDigest() string { return strings.Repeat("a", 64) }

func fixtureAggregate(t *testing.T) taskauthority.Aggregate {
	t.Helper()
	agg, err := taskauthority.NewAggregate("t1", "owner", "work", "ship", "proj", "parent-1")
	if err != nil {
		t.Fatal(err)
	}
	agg.Endpoint = &taskauthority.EndpointBinding{
		Backend: "pi", Handle: "h-1", LeaseID: "lease-1", FenceToken: "fence-1", BoundAtUnix: 1,
	}
	agg.Worktree = &taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo-1", Path: "/w", GitDir: "/g", CommonDir: "/c",
		Head: "abc123", LeaseID: "lease-1", FenceToken: "fence-1", BoundAtUnix: 1,
	}
	return agg
}

func fixtureHold() taskauthority.DispatchHold {
	return taskauthority.DispatchHold{
		SchemaVersion: SchemaVersion,
		ID:            "hold-1",
		Scope: taskauthority.DispatchHoldScope{
			ProjectIDs:  []string{"p1"},
			TaskIDs:     []string{"t1"},
			Generations: []string{"1"},
			ParentIDs:   []string{"parent-1"},
		},
		Actions:   []taskauthority.DispatchAction{taskauthority.DispatchActionStart, taskauthority.DispatchActionSpawn},
		Reason:    "gate",
		CreatedAt: 1,
	}
}

func fixtureInterpretation() taskauthority.DispatchInterpretation {
	return taskauthority.DispatchInterpretation{
		SchemaVersion:            SchemaVersion,
		ID:                       "interp-1",
		RequestedOrder:           []string{"t1", "t2"},
		ComputedReadiness:        []taskauthority.DispatchReadiness{{TaskID: "t1", Generation: "1", Ready: true}},
		SelectedTasks:            []string{"t1"},
		Evidence:                 []taskauthority.DispatchEvidence{{Source: "backlog", Path: "state/t1.meta", Field: "state", Value: "queued"}},
		DependencySnapshotDigest: testDigest(),
		Outcome:                  taskauthority.DispatchInterpretationAccepted,
		CreatedAt:                1,
	}
}

func fixtureDecision() taskauthority.DispatchDecision {
	return taskauthority.DispatchDecision{
		SchemaVersion:    SchemaVersion,
		Key:              "decision-1",
		InterpretationID: "interp-1",
		Reason:           "because",
		CreatedAt:        1,
		ResolvedAt:       2,
		Answer:           "proceed",
	}
}

func fixtureAuditLifecycle() taskauthority.AuditEvent {
	return taskauthority.AuditEvent{
		OperationID: "op-1",
		Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
		Kind:        taskauthority.AuditLifecycle,
		TaskID:      "t1",
		Generation:  1,
		Reason:      "create",
		After:       taskauthority.PhaseQueued,
		At:          1,
	}
}

func fixtureAuditDispatch() taskauthority.AuditEvent {
	return taskauthority.AuditEvent{
		OperationID: "op-2",
		Actor:       taskauthority.Actor{ID: "general"},
		Kind:        taskauthority.AuditDispatch,
		Reason:      "hold created: hold-1",
		At:          2,
	}
}

func fixtureReceipt() taskauthority.Receipt {
	return taskauthority.Receipt{
		OperationID: "op-1",
		Digest:      testDigest(),
		CommittedAt: 1,
		TaskID:      "t1",
		Generation:  1,
		Revision:    1,
		Phase:       taskauthority.PhaseQueued,
		Reopened:    false,
		Replayed:    true,
	}
}

func fixtureManifest(t *testing.T) TransactionManifest {
	t.Helper()
	payload, err := EncodeAggregate(fixtureAggregate(t))
	if err != nil {
		t.Fatal(err)
	}
	audit := fixtureAuditLifecycle()
	return TransactionManifest{
		OperationID:        "op-1",
		Digest:             testDigest(),
		ExpectedGeneration: 1,
		Before:             []ManifestEntry{{Path: "state/.task-authority/v1/aggregates/t1/1.json", Digest: testDigest()}},
		After: []ManifestEntry{{
			Path:    "state/.task-authority/v1/aggregates/t1/1.json",
			Digest:  DigestHex(payload),
			Payload: string(payload),
		}},
		Audit:     &audit,
		State:     ManifestPending,
		CreatedAt: 1,
	}
}

func TestSchemaDocumentRoundTrips(t *testing.T) {
	t.Run("aggregate", func(t *testing.T) {
		agg := fixtureAggregate(t)
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(data, []byte("\n")) {
			t.Errorf("encoded document must end with a newline")
		}
		got, err := DecodeAggregate(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, agg) {
			t.Fatalf("aggregate round trip = %+v, want %+v", got, agg)
		}
		again, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, data) {
			t.Errorf("aggregate encoding is not deterministic")
		}
	})

	t.Run("hold", func(t *testing.T) {
		hold := fixtureHold()
		data, err := EncodeHold(hold)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeHold(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, hold) {
			t.Fatalf("hold round trip = %+v, want %+v", got, hold)
		}
	})

	t.Run("interpretation", func(t *testing.T) {
		rec := fixtureInterpretation()
		data, err := EncodeInterpretation(rec)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeInterpretation(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, rec) {
			t.Fatalf("interpretation round trip = %+v, want %+v", got, rec)
		}
	})

	t.Run("decision", func(t *testing.T) {
		dec := fixtureDecision()
		data, err := EncodeDecision(dec)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeDecision(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, dec) {
			t.Fatalf("decision round trip = %+v, want %+v", got, dec)
		}
	})

	t.Run("audit lifecycle", func(t *testing.T) {
		ev := fixtureAuditLifecycle()
		data, err := EncodeAudit(ev)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeAudit(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, ev) {
			t.Fatalf("audit round trip = %+v, want %+v", got, ev)
		}
	})

	t.Run("audit dispatch", func(t *testing.T) {
		ev := fixtureAuditDispatch()
		data, err := EncodeAudit(ev)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeAudit(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, ev) {
			t.Fatalf("audit round trip = %+v, want %+v", got, ev)
		}
	})

	t.Run("receipt", func(t *testing.T) {
		receipt := fixtureReceipt()
		data, err := EncodeReceipt(receipt)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeReceipt(data)
		if err != nil {
			t.Fatal(err)
		}
		want := receipt
		want.Replayed = false // computed metadata, never persisted
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("receipt round trip = %+v, want %+v", got, want)
		}
	})

	t.Run("transaction manifest", func(t *testing.T) {
		manifest := fixtureManifest(t)
		data, err := EncodeTransactionManifest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeTransactionManifest(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, manifest) {
			t.Fatalf("manifest round trip = %+v, want %+v", got, manifest)
		}
	})
}

// setSchema re-encodes a valid document with a different schema_version.
func setSchema(t *testing.T, data []byte, version string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["schema_version"] = version
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSchemaUnknownVersionFailsClosed(t *testing.T) {
	base, err := EncodeAggregate(fixtureAggregate(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"munsu.task-authority/v3", "future/v9", "whatever"} {
		_, err := DecodeAggregate(setSchema(t, base, version))
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("schema %q: error = %v, want ErrUnsupportedVersion", version, err)
		}
		var ue *UnsupportedVersionError
		if !errors.As(err, &ue) {
			t.Fatalf("schema %q: error %v is not an *UnsupportedVersionError", version, err)
		}
		if ue.Kind != "unknown" || ue.Found != version {
			t.Fatalf("schema %q: UnsupportedVersionError = %+v", version, ue)
		}
	}

	// Every document family fails closed on an unknown version.
	docs := map[string][]byte{}
	docs["aggregate"], _ = EncodeAggregate(fixtureAggregate(t))
	docs["hold"], _ = EncodeHold(fixtureHold())
	docs["interpretation"], _ = EncodeInterpretation(fixtureInterpretation())
	docs["decision"], _ = EncodeDecision(fixtureDecision())
	docs["audit"], _ = EncodeAudit(fixtureAuditLifecycle())
	docs["receipt"], _ = EncodeReceipt(fixtureReceipt())
	docs["manifest"], _ = EncodeTransactionManifest(fixtureManifest(t))
	for family, data := range docs {
		if _, err := decodeForFamily(t, family, setSchema(t, data, "munsu.task-authority/v9")); !errors.Is(err, ErrUnsupportedVersion) {
			t.Errorf("%s: unknown version error = %v, want ErrUnsupportedVersion", family, err)
		}
	}

	// Encoding must never emit an unknown version.
	bad := fixtureAggregate(t)
	bad.SchemaVersion = "munsu.task-authority/v3"
	if _, err := EncodeAggregate(bad); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("EncodeAggregate(unknown version) error = %v, want ErrUnsupportedVersion", err)
	}

	// Corrupt input fails closed with a typed corrupt-document error.
	for _, data := range [][]byte{nil, {}, []byte("{not json")} {
		_, err := DecodeAggregate(data)
		if !errors.Is(err, ErrCorruptDocument) {
			t.Fatalf("DecodeAggregate(%q) error = %v, want ErrCorruptDocument", data, err)
		}
		var ce *CorruptDocumentError
		if !errors.As(err, &ce) {
			t.Fatalf("DecodeAggregate(%q) error %v is not a *CorruptDocumentError", data, err)
		}
	}

	// A document without a schema_version is corrupt, not readable.
	_, err = DecodeAggregate([]byte(`{"task_id":"t1","generation":1}`))
	if !errors.Is(err, ErrCorruptDocument) {
		t.Fatalf("missing schema_version error = %v, want ErrCorruptDocument", err)
	}
}

// decodeForFamily decodes data as the named document family so unknown-version
// checks can run uniformly over every record kind.
func decodeForFamily(t *testing.T, family string, data []byte) (any, error) {
	t.Helper()
	switch family {
	case "aggregate":
		return DecodeAggregate(data)
	case "hold":
		return DecodeHold(data)
	case "interpretation":
		return DecodeInterpretation(data)
	case "decision":
		return DecodeDecision(data)
	case "audit":
		return DecodeAudit(data)
	case "receipt":
		return DecodeReceipt(data)
	case "manifest":
		return DecodeTransactionManifest(data)
	}
	t.Fatalf("unknown family %q", family)
	return nil, nil
}

func TestUnsupportedVersionV1DetectOnly(t *testing.T) {
	v1Doc := []byte(`{"schema_version":"munsu.task-aggregate/v1","task_id":"t1","generation":"1","current":true,"owner":"o"}`)

	// Every document family recognizes the legacy v1 identity and refuses it
	// with a typed, inspectable detect-only error.
	families := []string{"aggregate", "hold", "interpretation", "decision", "audit", "receipt", "manifest"}
	for _, family := range families {
		_, err := decodeForFamily(t, family, v1Doc)
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Errorf("%s: v1 error = %v, want ErrUnsupportedVersion", family, err)
		}
		var ue *UnsupportedVersionError
		if !errors.As(err, &ue) {
			t.Fatalf("%s: v1 error %v is not an *UnsupportedVersionError", family, err)
		}
		if ue.Kind != "v1" || ue.Found != V1SchemaVersion {
			t.Errorf("%s: UnsupportedVersionError = %+v, want kind=v1 found=%q", family, ue, V1SchemaVersion)
		}
	}

	// Detection is read-only: the v1 document is never mutated or migrated.
	home := t.TempDir()
	v1Path := filepath.Join(home, "state", ".task-authority", "aggregates", "t1", "1.json")
	if err := os.MkdirAll(filepath.Dir(v1Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v1Path, v1Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(v1Path)
	if err != nil {
		t.Fatal(err)
	}
	has, err := HasV1Records(home)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatalf("HasV1Records = false, want true for legacy aggregate at %s", v1Path)
	}
	after, err := os.ReadFile(v1Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("v1 detection mutated the legacy document")
	}

	// Empty home and v1-only home report no v1 records.
	if has, err := HasV1Records(t.TempDir()); err != nil || has {
		t.Fatalf("HasV1Records(empty) = %v, %v, want false, nil", has, err)
	}
	v1Home := t.TempDir()
	v1Dir := filepath.Join(v1Home, "state", ".task-authority", "v1")
	if err := os.MkdirAll(v1Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if has, err := HasV1Records(v1Home); err != nil || has {
		t.Fatalf("HasV1Records(v1-only) = %v, %v, want false, nil", has, err)
	}

	// v1 aggregate paths never collide with the legacy v1 layout.
	rel, err := AggregateRelPath("t1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(rel, "state"+string(filepath.Separator)+".task-authority"+string(filepath.Separator)+"aggregates") {
		t.Fatalf("v1 aggregate path %q collides with the v1 layout", rel)
	}
	if !strings.HasPrefix(rel, "state"+string(filepath.Separator)+".task-authority"+string(filepath.Separator)+"v1") {
		t.Fatalf("v1 aggregate path %q is not under the v1 namespace", rel)
	}

	// Encode never emits a v1 identity.
	if data, err := EncodeAggregate(fixtureAggregate(t)); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(data), V1SchemaVersion) {
		t.Fatalf("v2 encoding contains legacy v1 schema identity")
	}
}

func TestSchemaCorruptRequiredFieldsFailClosed(t *testing.T) {
	// Encode-side: every document family rejects records missing required
	// identity fields before serialization.
	t.Run("aggregate required fields", func(t *testing.T) {
		agg := fixtureAggregate(t)
		agg.TaskID = ""
		if _, err := EncodeAggregate(agg); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("empty task id error = %v, want ErrCorruptDocument", err)
		}
		agg = fixtureAggregate(t)
		agg.Generation = 0
		if _, err := EncodeAggregate(agg); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("zero generation error = %v, want ErrCorruptDocument", err)
		}
		agg = fixtureAggregate(t)
		agg.Revision = 0
		if _, err := EncodeAggregate(agg); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("zero revision error = %v, want ErrCorruptDocument", err)
		}
		agg = fixtureAggregate(t)
		agg.Phase = taskauthority.Phase("in-flight")
		if _, err := EncodeAggregate(agg); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("invalid phase error = %v, want ErrCorruptDocument", err)
		}
		agg = fixtureAggregate(t)
		agg.Definition.Owner = "  "
		if _, err := EncodeAggregate(agg); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("blank owner error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("dispatch record required fields", func(t *testing.T) {
		hold := fixtureHold()
		hold.ID = ""
		if _, err := EncodeHold(hold); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("empty hold id error = %v, want ErrCorruptDocument", err)
		}
		hold = fixtureHold()
		hold.CreatedAt = 0
		if _, err := EncodeHold(hold); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("hold missing created timestamp error = %v, want ErrCorruptDocument", err)
		}

		rec := fixtureInterpretation()
		rec.ID = ""
		if _, err := EncodeInterpretation(rec); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("empty interpretation id error = %v, want ErrCorruptDocument", err)
		}
		rec = fixtureInterpretation()
		rec.CreatedAt = 0
		if _, err := EncodeInterpretation(rec); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("interpretation missing created timestamp error = %v, want ErrCorruptDocument", err)
		}

		dec := fixtureDecision()
		dec.Key = ""
		if _, err := EncodeDecision(dec); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("empty decision key error = %v, want ErrCorruptDocument", err)
		}
		dec = fixtureDecision()
		dec.InterpretationID = ""
		if _, err := EncodeDecision(dec); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("decision missing interpretation id error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("audit required fields", func(t *testing.T) {
		ev := fixtureAuditLifecycle()
		ev.Kind = "bogus"
		if _, err := EncodeAudit(ev); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("unknown audit kind error = %v, want ErrCorruptDocument", err)
		}
		ev = fixtureAuditLifecycle()
		ev.OperationID = ""
		if _, err := EncodeAudit(ev); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("audit missing operation id error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("receipt required fields", func(t *testing.T) {
		receipt := fixtureReceipt()
		receipt.OperationID = ""
		if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("receipt missing operation id error = %v, want ErrCorruptDocument", err)
		}
		receipt = fixtureReceipt()
		receipt.Digest = "short"
		if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("receipt short digest error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("transaction manifest required fields", func(t *testing.T) {
		m := fixtureManifest(t)
		m.OperationID = ""
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("manifest missing operation id error = %v, want ErrCorruptDocument", err)
		}
		m = fixtureManifest(t)
		m.Digest = "short"
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("manifest short digest error = %v, want ErrCorruptDocument", err)
		}
		m = fixtureManifest(t)
		m.State = ManifestState("bogus")
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("manifest unknown commit state error = %v, want ErrCorruptDocument", err)
		}
		m = fixtureManifest(t)
		m.After = nil
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("manifest without after entries error = %v, want ErrCorruptDocument", err)
		}
		m = fixtureManifest(t)
		entry := m.After[0]
		entry.Digest = testDigest() // does not match the payload
		m.After = []ManifestEntry{entry}
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("manifest digest mismatch error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("current pointer required fields", func(t *testing.T) {
		for _, data := range [][]byte{nil, []byte(""), []byte("0\n"), []byte("1.5\n"), []byte("a/b\n"), []byte("..\n")} {
			if _, err := DecodeCurrentPointer(data); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("DecodeCurrentPointer(%q) error = %v, want ErrCorruptDocument", data, err)
			}
		}
		if _, err := EncodeCurrentPointer(0); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("EncodeCurrentPointer(0) error = %v, want ErrInvalidPath", err)
		}
	})
}

func TestPathRelPaths(t *testing.T) {
	join := func(parts ...string) string { return filepath.Join(parts...) }

	t.Run("aggregate and current pointer", func(t *testing.T) {
		rel, err := AggregateRelPath("t1", 1)
		if err != nil {
			t.Fatal(err)
		}
		if want := join("state", ".task-authority", "v1", "aggregates", "t1", "1.json"); rel != want {
			t.Errorf("AggregateRelPath = %q, want %q", rel, want)
		}
		rel, err = AggregateRelPath("task-42", 7)
		if err != nil {
			t.Fatal(err)
		}
		if want := join("state", ".task-authority", "v1", "aggregates", "task-42", "7.json"); rel != want {
			t.Errorf("AggregateRelPath = %q, want %q", rel, want)
		}
		cur, err := CurrentPointerRelPath("t1")
		if err != nil {
			t.Fatal(err)
		}
		if want := join("state", ".task-authority", "v1", "aggregates", "t1", "current"); cur != want {
			t.Errorf("CurrentPointerRelPath = %q, want %q", cur, want)
		}
	})

	t.Run("dispatch records", func(t *testing.T) {
		cases := []struct {
			name string
			got  string
			want string
		}{
			{"hold", relPath(t, func() (string, error) { return HoldRelPath("hold-1") }), join("state", ".task-authority", "v1", "holds", "686f6c642d31.json")},
			{"interpretation", relPath(t, func() (string, error) { return InterpretationRelPath("interp-1") }), join("state", ".task-authority", "v1", "interpretations", "696e746572702d31.json")},
			{"decision", relPath(t, func() (string, error) { return DecisionRelPath("decision-1") }), join("state", ".task-authority", "v1", "decisions", "6465636973696f6e2d31.json")},
		}
		for _, tc := range cases {
			if tc.got != tc.want {
				t.Errorf("%s rel path = %q, want %q", tc.name, tc.got, tc.want)
			}
		}
	})

	t.Run("audit receipts and manifests keyed by operation", func(t *testing.T) {
		cases := []struct {
			name string
			got  string
			want string
		}{
			{"audit", relPath(t, func() (string, error) { return AuditRelPath("op:1") }), join("state", ".task-authority", "v1", "audit", "6f703a31.json")},
			{"receipt", relPath(t, func() (string, error) { return ReceiptRelPath("op:1") }), join("state", ".task-authority", "v1", "receipts", "6f703a31.json")},
			{"manifest", relPath(t, func() (string, error) { return TransactionManifestRelPath("op:1") }), join("state", ".task-authority", "v1", "transactions", "6f703a31.json")},
		}
		for _, tc := range cases {
			if tc.got != tc.want {
				t.Errorf("%s rel path = %q, want %q", tc.name, tc.got, tc.want)
			}
		}
	})

	t.Run("current pointer encoding", func(t *testing.T) {
		data, err := EncodeCurrentPointer(7)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "7\n" {
			t.Errorf("EncodeCurrentPointer(7) = %q, want \"7\\n\"", data)
		}
		got, err := DecodeCurrentPointer(data)
		if err != nil {
			t.Fatal(err)
		}
		if got != 7 {
			t.Errorf("DecodeCurrentPointer = %d, want 7", got)
		}
	})
}

func relPath(t *testing.T, fn func() (string, error)) string {
	t.Helper()
	rel, err := fn()
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestPathValidation(t *testing.T) {
	t.Run("task id", func(t *testing.T) {
		for _, id := range []string{"t1", "task-42.a", "t_1"} {
			if _, err := AggregateRelPath(id, 1); err != nil {
				t.Errorf("AggregateRelPath(%q, 1) error = %v, want nil", id, err)
			}
		}
		for _, id := range []string{"", ".", "..", "a/b", "a\\b"} {
			if _, err := AggregateRelPath(id, 1); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("AggregateRelPath(%q, 1) error = %v, want ErrInvalidPath", id, err)
			}
		}
	})

	t.Run("generation", func(t *testing.T) {
		if _, err := AggregateRelPath("t1", 0); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("AggregateRelPath(t1, 0) error = %v, want ErrInvalidPath", err)
		}
	})

	t.Run("free-form record ids", func(t *testing.T) {
		for _, id := range []string{"op-1", "hold:1", "decision 1"} {
			if _, err := safeFileID(id); err != nil {
				t.Errorf("safeFileID(%q) error = %v, want nil", id, err)
			}
		}
		for _, id := range []string{"", ".", "..", "a/b", "a\\b", ".hidden", "a\x00b"} {
			if _, err := safeFileID(id); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("safeFileID(%q) error = %v, want ErrInvalidPath", id, err)
			}
		}
		if got, err := safeFileID("op:1"); err != nil || got != "6f703a31" {
			t.Errorf("safeFileID(op:1) = %q, %v, want 6f703a31, nil", got, err)
		}
		if got, err := safeFileID("op 1"); err != nil || got != "6f702031" {
			t.Errorf("safeFileID(op 1) = %q, %v, want 6f702031, nil", got, err)
		}
	})
}

func TestPathPermissions(t *testing.T) {
	if DirPerm != 0o700 {
		t.Errorf("DirPerm = %o, want 0700", DirPerm)
	}
	if FilePerm != 0o600 {
		t.Errorf("FilePerm = %o, want 0600", FilePerm)
	}

	dir := filepath.Join(t.TempDir(), "nested", "authority")
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("EnsureDir mode = %o, want 0700", got)
	}

	// EnsureDir re-secures an existing directory, matching home.EnsureDirTree.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("EnsureDir after chmod 0755 = %o, want 0700", got)
	}
}

func TestSchemaTaskAuthorityHasNoFilesystemImports(t *testing.T) {
	forbidden := []string{
		`"os"`,
		`"io"`,
		`"path/filepath"`,
		`"syscall"`,
		`"os/exec"`,
		`"os/user"`,
		`"ioutil"`,
		"github.com/minhtri2710/munsu/internal/taskauthorityfs",
	}
	entries, err := os.ReadDir("../taskauthority")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../taskauthority", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range importPaths(data) {
			for _, bad := range forbidden {
				if path == bad {
					t.Errorf("%s imports %q: taskauthority must not import filesystem packages", name, bad)
				}
			}
		}
	}
}

// importPaths extracts the import paths of one Go source file.
func importPaths(src []byte) []string {
	var out []string
	lines := strings.Split(string(src), "\n")
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import (") {
			inBlock = true
			continue
		}
		if inBlock {
			if trimmed == ")" {
				inBlock = false
				continue
			}
			if strings.HasPrefix(trimmed, `"`) {
				out = append(out, strings.Trim(trimmed, `"`))
			}
			continue
		}
		if strings.HasPrefix(trimmed, `import "`) {
			out = append(out, strings.Trim(strings.TrimPrefix(trimmed, "import "), `"`))
		}
	}
	return out
}
