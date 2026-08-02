package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestLifecyclePartialErrorPreservesAuthoritativeState(t *testing.T) {
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "task")
	root := NewRootCommand()
	root.SetArgs([]string{"backlog", "start", "task", "--home", homeDir})
	err := root.Execute()
	var partial *LifecyclePartialError
	if !errors.As(err, &partial) || partial.TaskID != "task" || partial.State != "working" {
		t.Fatalf("partial = %T %v", err, err)
	}
	current, getErr := auth.Get("task")
	if getErr != nil || current.Phase != taskauthority.PhaseWorking {
		t.Fatalf("authoritative aggregate = %+v err=%v", current, getErr)
	}
	var out bytes.Buffer
	if code := WriteContractError(&out, err, []string{"backlog", "start", "task", "--output", "json"}); code != 1 || !strings.Contains(out.String(), `"error_code": "conflict"`) {
		t.Fatalf("partial contract code=%d output=%s", code, out.String())
	}
}
