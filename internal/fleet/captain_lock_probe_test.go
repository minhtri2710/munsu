package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	t.Run("absent file is free and not created", func(t *testing.T) {
		held, err := LockHeld(path)
		if err != nil {
			t.Fatal(err)
		}
		if held {
			t.Error("LockHeld = true for a nonexistent lock file")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("LockHeld created the lock file; the probe must stay read-only")
		}
	})

	t.Run("free file is free", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("leftover\n"), 0644); err != nil {
			t.Fatal(err)
		}
		held, err := LockHeld(path)
		if err != nil {
			t.Fatal(err)
		}
		if held {
			t.Error("LockHeld = true for an unheld lock file")
		}
	})

	t.Run("held file is held and the probe leaves the holder's lock intact", func(t *testing.T) {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		ok, err := tryLockFile(f)
		if err != nil || !ok {
			t.Fatalf("hold lock ok=%v err=%v", ok, err)
		}
		defer unlockFile(f)

		held, err := LockHeld(path)
		if err != nil {
			t.Fatal(err)
		}
		if !held {
			t.Error("LockHeld = false while the lock is held")
		}

		// A third handle must still be refused: the probe released its own
		// momentary lock and must not have taken the holder's.
		g, err := os.OpenFile(path, os.O_RDWR, 0644)
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		ok3, err := tryLockFile(g)
		if err != nil {
			t.Fatal(err)
		}
		if ok3 {
			t.Error("probe stole the lock: a third handle acquired it while the holder still held it")
		}
	})

	t.Run("released file is free again", func(t *testing.T) {
		f, err := os.OpenFile(path, os.O_RDWR, 0644)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := tryLockFile(f)
		if err != nil || !ok {
			t.Fatalf("re-lock ok=%v err=%v", ok, err)
		}
		if err := unlockFile(f); err != nil {
			t.Fatal(err)
		}
		f.Close()

		held, err := LockHeld(path)
		if err != nil {
			t.Fatal(err)
		}
		if held {
			t.Error("LockHeld = true after the lock was released")
		}
	})
}
