//go:build windows

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On Windows, Go never reports the Unix exec mode bits, so a regular .check
// file with a shebang must validate even though os.WriteFile ignores the 0755
// mode. This is the Windows-specific executability semantics: no exec bit to
// test, the shebang is the runnability gate.
func TestValidateCheckWithLstat_WindowsAcceptsExecutableScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	data := []byte("#!/bin/bash\necho hello\n")
	// 0644 here is deliberate: on Windows the mode is ignored, so even a
	// "non-executable" mode must not refuse a valid shebang script.
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCheckWithLstat(path); err != nil {
		t.Fatalf("expected valid on Windows, got: %v", err)
	}
}

// The fail-closed rejections are platform-independent and must still hold on
// Windows.
func TestValidateCheckWithLstat_WindowsRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.check")
	os.WriteFile(target, []byte("#!/bin/bash\necho\n"), 0644)
	link := filepath.Join(dir, "link.check")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := ValidateCheckWithLstat(link)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}
}

func TestValidateCheckWithLstat_WindowsRejectsMissingShebang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.WriteFile(path, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateCheckWithLstat(path)
	if err == nil || !strings.Contains(err.Error(), "shebang") {
		t.Fatalf("expected missing-shebang error, got: %v", err)
	}
}
