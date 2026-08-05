package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestHomeMkdirInitializesOpenableCanonicalHome proves `munsu home --mkdir`
// initializes a canonical Home (identity + verified layout), not only
// directories, so canonical commands work immediately and re-running stays
// idempotent.
func TestHomeMkdirInitializesOpenableCanonicalHome(t *testing.T) {
	homeDir := t.TempDir()

	run := func() string {
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"home", "--mkdir", "--home", homeDir})
		if err := root.Execute(); err != nil {
			t.Fatalf("home --mkdir: %v\n%s", err, out.String())
		}
		return out.String()
	}

	if out := run(); out == "" {
		t.Fatal("home --mkdir produced no output")
	}
	if _, err := home.Open(homeDir); err != nil {
		t.Fatalf("home --mkdir did not create an openable canonical home: %v", err)
	}
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init on the created home: %v", err)
	}
	// Idempotent: a second --mkdir run succeeds.
	run()
	if _, err := home.Open(homeDir); err != nil {
		t.Fatalf("home --mkdir after re-run not openable: %v", err)
	}
}

// TestInitCreatesOpenableCanonicalHomeIdempotently proves `munsu init`
// initializes a canonical Home and re-running init stays idempotent, leaving
// the home immediately usable by canonical commands.
func TestInitCreatesOpenableCanonicalHomeIdempotently(t *testing.T) {
	homeDir := t.TempDir()
	run := func() {
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"init", "--home", homeDir, "--skill", "skip"})
		if err := root.Execute(); err != nil {
			t.Fatalf("init: %v\n%s", err, out.String())
		}
	}
	run()
	if _, err := home.Open(homeDir); err != nil {
		t.Fatalf("init did not create an openable canonical home: %v", err)
	}
	run()
	if _, err := home.Open(homeDir); err != nil {
		t.Fatalf("home not openable after re-init: %v", err)
	}
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init after init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, home.IdentityFileName)); err != nil {
		t.Fatalf("identity file missing after init: %v", err)
	}
}
