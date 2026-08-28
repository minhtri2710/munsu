package cli

// Batch 6 of the .github/uncovered-guards.baseline burn-down: the refusal
// branches of internal/cli that no test entered.
//
// Every test here builds the exact state one guard refuses and asserts that
// guard's own message. Asserting `err != nil` is banned at a guard site: a
// refusal branch reached through a command has other refusals in front of it,
// and a bare non-nil check is green for every one of them. Where the message
// alone cannot separate the guard under test from an earlier one, the test
// also asserts what the error must NOT be.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

// wantErrContains fails unless err carries sub. `what` names the call so a
// failure says which refusal went missing.
func wantErrContains(t *testing.T, err error, sub, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was accepted, want the %q refusal", what, sub)
	}
	if !strings.Contains(err.Error(), sub) {
		t.Fatalf("%s = %v, want the %q refusal", what, err, sub)
	}
}

// --- internal/cli/root.go: the positional-argument validators ---------------
//
// These are called by cobra with the command being validated, so the test
// calls them the same way. A command literal is the whole fixture they read.

func TestGuardNoArgsRefusesAnyPositionalArgument(t *testing.T) {
	cmd := &cobra.Command{Use: "demo"}
	if err := NoArgs(cmd, nil); err != nil {
		t.Fatalf("NoArgs with no arguments = %v, want acceptance", err)
	}
	wantErrContains(t, NoArgs(cmd, []string{"extra"}), "accepts no arguments, received 1", "NoArgs with one argument")
}

func TestGuardMinimumNArgsRefusesFewerThanN(t *testing.T) {
	cmd := &cobra.Command{Use: "demo"}
	if err := MinimumNArgs(2)(cmd, []string{"a", "b"}); err != nil {
		t.Fatalf("MinimumNArgs(2) with two arguments = %v, want acceptance", err)
	}
	wantErrContains(t, MinimumNArgs(2)(cmd, []string{"a"}), "requires at least 2 arg(s), received 1", "MinimumNArgs(2) with one argument")
}

func TestGuardMaximumNArgsRefusesMoreThanN(t *testing.T) {
	cmd := &cobra.Command{Use: "demo"}
	if err := MaximumNArgs(1)(cmd, []string{"a"}); err != nil {
		t.Fatalf("MaximumNArgs(1) with one argument = %v, want acceptance", err)
	}
	wantErrContains(t, MaximumNArgs(1)(cmd, []string{"a", "b"}), "accepts at most 1 arg(s), received 2", "MaximumNArgs(1) with two arguments")
}

// --- internal/cli/contract.go: the output-format gate -----------------------

func TestGuardContractOutputRefusesAnUnsupportedFormat(t *testing.T) {
	cmd := &cobra.Command{Use: "demo"}
	configureContractCommand(cmd)
	if err := cmd.Flags().Set("output", "yaml"); err != nil {
		t.Fatal(err)
	}
	_, err := contractOutput(cmd)
	wantErrContains(t, err, `Unsupported output format "yaml"`, "contractOutput with --output yaml")

	for _, supported := range []string{OutputTOON, OutputJSON} {
		if err := cmd.Flags().Set("output", supported); err != nil {
			t.Fatal(err)
		}
		if got, err := contractOutput(cmd); err != nil || got != supported {
			t.Fatalf("contractOutput with --output %s = (%q, %v), want it accepted", supported, got, err)
		}
	}
}

// --- internal/cli/contract_commands.go, fleet_contract.go: --full and
// --version, both of which refuse before any home is read -------------------

func TestGuardTaskObserveRefusesFullBecauseNoFieldIsTruncated(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "task", "observe", "t1", "--full", "--home", homeDir)
	wantErrContains(t, err, "--full is unavailable because task observation has no truncated fields", "task observe --full")
}

// fleetSnapshotCmd returns the registered `fleet snapshot` command so a test
// can drive runFleetSnapshotV2 with the same flag set the CLI gives it.
func fleetSnapshotCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd, _, err := NewRootCommand().Find([]string{"fleet", "snapshot"})
	if err != nil {
		t.Fatalf("Find(fleet snapshot): %v", err)
	}
	return cmd
}

// The CLI dispatches into runFleetSnapshotV2 only under `version == 2`
// (fleet_cmd.go), so its own version check is a callee-side precondition, not
// a user-facing refusal: `munsu fleet snapshot --version 3` is refused one
// level up with a different message. The test calls the function directly,
// which is the only way to enter the branch, and pins both halves — the
// precondition holds, and the caller in front of it still refuses first.
func TestGuardFleetSnapshotV2RefusesAnyVersionOtherThanTwo(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	cmd := fleetSnapshotCmd(t)
	if err := cmd.Flags().Set("version", "3"); err != nil {
		t.Fatal(err)
	}
	err := runFleetSnapshotV2(cmd, Ctx{Home: homeDir})
	wantErrContains(t, err, "Only fleet snapshot version 2 is supported by this command", "runFleetSnapshotV2 called with version 3")

	out, err := runRoot(t, "fleet", "snapshot", "--version", "3", "--home", homeDir)
	wantErrContains(t, err, "Only fleet snapshot versions 1 and 2 are supported", "fleet snapshot --version 3 through the CLI")
	if strings.Contains(out, "supported by this command") {
		t.Fatal("the CLI reached runFleetSnapshotV2's version check, so that check is no longer a callee-side precondition")
	}
}

func TestGuardFleetSnapshotRefusesFullBecauseNoRowIsTruncated(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "fleet", "snapshot", "--version", "2", "--full", "--home", homeDir)
	wantErrContains(t, err, "--full is unavailable because fleet snapshot rows have no truncated fields", "fleet snapshot --full")
	// --version is checked first; a --full refusal that reads as the version
	// refusal would mean this test never reached the guard it names.
	if strings.Contains(err.Error(), "version 2 is supported") {
		t.Fatalf("fleet snapshot --full = %v, which is the --version refusal standing in front of it", err)
	}
}

// --- internal/cli/event_cmd.go ---------------------------------------------

func TestGuardEventAppendRequiresAType(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "event", "append", "e1", "--home", homeDir)
	wantErrContains(t, err, "Flag --type is required", "event append with no --type")

	if _, err := runRoot(t, "event", "append", "e1", "--type", "note", "--home", homeDir); err != nil {
		t.Fatalf("event append --type note = %v, want acceptance: the refusal above is only attributable to --type if this commits", err)
	}
}

// --- internal/cli/wake_cmd.go ----------------------------------------------

func TestGuardWakeClaimRequiresAConsumer(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "wake", "claim", "--home", homeDir)
	wantErrContains(t, err, "--consumer is required", "wake claim with no --consumer")

	if _, err := runRoot(t, "wake", "claim", "--consumer", "general", "--home", homeDir); err != nil {
		t.Fatalf("wake claim --consumer general = %v, want acceptance", err)
	}
}

func TestGuardWakeAckRequiresALeaseAndAtLeastOneEventID(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "wake", "ack", "lease-1", "--home", homeDir)
	wantErrContains(t, err, "Requires lease-id and at least one event-id", "wake ack with only a lease id")
}

// --- internal/cli/ready_cmd.go, report_cmd.go: the environment identity
// these commands run under -------------------------------------------------

func TestGuardReadyRefusesOutsideAManagedTask(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	t.Setenv("MUNSU_HOME", "")
	t.Setenv("MUNSU_TASK_ID", "t1")
	_, err := runRoot(t, "ready", "--event-id", "e1", "--home", homeDir)
	wantErrContains(t, err, "MUNSU_HOME is not set", "ready with no MUNSU_HOME")

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "")
	_, err = runRoot(t, "ready", "--event-id", "e1", "--home", homeDir)
	wantErrContains(t, err, "MUNSU_TASK_ID is not set", "ready with no MUNSU_TASK_ID")
}

func TestGuardReportRefusesAnInvalidStatusState(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "t1")

	_, err := runRoot(t, "report", "not-a-state", "hello", "--home", homeDir)
	wantErrContains(t, err, `Invalid status state "not-a-state"`, "report with an unknown state")
}

func TestGuardReportRefusesOutsideAManagedTask(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	t.Setenv("MUNSU_TASK_ID", "t1")
	t.Setenv("MUNSU_HOME", "")
	_, err := runRoot(t, "report", "working", "hello", "--home", homeDir)
	wantErrContains(t, err, "MUNSU_HOME is not set", "report with no MUNSU_HOME")

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "")
	_, err = runRoot(t, "report", "working", "hello", "--home", homeDir)
	wantErrContains(t, err, "MUNSU_TASK_ID is not set", "report with no MUNSU_TASK_ID")
}

func TestGuardReportRefusesACaptainWithNoParentStatusHome(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "captain:c1")
	t.Setenv("MUNSU_ROLE", "captain")
	t.Setenv("MUNSU_PARENT_STATUS", "")

	_, err := runRoot(t, "report", "working", "hello", "--home", homeDir)
	wantErrContains(t, err, "MUNSU_PARENT_STATUS not set for captain role", "report as captain with no MUNSU_PARENT_STATUS")
}

// --- internal/cli/skill_cmd.go ---------------------------------------------

func TestGuardSkillShowDeniesManagementSkillsToASoldier(t *testing.T) {
	t.Setenv("MUNSU_ROLE", "soldier")
	_, err := runRoot(t, "skill", "show", "munsu-ops")
	wantErrContains(t, err, "soldier role cannot inspect management skill", "skill show munsu-ops as a soldier")

	// The same command under any other role reaches the embed, which is what
	// makes the refusal above attributable to the role and not to the name.
	t.Setenv("MUNSU_ROLE", "general")
	if _, err := runRoot(t, "skill", "show", "munsu-ops"); err != nil {
		t.Fatalf("skill show munsu-ops as general = %v, want acceptance", err)
	}
}

// --- internal/cli/skills_validate.go ---------------------------------------

func TestGuardResolveBundleReferenceRefusesAnAbsolutePath(t *testing.T) {
	_, err := resolveBundleReference("skills/demo", "skills/demo/SKILL.md", "/etc/passwd")
	wantErrContains(t, err, "absolute paths are not allowed", "resolveBundleReference with an absolute reference")
	// The escape check below it emits a different message; an absolute path
	// would also escape, so the two must stay distinguishable.
	if strings.Contains(err.Error(), "escapes skill module") {
		t.Fatalf("resolveBundleReference = %v, which is the escape refusal rather than the absolute-path one", err)
	}
	if got, err := resolveBundleReference("skills/demo", "skills/demo/SKILL.md", "REFERENCE.md"); err != nil || got != "skills/demo/REFERENCE.md" {
		t.Fatalf("resolveBundleReference relative = (%q, %v), want it resolved", got, err)
	}
}

// --- internal/cli/selfupdate_update.go -------------------------------------

func TestGuardUpdateInRefusesAGitFileThatIsNotAWorktreePointer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := updateIn(root, filepath.Join(root, "munsu"))
	wantErrContains(t, err, "unexpected .git format in", "updateIn with a malformed .git file")
	// The bare-.git-directory branch above it refuses with a different
	// message; reaching that one would mean the fixture wrote no .git file.
	if strings.Contains(err.Error(), "is not a git repository") {
		t.Fatalf("updateIn = %v, which is the missing-repository refusal rather than the malformed-pointer one", err)
	}
}

// --- the session-bound endpoint adapters -----------------------------------
//
// Each adapter refuses before it touches a backend: an incomplete bound
// identity, or a resolved identity that disagrees with the one the caller
// recorded. The fake resolve function is what makes the disagreement
// constructible; a real backend would refuse for its own reasons first.

type guardBackend struct{ tornDown []string }

func (b *guardBackend) NewWindow(string, string) (string, error) { return "w1", nil }
func (b *guardBackend) SendKeys(string, string) error            { return nil }
func (b *guardBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *guardBackend) Teardown(handle string) error {
	b.tornDown = append(b.tornDown, handle)
	return nil
}

// boundMeta is a complete bound identity for a herdr endpoint; each test
// removes exactly the field whose guard it is entering.
func boundMeta() map[string]string {
	return map[string]string{
		"backend":            "herdr",
		"window":             "sess:p1",
		"herdr_session":      "sess",
		"herdr_workspace_id": "ws1",
		"herdr_tab_id":       "tab1",
	}
}

func TestGuardMailboxSenderRefusesAnIncompleteBoundIdentity(t *testing.T) {
	resolved := 0
	sender := sessionMailboxSender{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		resolved++
		return &guardBackend{}, "herdr", nil
	}}
	for _, tc := range []struct {
		name string
		home string
		meta map[string]string
	}{
		{"no home", "", boundMeta()},
		{"no backend", "/home", map[string]string{"window": "sess:p1"}},
		{"no window", "/home", map[string]string{"backend": "herdr"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sender.backend(tc.home, tc.meta)
			wantErrContains(t, err, "bound sender identity is incomplete", "sender.backend with "+tc.name)
		})
	}
	if resolved != 0 {
		t.Fatalf("resolve was called %d times; the guard must refuse before any backend is resolved", resolved)
	}
	if _, err := sender.backend("/home", boundMeta()); err != nil {
		t.Fatalf("sender.backend with a complete identity = %v, want acceptance", err)
	}
}

func TestGuardSoldierEndpointsRefuseAnIncompleteBoundIdentity(t *testing.T) {
	resolved := 0
	eps := sessionSoldierEndpoints{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		resolved++
		return &guardBackend{}, "herdr", nil
	}}
	for _, tc := range []struct {
		name string
		home string
		meta map[string]string
	}{
		{"no home", "", boundMeta()},
		{"no window", "/home", map[string]string{"backend": "herdr"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eps.backend(tc.home, tc.meta)
			wantErrContains(t, err, "soldier endpoint identity is incomplete", "soldier backend with "+tc.name)
		})
	}
	if resolved != 0 {
		t.Fatalf("resolve was called %d times; the guard must refuse before any backend is resolved", resolved)
	}
	// A soldier endpoint may carry no recorded backend name — that is the
	// difference from the sender above, and it must keep being accepted.
	if _, err := eps.backend("/home", map[string]string{"window": "sess:p1"}); err != nil {
		t.Fatalf("soldier backend with no recorded backend name = %v, want acceptance", err)
	}
}

func TestGuardBoundTeardownRefusesAnIncompleteBoundIdentity(t *testing.T) {
	resolved := 0
	td := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		resolved++
		return &guardBackend{}, "herdr", nil
	}}
	for _, tc := range []struct {
		name string
		home string
		meta map[string]string
	}{
		{"no home", "", boundMeta()},
		{"no backend", "/home", map[string]string{"window": "sess:p1"}},
		{"no window", "/home", map[string]string{"backend": "herdr"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := td.resolveBound(tc.home, tc.meta)
			wantErrContains(t, err, "bound teardown identity is incomplete", "resolveBound with "+tc.name)
		})
	}
	if resolved != 0 {
		t.Fatalf("resolve was called %d times; the guard must refuse before any backend is resolved", resolved)
	}
}

func TestGuardBoundTeardownRefusesABackendOtherThanTheRecordedOne(t *testing.T) {
	td := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &guardBackend{}, "tmux", nil
	}}
	_, _, err := td.resolveBound("/home", boundMeta())
	wantErrContains(t, err, `bound backend resolved as "tmux"`, "resolveBound where the resolver disagrees with meta")
	if strings.Contains(err.Error(), "identity is incomplete") {
		t.Fatalf("resolveBound = %v, which is the incomplete-identity refusal in front of it", err)
	}
}

func TestGuardBoundTeardownRefusesAHerdrSessionItDoesNotOwn(t *testing.T) {
	td := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &guardBackend{}, "herdr", nil
	}}
	meta := boundMeta()
	meta["window"] = "other-session:p1" // the handle names a session the claim does not own
	_, _, err := td.resolveBound("/home", meta)
	wantErrContains(t, err, "herdr session ownership mismatch", "resolveBound with a foreign herdr session in the handle")

	if _, _, err := td.resolveBound("/home", boundMeta()); err != nil {
		t.Fatalf("resolveBound with the owned session = %v, want acceptance", err)
	}
}

func TestGuardDisposeRefusesARequestThatDoesNotMatchTheBoundEndpoint(t *testing.T) {
	bk := &guardBackend{}
	td := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return bk, "herdr", nil
	}}
	match := fleet.DisposeRequest{
		Home: "/home", Backend: "herdr", Handle: "sess:p1",
		SessionOwner: "sess", WorkspaceID: "ws1", TabID: "tab1",
	}
	for _, tc := range []struct {
		name   string
		mutate func(r fleet.DisposeRequest) fleet.DisposeRequest
	}{
		{"a different home", func(r fleet.DisposeRequest) fleet.DisposeRequest { r.Home = "/other"; return r }},
		{"a different backend", func(r fleet.DisposeRequest) fleet.DisposeRequest { r.Backend = "tmux"; return r }},
		{"a different handle", func(r fleet.DisposeRequest) fleet.DisposeRequest { r.Handle = "sess:p9"; return r }},
		{"a different session owner", func(r fleet.DisposeRequest) fleet.DisposeRequest { r.SessionOwner = "other"; return r }},
		{"a different workspace", func(r fleet.DisposeRequest) fleet.DisposeRequest { r.WorkspaceID = "ws9"; return r }},
		{"a different tab", func(r fleet.DisposeRequest) fleet.DisposeRequest { r.TabID = "tab9"; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := td.Dispose("/home", boundMeta(), tc.mutate(match))
			wantErrContains(t, err, "dispose request does not match bound endpoint metadata", "Dispose with "+tc.name)
		})
	}
	if len(bk.tornDown) != 0 {
		t.Fatalf("Teardown ran %v; a mismatched dispose must never reach the backend", bk.tornDown)
	}
	if err := td.Dispose("/home", boundMeta(), match); err != nil {
		t.Fatalf("Dispose with the matching request = %v, want acceptance", err)
	}
	if len(bk.tornDown) != 1 || bk.tornDown[0] != "sess:p1" {
		t.Fatalf("Teardown calls = %v, want exactly the bound handle", bk.tornDown)
	}
}

func TestGuardDisposeRefusesWorkspaceCloseDenialOutsideHerdrWithAWorkspace(t *testing.T) {
	req := fleet.DisposeRequest{
		Home: "/home", Backend: "herdr", Handle: "sess:p1",
		SessionOwner: "sess", WorkspaceID: "ws1", TabID: "tab1",
		DenyWorkspaceClose: true,
	}

	// Denial on a non-herdr backend: the policy has no meaning there.
	tmuxMeta := boundMeta()
	tmuxMeta["backend"] = "tmux"
	tmuxReq := req
	tmuxReq.Backend = "tmux"
	tmux := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &guardBackend{}, "tmux", nil
	}}
	wantErrContains(t, tmux.Dispose("/home", tmuxMeta, tmuxReq), `workspace-close denial is invalid for backend "tmux"`, "Dispose denying workspace close on tmux")

	// Denial on herdr with no workspace to deny.
	noWorkspaceMeta := boundMeta()
	noWorkspaceMeta["herdr_workspace_id"] = ""
	noWorkspaceReq := req
	noWorkspaceReq.WorkspaceID = ""
	herdr := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &guardBackend{}, "herdr", nil
	}}
	wantErrContains(t, herdr.Dispose("/home", noWorkspaceMeta, noWorkspaceReq), `workspace-close denial is invalid for backend "herdr"`, "Dispose denying workspace close with no workspace id")
}

func TestGuardDisposeRefusesWorkspaceClosePolicyOnAnAdapterThatCannotCarryIt(t *testing.T) {
	// name "herdr" with a resolved adapter that is not the herdr adapter: the
	// policy cannot be attached, so disposal fails closed instead of tearing
	// the endpoint down with the policy silently dropped.
	td := sessionBoundTeardown{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &guardBackend{}, "herdr", nil
	}}
	req := fleet.DisposeRequest{
		Home: "/home", Backend: "herdr", Handle: "sess:p1",
		SessionOwner: "sess", WorkspaceID: "ws1", TabID: "tab1",
		DenyWorkspaceClose: true,
	}
	err := td.Dispose("/home", boundMeta(), req)
	wantErrContains(t, err, "herdr workspace-close policy is unsupported by resolved adapter", "Dispose with a herdr policy on a non-herdr adapter")
	if strings.Contains(err.Error(), "denial is invalid for backend") {
		t.Fatalf("Dispose = %v, which is the denial-validity refusal in front of it", err)
	}
}

func TestGuardCaptainLaunchEndpointRequiresAnExplicitBackendIdentity(t *testing.T) {
	resolved := 0
	ep := sessionLaunchEndpoint{resolve: func(string, string) (backend.Backend, string, error) {
		resolved++
		return &guardBackend{}, "tmux", nil
	}}
	_, err := ep.Launch("/home", fleet.LaunchRequest{})
	wantErrContains(t, err, "captain launch requires an explicit backend identity", "Launch with no backend identity")
	if resolved != 0 {
		t.Fatalf("resolve was called %d times; the guard must refuse before resolution", resolved)
	}
}

func TestGuardCaptainCleanupRequiresTheBoundBackendIdentity(t *testing.T) {
	resolved := 0
	ep := sessionLaunchEndpoint{resolve: func(string, string) (backend.Backend, string, error) {
		resolved++
		return &guardBackend{}, "tmux", nil
	}}
	err := ep.Cleanup("/home", fleet.LaunchResult{Window: "w1"})
	wantErrContains(t, err, "captain cleanup requires the bound backend identity", "Cleanup with no backend identity in the launch result")
	if resolved != 0 {
		t.Fatalf("resolve was called %d times; the guard must refuse before resolution", resolved)
	}

	bk := &guardBackend{}
	ok := sessionLaunchEndpoint{resolve: func(string, string) (backend.Backend, string, error) { return bk, "tmux", nil }}
	if err := ok.Cleanup("/home", fleet.LaunchResult{Backend: "tmux", Window: "w1"}); err != nil {
		t.Fatalf("Cleanup with the bound identity = %v, want acceptance", err)
	}
	if len(bk.tornDown) != 1 || bk.tornDown[0] != "w1" {
		t.Fatalf("Teardown calls = %v, want exactly the launched window", bk.tornDown)
	}
}

func TestGuardSpawnEndpointsRefuseAnUnboundEndpoint(t *testing.T) {
	eps := &spawnSessionEndpoints{bound: map[string]backend.Backend{}}
	bound := fleet.CreatedEndpoint{Backend: "tmux", Handle: "w1"}
	eps.bound[spawnEndpointKey(bound)] = &guardBackend{}

	_, err := eps.backend(fleet.CreatedEndpoint{Backend: "tmux", Handle: "w2"})
	wantErrContains(t, err, `endpoint "w2" on backend "tmux" is not bound`, "backend for an endpoint this session never created")

	if _, err := eps.backend(bound); err != nil {
		t.Fatalf("backend for the bound endpoint = %v, want acceptance", err)
	}
}

// --- internal/cli/captain_activation.go ------------------------------------

func TestGuardEnsureCaptainReadyRefusesADeadEndpointWithNoRecoveryConfigured(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	if err := mhome.WriteMeta(parent, "captain:test", map[string]string{"kind": "captain", "home": captainHome}); err != nil {
		t.Fatal(err)
	}
	probe := &sequenceCaptainProbe{results: []fleet.CaptainProbeResult{{PaneAlive: false, AgentAlive: false}}}
	err := ensureCaptainReadyWithWait(parent, fleet.Info{ID: "test", Home: captainHome}, probe, nil, 1, func() {})
	wantErrContains(t, err, "captain endpoint unavailable", "ensureCaptainReady with a dead endpoint and no recovery function")
	// The live-but-not-ready refusal above it is a different sentence; reading
	// it here would mean the probe fixture never made the endpoint dead.
	if strings.Contains(err.Error(), "alive but not ready") {
		t.Fatalf("ensureCaptainReadyWithWait = %v, which is the not-ready refusal rather than the no-recovery one", err)
	}
}

// --- internal/cli/config_cmd.go, config_dispatch_cmd.go --------------------

func TestGuardConfigSetCaptainHarnessRefusesALineWithNoHarnessToken(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	// "default <model>" parses to an empty harness (the unset sentinel takes
	// the whole first field) while still carrying text, which is the only
	// shape that reaches this branch: an empty value and a bare "default" are
	// excluded by the enclosing condition, and a "#" line is excluded here.
	_, err := runRoot(t, "config", "set", "captain-harness", "default claude", "--home", homeDir)
	wantErrContains(t, err, "empty harness token in", "config set captain-harness with a sentinel-only line")

	if _, err := runRoot(t, "config", "set", "captain-harness", "# commented out", "--home", homeDir); err != nil {
		t.Fatalf("config set captain-harness with a comment line = %v, want acceptance: the comment is what this guard exempts", err)
	}
}

func TestGuardConfigDispatchAddRequiresItsIdentifyingFlags(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no --name", []string{"--harness", "claude", "--match", "*"}, "add: --name is required"},
		{"no --harness", []string{"--name", "p1", "--match", "*"}, "add: --harness is required"},
		{"neither --match nor --when", []string{"--name", "p1", "--harness", "claude"}, "add: at least one --match or --when is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runRoot(t, append([]string{"config", "dispatch", "add", "--home", homeDir}, tc.args...)...)
			wantErrContains(t, err, tc.want, "config dispatch add with "+tc.name)
		})
	}
}

func TestGuardConfigDispatchAddRefusesToOverwriteWithoutReplace(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	add := []string{"config", "dispatch", "add", "--name", "p1", "--harness", "claude", "--match", "*", "--home", homeDir}
	if _, err := runRoot(t, add...); err != nil {
		t.Fatalf("first add: %v", err)
	}
	_, err := runRoot(t, add...)
	wantErrContains(t, err, `add: profile "p1" already exists (pass --replace to overwrite)`, "config dispatch add for an existing name")

	if _, err := runRoot(t, append(add, "--replace")...); err != nil {
		t.Fatalf("config dispatch add --replace = %v, want acceptance: --replace is what the refusal above points at", err)
	}
}

func TestGuardConfigDispatchRmRefusesAProfileThatIsNotThere(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if _, err := runRoot(t, "config", "dispatch", "add", "--name", "p1", "--harness", "claude", "--match", "*", "--home", homeDir); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	_, err := runRoot(t, "config", "dispatch", "rm", "p2", "--home", homeDir)
	wantErrContains(t, err, `rm: profile "p2" not found`, "config dispatch rm for an absent name")

	if _, err := runRoot(t, "config", "dispatch", "rm", "p1", "--home", homeDir); err != nil {
		t.Fatalf("config dispatch rm p1 = %v, want acceptance", err)
	}
}

// --- internal/cli/decisionhold_cmd.go --------------------------------------

func TestGuardDecisionHoldHoldRequiresAReasonAndAnOrigin(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	_, err := runRoot(t, "decision-hold", "hold", "approach", "--from", "scout-r2", "--home", homeDir)
	wantErrContains(t, err, "--reason is required", "decision-hold hold with no --reason")

	_, err = runRoot(t, "decision-hold", "hold", "approach", "--reason", "pick one", "--home", homeDir)
	wantErrContains(t, err, "--from is required", "decision-hold hold with no --from")
}

func TestGuardDecisionHoldResolveRequiresAnAnswerAndAnOrigin(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	_, err := runRoot(t, "decision-hold", "resolve", "approach", "--from", "scout-r2", "--home", homeDir)
	wantErrContains(t, err, "--answer is required", "decision-hold resolve with no --answer")

	_, err = runRoot(t, "decision-hold", "resolve", "approach", "--answer", "React", "--home", homeDir)
	wantErrContains(t, err, "--from is required", "decision-hold resolve with no --from")
}

func TestGuardDecisionHoldCompleteRefusesAnAttestationThatNamesKeys(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	_, err := runRoot(t, "decision-hold", "complete", "scout-r2", "approach", "--none", "--home", homeDir)
	wantErrContains(t, err, "--none cannot be combined with explicit keys", "decision-hold complete --none with a key")

	_, err = runRoot(t, "decision-hold", "complete", "scout-r2", "--home", homeDir)
	wantErrContains(t, err, "specify at least one key or --none", "decision-hold complete with neither keys nor --none")

	if _, err := runRoot(t, "decision-hold", "complete", "scout-r2", "--none", "--home", homeDir); err != nil {
		t.Fatalf("decision-hold complete --none = %v, want acceptance", err)
	}
}

// --- internal/cli/spawn_cmd.go: send, peek, promote ------------------------

func TestGuardSendRefusesUplinkToTheGeneral(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "send", "general", "status?", "--home", homeDir)
	wantErrContains(t, err, "uplink use munsu report; send is downlink only", "send addressed to general")
}

func TestGuardSendRefusesACaptainWithNoHomeRecorded(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if err := mhome.WriteMeta(homeDir, "captain:c1", map[string]string{"kind": "captain"}); err != nil {
		t.Fatal(err)
	}
	// send refuses gate agents before it reads meta. Run it from the temp
	// home (not a git checkout), so the metadata guard is the next refusal.
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
	_, err = runRoot(t, "send", "captain:c1", "do the thing", "--home", homeDir)
	wantErrContains(t, err, "has no home in meta", "send to a captain whose meta records no home")
}

func TestGuardPeekRefusesATaskWithNoWindowEndpoint(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if err := mhome.WriteMeta(homeDir, "t1", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "peek", "t1", "--home", homeDir)
	wantErrContains(t, err, "task t1 has no window endpoint", "peek at a task with no window in meta")
	// The read above it fails with its own wrapper when the task is unknown.
	if strings.Contains(err.Error(), "reading task") {
		t.Fatalf("peek = %v, which is the meta-read failure rather than the missing-window refusal", err)
	}
}

// newPeekCmdWithCapture takes the capture port as a parameter, so the
// unconfigured-port branch is entered by composing the command with a nil
// port — the state the guard exists for.
func TestGuardPeekRefusesWhenNoCapturePortIsConfigured(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if err := mhome.WriteMeta(homeDir, "t1", map[string]string{"kind": "ship", "window": "sess:p1"}); err != nil {
		t.Fatal(err)
	}
	// The registered `peek` always carries the real capture port, so the
	// command is composed here with a nil port instead — that composition is
	// the only state this guard exists for.
	cmd := newPeekCmdWithCapture(nil)
	cmd.SetArgs([]string{"t1"})
	previous := homeOverride
	homeOverride = homeDir
	t.Cleanup(func() { homeOverride = previous })
	err := cmd.Execute()
	wantErrContains(t, err, "task-bound capture is not configured", "peek composed with no capture port")
	if strings.Contains(err.Error(), "has no window endpoint") {
		t.Fatalf("peek = %v, which is the missing-window refusal in front of it", err)
	}
	// The other half, the one that keeps this a seam-only precondition: the
	// registered command must still carry a real port. If it ever composes
	// with nil, the branch above stops being unreachable from the CLI.
	if _, err := runRoot(t, "peek", "t1", "--home", homeDir); err != nil &&
		strings.Contains(err.Error(), "task-bound capture is not configured") {
		t.Fatal("the registered peek reached the nil-capture refusal, so it is no longer composed with a real port")
	}
}

func TestGuardPromoteRefusesATaskThatIsNotAScout(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "t1", "ship")
	_, err := runRoot(t, "promote", "t1", "--home", homeDir)
	wantErrContains(t, err, `task t1 has kind="ship", can only promote kind=scout`, "promote of a ship task")
}

// --- internal/cli/session_cmd.go -------------------------------------------

func TestBriefRecoveryPrecedesTaskFence(t *testing.T) {
	oldRecover, oldWrite := recoverBriefHandoffs, writeBriefArtifact
	order := []string{}
	recoverBriefHandoffs = func(string) error { order = append(order, "recover"); return nil }
	writeBriefArtifact = func(auth *taskauthority.Canonical, id string, write func() error) error {
		order = append(order, "write")
		return nil
	}
	t.Cleanup(func() { recoverBriefHandoffs, writeBriefArtifact = oldRecover, oldWrite })
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "ordered", "ship")
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.ProjectOverlay{Backend: "tmux"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "project", "add", "demo-repo", t.TempDir(), "--home", homeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "brief", "ordered", "demo-repo", "--home", homeDir); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "recover,write" {
		t.Fatalf("order = %v", order)
	}
}

func TestBriefWritesKnownLegacyAndForcedTasks(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "known", "ship")
	oldWrite := writeBriefArtifact
	called := false
	writeBriefArtifact = func(auth *taskauthority.Canonical, id string, write func() error) error {
		called = true
		return auth.WriteTaskDataArtifactByID(id, write)
	}
	t.Cleanup(func() { writeBriefArtifact = oldWrite })
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.ProjectOverlay{Backend: "tmux"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "project", "add", "demo-repo", t.TempDir(), "--home", homeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "brief", "known", "demo-repo", "--home", homeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "data", "known", "brief.md")); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("write fence was not invoked")
	}
	for _, force := range []bool{false, true} {
		id := "unknown"
		if force {
			id = "forced"
		}
		args := []string{"brief", id, "demo-repo", "--home", homeDir}
		if force {
			args = append(args, "--force")
		} else if err := os.WriteFile(filepath.Join(homeDir, "state", id+".meta"), []byte("kind=ship\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := runRoot(t, args...); err != nil {
			t.Fatalf("brief force=%v: %v", force, err)
		}
		if _, err := os.Stat(filepath.Join(homeDir, "data", id, "brief.md")); err != nil {
			t.Fatalf("brief missing force=%v: %v", force, err)
		}
	}
}

func TestSessionStartGCUsesRawIDOwnershipForForcedBriefs(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.ProjectOverlay{Backend: "tmux"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "project", "add", "demo-repo", t.TempDir(), "--home", homeDir); err != nil {
		t.Fatal(err)
	}
	id := "forced-unknown"
	if _, err := runRoot(t, "brief", id, "demo-repo", "--force", "--home", homeDir); err != nil {
		t.Fatalf("forced brief: %v", err)
	}
	briefDir := filepath.Join(homeDir, "data", id)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(briefDir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.RunSessionStartWithWatcher(io.Discard, homeDir, nil, nil, taskDataDirReclaimer(homeDir)); err != nil {
		t.Fatalf("session-start: %v", err)
	}
	// Session-start holds the session lock (state/.lock) for the process
	// lifetime by design (internal/home/watcher_lock.go); on Windows the open
	// handle pins the home directory and t.TempDir teardown cannot remove it
	// (#549). This test process outlives the session, so release the lock on
	// the path the test actually takes.
	if err := mhome.ReleaseSessionLock(homeDir); err != nil {
		t.Fatalf("release session lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(briefDir, "brief.md")); err != nil {
		t.Fatalf("forced brief was reclaimed: %v", err)
	}

	captainHome := t.TempDir()
	initCLITestHome(t, captainHome)
	captainStem, err := mhome.DurableKey("captain:c1")
	if err != nil {
		t.Fatal(err)
	}
	captainDir := filepath.Join(captainHome, "data", captainStem)
	if err := os.MkdirAll(captainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(captainDir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.RunSessionStartWithWatcher(io.Discard, captainHome, nil, nil, taskDataDirReclaimer(captainHome)); err != nil {
		t.Fatalf("captain cleanup session-start: %v", err)
	}
	if err := mhome.ReleaseSessionLock(captainHome); err != nil {
		t.Fatalf("release captain session lock: %v", err)
	}
	if _, err := os.Stat(captainDir); !os.IsNotExist(err) {
		t.Fatalf("empty captain directory remains, stat err: %v", err)
	}
}

func TestGuardBriefRefusesATaskThatIsNotAScout(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "t1", "ship")
	// The delivery-mode resolution in front of the kind check reads the typed
	// base document and the project registry, and fails closed without both.
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "project", "add", "demo-repo", t.TempDir(), "--home", homeDir); err != nil {
		t.Fatalf("seeding the project registry: %v", err)
	}
	_, err := runRoot(t, "brief", "t1", "demo-repo", "--scout", "--home", homeDir)
	wantErrContains(t, err, `task "t1" is not a scout`, "brief --scout for a ship task")
}

func TestGuardAfkCheckRefusesWhileActionableStateRemains(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	seedGuardActionableWake(t, homeDir)

	_, err := runRoot(t, "afk", "return", "check", "--home", homeDir)
	wantErrContains(t, err, "actionable AFK state remains", "afk return check with an unresolved actionable wake")
}

func TestGuardAfkDrainRequiresAConsumer(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "afk", "drain", "--home", homeDir)
	wantErrContains(t, err, "--consumer is required", "afk drain with no --consumer")

	if _, err := runRoot(t, "afk", "drain", "--consumer", "general", "--home", homeDir); err != nil {
		t.Fatalf("afk drain --consumer general = %v, want acceptance", err)
	}
}

func TestGuardWatchArmRefusesWhenTheWatcherStartFails(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	previous := startWatcherProcess
	startWatcherProcess = func(string) (int, error) { return 0, errors.New("no watcher binary") }
	t.Cleanup(func() { startWatcherProcess = previous })

	out, err := runRoot(t, "watch-arm", "--home", homeDir)
	wantErrContains(t, err, "watch-arm failed: failed", "watch-arm after a failed watcher start")
	if strings.Contains(out, "Watcher armed") {
		t.Fatalf("watch-arm output = %q, still prints the armed message for a watcher that never started", out)
	}
}

// --- internal/cli/task_cmd.go: cleanup-abort -------------------------------

func TestGuardTaskCleanupAbortRefusesATaskWithNoCleanupClaim(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "t1", "ship")
	_, err := runRoot(t, "task", "cleanup-abort", "t1", "--home", homeDir)
	wantErrContains(t, err, "task t1 has no cleanup claim to abort", "task cleanup-abort on a live task")
}

// --- internal/cli/task_authority_reads.go ----------------------------------

func TestGuardResolveCurrentTaskIDRefusesAnIDTwoHomesClaim(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "t1", "ship")

	captainHome := filepath.Join(homeDir, "captains", "c1")
	captainAuth := testAuthorityFor(t, captainHome)
	seedGuardTask(t, captainAuth, "t1", "ship")

	_, err := resolveCurrentTaskID(homeDir, "t1")
	wantErrContains(t, err, "t1", "resolveCurrentTaskID with two canonical owners")
	var ambiguous *mhome.AmbiguousTaskIDError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("resolveCurrentTaskID = %v (%T), want the typed AmbiguousTaskIDError the callers render correction commands from", err, err)
	}
	if len(ambiguous.Matches) != 2 {
		t.Fatalf("ambiguous matches = %v, want both owning homes", ambiguous.Matches)
	}

	if got, err := resolveCurrentTaskID(captainHome, "t1"); err != nil || got != "t1" {
		t.Fatalf("resolveCurrentTaskID against a single owner = (%q, %v), want it resolved", got, err)
	}
}

func TestGuardTaskCleanupAbortRefusesAnAlreadyCompletedCleanupClaim(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "t1", "ship")
	seedGuardCompletedCleanupClaim(t, auth, "t1")

	_, err := runRoot(t, "task", "cleanup-abort", "t1", "--home", homeDir)
	wantErrContains(t, err, "is already completed; nothing to abort", "task cleanup-abort on a completed cleanup claim")
	// The nil-claim refusal stands immediately in front of this one and would
	// be green for the same command against a task that was never retired.
	if strings.Contains(err.Error(), "no cleanup claim to abort") {
		t.Fatalf("task cleanup-abort = %v, which is the nil-claim refusal in front of it", err)
	}
}

// --- internal/cli/captain_cmd.go -------------------------------------------

func TestGuardCaptainUpdateRefusesAFailingOutcome(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	// A directory carrying no captain marker fails provenance, and
	// invalid-provenance is one of the outcomes IsFailure() classifies as
	// failed — the only states that reach this refusal.
	notACaptain := filepath.Join(t.TempDir(), "not-a-captain")
	if err := os.MkdirAll(notACaptain, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "captain", "update", notACaptain, "--home", homeDir)
	wantErrContains(t, err, "update failed: invalid-provenance", "captain update of a directory that is not a captain home")
}

func TestGuardCaptainRecoverRefusesAnIDTheRegistryDoesNotCarry(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	_, err := runRoot(t, "captain", "recover", "nope", "--home", homeDir)
	wantErrContains(t, err, `no registered captain with id "nope"`, "captain recover of an unregistered id")
	// The registry read in front of it fails with its own wrapper.
	if strings.Contains(err.Error(), "listing registered captains") {
		t.Fatalf("captain recover = %v, which is the registry-read failure in front of the refusal", err)
	}
}

// --- internal/cli/delivery_cmd.go ------------------------------------------

// buildDeliverRequest reads the provider through the package-level
// FetchProviderSnapshot seam, so the test stubs it: the guard under test sits
// after that read and would otherwise never be reached without network.
func TestGuardBuildDeliverRequestRefusesATaskWithNoBoundWorktree(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedGuardTask(t, auth, "t1", "ship")

	previous := fleet.FetchProviderSnapshot
	fleet.FetchProviderSnapshot = func(prURL string) (*fleet.ProviderSnapshot, error) {
		return &fleet.ProviderSnapshot{
			Provider: "github", Owner: "o", Repo: "r", Number: 1, URL: prURL,
			BaseRef: "main", HeadRef: "feature", HeadSHA: "abc", State: "OPEN",
			Checks:     []domain.CheckRun{{Status: domain.CheckPassed}},
			Reviews:    []domain.Review{{State: domain.ReviewApproved}},
			ObservedAt: "2026-01-01T00:00:00Z",
		}, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = previous })

	_, err := buildDeliverRequest(auth, "t1", "https://github.com/o/r/pull/1", nil)
	wantErrContains(t, err, "has no bound worktree; spawn it before delivery", "buildDeliverRequest for a task with no worktree binding")
	// Three reads fail in front of this guard, each with its own wrapper.
	for _, earlier := range []string{"capturing delivery identity", "resolving task"} {
		if strings.Contains(err.Error(), earlier) {
			t.Fatalf("buildDeliverRequest = %v, which is the %q failure in front of the refusal", err, earlier)
		}
	}
}

// --- internal/cli/doctor_cmd.go: the two os.Exit refusals ------------------
//
// Both branches call os.Exit, so entering them in the test process takes the
// run down with it. They are entered from a re-executed child instead, which
// inherits the lane's coverage directory — so the branches are measured, not
// waived, and the assertion is on the child's exit code and report.

func TestGuardDoctorExitsNonZeroWhenAHardRequiredToolIsMissing(t *testing.T) {
	if os.Getenv("MUNSU_GUARD_CHILD") == "doctor-exit" {
		homeDir := os.Getenv("MUNSU_GUARD_CHILD_HOME")
		// Reached only in the child: this call does not return.
		_, _ = runRoot(t, "doctor", "--home", homeDir)
		guardChildReturned()
	}

	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	// An empty PATH makes every hard-required tool missing, which is the only
	// state that sets the non-zero exit code.
	code, output := runGuardChild(t, "TestGuardDoctorExitsNonZeroWhenAHardRequiredToolIsMissing", "doctor-exit", homeDir, "PATH=")
	if code != 1 {
		t.Fatalf("doctor with an empty PATH exited %d, want the 1 the refusal exits with\n%s", code, output)
	}
	if strings.Contains(output, "GUARD-CHILD-RETURNED") {
		t.Fatalf("doctor returned instead of exiting, so the refusal under test was never entered\n%s", output)
	}
	if !strings.Contains(output, "Some required tools are missing.") {
		t.Fatalf("doctor output = %q, want the missing-tools report that sets the exit code", output)
	}
}

func TestGuardCheckInstructionsExitsNonZeroOnADocCodeMismatch(t *testing.T) {
	if os.Getenv("MUNSU_GUARD_CHILD") == "check-instructions" {
		homeDir := os.Getenv("MUNSU_GUARD_CHILD_HOME")
		_, _ = runRoot(t, "doctor", "--check-instructions", "--home", homeDir)
		guardChildReturned()
	}

	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	agents := "Run `munsu no-such-command --no-such-flag` to do the thing.\n"
	if err := os.WriteFile(filepath.Join(homeDir, "AGENTS.md"), []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	code, output := runGuardChild(t, "TestGuardCheckInstructionsExitsNonZeroOnADocCodeMismatch", "check-instructions", homeDir)
	if code != 1 {
		t.Fatalf("doctor --check-instructions over a doc naming an unreal command exited %d, want the 1 the refusal exits with\n%s", code, output)
	}
	if strings.Contains(output, "GUARD-CHILD-RETURNED") {
		t.Fatalf("check-instructions returned instead of exiting, so the refusal under test was never entered\n%s", output)
	}
	if !strings.Contains(output, "doc-code mismatches") {
		t.Fatalf("check-instructions output = %q, want the mismatch report that sets the exit code", output)
	}
}

// guardChildReturned ends a child whose refusal branch did NOT fire. The exit
// code has to differ from the 1 the branch itself exits with — a t.Fatal here
// would exit 1 too, and the parent's assertion would then be green whether or
// not the guard ran, which is the tautology this batch bans.
const guardChildNoExitCode = 97

func guardChildReturned() {
	fmt.Println("GUARD-CHILD-RETURNED: the refusal branch did not fire")
	os.Exit(guardChildNoExitCode)
}

// runGuardChild re-executes this test binary for one named test with the child
// marker set, so a branch that calls os.Exit can be entered without taking the
// parent test process down with it. The child inherits the coverage directory
// of the lane that spawned it, so the branch it enters is measured there.
func runGuardChild(t *testing.T, testName, marker, homeDir string, extraEnv ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^"+testName+"$", "-test.v")
	cmd.Env = append(os.Environ(),
		"MUNSU_GUARD_CHILD="+marker,
		"MUNSU_GUARD_CHILD_HOME="+homeDir,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running the guard child: %v\n%s", err, output)
	}
	return exitErr.ExitCode(), string(output)
}

// --- fixtures --------------------------------------------------------------

// seedGuardTask commits one canonical task of the given kind, which is the
// state the kind-checking guards read.
func seedGuardTask(t *testing.T, auth *taskauthority.Canonical, taskID, kind string) {
	t.Helper()
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      tid,
		Owner:       "owner",
		Description: "guard fixture",
		Kind:        kind,
		Reason:      "test",
	}
	if kind == "scout" {
		req.ScoutScope = "investigate"
		req.ScoutRuntimeBudgetSecs = 300
	}
	if _, err := auth.Create(mustCanonicalOp(t, "op-guard-create-"+taskID+"-"+kind, req), req); err != nil {
		t.Fatal(err)
	}
}

// seedGuardCompletedCleanupClaim retires the task and reconciles its durable
// cleanup claim to completed — the terminal claim state `cleanup-abort`
// refuses. The claim identity is the retirement Operation identity, so the
// retirement is committed under exactly the id the cleanup continuation
// carries.
func seedGuardCompletedCleanupClaim(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	claimOp := fmt.Sprintf("task-retire-%s-%s", taskID, agg.Generation)
	retire := taskauthority.CanonicalRetireRequest{
		HomeID:       auth.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "guard fixture",
	}
	if _, err := auth.Retire(mustCanonicalOp(t, claimOp, retire), retire); err != nil {
		t.Fatal(err)
	}

	agg, err = auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteCleanupRequest{
		HomeID:           auth.HomeID(),
		TaskID:           tid,
		Precondition:     domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		ClaimOperationID: claimOp,
		ClaimGeneration:  agg.Generation,
		Reason:           "guard fixture",
	}
	if _, err := auth.CompleteCleanup(mustCanonicalOp(t, "op-guard-complete-cleanup-"+taskID, complete), complete); err != nil {
		t.Fatal(err)
	}

	agg, err = auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupCompleted {
		t.Fatalf("cleanup claim = %+v, want a completed claim or the refusal under test is never reached", agg.CleanupClaim)
	}
}

// seedGuardActionableWake writes the durable AFK digest with one non-routine
// entry, which is exactly what orchestrator.IsClean refuses to call clean.
func seedGuardActionableWake(t *testing.T, homeDir string) {
	t.Helper()
	digest := orchestrator.BatchedEscalation{
		Entries: []orchestrator.BatchedEntry{{
			Kind: "afk", Key: "t1", Payload: "build failed", Type: orchestrator.EscalationFailure, At: time.Unix(1, 0),
		}},
		EscalatedCount: 1,
	}
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(homeDir, "state", ".afk-digest")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	if orchestrator.IsClean(homeDir) {
		t.Fatal("the digest fixture reads as clean, so the refusal under test would never be reached")
	}
}
