package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestSpawnArmEnsuresWatcherOnlyWhenRequested(t *testing.T) {
	for _, arm := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-arm", true: "with-arm"}[arm], func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("MUNSU_GUARD_SKIP", "1")
			t.Setenv("MUNSU_ROLE", "general")
			t.Setenv("GEMINI_API_KEY", "test-key")

			repoDir := t.TempDir()
			runGit(t, repoDir, "init", "-b", "main")
			runGit(t, repoDir, "config", "user.email", "test@example.com")
			runGit(t, repoDir, "config", "user.name", "Test")
			runGit(t, repoDir, "commit", "--allow-empty", "-m", "initial commit")
			if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config: config.FleetBaseConfig{
					SoldierHarness: "pi",
					Backend:        "tmux",
				},
			}); err != nil {
				t.Fatalf("store fleet base: %v", err)
			}
			if err := fleet.Add(homeDir, "alpha", repoDir, false); err != nil {
				t.Fatalf("register project: %v", err)
			}
			if output, err := runTaskCommand(t, []string{"task", "add", "arm-task", "Test watcher arming", "--repo", "alpha", "--home", homeDir}); err != nil {
				t.Fatalf("add task: %v\n%s", err, output)
			}
			if output, err := runRoot(t, "brief", "arm-task", "alpha", "--home", homeDir); err != nil {
				t.Fatalf("scaffold brief: %v\n%s", err, output)
			}

			gitBin, err := exec.LookPath("git")
			if err != nil {
				t.Fatalf("git on PATH: %v", err)
			}
			tmuxDir := t.TempDir()
			testutil.WriteFakeExecutable(t, filepath.Join(tmuxDir, "tmux"), `#!/bin/sh
case "$1" in
  has-session|list-windows) exit 0 ;;
  new-window) printf '@1\n' ;;
  list-panes) printf '%%1\n' ;;
  display-message) printf 'pi\n' ;;
  capture-pane) printf '> ready\n' ;;
  send-keys)
    if [ "$4" = "-l" ]; then eval "$6"; fi
    ;;
esac
`)
			piDir := t.TempDir()
			testutil.WriteFakeExecutable(t, filepath.Join(piDir, "pi"), "#!/bin/sh\nexit 0\n")
			path := append([]string{tmuxDir, piDir, filepath.Dir(gitBin)}, testutil.BashShellDirs(t)...)
			t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
			t.Chdir(repoDir)

			starts := 0
			oldStarter := startWatcherProcess
			startWatcherProcess = func(gotHome string) (int, error) {
				starts++
				if gotHome != homeDir {
					t.Errorf("watcher home = %q, want %q", gotHome, homeDir)
				}
				return 0, errors.New("watcher startup blocked")
			}
			t.Cleanup(func() { startWatcherProcess = oldStarter })

			args := []string{"spawn", "arm-task", "alpha", "--home", homeDir}
			if arm {
				args = append(args, "--arm")
			}
			var output, stderr string
			stdout, capturedStderr := captureBoth(func() {
				var spawnErr error
				output, spawnErr = runRoot(t, args...)
				if spawnErr != nil {
					t.Errorf("spawn command: %v\n%s", spawnErr, output)
				}
			})
			stderr = capturedStderr
			if !strings.Contains(stdout, "Spawned soldier") {
				t.Fatalf("spawn output = %q, want successful spawn", stdout)
			}
			if want := map[bool]int{false: 0, true: 1}[arm]; starts != want {
				t.Fatalf("watcher start calls = %d, want %d; stdout=%q stderr=%q", starts, want, stdout, stderr)
			}
			if arm && !strings.Contains(stderr, "warning: failed to ensure watcher:") {
				t.Fatalf("spawn --arm stderr = %q, want warn-only ensure failure", stderr)
			}
		})
	}
}
