package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

// seededCaptainHome is an initialized home that is already known to be a
// captain home (its .captain-provenance exists) with the given marker content;
// a nil marker leaves the marker missing.
func seededCaptainHome(t *testing.T, marker *string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "captain-dir-name")
	initCLITestHome(t, dir)
	if err := os.WriteFile(filepath.Join(dir, fleet.CaptainProvenanceName), []byte("source-repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if marker != nil {
		if err := os.WriteFile(filepath.Join(dir, home.CaptainProvenanceMarkerName), []byte(*marker), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHomeIdentity(t *testing.T) {
	malformed := "munsu-v2\ncaptain-1\n"
	t.Run("a captain home with a missing marker refuses", func(t *testing.T) {
		_, _, err := homeIdentity(seededCaptainHome(t, nil))
		if err == nil || !strings.Contains(err.Error(), "munsu captain seed <id> <home> --repo <repo>") {
			t.Fatalf("err = %v, want the refusal naming munsu captain seed", err)
		}
	})
	t.Run("a captain home with a malformed marker refuses and names the fix", func(t *testing.T) {
		_, _, err := homeIdentity(seededCaptainHome(t, &malformed))
		if err == nil || !strings.Contains(err.Error(), "is malformed") || !strings.Contains(err.Error(), "munsu captain seed <id> <home> --repo <repo>") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an unreadable marker refuses, says it cannot be read and does not offer seed", func(t *testing.T) {
		dir := seededCaptainHome(t, nil)
		marker := filepath.Join(dir, home.CaptainProvenanceMarkerName)
		if err := os.Mkdir(marker, 0o700); err != nil { // reading a directory fails with EISDIR
			t.Fatal(err)
		}
		_, _, err := homeIdentity(dir)
		if err == nil || !strings.Contains(err.Error(), "captain provenance marker "+marker+" cannot be read") || strings.Contains(err.Error(), "munsu captain seed") {
			t.Fatalf("err = %v, want the unreadable-marker refusal naming %s without the seed hint", err, marker)
		}
	})
	t.Run("a seeded captain home names its marker id", func(t *testing.T) {
		dir := seededCaptainHome(t, nil)
		if err := home.SeedCaptainProvenance(dir, "captain-1"); err != nil {
			t.Fatal(err)
		}
		if id, rank, err := homeIdentity(dir); err != nil || id != "captain-1" || rank != home.RankCaptain {
			t.Fatalf("identity = %q %q %v", id, rank, err)
		}
	})
	t.Run("a copied captain home refuses and does not offer seed", func(t *testing.T) {
		dir := seededCaptainHome(t, nil)
		copied := "munsu-v2\ncaptain-1\n" + filepath.Join(t.TempDir(), "elsewhere") + "\n"
		if err := os.WriteFile(filepath.Join(dir, home.CaptainProvenanceMarkerName), []byte(copied), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := homeIdentity(dir)
		if err == nil || !errors.Is(err, home.ErrCaptainHomeMismatch) || !strings.Contains(err.Error(), "may have been copied/moved") || strings.Contains(err.Error(), "munsu captain seed") {
			t.Fatalf("err = %v, want the copied-home refusal without the seed hint", err)
		}
	})
	t.Run("the General's markerless home is still named by its directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "general-home")
		initCLITestHome(t, dir)
		if id, rank, err := homeIdentity(dir); err != nil || id != "general-home" || rank != home.RankGeneral {
			t.Fatalf("identity = %q %q %v", id, rank, err)
		}
	})
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
		meta   bool
	}{
		{"task", "resolving task owner", map[string]string{"MUNSU_ROLE": "general"}, []string{"task", "add", "t1", "desc"}, false},
		{"consume-ready", "consume-ready: deriving sender identity", map[string]string{"MUNSU_ROLE": "general"}, []string{"consume-ready", "t1"}, false},
		{"soldier report", "report: deriving sender identity", map[string]string{"MUNSU_ROLE": "soldier", "MUNSU_TASK_ID": "t1"}, []string{"report", "review-ready", "ready", "--ring", "no-ring"}, false},
		{"captain report", "report: deriving sender identity", map[string]string{"MUNSU_ROLE": "captain", "MUNSU_TASK_ID": "t1"}, []string{"report", "done", "finished", "--ring", "no-ring"}, false},
		{"send", "send: deriving sender identity", map[string]string{"MUNSU_ROLE": "general"}, []string{"send", "t1", "line"}, true},
		{"soldier-flush", "soldier-flush: deriving sender identity", map[string]string{"MUNSU_ROLE": "general"}, []string{"soldier-flush", "t1"}, false},
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
				if cmd.meta {
					if err := home.WriteMeta(homeDir, "t1", map[string]string{"kind": "soldier"}); err != nil {
						t.Fatal(err)
					}
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
