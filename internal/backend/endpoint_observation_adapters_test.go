package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeAdapterObservationContract(t *testing.T) {
	fakeBin := t.TempDir()
	writeObservationFakeTmux(t, fakeBin)
	writeObservationFakeHerdr(t, fakeBin)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}
