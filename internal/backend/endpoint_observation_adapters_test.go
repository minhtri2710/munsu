package backend

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestRuntimeAdapterObservationContract(t *testing.T) {
	fakeBin := t.TempDir()
	writeObservationFakeTmux(t, fakeBin)
	writeObservationFakeHerdr(t, fakeBin)
	testutil.PrependPath(t, fakeBin)

	tests := []struct {
		name   string
		bk     Backend
		handle string
		want   EndpointObservationState
	}{
		{"tmux alive", &TmuxBackend{}, "alive", EndpointAlive},
		{"tmux authoritative absent", &TmuxBackend{}, "dead", EndpointDead},
		{"tmux operational failure", &TmuxBackend{}, "fail", EndpointUnresponsive},
		{"herdr alive agent", NewHerdrBackend("test"), "alive", EndpointAlive},
		{"herdr pane without agent is starting", NewHerdrBackend("test"), "starting", EndpointStarting},
		{"herdr authoritative absent", NewHerdrBackend("test"), "dead", EndpointDead},
		{"herdr operational failure", NewHerdrBackend("test"), "fail", EndpointUnresponsive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObserveEndpoint(tt.bk, tt.handle)
			if got.State() != tt.want {
				t.Fatalf("state=%v detail=%q want %v", got.State(), got.Detail, tt.want)
			}
			if (tt.want == EndpointUnresponsive) && got.State() == EndpointDead {
				t.Fatal("operational failure must never be dead")
			}
		})
	}
}

// TestListAdapterObservationContract extends the contract above to the three
// JSON-list adapters. tmux and herdr are covered by name in
// TestRuntimeAdapterObservationContract; zellij, cmux and orca reach
// ObserveEndpoint through their own probes, so they need their own fakes to
// show the same two propositions hold end-to-end: an authoritative EMPTY result
// is exact absence, and an operational failure is never read as absence
// (BEO-16 mandatory contract). It also pins that a raw adapter observation
// concludes no freshness of its own.
func TestListAdapterObservationContract(t *testing.T) {
	cases := []struct {
		name   string
		binDir string
		mk     func() Backend
		handle string
		want   LifecycleState
	}{
		{"zellij authoritative absent", writeObservationFakeEmptyList(t, "zellij", "[]"), func() Backend { return NewZellijBackend("s") }, "s:terminal_1", LifecycleDead},
		{"zellij operational failure", writeObservationFakeFailing(t, "zellij", "connection refused"), func() Backend { return NewZellijBackend("s") }, "s:terminal_1", LifecycleUnknown},
		{"cmux authoritative absent", writeObservationFakeEmptyList(t, "cmux", `{"result":{"workspaces":[]}}`), func() Backend { return newCmuxBackend() }, "ws|surf", LifecycleDead},
		{"cmux operational failure", writeObservationFakeFailing(t, "cmux", "connection refused"), func() Backend { return newCmuxBackend() }, "ws|surf", LifecycleUnknown},
		{"orca authoritative absent", writeObservationFakeEmptyList(t, "orca", `{"terminals":[]}`), func() Backend { return NewOrcaBackend() }, "ctr:term", LifecycleDead},
		{"orca operational failure", writeObservationFakeFailing(t, "orca", "connection refused"), func() Backend { return NewOrcaBackend() }, "ctr:term", LifecycleUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.PrependPath(t, tc.binDir)
			obs := ObserveEndpoint(tc.mk(), tc.handle)
			if obs.Lifecycle != tc.want {
				t.Fatalf("lifecycle = %v (state=%v) want %v (detail=%q)", obs.Lifecycle, obs.State(), tc.want, obs.Detail)
			}
			// An adapter probe never concludes freshness: it is always
			// FreshnessUnknown and therefore never Absent()/Live() on its own.
			if obs.Freshness != FreshnessUnknown || obs.Incarnation != "" {
				t.Fatalf("adapter observation must be fresh-unknown/empty-incarnation: %+v", obs)
			}
			if obs.Absent() || obs.Live() {
				t.Fatalf("raw adapter observation must not be Live/Absent: %+v", obs)
			}
		})
	}
}

// writeObservationFakeEmptyList writes a JSON-list backend (zellij/cmux/orca)
// that returns an EMPTY authoritative result (exact absence) into a fresh dir,
// and returns that dir to prepend to PATH.
func writeObservationFakeEmptyList(t *testing.T, name, emptyJSON string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + "echo '" + emptyJSON + "'\n" + "exit 0\n"
	testutil.WriteFakeExecutable(t, filepath.Join(dir, name), script)
	return dir
}

// writeObservationFakeFailing writes a backend binary that fails the way an
// unreachable server does — a message on stderr and a non-zero exit — into a
// fresh dir, and returns that dir to prepend to PATH.
func writeObservationFakeFailing(t *testing.T, name, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + fmt.Sprintf("echo %q >&2\n", stderr) + "exit 1\n"
	testutil.WriteFakeExecutable(t, filepath.Join(dir, name), script)
	return dir
}

func writeObservationFakeTmux(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
if [ "$1" = "list-panes" ]; then
  case "$3" in
    alive) echo "$3: 1" ; exit 0 ;;
    dead) echo "can't find window: $3" >&2; exit 1 ;;
    fail) echo "permission denied" >&2; exit 1 ;;
  esac
fi
exit 0
`
	testutil.WriteFakeExecutable(t, path, script)
}

func writeObservationFakeHerdr(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "herdr")
	script := `#!/bin/sh
if [ "$1" = "--session" ]; then
  shift 2
fi
if [ "$1" = "pane" ] && [ "$2" = "get" ]; then
  case "$3" in
    alive|starting) echo '{"result":{"pane_id":"'$3'"}}'; exit 0 ;;
    dead) echo '{"error":{"code":"pane_not_found","message":"missing"}}' >&2; exit 1 ;;
    fail) echo 'permission denied' >&2; exit 1 ;;
  esac
fi
if [ "$1" = "agent" ] && [ "$2" = "get" ]; then
  case "$3" in
    alive) echo '{"result":{"agent":{"agent_status":"working"}}}'; exit 0 ;;
    starting) echo '{"error":{"code":"agent_not_found","message":"not registered"}}'; exit 1 ;;
  esac
fi
exit 1
`
	testutil.WriteFakeExecutable(t, path, script)
}
