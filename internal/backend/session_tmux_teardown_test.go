package backend

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestTmuxTeardownSettlesOnlyAbsence: a kill that fails because the target is
// already gone is done; every other failure (server, socket, permission,
// timeout) is returned and never read as a finished teardown.
func TestTmuxTeardownSettlesOnlyAbsence(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "tmux"), `#!/bin/sh
[ "$1" = "kill-window" ] || exit 0
case "$3" in
  killed) exit 0 ;;
  gone) echo "can't find window: $3" >&2; exit 1 ;;
  noserver) echo "no server running on /tmp/tmux-1/default" >&2; exit 1 ;;
  denied) echo "permission denied" >&2; exit 1 ;;
  hung) echo "can't find window: $3" >&2; exec sleep 30 ;;
esac
exit 0
`)
	testutil.PrependPath(t, dir)

	tests := []struct {
		target  string
		wantErr string
	}{
		{"killed", ""},
		{"gone", ""},
		{"noserver", "no server running"},
		{"denied", "permission denied"},
		{"hung", "tmux kill-window"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			err := (&TmuxBackend{}).Teardown(tt.target)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Teardown(%s) = %v, want nil", tt.target, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Teardown(%s) = %v, want error containing %q", tt.target, err, tt.wantErr)
			}
		})
	}
}
