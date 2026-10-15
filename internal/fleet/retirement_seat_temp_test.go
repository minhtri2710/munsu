package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestFinalizeCleanupRemovesSeatTemp removes the retired seat temp, read-only
// module cache included, without touching a link target, and leaves the seat
// temp of another generation, another home, and an ID the label folds to the
// same text.
func TestFinalizeCleanupRemovesSeatTemp(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := mhome.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	seat := seatTempDir(tmp, "home-a", "T-1", "2")
	others := []string{
		seatTempDir(tmp, "home-a", "T-1", "3"),
		seatTempDir(tmp, "home-b", "T-1", "2"),
		seatTempDir(tmp, "home-a", "t-1", "2"),
	}
	outside := t.TempDir()
	modDir := filepath.Join(seat, "go-mod", "example.com", "m@v1")
	for _, d := range append([]string{modDir}, others...) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module m\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(seat, "link")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{modDir, filepath.Dir(modDir), outside} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Chmod(outside, 0o755) })

	launch := &taskauthority.LaunchIntent{SeatEnv: taskauthority.SeatEnv{TempDir: tmp}}
	if err := finalizeCompletedProjectionCleanup(Options{HomeDir: homeDir, ID: "T-1"}, "home-a", launch, 2, taskauthority.KindShip, &TeardownResult{}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := os.Lstat(seat); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seat temp still present: %v", err)
	}
	for _, o := range others {
		if _, err := os.Stat(o); err != nil {
			t.Fatalf("seat temp %s of another seat removed: %v", o, err)
		}
	}
	if fi, err := os.Stat(outside); err != nil || fi.Mode().Perm() != 0o555 {
		t.Fatalf("link target touched: %v, %v", fi, err)
	}
}
