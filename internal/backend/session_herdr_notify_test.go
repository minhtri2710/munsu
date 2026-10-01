package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestHerdrNotifyShowsPopup pins the popup argv and that a failing herdr is
// returned, never swallowed.
func TestHerdrNotifyShowsPopup(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "herdr"), "#!/bin/sh\nif [ \"$1\" = \"--session\" ]; then shift 2; fi\nprintf '%s\\n' \"$@\" > \""+argvFile+"\"\n[ \"$FAKE_NOTIFY_FAIL\" = 1 ] && { echo 'popup refused' >&2; exit 1; }\nexit 0\n")
	testutil.PrependPath(t, dir)

	var notifier HumanNotifier = NewHerdrBackend("test")
	if err := notifier.Notify("needs you", "decision pending"); err != nil {
		t.Fatalf("Notify = %v, want nil", err)
	}
	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)), "notification\nshow\nneeds you\n--body\ndecision pending"; got != want {
		t.Fatalf("herdr argv = %q, want %q", got, want)
	}

	t.Setenv("FAKE_NOTIFY_FAIL", "1")
	if err := notifier.Notify("needs you", "decision pending"); err == nil {
		t.Fatal("Notify must return herdr's failure")
	}
}
