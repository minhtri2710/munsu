package cli

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
)

// backtickedCommand matches a `munsu ...` command path quoted in user-facing
// text. Errors that tell an operator what to run quote the command this way
// (the same convention root.go uses for its `--help` hints), which is what
// makes the instruction extractable and therefore checkable.
var backtickedCommand = regexp.MustCompile("`(munsu[^`]*)`")

// resolveCommandPath walks the real cobra tree the binary registers and
// reports whether the argument words of a `munsu ...` instruction name a
// runnable command. Placeholders (<task-id>), flags and flag values are not
// command names, so the walk stops at the first non-command word.
func resolveCommandPath(root *cobra.Command, words []string) (problem string, ok bool) {
	cur := root
	consumed := 0
	for _, w := range words {
		if strings.HasPrefix(w, "<") || strings.HasPrefix(w, "-") {
			break
		}
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == w || sub.HasAlias(w) {
				next = sub
				break
			}
		}
		if next == nil {
			return fmt.Sprintf("unregistered word %q", w), false
		}
		cur = next
		consumed++
	}
	if consumed == 0 {
		return "resolved path is not runnable: no command words consumed", false
	}
	if !cur.Runnable() {
		return fmt.Sprintf("resolved command %q is not runnable", cur.CommandPath()), false
	}
	return "", true
}

// TestDeliveryIdentityErrorsNameRegisteredCommands is the standing guard for a
// defect that shipped once already: the identity error told operators to run
// `pr-check`, a command removed with the legacy delivery path (#414 B), so the
// advice printed by merge-status resolved to nothing. Nothing else in CI reads
// command names out of Go strings, so this drives the real failure path, reads
// the command out of the error the user would actually see, and resolves it
// against the command tree the binary registers.
func TestDeliveryIdentityErrorsNameRegisteredCommands(t *testing.T) {
	root := NewRootCommand()
	const id = "identity-advice"
	homeDir := deliveryGuardHome(t, id, deliveryGuardGitHub)

	err := fleet.MergeStatus(homeDir, id)
	if err == nil {
		t.Fatal("expected merge-status to fail for a task with no delivery authorization")
	}
	msg := err.Error()

	matches := backtickedCommand.FindAllStringSubmatch(msg, -1)
	if len(matches) == 0 {
		t.Fatalf("error gives the operator no runnable command: %s", msg)
	}
	for _, m := range matches {
		words := strings.Fields(m[1])
		if problem, ok := resolveCommandPath(root, words[1:]); !ok {
			t.Errorf("error names %q, but resolver rejected it (%s): %s",
				m[1], problem, msg)
		}
	}
}

func TestDeliveryCommandRegistersReviewDiff(t *testing.T) {
	root := NewRootCommand()
	for _, command := range []string{"review-diff"} {
		if problem, ok := resolveCommandPath(root, []string{"delivery", command, "<task-id>"}); !ok {
			t.Errorf("delivery %s is not a registered runnable command: %s", command, problem)
		}
	}
}

// TestResolveCommandPathRejectsUnregisteredCommand pins the oracle itself: the
// walk above must fail on the exact shape the defect had, otherwise the guard
// would pass no matter what the errors said.
func TestResolveCommandPathRejectsUnregisteredCommand(t *testing.T) {
	root := NewRootCommand()

	if problem, ok := resolveCommandPath(root, []string{"delivery", "<task-id>"}); ok {
		t.Error("resolveCommandPath accepted a non-runnable group command")
	} else if !strings.Contains(problem, "not runnable") {
		t.Errorf("reported the wrong problem: got %q, want not runnable", problem)
	}

	if problem, ok := resolveCommandPath(root, []string{"delivery", "pr-check", "<task-id>"}); ok {
		t.Error("resolveCommandPath accepted the removed pr-check command")
	} else if !strings.Contains(problem, "unregistered") || !strings.Contains(problem, "pr-check") {
		t.Errorf("reported the wrong problem: got %q, want unregistered pr-check", problem)
	}

	if problem, ok := resolveCommandPath(root, nil); ok {
		t.Error("resolveCommandPath accepted an empty command path")
	} else if !strings.Contains(problem, "no command words consumed") {
		t.Errorf("reported the wrong problem: got %q, want no command words consumed", problem)
	}

	if _, ok := resolveCommandPath(root, []string{"delivery", "pr-merge", "<task-id>", "<pr-url>"}); !ok {
		t.Error("resolveCommandPath rejected a registered command")
	}
}
