package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
)

// A munsu in a fenced captain sends a seat command to its seat and runs every
// other command itself; outside a captain it runs everything itself.
func TestRunCaptainSeatRequest(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	absent := filepath.Join(t.TempDir(), "seat.sock")
	for _, tt := range []struct {
		name, seat string
		args       []string
		wantOK     bool
		wantErr    string
	}{
		{"outside a captain", "", []string{"spawn", "T-1"}, false, ""},
		{"a command the fence runs", absent, []string{"config", "get", "backend"}, false, ""},
		{"a seat command", absent, []string{"spawn", "T-1"}, true, "munsu: captain seat:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(fleet.CaptainSeatEnv, tt.seat)
			var stderr bytes.Buffer
			code, ok := RunCaptainSeatRequest(tt.args, &stderr)
			if ok != tt.wantOK || (ok && code != 1) || !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("RunCaptainSeatRequest = %d, %v, stderr %q", code, ok, stderr.String())
			}
		})
	}
}
