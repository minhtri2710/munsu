package cli

import (
	"io"
	"strings"
	"testing"
)

func TestSpawnRejectsReopenFlag(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "proj", "--reopen"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --reopen") {
		t.Fatalf("spawn --reopen = %v, want unknown flag", err)
	}
}
