//go:build !windows

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCheckWithLstat_NotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateCheckWithLstat(path)
	if err == nil {
		t.Fatal("expected error for non-executable")
	}
}
