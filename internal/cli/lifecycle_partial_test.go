package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestLifecyclePartialErrorPreservesAuthoritativeState(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand()
	root.SetArgs([]string{"backlog", "start", "task", "--home", homeDir})
	err := root.Execute()
	var partial *LifecyclePartialError
	if !errors.As(err, &partial) || partial.TaskID != "task" || partial.State != "working" {
		t.Fatalf("partial = %T %v", err, err)
	}
	current, ok, readErr := home.ReadCurrentTaskAggregate(homeDir, "task")
	if readErr != nil || !ok || current.State != "working" {
		t.Fatalf("authoritative aggregate = %+v ok=%v err=%v", current, ok, readErr)
	}
	var out bytes.Buffer
	if code := WriteContractError(&out, err, []string{"backlog", "start", "task", "--output", "json"}); code != 1 || !strings.Contains(out.String(), `"error_code": "conflict"`) {
		t.Fatalf("partial contract code=%d output=%s", code, out.String())
	}
}
