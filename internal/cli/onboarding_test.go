package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestAutoDetectConfig_OnlyWhenAbsent verifies that autoDetectConfig writes config
// files only when they don't exist, and does not clobber without --reconfigure.
func TestAutoDetectConfig_OnlyWhenAbsent(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-set the backend config key to a specific value
	if err := config.Set(tmpDir, "backend", "my-custom-backend"); err != nil {
		t.Fatal(err)
	}

	if err := autoDetectConfig(tmpDir, false); err != nil {
		t.Fatal(err)
	}

	// Verify backend was NOT overwritten
	val, err := config.Get(tmpDir, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "my-custom-backend" {
		t.Errorf("backend config was clobbered: got %q, want %q", val, "my-custom-backend")
	}
}

// TestAutoDetectConfig_Reconfigure verifies that --reconfigure does NOT write
// backend config (backend is runtime context, not init-time preference).
func TestAutoDetectConfig_Reconfigure(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-set the backend config key to a specific value
	if err := config.Set(tmpDir, "backend", "my-custom-backend"); err != nil {
		t.Fatal(err)
	}

	if err := autoDetectConfig(tmpDir, true); err != nil {
		t.Fatal(err)
	}

	// Backend is runtime context — even --reconfigure should not overwrite it
	val, err := config.Get(tmpDir, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "my-custom-backend" {
		t.Errorf("backend config should not be overwritten with --reconfigure (runtime context); got %q, want %q", val, "my-custom-backend")
	}
}

// TestAutoDetectConfig_InitHome verifies full init flow in a tmp home.
// Backend is runtime context and must NOT be persisted at init time.
func TestAutoDetectConfig_InitHome(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize the canonical home tree
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}

	if err := autoDetectConfig(tmpDir, false); err != nil {
		t.Fatal(err)
	}

	// Backend is runtime context — init should NOT persist it
	if _, err := config.Get(tmpDir, "backend"); err == nil {
		t.Error("autoDetectConfig should NOT write backend (runtime context, not init-time preference)")
	}

	// soldier-harness is persisted into config/base.json only when
	// harness.Detect() succeeds (may or may not in the test env — that's OK).
}

// TestAutoDetectConfig_Idempotent verifies re-running without --reconfigure
// does not change already-written config.
func TestAutoDetectConfig_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()

	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}

	// First run
	if err := autoDetectConfig(tmpDir, false); err != nil {
		t.Fatal(err)
	}

	// Read what was written
	originalBackend, _ := config.Get(tmpDir, "backend")

	// Second run (without --reconfigure)
	if err := autoDetectConfig(tmpDir, false); err != nil {
		t.Fatal(err)
	}

	// Should be unchanged
	val, _ := config.Get(tmpDir, "backend")
	if val != originalBackend {
		t.Errorf("second run changed backend from %q to %q", originalBackend, val)
	}
}

// TestInitDiagnosticsAndNextStepsFollowHomeState runs the diagnostics init
// prints over a controlled PATH and renders the next steps from them.
func TestInitDiagnosticsAndNextStepsFollowHomeState(t *testing.T) {
	const (
		backendStep = "munsu config set backend herdr|tmux"
		harnessStep = "munsu config set soldier-harness <name>"
		doctorStep  = "munsu doctor"
		projectStep = "munsu project add <name> <path-or-url>"
	)
	for _, tc := range []struct {
		name            string
		backend         string
		harness         string
		onPath          []string
		addProject      bool
		wantTool        string
		wantSteps       []string
		absentTool      []string
		absentStep      []string
		wantMissingNone bool
	}{
		{
			name: "herdr resolved", backend: "herdr", harness: "claude",
			onPath:   []string{"gh-axi", "gh", "fuser", "herdr"},
			wantTool: "FOUND: herdr at ", wantSteps: []string{projectStep},
			absentTool: []string{"tmux"}, absentStep: []string{backendStep, harnessStep, doctorStep},
			wantMissingNone: true,
		},
		{
			name: "backend unset", harness: "claude",
			onPath:     []string{"gh-axi", "gh", "fuser"},
			wantSteps:  []string{backendStep, projectStep},
			absentTool: []string{"tmux", "herdr"}, absentStep: []string{harnessStep, doctorStep},
			wantMissingNone: true,
		},
		{
			name: "tmux resolved but missing, no harness", backend: "tmux",
			onPath:   []string{"gh-axi", "gh", "fuser"},
			wantTool: "MISSING: tmux", wantSteps: []string{harnessStep, doctorStep, projectStep},
			absentStep: []string{backendStep},
		},
		{
			name: "project registered", backend: "tmux", harness: "claude",
			onPath: []string{"gh-axi", "gh", "fuser", "tmux"}, addProject: true,
			wantTool: "FOUND: tmux at ", absentStep: []string{backendStep, harnessStep, doctorStep, projectStep},
			wantMissingNone: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config:        config.FleetBaseConfig{Backend: tc.backend, SoldierHarness: tc.harness},
			}); err != nil {
				t.Fatal(err)
			}
			if tc.addProject {
				if out, err := runRoot(t, "project", "add", "sample", t.TempDir(), "--home", homeDir); err != nil {
					t.Fatalf("project add: %v\n%s", err, out)
				}
			}
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
				t.Fatal(err)
			}
			for _, tool := range tc.onPath {
				testutil.WriteFakeExecutable(t, filepath.Join(bin, tool), "#!/bin/sh\nexit 0\n")
			}
			testutil.SetPath(t, bin)

			res, err := bootstrap.Run(homeDir, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			var diag []string
			for _, d := range res.Tools {
				diag = append(diag, d.String())
			}
			diagText := strings.Join(diag, "\n")
			if tc.wantTool != "" && !strings.Contains(diagText, tc.wantTool) {
				t.Errorf("diagnostics lack %q:\n%s", tc.wantTool, diagText)
			}
			for _, tool := range tc.absentTool {
				if strings.Contains(diagText, ": "+tool+" ") {
					t.Errorf("diagnostics report %s although it is not the resolved backend:\n%s", tool, diagText)
				}
			}
			if tc.wantMissingNone && strings.Contains(diagText, "MISSING") {
				t.Errorf("diagnostics report a missing tool:\n%s", diagText)
			}

			var out bytes.Buffer
			printNextSteps(&out, homeDir, res)
			steps := out.String()
			for _, s := range append(tc.wantSteps, "munsu session-start") {
				if !strings.Contains(steps, "  "+s+" ") {
					t.Errorf("next steps lack %q:\n%s", s, steps)
				}
			}
			for _, s := range tc.absentStep {
				if strings.Contains(steps, s) {
					t.Errorf("next steps show %q, which the home does not need:\n%s", s, steps)
				}
			}
			if strings.Contains(steps, "home --mkdir") {
				t.Errorf("next steps still offer home --mkdir:\n%s", steps)
			}
			col := -1
			for _, line := range strings.Split(steps, "\n") {
				if !strings.HasPrefix(line, "  munsu ") {
					continue
				}
				gap := line[2+strings.Index(line[2:], "  "):]
				desc := len(line) - len(strings.TrimLeft(gap, " "))
				if col == -1 {
					col = desc
				} else if desc != col {
					t.Errorf("description column %d, want %d:\n%s", desc, col, steps)
				}
			}
		})
	}
}

// TestEnsureDirTreeConfigFiles verifies the initialized home tree plus
// autoDetectConfig create proper files.
func TestEnsureDirTreeConfigFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize the canonical home, don't run autoDetectConfig
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Config dir should exist
	configDir := filepath.Join(tmpDir, "config")
	if fi, err := os.Stat(configDir); err != nil || !fi.IsDir() {
		t.Fatalf("config dir should exist: %v", err)
	}

	// No config files should exist yet
	if _, err := config.Get(tmpDir, "backend"); err == nil {
		t.Error("backend should not exist before autoDetectConfig")
	}
}
