// Tests for the CLI-side check validation port. The watcher validates a check
// artifact through this port, and fleet.RetireMergedPoll re-validates the same
// path as step 0 of retirement. Those two sites must apply one rule: if they
// could diverge, the watcher would have to decide which verdict wins.
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
)

func TestCheckValidationPortAppliesTheRetirementValidator(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.check")
	if err := os.WriteFile(valid, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.check")
	if err := os.WriteFile(empty, nil, 0755); err != nil {
		t.Fatal(err)
	}
	noShebang := filepath.Join(dir, "no-shebang.check")
	if err := os.WriteFile(noShebang, []byte("echo ready\n"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.check")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.check")

	for _, path := range []string{valid, empty, noShebang, link, missing} {
		port := (fleetCheckValidationPort{}).ValidateCheck(path)
		direct := fleet.ValidateCheckWithLstat(path)
		if (port == nil) != (direct == nil) {
			t.Fatalf("%s: port verdict = %v, retirement validator verdict = %v", filepath.Base(path), port, direct)
		}
	}
}
