package fleet

import (
	"strings"
	"testing"
)

// The seat runs the charter's record and pane commands and refuses the rest,
// and any request that names another home: each refusal with its nearest
// allowed request.
func TestCaptainSeatRequestRefusals(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"a charter command", []string{"spawn", "T-1", "proj"}, ""},
		{"a harness hook", []string{"session-start", "--output", "json"}, ""},
		{"a configuration command", []string{"config", "set", "backend", "tmux"}, "runs only inside the captain's fence"},
		{"the git shim's guard", []string{"git-guard", "-c", "core.pager=sh", "log"}, "runs only inside the captain's fence"},
		{"a global flag before the command", []string{"--home", "/g", "task", "list"}, "runs only inside the captain's fence"},
		{"no command", nil, "runs only inside the captain's fence"},
		{"another home", []string{"task", "done", "T-1", "--home", "/g"}, "--home is refused"},
		{"another home, one argument", []string{"task", "done", "T-1", "--home=/g"}, "--home is refused"},
		{"a message that mentions --home", []string{"send", "T-1", "use --home with care"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := captainSeatRequestError(tt.args)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("refused %q: %v", tt.args, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCaptainSeatMetaRecordsTheFence(t *testing.T) {
	if got := (captainSeat{reason: "fence: no write fence implemented for GOOS linux"}).meta(); got["fence"] != "unfenced" || got["fence_reason"] == "" {
		t.Fatalf("unfenced meta = %v", got)
	}
}
