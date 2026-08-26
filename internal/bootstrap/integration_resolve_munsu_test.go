package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveMunsuPath_TrustsLookPathWithoutPosixExecBit is a regression test
// for #665: after a successful exec.LookPath("munsu"), resolveMunsuPath must
// trust the platform's own executability decision and must NOT require POSIX
// mode bits. On Windows os.Stat reports mode 0 for a normal binary, so the
// historical info.Mode()&0o111 check would reject a valid munsu.exe.
func TestResolveMunsuPath_TrustsLookPathWithoutPosixExecBit(t *testing.T) {
	dir := t.TempDir()
	// A regular, non-directory file WITHOUT the POSIX exec bit. This mirrors
	// how a found Windows binary looks to os.Stat (mode 0), which the old
	// &0o111 check rejected.
	bin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(bin, []byte("fake"), 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}

	origLook := lookPath
	origExe := osExecutable
	defer func() { lookPath = origLook; osExecutable = origExe }()
	lookPath = func(name string) (string, error) { return bin, nil }
	osExecutable = func() (string, error) { return "/some/other/prog", nil }

	got, err := resolveMunsuPath()
	if err != nil {
		t.Fatalf("resolveMunsuPath returned error: %v", err)
	}
	if got == "" || filepath.Base(got) != "munsu" {
		t.Fatalf("expected resolved munsu binary, got %q", got)
	}
}

// TestResolveMunsuPath_AcceptsMunsuTestExe is a regression test for #665: the
// os.Executable fallback must recognize a munsu.test.exe build (the Windows
// Go test binary name) in addition to the existing accepted names.
func TestResolveMunsuPath_AcceptsMunsuTestExe(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "munsu.test.exe")
	if err := os.WriteFile(bin, []byte("fake"), 0755); err != nil {
		t.Fatalf("write bin: %v", err)
	}

	origLook := lookPath
	origExe := osExecutable
	defer func() { lookPath = origLook; osExecutable = origExe }()
	lookPath = func(name string) (string, error) { return "", os.ErrNotExist }
	osExecutable = func() (string, error) { return bin, nil }

	got, err := resolveMunsuPath()
	if err != nil {
		t.Fatalf("resolveMunsuPath returned error: %v", err)
	}
	if got == "" || filepath.Base(got) != "munsu.test.exe" {
		t.Fatalf("expected resolved munsu.test.exe binary, got %q", got)
	}
}

// TestResolveMunsuPath_FailsClosedOnNonMunsuExe preserves fail-closed
// behavior: when LookPath finds nothing and the current executable is not a
// munsu binary, resolution must error rather than return an unrelated path.
func TestResolveMunsuPath_FailsClosedOnNonMunsuExe(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "notmunsu")
	if err := os.WriteFile(bin, []byte("fake"), 0755); err != nil {
		t.Fatalf("write bin: %v", err)
	}

	origLook := lookPath
	origExe := osExecutable
	defer func() { lookPath = origLook; osExecutable = origExe }()
	lookPath = func(name string) (string, error) { return "", os.ErrNotExist }
	osExecutable = func() (string, error) { return bin, nil }

	if _, err := resolveMunsuPath(); err == nil {
		t.Fatal("expected error for non-munsu executable, got nil")
	}
}
