package cli

import (
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
	t.Run("a copied captain home keeps the marker id", func(t *testing.T) {
		dir := seededCaptainHome(t, nil)
		copied := "munsu-v2\ncaptain-1\n" + filepath.Join(t.TempDir(), "elsewhere") + "\n"
		if err := os.WriteFile(filepath.Join(dir, home.CaptainProvenanceMarkerName), []byte(copied), 0o600); err != nil {
			t.Fatal(err)
		}
		if id, rank, err := homeIdentity(dir); err != nil || id != "captain-1" || rank != home.RankCaptain {
			t.Fatalf("identity = %q %q %v, want today's marker id for a copied home", id, rank, err)
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

// TestCaptainHomeCommandsRefuseAnUnusableMarker runs one command of each family
// that derived a sender or owner identity from the home's basename.
func TestCaptainHomeCommandsRefuseAnUnusableMarker(t *testing.T) {
	invocations := []struct {
		family string
		env    map[string]string
		args   []string
		meta   bool
	}{
		{"task", map[string]string{"MUNSU_ROLE": "general"}, []string{"task", "add", "t1", "desc"}, false},
		{"consume-ready", map[string]string{"MUNSU_ROLE": "general"}, []string{"consume-ready", "t1"}, false},
		{"report", map[string]string{"MUNSU_ROLE": "soldier", "MUNSU_TASK_ID": "t1"}, []string{"report", "review-ready", "ready", "--ring", "no-ring"}, false},
		{"send", map[string]string{"MUNSU_ROLE": "general"}, []string{"send", "t1", "line"}, true},
		{"soldier-flush", map[string]string{"MUNSU_ROLE": "general"}, []string{"soldier-flush", "t1"}, false},
	}
	run := func(t *testing.T, homeDir string, tc int) error {
		inv := invocations[tc]
		t.Setenv("MUNSU_HOME", homeDir)
		t.Setenv("MUNSU_GUARD_SKIP", "1")
		for k, v := range inv.env {
			t.Setenv(k, v)
		}
		if inv.meta {
			if err := home.WriteMeta(homeDir, "t1", map[string]string{"kind": "soldier"}); err != nil {
				t.Fatal(err)
			}
		}
		_, err := runRoot(t, inv.args...)
		return err
	}
	for i, inv := range invocations {
		t.Run(inv.family+" refuses a captain home with no marker", func(t *testing.T) {
			err := run(t, seededCaptainHome(t, nil), i)
			if err == nil || !strings.Contains(err.Error(), "munsu captain seed <id> <home> --repo <repo>") {
				t.Fatalf("err = %v, want the unusable-marker refusal", err)
			}
		})
		t.Run(inv.family+" still runs in the General's home", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "general-home")
			initCLITestHome(t, dir)
			err := run(t, dir, i)
			if err != nil && (strings.Contains(err.Error(), "deriving sender identity") || strings.Contains(err.Error(), "resolving task owner")) {
				t.Fatalf("err = %v, the General's home must not be refused for a missing marker", err)
			}
		})
	}
}

func TestCaptainReportRefusesAnUnusableMarker(t *testing.T) {
	general := filepath.Join(t.TempDir(), "general-home")
	initCLITestHome(t, general)
	t.Setenv("MUNSU_HOME", seededCaptainHome(t, nil))
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	t.Setenv("MUNSU_ROLE", "captain")
	t.Setenv("MUNSU_TASK_ID", "t1")
	t.Setenv("MUNSU_PARENT_STATUS", general)
	_, err := runRoot(t, "report", "done", "finished", "--ring", "no-ring")
	if err == nil || !strings.Contains(err.Error(), "report: deriving sender identity") || !strings.Contains(err.Error(), "munsu captain seed <id> <home> --repo <repo>") {
		t.Fatalf("err = %v, want the captain report refused for its unusable marker", err)
	}
}
