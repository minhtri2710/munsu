package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformLockContentionAndReacquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ok, err := tryLockFile(first)
	if err != nil || !ok {
		t.Fatalf("first lock ok=%v err=%v", ok, err)
	}
	ok, err = tryLockFile(second)
	if err != nil || ok {
		t.Fatalf("contended lock ok=%v err=%v", ok, err)
	}
	if err := unlockFile(first); err != nil {
		t.Fatal(err)
	}
	ok, err = tryLockFile(second)
	if err != nil || !ok {
		t.Fatalf("reacquire ok=%v err=%v", ok, err)
	}
	if err := unlockFile(second); err != nil {
		t.Fatal(err)
	}
}
