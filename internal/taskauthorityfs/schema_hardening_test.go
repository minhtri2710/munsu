package taskauthorityfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestSafeFileIDCollisionFreeAndRoundTrip is the regression test for the
// non-injective safeFileID normalization: "op:1" and "op 1" both normalized
// to "op_1", so two distinct record identities collided on disk. The encoding
// must be deterministic, collision-free over distinct raw IDs, and reversible.
func TestSafeFileIDCollisionFreeAndRoundTrip(t *testing.T) {
	// Pairs that previously collided under the "_" replacer.
	ids := []string{
		"op:1", "op 1", "op%3A1", "op_1", "op-1", "op.1",
		"decision:42", "decision 42", "decision-42",
		"hold a", "hold:a", "hold\x00a", // NUL is rejected by validation, but keep the set broad
		"100%", "a:b c", "x/y", "a\\b",
	}
	var valid []string
	for _, id := range ids {
		if strings.ContainsAny(id, "/\\\x00") {
			if _, err := safeFileID(id); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("safeFileID(%q) error = %v, want ErrInvalidPath", id, err)
			}
			continue
		}
		valid = append(valid, id)
	}
	seen := make(map[string]string, len(valid))
	for _, id := range valid {
		name, err := safeFileID(id)
		if err != nil {
			t.Fatalf("safeFileID(%q) error = %v, want nil", id, err)
		}
		if prev, ok := seen[name]; ok {
			t.Fatalf("safeFileID(%q) and safeFileID(%q) both encode to %q", prev, id, name)
		}
		seen[name] = id
		if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || name == "." || name == ".." {
			t.Errorf("safeFileID(%q) = %q is not a filesystem-safe filename", id, name)
		}
		// Round trip: the raw identity must be recoverable from the filename.
		got, err := fileIDDecode(name)
		if err != nil {
			t.Fatalf("fileIDDecode(%q) error = %v, want nil", name, err)
		}
		if got != id {
			t.Errorf("round trip safeFileID(%q) = %q, fileIDDecode = %q, want %q", id, name, got, id)
		}
		// Determinism: encoding the same id twice yields the same filename.
		again, err := safeFileID(id)
		if err != nil {
			t.Fatal(err)
		}
		if again != name {
			t.Errorf("safeFileID(%q) not deterministic: %q then %q", id, name, again)
		}
	}

	// Malformed filenames must not decode.
	for _, bad := range []string{"%", "abc", "6f703a3", "6f703a31zz", "6f;03a31", "6F703A31"} {
		if _, err := fileIDDecode(bad); err == nil {
			t.Errorf("fileIDDecode(%q) = nil error, want malformed-escape failure", bad)
		}
	}
}

// TestTransactionManifestPathValidation is the regression test for manifest
// Before/After path validation: every entry path must be relative, clean,
// traversal-free, and constrained under the v2 authority namespace, with no
// duplicate target paths.
func TestTransactionManifestPathValidation(t *testing.T) {
	base := fixtureManifest(t)
	validPaths := []string{
		"state/.task-authority/v1/aggregates/t1/1.json",
		"state/.task-authority/v1/aggregates/t1/current",
		"state/.task-authority/v1/receipts/op-1.json",
	}
	invalidPaths := []string{
		"",                                     // empty
		"/state/.task-authority/v1/a.json",     // absolute (native)
		`\state\.task-authority\v2\a.json`,     // absolute (windows)
		"state/.task-authority/v1/../escape",   // parent traversal
		"../escape",                            // leading traversal
		"state/other/place",                    // outside the v2 namespace
		"state/.task-authority/v1",             // the root itself, not a file under it
		"state/.task-authority/v1evil/a.json",  // prefix lookalike of the root
		"state/.task-authority/v1/./a.json",    // not clean
		"state/.task-authority/v1/a//b.json",   // not clean
		"state/.task-authority/v1/a/../b.json", // not clean
	}

	t.Run("valid paths accepted", func(t *testing.T) {
		for _, p := range validPaths {
			m := base
			entry := m.After[0]
			entry.Path = p
			m.After = []ManifestEntry{entry}
			m.Before = []ManifestEntry{{Path: p, Digest: testDigest()}}
			if _, err := EncodeTransactionManifest(m); err != nil {
				t.Errorf("EncodeTransactionManifest(path %q) error = %v, want nil", p, err)
			}
		}
	})

	t.Run("invalid paths rejected", func(t *testing.T) {
		for _, p := range invalidPaths {
			m := base
			entry := m.After[0]
			entry.Path = p
			m.After = []ManifestEntry{entry}
			if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeTransactionManifest(path %q) error = %v, want ErrCorruptDocument", p, err)
			}
		}
	})

	t.Run("before entries validated too", func(t *testing.T) {
		for _, p := range invalidPaths {
			m := base
			entry := m.Before[0]
			entry.Path = p
			m.Before = []ManifestEntry{entry}
			if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeTransactionManifest(before path %q) error = %v, want ErrCorruptDocument", p, err)
			}
		}
	})

	t.Run("duplicate after paths rejected", func(t *testing.T) {
		m := base
		m.After = []ManifestEntry{m.After[0], m.After[0]}
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("duplicate after paths error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("duplicate before paths rejected", func(t *testing.T) {
		m := base
		m.Before = []ManifestEntry{m.Before[0], m.Before[0]}
		if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("duplicate before paths error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("same path in before and after is an update, not a duplicate", func(t *testing.T) {
		m := base
		entry := m.After[0]
		m.Before = []ManifestEntry{{Path: entry.Path, Digest: testDigest()}}
		m.After = []ManifestEntry{entry}
		if _, err := EncodeTransactionManifest(m); err != nil {
			t.Errorf("update-shaped manifest error = %v, want nil", err)
		}
	})
}

// TestDigestRequiresHexCharacters is the regression test for length-only
// digest validation: a 64-character non-hex digest was accepted by every
// digest field. Digests must be exactly 64 hexadecimal characters.
func TestDigestRequiresHexCharacters(t *testing.T) {
	malformed := []string{
		"",
		"short",
		strings.Repeat("g", 64),       // valid length, invalid hex
		strings.Repeat("0", 63) + "z", // valid length, invalid last hex
		"g" + strings.Repeat("0", 63), // valid length, invalid first hex
		strings.Repeat("0", 65),       // too long
	}

	t.Run("receipt digest", func(t *testing.T) {
		for _, digest := range malformed {
			receipt := fixtureReceipt()
			receipt.Digest = digest
			if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeReceipt(digest %q) error = %v, want ErrCorruptDocument", digest, err)
			}
		}
	})

	t.Run("manifest request digest", func(t *testing.T) {
		for _, digest := range malformed {
			m := fixtureManifest(t)
			m.Digest = digest
			if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeTransactionManifest(digest %q) error = %v, want ErrCorruptDocument", digest, err)
			}
		}
	})

	t.Run("after entry digest", func(t *testing.T) {
		for _, digest := range malformed {
			m := fixtureManifest(t)
			entry := m.After[0]
			entry.Digest = digest
			m.After = []ManifestEntry{entry}
			if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeTransactionManifest(after digest %q) error = %v, want ErrCorruptDocument", digest, err)
			}
		}
	})

	t.Run("before entry digest", func(t *testing.T) {
		for _, digest := range malformed {
			m := fixtureManifest(t)
			entry := m.Before[0]
			entry.Digest = digest
			m.Before = []ManifestEntry{entry}
			if _, err := EncodeTransactionManifest(m); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeTransactionManifest(before digest %q) error = %v, want ErrCorruptDocument", digest, err)
			}
		}
	})

	t.Run("uppercase hex remains valid", func(t *testing.T) {
		uppercase := strings.Repeat("A", 64)
		receipt := fixtureReceipt()
		receipt.Digest = uppercase
		if _, err := EncodeReceipt(receipt); err != nil {
			t.Errorf("EncodeReceipt(uppercase hex) error = %v, want nil", err)
		}
		m := fixtureManifest(t)
		m.Digest = uppercase
		entry := m.After[0]
		entry.Digest = DigestHex([]byte(entry.Payload)) // keep payload consistent
		m.After = []ManifestEntry{entry}
		m.Before[0].Digest = uppercase
		if _, err := EncodeTransactionManifest(m); err != nil {
			t.Errorf("EncodeTransactionManifest(uppercase hex) error = %v, want nil", err)
		}
	})
}

// TestHasV1RecordsExpandedDetection is the regression test for read-only v1
// detection: legacy records live in the aggregates dir, the worktree-leases
// dir, and the legacy .dispatch control dir, and detection must never mutate
// any of them.
func TestHasV1RecordsExpandedDetection(t *testing.T) {
	legacyDirs := []string{
		"state/.task-authority/aggregates",
		"state/.task-authority/worktree-leases",
		"state/.dispatch",
	}
	recordBytes := []byte(`{"schema_version":"munsu.task-aggregate/v1","current":true}`)
	for _, rel := range legacyDirs {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			home := t.TempDir()
			record := filepath.Join(home, rel, "t1", "1.json")
			if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(record, recordBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			has, err := HasV1Records(home)
			if err != nil {
				t.Fatal(err)
			}
			if !has {
				t.Fatalf("HasV1Records = false, want true for legacy record at %s", record)
			}
			after, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("HasV1Records mutated the legacy record at %s", record)
			}
			// Detection must not create any v2 state.
			if _, err := os.Stat(filepath.Join(home, "state", ".task-authority", "v1")); !os.IsNotExist(err) {
				t.Fatalf("HasV1Records created v2 state: %v", err)
			}
		})
	}

	t.Run("empty home reports no v1 records", func(t *testing.T) {
		if has, err := HasV1Records(t.TempDir()); err != nil || has {
			t.Fatalf("HasV1Records(empty) = %v, %v, want false, nil", has, err)
		}
	})

	t.Run("v2-only home reports no v1 records", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "state", ".task-authority", "v1", "aggregates"), 0o700); err != nil {
			t.Fatal(err)
		}
		if has, err := HasV1Records(home); err != nil || has {
			t.Fatalf("HasV1Records(v2-only) = %v, %v, want false, nil", has, err)
		}
	})

	t.Run("hidden entries are ignored", func(t *testing.T) {
		home := t.TempDir()
		dir := filepath.Join(home, "state", ".dispatch")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".tmp"), recordBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if has, err := HasV1Records(home); err != nil || has {
			t.Fatalf("HasV1Records(hidden-only) = %v, %v, want false, nil", has, err)
		}
	})
}

// TestReceiptValidationHardening is the regression test for receipt
// validation: receipts require a committed timestamp, and lifecycle outcome
// fields (task id, generation, revision, phase) must be all-or-none and
// coherent, while hold-only receipts without lifecycle outcomes stay valid.
func TestReceiptValidationHardening(t *testing.T) {
	t.Run("missing committed timestamp rejected", func(t *testing.T) {
		receipt := fixtureReceipt()
		receipt.CommittedAt = 0
		if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("EncodeReceipt(CommittedAt=0) error = %v, want ErrCorruptDocument", err)
		}
		receipt = fixtureReceipt()
		receipt.CommittedAt = -1
		if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("EncodeReceipt(CommittedAt=-1) error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("partial lifecycle outcome rejected", func(t *testing.T) {
		partials := []struct {
			name  string
			apply func(*taskauthority.Receipt)
		}{
			{"task id only", func(r *taskauthority.Receipt) {
				r.Generation, r.Revision, r.Phase = 0, 0, ""
			}},
			{"generation only", func(r *taskauthority.Receipt) {
				r.TaskID, r.Revision, r.Phase = "", 0, ""
			}},
			{"revision only", func(r *taskauthority.Receipt) {
				r.TaskID, r.Generation, r.Phase = "", 0, ""
			}},
			{"phase only", func(r *taskauthority.Receipt) {
				r.TaskID, r.Generation, r.Revision = "", 0, 0
			}},
			{"missing revision", func(r *taskauthority.Receipt) {
				r.Revision = 0
			}},
			{"missing generation", func(r *taskauthority.Receipt) {
				r.Generation = 0
			}},
			{"missing task id", func(r *taskauthority.Receipt) {
				r.TaskID = ""
			}},
			{"reopened without lifecycle", func(r *taskauthority.Receipt) {
				r.TaskID, r.Generation, r.Revision, r.Phase = "", 0, 0, ""
				r.Reopened = true
			}},
		}
		for _, tc := range partials {
			receipt := fixtureReceipt()
			tc.apply(&receipt)
			if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
				t.Errorf("EncodeReceipt(%s) error = %v, want ErrCorruptDocument", tc.name, err)
			}
		}
	})

	t.Run("invalid lifecycle values rejected", func(t *testing.T) {
		receipt := fixtureReceipt()
		receipt.Phase = taskauthority.Phase("in-flight")
		if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("EncodeReceipt(invalid phase) error = %v, want ErrCorruptDocument", err)
		}
		receipt = fixtureReceipt()
		receipt.TaskID = "../escape"
		if _, err := EncodeReceipt(receipt); !errors.Is(err, ErrCorruptDocument) {
			t.Errorf("EncodeReceipt(unsafe task id) error = %v, want ErrCorruptDocument", err)
		}
	})

	t.Run("valid hold-only receipt preserved", func(t *testing.T) {
		receipt := taskauthority.Receipt{
			OperationID: "op-1",
			Digest:      testDigest(),
			CommittedAt: 1,
		}
		data, err := EncodeReceipt(receipt)
		if err != nil {
			t.Fatalf("EncodeReceipt(hold-only) error = %v, want nil", err)
		}
		got, err := DecodeReceipt(data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal([]byte(got.OperationID), []byte("op-1")) || got.Digest != testDigest() || got.CommittedAt != 1 {
			t.Errorf("hold-only receipt round trip = %+v, want identity preserved", got)
		}
	})

	t.Run("full lifecycle outcome valid", func(t *testing.T) {
		receipt := fixtureReceipt()
		if _, err := EncodeReceipt(receipt); err != nil {
			t.Errorf("EncodeReceipt(full lifecycle) error = %v, want nil", err)
		}
	})
}

// TestFileIDEncodeIsStableAcrossDocuments proves the operation-keyed document
// paths use the collision-free encoding end to end.
func TestFileIDEncodeIsStableAcrossDocuments(t *testing.T) {
	for _, id := range []string{"op:1", "op 1", "decision a:b"} {
		audit, err := AuditRelPath(id)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := ReceiptRelPath(id)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := TransactionManifestRelPath(id)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(audit)
		if filepath.Base(receipt) != name || filepath.Base(manifest) != name {
			t.Errorf("id %q: audit/receipt/manifest filenames diverge: %q %q %q", id, filepath.Base(audit), filepath.Base(receipt), filepath.Base(manifest))
		}
		got, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			t.Fatal(err)
		}
		if got != id {
			t.Errorf("id %q encoded to %q, decoded to %q", id, name, got)
		}
	}
}

// TestManifestEncodeDecodeRejectsUnsafeJSON ensures JSON documents cannot
// smuggle traversal paths past the JSON decoder into the path validation.
func TestManifestEncodeDecodeRejectsUnsafeJSON(t *testing.T) {
	data, err := EncodeTransactionManifest(fixtureManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	after, ok := doc["record"].(map[string]any)["after"].([]any)
	if !ok || len(after) == 0 {
		t.Fatal("cannot find after entries in encoded manifest")
	}
	entry := after[0].(map[string]any)
	entry["path"] = "../../escape.json"
	doc["record"].(map[string]any)["after"] = []any{entry}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTransactionManifest(out); !errors.Is(err, ErrCorruptDocument) {
		t.Errorf("DecodeTransactionManifest(traversal via JSON) error = %v, want ErrCorruptDocument", err)
	}
}
