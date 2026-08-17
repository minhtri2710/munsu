package home

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The refusal branches of the canonical home mechanics: identity, layout,
// scoped locks and leases, captain provenance, and writer identity.
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

	state := filepath.Join(root, CanonicalLayout.State)
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

// A lease is owned by somebody, for a bounded time. An empty owner or a
// non-positive ttl would produce a lease nothing could be attributed to or
// that expires at acquisition.
func TestAcquireLeaseRefusesUnusableOwnerOrTTL(t *testing.T) {
	h := newTestHome(t)

	// Control: the same call with an owner and a positive ttl succeeds.
	lease, err := h.AcquireLease("delivery", time.Minute, "captain-1")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if _, err := h.AcquireLease("delivery", time.Minute, ""); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("AcquireLease with no owner = %v, want ErrInvalidScope", err)
	}
	if _, err := h.AcquireLease("delivery", 0, "captain-1"); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("AcquireLease with a zero ttl = %v, want ErrInvalidScope", err)
	}
}

// fenceLeaseRecord rewrites the durable lease record so it no longer matches
// the fencing token the in-memory Lease holds — the state a reclaim by a newer
// owner leaves behind.
func fenceLeaseRecord(t *testing.T, l *Lease) {
	t.Helper()
	rec, err := readLeaseRecord(l.path)
	if err != nil {
		t.Fatalf("read lease record: %v", err)
	}
	rec.FenceToken = uint64(l.token) + 1
	if err := writeLeaseRecord(l.path, rec); err != nil {
		t.Fatalf("write lease record: %v", err)
	}
}

// Renew is the point where a holder discovers it no longer owns the lease. A
// stale fencing token, an expiry already in the past, a released lease, or a
// non-positive ttl each mean the caller must not keep acting as owner.
func TestRenewRefusesLeasesTheHolderNoLongerOwns(t *testing.T) {
	acquire := func(t *testing.T, scope string) (*Home, *Lease) {
		t.Helper()
		h := newTestHome(t)
		l, err := h.AcquireLease(scope, time.Minute, "captain-1")
		if err != nil {
			t.Fatalf("AcquireLease: %v", err)
		}
		// Control: the freshly acquired lease renews, so each refusal below is
		// attributable to the single change that test makes.
		if err := l.Renew(time.Minute); err != nil {
			t.Fatalf("Renew on a fresh lease: %v", err)
		}
		return h, l
	}

	t.Run("a non-positive ttl", func(t *testing.T) {
		_, l := acquire(t, "renew-ttl")
		if err := l.Renew(0); !errors.Is(err, ErrInvalidScope) {
			t.Fatalf("Renew(0) = %v, want ErrInvalidScope", err)
		}
	})

	t.Run("a lease this holder already released", func(t *testing.T) {
		h, l := acquire(t, "renew-released")
		if err := l.Release(); err != nil {
			t.Fatalf("Release: %v", err)
		}
		// A successor takes the scope before the released lease renews. Without
		// that, the released lease and a durably-absent record are
		// indistinguishable — both surface ErrLeaseExpired, and the assertion
		// would hold even with this guard removed. With a successor's record on
		// disk, only the released check produces ErrLeaseExpired; falling
		// through would reach the fencing check and produce ErrFenced.
		successor, err := h.AcquireLease("renew-released", time.Minute, "captain-2")
		if err != nil {
			t.Fatalf("successor AcquireLease: %v", err)
		}
		defer successor.Release()

		if err := l.Renew(time.Minute); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("Renew after Release = %v, want ErrLeaseExpired", err)
		}
	})

	t.Run("a lease fenced by a newer owner", func(t *testing.T) {
		_, l := acquire(t, "renew-fenced")
		fenceLeaseRecord(t, l)
		if err := l.Renew(time.Minute); !errors.Is(err, ErrFenced) {
			t.Fatalf("Renew on a fenced lease = %v, want ErrFenced", err)
		}
	})

	t.Run("a lease whose durable expiry has passed", func(t *testing.T) {
		_, l := acquire(t, "renew-expired")
		rec, err := readLeaseRecord(l.path)
		if err != nil {
			t.Fatalf("read lease record: %v", err)
		}
		rec.ExpiresAtUnix = time.Now().Add(-time.Minute).Unix()
		if err := writeLeaseRecord(l.path, rec); err != nil {
			t.Fatalf("write lease record: %v", err)
		}
		if err := l.Renew(time.Minute); !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("Renew on an expired lease = %v, want ErrLeaseExpired", err)
		}
	})
}

// Releasing is also a fenced operation: a holder that has been reclaimed must
// not delete the lease record its successor now owns.
func TestReleaseRefusesToDeleteALeaseFencedByANewerOwner(t *testing.T) {
	h := newTestHome(t)
	l, err := h.AcquireLease("release-fenced", time.Minute, "captain-1")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	fenceLeaseRecord(t, l)

	if err := l.Release(); !errors.Is(err, ErrFenced) {
		t.Fatalf("Release on a fenced lease = %v, want ErrFenced", err)
	}
	// The successor's record survives: the refusal protected it.
	if _, err := readLeaseRecord(l.path); err != nil {
		t.Fatalf("lease record after a refused Release: %v", err)
	}

	// Control: a lease whose token still matches releases and removes the
	// record.
	own, err := h.AcquireLease("release-owned", time.Minute, "captain-1")
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if err := own.Release(); err != nil {
		t.Fatalf("Release on an owned lease: %v", err)
	}
	if _, err := readLeaseRecord(own.path); !os.IsNotExist(err) {
		t.Fatalf("lease record after Release: %v, want it removed", err)
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

// Only one watcher may hold a home. A lease held by a process that is still
// alive is not reclaimable — reclaiming it would run two watchers over the
// same home. A lease left by a dead process is.
func TestClaimWatcherLeaseRefusesALeaseHeldByALiveProcess(t *testing.T) {
	dir := t.TempDir()

	// A real child process this test owns. PID 1 would not do: isProcessAlive
	// shells out to `kill -0`, which an unprivileged user cannot signal, so
	// init reads as dead.
	live := exec.Command("sleep", "60")
	if err := live.Start(); err != nil {
		t.Fatalf("start live process: %v", err)
	}
	t.Cleanup(func() {
		_ = live.Process.Kill()
		_ = live.Wait()
	})

	if _, err := writeLeaseFile(WatcherLeasePath(dir), &WatcherLease{
		Home:      Canonical(dir),
		PID:       live.Process.Pid,
		StartedAt: time.Now().Unix(),
		UpdatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("write existing lease: %v", err)
	}

	claimed, err := ClaimWatcherLease(dir, os.Getpid())
	if err == nil {
		t.Fatal("ClaimWatcherLease took a lease held by a live process")
	}
	if claimed {
		t.Fatal("ClaimWatcherLease reported a claim it refused")
	}
	if !strings.Contains(err.Error(), "watcher lease held by pid") {
		t.Fatalf("error = %v, want the live-holder refusal", err)
	}

	// Control: the same call against a lease left by a PID that is not running
	// reclaims it, so the refusal above came from liveness and not from the
	// mere presence of a lease file.
	dead := findDeadPIDForGuards(t)
	if _, err := writeLeaseFile(WatcherLeasePath(dir), &WatcherLease{
		Home:      Canonical(dir),
		PID:       dead,
		StartedAt: time.Now().Unix(),
		UpdatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("write stale lease: %v", err)
	}
	claimed, err = ClaimWatcherLease(dir, os.Getpid())
	if err != nil || !claimed {
		t.Fatalf("ClaimWatcherLease over a dead holder = (%v, %v), want (true, nil)", claimed, err)
	}
}

// findDeadPIDForGuards returns a PID that isProcessAlive reports as not
// running. isProcessAlive shells out per call, so this probes a handful of
// candidates rather than scanning a range.
func findDeadPIDForGuards(t *testing.T) int {
	t.Helper()
	for _, pid := range []int{1 << 22, 1<<22 - 1, 1<<22 - 2, 1 << 21, 1<<21 - 1} {
		if !isProcessAlive(pid) {
			return pid
		}
	}
	t.Skip("no unused PID found to build a stale-lease fixture")
	return 0
}
