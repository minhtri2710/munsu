package home

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The refusal branches of the canonical home mechanics: identity, layout,
// scoped locks, captain provenance, and writer identity.
//
// Each test asserts the accepted state as a control — either before breaking
// it or after repairing it — so a refusal is attributable to the one thing the
// test changed rather than to an earlier guard on the same path.

// A home is identified by a durable identity file. An identity with no ID
// cannot be matched against anything, so opening that home fails closed
// instead of resolving to an anonymous home.
func TestOpenRefusesIdentityWithNoID(t *testing.T) {
	h := newTestHome(t)
	root := h.Root()

	// Control: the untouched home opens.
	if _, err := Open(root); err != nil {
		t.Fatalf("Open on an untouched home: %v", err)
	}

	path := identityPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	var id HomeIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		t.Fatalf("decode identity: %v", err)
	}
	id.ID = "   "
	blanked, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("encode identity: %v", err)
	}
	if err := os.WriteFile(path, append(blanked, '\n'), 0600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	if _, err := Open(root); !errors.Is(err, ErrMalformedIdentity) {
		t.Fatalf("Open with a blank identity ID: got %v, want ErrMalformedIdentity", err)
	}
}

// Open takes an explicit root; an empty one is a caller mistake that must not
// be resolved relative to the working directory.
func TestOpenRefusesEmptyRoot(t *testing.T) {
	h := newTestHome(t)
	if _, err := Open(h.Root()); err != nil {
		t.Fatalf("Open on a real root: %v", err)
	}
	if _, err := Open(""); !errors.Is(err, ErrEmptyRoot) {
		t.Fatalf("Open(\"\") = %v, want ErrEmptyRoot", err)
	}
}

// The canonical layout is four directories. A path in that layout that exists
// but is a file is a malformed home, not a recoverable one: writing through it
// would silently lose every record that belongs under it.
func TestOpenRefusesLayoutDirectoryReplacedByAFile(t *testing.T) {
	h := newTestHome(t)
	root := h.Root()
	if _, err := Open(root); err != nil {
		t.Fatalf("Open on an untouched home: %v", err)
	}

	state := filepath.Join(root, canonicalLayoutRoots.State)
	if err := os.RemoveAll(state); err != nil {
		t.Fatalf("remove state dir: %v", err)
	}
	if err := os.WriteFile(state, []byte("not a directory\n"), 0600); err != nil {
		t.Fatalf("write file over state dir: %v", err)
	}

	if _, err := Open(root); !errors.Is(err, ErrMalformedLayout) {
		t.Fatalf("Open with state/ as a file: got %v, want ErrMalformedLayout", err)
	}
}

// The provenance marker is what distinguishes a captain home from a copy of
// one. Every refusal below is a marker that cannot answer "which captain is
// this, and is this still the home it was seeded as".
func TestValidateCaptainProvenanceRefusesUnusableMarkers(t *testing.T) {
	seeded := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := SeedCaptainProvenance(dir, "captain-1"); err != nil {
			t.Fatalf("SeedCaptainProvenance: %v", err)
		}
		// Control: the seeded marker validates to its captain id, so each
		// refusal below comes from the one line the case rewrites.
		id, err := ValidateCaptainProvenance(dir)
		if err != nil || id != "captain-1" {
			t.Fatalf("ValidateCaptainProvenance = (%q, %v), want (captain-1, nil)", id, err)
		}
		return dir
	}
	rewrite := func(t *testing.T, dir, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, CaptainProvenanceMarkerName), []byte(content), 0600); err != nil {
			t.Fatalf("rewrite marker: %v", err)
		}
	}

	for _, tc := range []struct {
		name    string
		content func(canonical string) string
		wantSub string
	}{
		{"a marker with fewer than three lines", func(string) string { return "munsu-v2\ncaptain-1\n" }, "expected exactly 3 lines"},
		{"a marker with extra content", func(c string) string { return "munsu-v2\ncaptain-1\n" + c + "\nextra\n" }, "has extra content"},
		{"a marker from an unsupported version", func(c string) string { return "munsu-v1\ncaptain-1\n" + c + "\n" }, "unsupported version"},
		{"a marker with a blank id", func(c string) string { return "munsu-v2\n \n" + c + "\n" }, "empty id"},
		{"a marker recording a different canonical home", func(string) string { return "munsu-v2\ncaptain-1\n/somewhere/else\n" }, "may have been copied/moved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := seeded(t)
			canonical, err := CanonicalCaptainHome(dir)
			if err != nil {
				t.Fatalf("CanonicalCaptainHome: %v", err)
			}
			rewrite(t, dir, tc.content(canonical))
			if _, err := ValidateCaptainProvenance(dir); err == nil {
				t.Fatalf("ValidateCaptainProvenance accepted %s", tc.name)
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want the %q refusal", err, tc.wantSub)
			}
		})
	}
}

// The writer identity file names which process may write a given kind of
// record. A record from another schema, or one whose kind does not match the
// slot it is being published into, cannot carry that authority.
func TestPublishWriterIdentityRefusesMismatchedSchemaOrKind(t *testing.T) {
	dir := t.TempDir()
	identity := testWriterIdentity(t, dir)

	// Control: the fixture publishes.
	if err := PublishWriterIdentity(dir, "watcher", identity); err != nil {
		t.Fatalf("PublishWriterIdentity: %v", err)
	}

	unsupported := identity
	unsupported.SchemaVersion = 2
	if err := PublishWriterIdentity(dir, "watcher", unsupported); err == nil {
		t.Fatal("PublishWriterIdentity accepted an unsupported schema")
	} else if !strings.Contains(err.Error(), "unsupported writer identity schema") {
		t.Fatalf("error = %v, want the schema refusal", err)
	}

	otherKind := identity
	otherKind.Kind = "daemon"
	if err := PublishWriterIdentity(dir, "watcher", otherKind); err == nil {
		t.Fatal("PublishWriterIdentity accepted an identity for a different kind")
	} else if !strings.Contains(err.Error(), "kind mismatch") {
		t.Fatalf("error = %v, want the kind refusal", err)
	}
}

// A wake lease file always opens with a header line. A file with no header is
// truncated, so the acks it would authorize cannot be attributed to any lease.
func TestAckWakesRefusesAnEmptyLeaseFile(t *testing.T) {
	dir := t.TempDir()
	path := LeaseFilePath(dir, "lease-1")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("write empty lease: %v", err)
	}

	if err := AckWakes(dir, "lease-1", []string{"epoch:evt"}); err == nil {
		t.Fatal("AckWakes accepted an empty lease file")
	} else if !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("error = %v, want the empty-lease refusal", err)
	}

	// Control: the same call over a lease file that has a header and the event
	// succeeds, so the refusal above was the missing header.
	if err := os.WriteFile(path, []byte("lease-1\tconsumer\t1\t"+
		"9999999999\nepoch\tevt\tcaptain\tsummary\t1\n"), 0600); err != nil {
		t.Fatalf("write lease with header: %v", err)
	}
	if err := AckWakes(dir, "lease-1", []string{"epoch:evt"}); err != nil {
		t.Fatalf("AckWakes over a headed lease file: %v", err)
	}
}

// ResolveWake reads the lease to confirm the event it is resolving really
// belongs to that lease. A truncated lease file cannot answer that, so the
// resolution is refused rather than recorded against an unverifiable lease.
func TestResolveWakeRefusesAnEmptyLeaseFile(t *testing.T) {
	dir := t.TempDir()
	path := LeaseFilePath(dir, "lease-1")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("write empty lease: %v", err)
	}

	if err := ResolveWake(dir, "lease-1", "epoch:evt", "done"); err == nil {
		t.Fatal("ResolveWake accepted an empty lease file")
	} else if !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("error = %v, want the empty-lease refusal", err)
	}

	// Control: with a header and the event present, the same call resolves.
	if err := os.WriteFile(path, []byte("lease-1\tconsumer\t1\t"+
		"9999999999\nepoch\tevt\tcaptain\tsummary\t1\n"), 0600); err != nil {
		t.Fatalf("write lease with header: %v", err)
	}
	if err := ResolveWake(dir, "lease-1", "epoch:evt", "done"); err != nil {
		t.Fatalf("ResolveWake over a headed lease file: %v", err)
	}
}
