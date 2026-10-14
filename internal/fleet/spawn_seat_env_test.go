package fleet

import (
	"bytes"
	"path/filepath"
	"testing"
)

// A resume rebuilds the launch script from the intent's recorded SeatEnv, not
// from the resuming process's environment, so a changed TMPDIR or HOME cannot
// change the bytes the committed intent describes.
func TestResumeRebuildsTheSameLaunchBytesUnderAChangedEnv(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(base, "tmp-a"))
	t.Setenv("HOME", filepath.Join(base, "home-a"))
	recorded, err := currentSeatEnv()
	if err != nil {
		t.Fatal(err)
	}
	in := launchArtifactInputForGuards(t)
	in.SeatEnv = recorded
	_, first, err := prepareLaunchScript(in)
	if err != nil {
		t.Fatalf("prepareLaunchScript at prepare time: %v", err)
	}

	t.Setenv("TMPDIR", filepath.Join(base, "tmp-b"))
	t.Setenv("HOME", filepath.Join(base, "home-b"))
	now, err := currentSeatEnv()
	if err != nil {
		t.Fatal(err)
	}
	if now == recorded {
		t.Fatal("changing TMPDIR and HOME did not change the current seat env; the test proves nothing")
	}
	in.SeatEnv = recorded
	_, resumed, err := prepareLaunchScript(in)
	if err != nil {
		t.Fatalf("prepareLaunchScript at resume: %v", err)
	}
	if !bytes.Equal(first, resumed) {
		t.Fatal("resume under a changed env rebuilt different launch bytes")
	}
}
