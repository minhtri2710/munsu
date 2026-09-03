package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFleetSnapshotEmitsSingleContract verifies the collapsed command emits
// exactly one fleet.snapshot shape with no --version selector and no v1
// raw-passthrough kind, for both TOON and JSON output.
func TestFleetSnapshotEmitsSingleContract(t *testing.T) {
	home := t.TempDir()
	initCLITestHome(t, home)
	t.Setenv("MUNSU_HOME", home)

	out, err := runRoot(t, "fleet", "snapshot")
	if err != nil {
		t.Fatalf("fleet snapshot: %v", err)
	}
	if !strings.Contains(out, "kind: fleet.snapshot") {
		t.Errorf("expected kind: fleet.snapshot, got:\n%s", out)
	}
	if strings.Contains(out, "fleet.snapshot.v1") {
		t.Errorf("v1 raw-passthrough kind must be gone:\n%s", out)
	}
	if strings.Contains(out, "schema:") {
		t.Errorf("dead Schema field must be dropped from fleet.FleetSnapshot:\n%s", out)
	}

	j, err := runRoot(t, "fleet", "snapshot", "--output", "json")
	if err != nil {
		t.Fatalf("fleet snapshot json: %v", err)
	}
	var resp struct {
		Kind string `json:"kind"`
		Data struct {
			Schema string `json:"schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(j), &resp); err != nil {
		t.Fatalf("json: %v\n%s", err, j)
	}
	if resp.Kind != "fleet.snapshot" {
		t.Errorf("kind = %q, want fleet.snapshot", resp.Kind)
	}
	if resp.Data.Schema != "" {
		t.Errorf("dead Schema field still present on fleet snapshot data: %q", resp.Data.Schema)
	}
}

// TestFleetSnapshotRejectsVersionSelector verifies the --version selector is
// removed: passing --version 2 is an unknown flag, proving a single contract.
func TestFleetSnapshotRejectsVersionSelector(t *testing.T) {
	home := t.TempDir()
	initCLITestHome(t, home)
	_, err := runRoot(t, "fleet", "snapshot", "--version", "2", "--home", home)
	if err == nil {
		t.Fatal("expected --version to be an unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag: --version") {
		t.Errorf("expected unknown flag: --version, got: %v", err)
	}
}

// TestFleetSnapshotSurfacesRawCurrentStateError verifies the command surfaces
// the raw current-state error instead of wrapping it, and that the home path
// in that error is NOT pre-quoted (the home-path-not-pre-quoted invariant).
func TestFleetSnapshotSurfacesRawCurrentStateError(t *testing.T) {
	home := t.TempDir()
	initCLITestHome(t, home)
	t.Setenv("MUNSU_HOME", home)

	// A meta-only task (no canonical Task Authority record) forces
	// fleet.Snapshot to fail while reading authoritative current state.
	if err := os.WriteFile(filepath.Join(home, "state", "alpha.meta"), []byte("kind=ship\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "fleet", "snapshot")
	if err == nil {
		t.Fatalf("expected a current-state error, got clean output:\n%s", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, "no canonical Task Authority record") {
		t.Errorf("raw current-state error not surfaced; got: %v", err)
	}
	if strings.Contains(msg, "Unable to read fleet state") {
		t.Errorf("error was re-wrapped instead of surfaced raw: %v", err)
	}
	if strings.Contains(msg, `home "`) {
		t.Errorf("home path must not be pre-quoted: %v", err)
	}
	if !strings.Contains(msg, "in home "+home) {
		t.Errorf("home path missing from surfaced error: %v", err)
	}
}
