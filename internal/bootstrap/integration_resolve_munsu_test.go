package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveMunsuPath_TrustsLookPathWithoutPosixExecBit is a regression test
// for #665: after a successful exec.LookPath("munsu"), resolveMunsuPath must
// trust the platform's own executability decision and must NOT recheck POSIX
// mode bits.
func TestResolveMunsuPath_TrustsLookPathWithoutPosixExecBit(t *testing.T) {
	dir := t.TempDir()
	// A regular, non-directory file WITHOUT the POSIX exec bit. LookPath's
	// successful result represents the platform's executability decision; the
	// old &0o111 recheck incorrectly imposed a POSIX-only condition.
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
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat bin: %v", err)
	}
	t.Logf("resolved %q after injected LookPath success; mode=%#o", got, info.Mode().Perm())
}

func TestResolveMunsuPath_AcceptsMunsuTest(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "munsu.test")
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
	if got == "" || filepath.Base(got) != "munsu.test" {
		t.Fatalf("expected resolved munsu.test binary, got %q", got)
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

func TestResolveMunsuPath_FailsClosedOnLookPathDirectory(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "munsu")
	if err := os.Mkdir(candidate, 0755); err != nil {
		t.Fatalf("make candidate directory: %v", err)
	}

	origLook := lookPath
	origExe := osExecutable
	defer func() { lookPath = origLook; osExecutable = origExe }()
	lookPath = func(name string) (string, error) { return candidate, nil }
	osExecutable = func() (string, error) { return filepath.Join(dir, "notmunsu"), nil }

	if _, err := resolveMunsuPath(); err == nil {
		t.Fatal("expected error for directory returned by LookPath, got nil")
	}
}

func TestResolveMunsuPath_FailsClosedOnMunsuNamedDirectoryFallback(t *testing.T) {
	dir := t.TempDir()
	candidate := filepath.Join(dir, "munsu.test")
	if err := os.Mkdir(candidate, 0755); err != nil {
		t.Fatalf("make fallback directory: %v", err)
	}

	origLook := lookPath
	origExe := osExecutable
	defer func() { lookPath = origLook; osExecutable = origExe }()
	lookPath = func(name string) (string, error) { return "", os.ErrNotExist }
	osExecutable = func() (string, error) { return candidate, nil }

	if _, err := resolveMunsuPath(); err == nil {
		t.Fatal("expected error for directory fallback, got nil")
	}
}
