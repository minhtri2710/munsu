package home

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeLeaseFixture(t *testing.T, home, name, content string) {
	t.Helper()
	if err := os.MkdirAll(LeaseDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(LeaseDir(home), name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

const claimedWakeLine = "100\t1\tuplink\tgreet-go\tdone: pr\\topen\n"

// TestListClaimedLeasesReadsFieldsInClaimOrder pins the shape of a listed
// lease: header fields, decoded wakes, resolution state, and the order.
func TestListClaimedLeasesReadsFieldsInClaimOrder(t *testing.T) {
	home := t.TempDir()
	writeLeaseFixture(t, home, "lease-b", "lease-b\tmunsu:herdr\t2000\t1500\n"+claimedWakeLine)
	writeLeaseFixture(t, home, "lease-a", "lease-a\tgeneral\t1900\t1500\n101\t2\tsignal\tfix-x\tblocked: x\n")
	writeLeaseFixture(t, home, "lease-c", "lease-c\tgeneral\t1800\t1000\n102\t3\tsignal\tfix-y\tfailed: y\n")
	if err := writeWakeResolution(home, wakeResolutionRecord{LeaseID: "lease-b", EventID: "100:1", Summary: "s", State: "prepared", UpdatedAt: 1700}); err != nil {
		t.Fatal(err)
	}

	leases, err := ListClaimedLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range leases {
		ids = append(ids, l.LeaseID)
	}
	if want := []string{"lease-c", "lease-a", "lease-b"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v (claim time, then lease ID)", ids, want)
	}
	b := leases[2]
	if b.Consumer != "munsu:herdr" || b.ClaimedAt != 1500 || b.ExpiresAt != 2000 || b.ModTime.IsZero() {
		t.Fatalf("lease-b header fields = %+v", b)
	}
	want := ClaimedLeaseWake{
		ClaimedWakeRecord: ClaimedWakeRecord{Epoch: "100", Seq: "1", Kind: "uplink", Key: "greet-go", Payload: "done: pr\topen"},
		Resolution:        "prepared", ResolutionAt: 1700,
	}
	if len(b.Wakes) != 1 || b.Wakes[0] != want {
		t.Fatalf("lease-b wakes = %+v, want %+v", b.Wakes, want)
	}
}

// TestListClaimedLeasesSkipsShapesItCannotRead plants every shape the lister
// must tolerate beside one valid lease; only the valid lease may come back.
func TestListClaimedLeasesSkipsShapesItCannotRead(t *testing.T) {
	home := t.TempDir()
	writeLeaseFixture(t, home, "lease-ok", "lease-ok\tgeneral\t2000\t1500\n"+claimedWakeLine)
	writeLeaseFixture(t, home, ".home-write-123", "lease-tmp\tgeneral\t2000\t1500\n"+claimedWakeLine)
	writeLeaseFixture(t, home, "lease-empty", "")
	writeLeaseFixture(t, home, "lease-short-header", "lease-short-header\tgeneral\n"+claimedWakeLine)
	writeLeaseFixture(t, home, "lease-bad-expiry", "lease-bad-expiry\tgeneral\tsoon\t1500\n"+claimedWakeLine)
	writeLeaseFixture(t, home, "lease-no-wakes", "lease-no-wakes\tgeneral\t2000\t1500\nnot a wake line\n")
	writeLeaseFixture(t, home, "bad:id", "bad:id\tgeneral\t2000\t1500\n"+claimedWakeLine)
	if err := os.Mkdir(filepath.Join(LeaseDir(home), "lease-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	// A resolution record that does not parse is no resolution.
	if err := os.MkdirAll(filepath.Join(home, wakeResolutionDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolutionPath(home, "lease-ok", "100:1"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	leases, err := ListClaimedLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].LeaseID != "lease-ok" {
		t.Fatalf("leases = %+v, want only lease-ok", leases)
	}
	if got := leases[0].Wakes[0].Resolution; got != "" {
		t.Fatalf("unparsable resolution read as %q, want none", got)
	}
}

// TestListClaimedLeasesToleratesAVanishingLease proves a lease removed between
// the directory read and the file read is skipped, not an error: reclaim and
// ack remove leases while a reader walks the directory.
func TestListClaimedLeasesToleratesAVanishingLease(t *testing.T) {
	home := t.TempDir()
	writeLeaseFixture(t, home, "lease-gone", "lease-gone\tgeneral\t2000\t1500\n"+claimedWakeLine)
	writeLeaseFixture(t, home, "lease-ok", "lease-ok\tgeneral\t2000\t1500\n"+claimedWakeLine)
	real := readClaimedLeaseFile
	t.Cleanup(func() { readClaimedLeaseFile = real })
	readClaimedLeaseFile = func(path string) ([]byte, time.Time, error) {
		if filepath.Base(path) == "lease-gone" {
			return nil, time.Time{}, os.ErrNotExist
		}
		return real(path)
	}

	leases, err := ListClaimedLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].LeaseID != "lease-ok" {
		t.Fatalf("leases = %+v, want only lease-ok", leases)
	}
}

// TestListClaimedLeasesFailsClosed proves an unsafe lease directory or a read
// error other than absence is returned, so a reader reports unknown and never
// none.
func TestListClaimedLeasesFailsClosed(t *testing.T) {
	t.Run("lease root is not a directory", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(LeaseDir(home), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ListClaimedLeases(home); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("got %v, want a not-a-directory error", err)
		}
	})
	t.Run("directory read fails", func(t *testing.T) {
		home := t.TempDir()
		writeLeaseFixture(t, home, "lease-ok", "lease-ok\tgeneral\t2000\t1500\n"+claimedWakeLine)
		boom := errors.New("boom")
		real := readClaimedLeaseDir
		t.Cleanup(func() { readClaimedLeaseDir = real })
		readClaimedLeaseDir = func(string) ([]os.DirEntry, error) { return nil, boom }
		if _, err := ListClaimedLeases(home); !errors.Is(err, boom) {
			t.Fatalf("got %v, want the directory read error", err)
		}
	})
	t.Run("file read fails", func(t *testing.T) {
		home := t.TempDir()
		writeLeaseFixture(t, home, "lease-ok", "lease-ok\tgeneral\t2000\t1500\n"+claimedWakeLine)
		boom := errors.New("boom")
		real := readClaimedLeaseFile
		t.Cleanup(func() { readClaimedLeaseFile = real })
		readClaimedLeaseFile = func(string) ([]byte, time.Time, error) { return nil, time.Time{}, boom }
		if _, err := ListClaimedLeases(home); !errors.Is(err, boom) {
			t.Fatalf("got %v, want the file read error", err)
		}
	})
}

// TestListClaimedLeasesCreatesNothing proves the read takes no lock and makes
// no directory: a home with no state directory, and one with a state directory
// but no lease directory, are listed empty and left exactly as found.
func TestListClaimedLeasesCreatesNothing(t *testing.T) {
	for name, prepare := range map[string]func(home string){
		"no state directory":    func(string) {},
		"no lease directory":    func(home string) { _ = os.MkdirAll(filepath.Join(home, "state"), 0755) },
		"empty lease directory": func(home string) { _ = os.MkdirAll(LeaseDir(home), 0700) },
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			prepare(home)
			before := listTree(t, home)
			leases, err := ListClaimedLeases(home)
			if err != nil || len(leases) != 0 {
				t.Fatalf("ListClaimedLeases = %v, %v, want none", leases, err)
			}
			if after := listTree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatalf("home changed: before %v, after %v", before, after)
			}
		})
	}
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// TestClaimStateBoundaries pins each derived state at its boundary. Every
// instant is built from the lease's own facts and the threshold passed in.
func TestClaimStateBoundaries(t *testing.T) {
	const stallAfter = 5 * time.Minute
	claimed := time.Unix(10_000, 0)
	lease := ClaimedLease{LeaseID: "lease-1", Consumer: "general", ClaimedAt: claimed.Unix(), ExpiresAt: claimed.Add(time.Minute).Unix(), ModTime: claimed}
	wake := ClaimedLeaseWake{ClaimedWakeRecord: ClaimedWakeRecord{Epoch: "9000", Seq: "1"}}

	cases := []struct {
		name  string
		lease ClaimedLease
		wake  ClaimedLeaseWake
		now   time.Time
		want  string
	}{
		{"before expiry", lease, wake, claimed.Add(time.Minute - time.Second), ClaimHeld},
		{"at expiry", lease, wake, claimed.Add(time.Minute), ClaimExpired},
		{"expired, inside the stall window", lease, wake, claimed.Add(stallAfter), ClaimExpired},
		{"one second past the stall window", lease, wake, claimed.Add(stallAfter + time.Second), ClaimStalled},
		{"partial ack moves the anchor", func() ClaimedLease { l := lease; l.ModTime = claimed.Add(4 * time.Minute); return l }(), wake, claimed.Add(stallAfter + time.Second), ClaimExpired},
		{"prepared resolution moves the anchor", lease, withResolution(wake, resolutionPrepared, claimed.Add(4*time.Minute)), claimed.Add(stallAfter + time.Second), ClaimResolving},
		{"prepared resolution that never completed", lease, withResolution(wake, resolutionPrepared, claimed.Add(time.Minute)), claimed.Add(time.Minute + stallAfter + time.Second), ClaimStalled},
		{"completed resolution still listed", lease, withResolution(wake, resolutionCompleted, claimed.Add(time.Minute)), claimed.Add(time.Hour), ClaimResolved},
		{"header without a claim time falls back to the wake epoch", func() ClaimedLease { l := lease; l.ClaimedAt = 0; l.ModTime = time.Time{}; return l }(), wake, time.Unix(9000, 0).Add(stallAfter + time.Second), ClaimStalled},
		{"no time evidence at all is never stalled", func() ClaimedLease { l := lease; l.ClaimedAt = 0; l.ModTime = time.Time{}; return l }(), ClaimedLeaseWake{}, claimed.Add(time.Hour), ClaimExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, evidence := ClaimState(tc.lease, tc.wake, tc.now, stallAfter)
			if got != tc.want {
				t.Fatalf("state = %q (%s), want %q", got, evidence, tc.want)
			}
			if (got == ClaimHeld) != (evidence == "") {
				t.Fatalf("state %q evidence %q: only held carries no evidence", got, evidence)
			}
		})
	}
}

func withResolution(w ClaimedLeaseWake, state string, at time.Time) ClaimedLeaseWake {
	w.Resolution, w.ResolutionAt = state, at.Unix()
	return w
}
