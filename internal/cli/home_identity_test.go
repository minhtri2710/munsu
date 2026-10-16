package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// seededCaptainHome is an initialized home that is already known to be a
// captain home (its .captain-provenance exists) with the given marker content;
// a nil marker leaves the marker missing.
func seededCaptainHome(t *testing.T, marker *string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "captain-dir-name")
	initCLITestHome(t, dir)
	if err := os.WriteFile(filepath.Join(dir, home.CaptainProvenanceName), []byte("source-repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if marker != nil {
		if err := os.WriteFile(filepath.Join(dir, home.CaptainProvenanceMarkerName), []byte(*marker), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCaptainHomeCommandsIdentityGuard runs one command of each family that
// derives a sender or owner identity from the home: a captain home with a
// missing or malformed marker refuses at that derivation, and one with a valid
// marker passes it.
func TestCaptainHomeCommandsIdentityGuard(t *testing.T) {
	commands := []struct {
		family string
		wrap   string
		env    map[string]string
		args   []string
		bound  bool
	}{
		{"task", "resolving task owner", map[string]string{"MUNSU_ROLE": "general"}, []string{"task", "add", "t1", "desc"}, false},
		{"soldier-flush", "soldier-flush: deriving sender identity", map[string]string{"MUNSU_ROLE": "general"}, []string{"soldier-flush", "t1"}, false},
		{"consume-ready", "consume-ready: deriving sender identity", map[string]string{"MUNSU_ROLE": "general"}, []string{"consume-ready", "t1"}, false},
		{"captain report", "report: deriving sender identity", map[string]string{"MUNSU_ROLE": "captain", "MUNSU_TASK_ID": "t1"}, []string{"report", "done", "finished"}, false},
		{"send", "send: deriving sender identity", map[string]string{"MUNSU_ROLE": "general"}, []string{"send", "t1", "line"}, true},
	}
	malformed := "munsu-v2\ncaptain-1\n"
	markers := []struct {
		name    string
		home    func(t *testing.T) string
		refused string
	}{
		{"block: missing marker", func(t *testing.T) string { return seededCaptainHome(t, nil) }, "has no " + home.CaptainProvenanceMarkerName + " marker"},
		{"block: malformed marker", func(t *testing.T) string { return seededCaptainHome(t, &malformed) }, "is malformed"},
		{"allow: valid marker", func(t *testing.T) string {
			dir := seededCaptainHome(t, nil)
			if err := home.SeedCaptainProvenance(dir, "captain-1"); err != nil {
				t.Fatal(err)
			}
			return dir
		}, ""},
	}
	for _, cmd := range commands {
		for _, marker := range markers {
			t.Run(cmd.family+"/"+marker.name, func(t *testing.T) {
				homeDir := marker.home(t)
				t.Setenv("MUNSU_HOME", homeDir)
				t.Setenv("MUNSU_GUARD_SKIP", "1")
				for k, v := range cmd.env {
					t.Setenv(k, v)
				}
				if cmd.env["MUNSU_ROLE"] == "captain" {
					general := filepath.Join(t.TempDir(), "general-home")
					initCLITestHome(t, general)
					t.Setenv("MUNSU_PARENT_STATUS", general)
				}
				if cmd.bound {
					mustBindCLIEndpoint(t, homeDir, "t1", taskauthority.EndpointBinding{Backend: "tmux", Handle: "@1"})
				}
				_, err := runRoot(t, cmd.args...)
				if marker.refused == "" {
					if err != nil && (strings.Contains(err.Error(), cmd.wrap) || strings.Contains(err.Error(), "munsu captain seed")) {
						t.Fatalf("err = %v, a captain home with a valid marker must pass the identity derivation", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), cmd.wrap) || !strings.Contains(err.Error(), marker.refused) || !strings.Contains(err.Error(), "munsu captain seed <id> <home> --repo <repo>") {
					t.Fatalf("err = %v, want %q refusing with %q naming munsu captain seed", err, cmd.wrap, marker.refused)
				}
			})
		}
	}
}
