package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// writePromptFakeTmux fakes a tmux whose pane screen is a file: a paste appends
// the buffer, and Enter appends a turn line except in the "noturn" pane.
func writePromptFakeTmux(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	script := `#!/bin/sh
D=` + dir + `
echo "$*" >> "$D/log"
case "$1" in
list-panes)
  case "$3" in
    dead) echo "can't find window: dead" >&2; exit 1 ;;
    *) echo "$3: 1" ;;
  esac ;;
display-message)
  if [ "$5" = '#{pane_tty}' ]; then
    if [ "$4" = linebuffered ]; then echo icanon > "$D/tty"; else echo -icanon > "$D/tty"; fi
    echo "$D/tty"; exit 0
  fi
  case "$4" in
    shell) echo 99999999 zsh ;;
    turnsshell) if [ -f "$D/screen.$4" ]; then echo 99999999 zsh; else echo 99999999 claude; fi ;;
    *) echo 99999999 claude ;;
  esac ;;
capture-pane) cat "$D/screen.$3" 2>/dev/null ;;
set-buffer) printf '%s' "$5" > "$D/buf" ;;
paste-buffer) cat "$D/buf" >> "$D/screen.$8"; rm -f "$D/buf" ;;
send-keys)
  if [ "$3" != noturn ]; then echo "working" >> "$D/screen.$3"; fi ;;
esac
exit 0
`
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "tmux"), script)
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "stty"), "#!/bin/sh\ncat\n")
	testutil.PrependPath(t, dir)
	old := tmuxPromptSettle
	tmuxPromptSettle = time.Millisecond
	t.Cleanup(func() { tmuxPromptSettle = old })
	return log
}

func TestTmuxAgentPrompt(t *testing.T) {
	for _, tc := range []struct {
		pane           string
		want           PromptStatus
		typed, entered bool
	}{
		{"agent", PromptSubmitted, true, true},
		{"noturn", PromptStalled, true, true},
		{"shell", PromptUnsupported, false, false},
		{"linebuffered", PromptUnsupported, false, false},
		{"turnsshell", PromptUnsupported, true, false},
		{"dead", PromptEndpointDead, false, false},
	} {
		t.Run(tc.pane, func(t *testing.T) {
			log := writePromptFakeTmux(t)
			got := SubmitPrompt(&TmuxBackend{}, tc.pane, "do the $(thing)\nnext line")
			if got.Status != tc.want {
				t.Fatalf("SubmitPrompt(%s) = %s, want %s", tc.pane, got, tc.want)
			}
			calls, _ := os.ReadFile(log)
			if typed := strings.Contains(string(calls), "paste-buffer"); typed != tc.typed {
				t.Fatalf("pane %s typed=%v, want %v; calls:\n%s", tc.pane, typed, tc.typed, calls)
			}
			entered := strings.Contains(string(calls), "send-keys -t "+tc.pane+" Enter")
			cleared := strings.Contains(string(calls), "send-keys -t "+tc.pane+" C-u")
			if entered != tc.entered || cleared != (tc.typed && !tc.entered) {
				t.Fatalf("pane %s entered=%v cleared=%v, want entered=%v; calls:\n%s", tc.pane, entered, cleared, tc.entered, calls)
			}
		})
	}
}
