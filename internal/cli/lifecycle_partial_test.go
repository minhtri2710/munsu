package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestLifecyclePartialErrorPreservesAuthoritativeState(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	// Force the .status projection to fail so the authoritative transition
	// returns a typed partial result.
	if err := os.MkdirAll(filepath.Join(homeDir, "state", "task.status"), 0755); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand()
	root.SetArgs([]string{"task", "start", "task", "--home", homeDir})
	err := root.Execute()
	var partial *LifecyclePartialError
	if !errors.As(err, &partial) || partial.TaskID != "task" || partial.State != "working" {
		t.Fatalf("partial = %T %v", err, err)
	}
	current, getErr := auth.Get(mustTaskIDFor(t, "task"))
	if getErr != nil || current.Phase != taskauthority.PhaseWorking {
		t.Fatalf("authoritative aggregate = %+v err=%v", current, getErr)
	}
	var out bytes.Buffer
	if code := WriteContractError(&out, err, []string{"task", "start", "task", "--output", "json"}); code != 1 || !strings.Contains(out.String(), `"error_code": "conflict"`) {
		t.Fatalf("partial contract code=%d output=%s", code, out.String())
	}
}
