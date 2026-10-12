package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// The refusal branches of the soldier launch builders. Every case here builds
// a state that satisfies EVERY earlier guard in the same function and then
// breaks exactly one condition, so the observed error can only come from the
// branch under test. The control assertion (the same input with the offending
// field repaired must succeed) is what makes that claim checkable rather than
// asserted: without it a test can pass while an earlier guard is what refused.

// launchPromptInputForGuards is the minimal ship-task input BuildLaunchPrompt
// accepts. Each guard test copies it and breaks one field.
func launchPromptInputForGuards(t *testing.T) LaunchPromptInput {
	t.Helper()
	return LaunchPromptInput{
		TaskID:          "guard-task",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		Repository:      "test-repo",
		ParentCaptainID: "captain-1",
		ParentHome:      t.TempDir(),
		WorktreePath:    t.TempDir(),
		HomeDir:         t.TempDir(),
		BriefContent:    []byte("# brief\n\nDo the work.\n"),
		HarnessName:     "pi",
	}
}

func TestBuildLaunchPromptRefusesMissingWorktreePath(t *testing.T) {
	in := launchPromptInputForGuards(t)
	in.WorktreePath = ""

	_, _, err := BuildLaunchPrompt(in)
	if err == nil {
		t.Fatal("BuildLaunchPrompt accepted an input with no worktree path")
	}
	if !strings.Contains(err.Error(), "worktree path is required") {
		t.Fatalf("error = %v, want the worktree-path refusal", err)
	}

	// Control: only the worktree path was wrong, so repairing it passes.
	in.WorktreePath = t.TempDir()
	if _, _, err := BuildLaunchPrompt(in); err != nil {
		t.Fatalf("repaired input still refused: %v", err)
	}
}

func TestBuildLaunchPromptRefusesMissingHomeDir(t *testing.T) {
	in := launchPromptInputForGuards(t)
	in.HomeDir = ""

	_, _, err := BuildLaunchPrompt(in)
	if err == nil {
		t.Fatal("BuildLaunchPrompt accepted an input with no home dir")
	}
	if !strings.Contains(err.Error(), "home dir is required") {
		t.Fatalf("error = %v, want the home-dir refusal", err)
	}

	in.HomeDir = t.TempDir()
	if _, _, err := BuildLaunchPrompt(in); err != nil {
		t.Fatalf("repaired input still refused: %v", err)
	}
}

// A scout task without a scope has no bounded contract to launch under, so the
// launch is refused before any prompt is built.
func TestBuildLaunchPromptRefusesScoutWithoutBoundedContract(t *testing.T) {
	for _, scope := range []string{"", "   "} {
		t.Run(fmt.Sprintf("scope %q", scope), func(t *testing.T) {
			in := launchPromptInputForGuards(t)
			in.TaskKind = "scout"
			in.ScoutScope = scope

			_, _, err := BuildLaunchPrompt(in)
			if err == nil {
				t.Fatal("BuildLaunchPrompt accepted a scout with no bounded contract")
			}
			if !strings.Contains(err.Error(), "scout scope is required") {
				t.Fatalf("error = %v, want the scout-contract refusal", err)
			}

			// Control: the same scout input with a scope passes, so the
			// refusal above came from this branch and no earlier one.
			in.ScoutScope = "map the delivery lane"
			_, env, err := BuildLaunchPrompt(in)
			if err != nil {
				t.Fatalf("repaired scout input still refused: %v", err)
			}
			if env.ScoutScope != "map the delivery lane" {
				t.Fatalf("envelope scout scope = %q, want the input scope", env.ScoutScope)
			}
		})
	}
}

// The mirror refusal: a ship task carrying a scout scope would launch under a
// contract its charter never states, so it is refused rather than silently
// dropped.
func TestBuildLaunchPromptRefusesScoutContractOnShipTask(t *testing.T) {
	in := launchPromptInputForGuards(t)
	in.TaskKind = "ship"
	in.ScoutScope = "map the delivery lane"

	_, _, err := BuildLaunchPrompt(in)
	if err == nil {
		t.Fatal("BuildLaunchPrompt accepted a ship task carrying a scout contract")
	}
	if !strings.Contains(err.Error(), "scout contract is only valid for scout tasks") {
		t.Fatalf("error = %v, want the ship/scout-contract refusal", err)
	}

	in.ScoutScope = ""
	if _, _, err := BuildLaunchPrompt(in); err != nil {
		t.Fatalf("repaired ship input still refused: %v", err)
	}
}

// launchArtifactInputForGuards is an input buildLaunchArtifact accepts. Each
// guard test copies it and breaks one field.
func launchArtifactInputForGuards(t *testing.T) LaunchArtifactInput {
	t.Helper()
	wtDir, homeDir := t.TempDir(), t.TempDir()
	return LaunchArtifactInput{
		WorktreePath:   wtDir,
		LaunchDir:      wtDir,
		HomeDir:        homeDir,
		PathDirs:       seatPathDirs(homeDir),
		SeatEnv:        testSeatEnv(),
		TaskID:         "guard-task",
		SnapshotDigest: "sha256:snapshot",
		LaunchBin:      "pi",
		LaunchArgs:     []string{"--model", "gpt-5", "prompt text"},
		LaunchID:       "launch-1",
		Generation:     "3",
		EndpointFence:  "fence-1",
	}
}

// testSeatEnv is the seat's launch environment as the test process sees it.
func testSeatEnv() taskauthority.SeatEnv {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return taskauthority.SeatEnv{TempDir: os.TempDir(), UserHome: home}
}

// A harness with no prompt-arg delivery yields an empty bin or arg list. The
// artifact must refuse rather than write a script that execs nothing.
func TestBuildLaunchArtifactRefusesWithoutPromptArgCommand(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(*LaunchArtifactInput)
		repair  func(*LaunchArtifactInput)
		wantSub string
	}{
		{
			name:    "no launch binary",
			break_:  func(in *LaunchArtifactInput) { in.LaunchBin = "" },
			repair:  func(in *LaunchArtifactInput) { in.LaunchBin = "pi" },
			wantSub: "no prompt-arg launch command",
		},
		{
			name:    "no launch directory",
			break_:  func(in *LaunchArtifactInput) { in.LaunchDir = "" },
			repair:  func(in *LaunchArtifactInput) { in.LaunchDir = in.WorktreePath },
			wantSub: "launch directory is required",
		},
		{
			name:    "no launch args",
			break_:  func(in *LaunchArtifactInput) { in.LaunchArgs = nil },
			repair:  func(in *LaunchArtifactInput) { in.LaunchArgs = []string{"prompt text"} },
			wantSub: "no prompt-arg launch command",
		},
		{
			name:    "no seat PATH",
			break_:  func(in *LaunchArtifactInput) { in.PathDirs = nil },
			repair:  func(in *LaunchArtifactInput) { in.PathDirs = seatPathDirs(in.HomeDir) },
			wantSub: "the seat PATH is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := launchArtifactInputForGuards(t)
			tc.break_(&in)

			_, err := buildLaunchArtifact(in)
			if err == nil {
				t.Fatal("buildLaunchArtifact accepted an incomplete launch command")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want %q", err, tc.wantSub)
			}
			// The refusal happens before any file is written.
			if _, statErr := os.Stat(filepath.Join(in.WorktreePath, LaunchScriptName)); !os.IsNotExist(statErr) {
				t.Fatalf("refused launch still wrote %s (stat err = %v)", LaunchScriptName, statErr)
			}

			tc.repair(&in)
			if _, err := buildLaunchArtifact(in); err != nil {
				t.Fatalf("repaired input still refused: %v", err)
			}
		})
	}
}

// The re-entrant launch guard the script embeds is keyed on the exact launch
// identity. An incomplete identity would produce a guard that cannot tell a
// re-entry from a different launch, so the artifact is refused.
func TestBuildLaunchArtifactRefusesIncompleteLaunchIdentity(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(*LaunchArtifactInput)
		repair func(*LaunchArtifactInput)
	}{
		{"no launch id", func(in *LaunchArtifactInput) { in.LaunchID = "" }, func(in *LaunchArtifactInput) { in.LaunchID = "launch-1" }},
		{"no generation", func(in *LaunchArtifactInput) { in.Generation = "" }, func(in *LaunchArtifactInput) { in.Generation = "3" }},
		{"no endpoint fence", func(in *LaunchArtifactInput) { in.EndpointFence = "" }, func(in *LaunchArtifactInput) { in.EndpointFence = "fence-1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := launchArtifactInputForGuards(t)
			tc.break_(&in)

			_, err := buildLaunchArtifact(in)
			if err == nil {
				t.Fatal("buildLaunchArtifact accepted an incomplete launch identity")
			}
			if !strings.Contains(err.Error(), "re-entrant launch guard requires the exact launch identity") {
				t.Fatalf("error = %v, want the launch-identity refusal", err)
			}
			if _, statErr := os.Stat(filepath.Join(in.WorktreePath, LaunchScriptName)); !os.IsNotExist(statErr) {
				t.Fatalf("refused launch still wrote %s (stat err = %v)", LaunchScriptName, statErr)
			}

			tc.repair(&in)
			art, err := buildLaunchArtifact(in)
			if err != nil {
				t.Fatalf("repaired input still refused: %v", err)
			}
			if art.GuardIdentity != in.LaunchID+"|"+in.Generation+"|"+in.EndpointFence {
				t.Fatalf("guard identity = %q, want the exact launch identity", art.GuardIdentity)
			}
		})
	}
}
