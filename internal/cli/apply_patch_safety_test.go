package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runCodexApplyPatch feeds a codex PreToolUse payload for `apply_patch` on
// stdin and returns the exit code and stderr — exactly what the hook sees.
// The payload shape is codex's own: the raw patch text under `command`.
func runCodexApplyPatch(t *testing.T, checkPath, patchBody string) (int, string) {
	t.Helper()
	return runCodexPayload(t, checkPath, map[string]any{
		"hookEventName": "PreToolUse",
		"tool_name":     "apply_patch",
		"tool_input":    map[string]any{"command": patchBody},
	})
}

func runCodexPayload(t *testing.T, checkPath string, payload map[string]any) (int, string) {
	t.Helper()
	exitCode := 0
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	encoded := mustJSON(t, payload)
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	go func() {
		w.Write(encoded)
		w.Close()
	}()
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	_, stderr := captureBoth(func() {
		if err := runSafetyCheck(cmd, checkPath, "", "", "codex"); err != nil {
			exitCode = 1
		}
	})
	return exitCode, stderr
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

// patchTouching renders a minimal but well-formed apply-patch document that
// updates target, with body as the added content line.
func patchTouching(target, body string) string {
	return "*** Begin Patch\n" +
		"*** Update File: " + target + "\n" +
		"@@\n" +
		"+" + body + "\n" +
		"*** End Patch\n"
}

// TestApplyPatchIntoBoundPrimaryCheckoutRefused is the blocked direction: a
// patch whose target is the shared checkout of the bound repository is refused.
// Before the channel split this call was allowed, because the patch body went
// to the command channel and the file-write branch was unreachable for it.
func TestApplyPatchIntoBoundPrimaryCheckoutRefused(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-patch")

	target := filepath.Join(primary, "internal", "backend", "worktree.go")
	code, stderr := runCodexApplyPatch(t, worktree, patchTouching(target, "harmless line"))
	if code != 2 {
		t.Fatalf("apply_patch into bound primary checkout: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "shared primary checkout of the bound repository refused for file write") {
		t.Fatalf("deny reason does not name the bound repository: %q", stderr)
	}
}

// TestApplyPatchRelativeTargetResolvedAgainstCwd covers the same direction for
// a relative target, which is how codex normally writes them: resolution
// happens against the directory the tool call runs in, so the same patch text
// is refused or allowed depending on where it lands.
func TestApplyPatchRelativeTargetResolvedAgainstCwd(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-patch-rel")

	body := patchTouching("internal/backend/worktree.go", "harmless line")
	if block, reason := evaluatePatchSafety(primary, body); !block {
		t.Fatalf("relative target under the bound primary checkout was allowed (reason=%q)", reason)
	}
	if block, reason := evaluatePatchSafety(worktree, body); block {
		t.Fatalf("relative target under the bound worktree was refused: %s", reason)
	}
}

// TestApplyPatchIntoWorktreeAllowedDespiteBlockedStringInBody is the regression
// test for the measured bug. A patch that legitimately edits the guard's own
// source — a file that contains the literal "munsu watch arm" — used to be
// refused, because the patch body was scanned by the shell-blocking ladder. The
// decision must come from the target, never from the content.
func TestApplyPatchIntoWorktreeAllowedDespiteBlockedStringInBody(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-patch")

	target := filepath.Join(worktree, "internal", "cli", "integrate_cmd.go")
	for _, body := range []string{
		`if strings.Contains(effectiveCommand, "munsu watch arm") {`,
		`cd .no-mistakes && munsu watch stop`,
		`git push --force origin main`,
	} {
		code, stderr := runCodexApplyPatch(t, worktree, patchTouching(target, body))
		if code != 0 {
			t.Fatalf("apply_patch inside worktree with body %q: exit=%d, want 0 (stderr=%q)", body, code, stderr)
		}
	}
}

// TestApplyPatchUnparseableRefused holds the fail-closed decision: once a tool
// is declared covered, an unreadable payload is a refusal, not a pass.
func TestApplyPatchUnparseableRefused(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-patch")

	for name, body := range map[string]string{
		"empty":             "",
		"no envelope":       "*** Update File: internal/cli/integrate_cmd.go\n+x\n",
		"envelope unclosed": "*** Begin Patch\n*** Update File: internal/cli/integrate_cmd.go\n+x\n",
		"no file header":    "*** Begin Patch\n@@\n+x\n*** End Patch\n",
	} {
		code, stderr := runCodexApplyPatch(t, worktree, body)
		if code != 2 {
			t.Fatalf("%s patch body: exit=%d, want 2 (stderr=%q)", name, code, stderr)
		}
		if !strings.Contains(stderr, "not a readable patch") {
			t.Fatalf("%s patch body: deny reason does not name the parse failure: %q", name, stderr)
		}
	}
}

// TestApplyPatchBodyNeverReachesCommandChannel pins the classification itself:
// the patch text must land in the patch channel, leaving the command channel
// empty, whatever the text happens to contain.
func TestApplyPatchBodyNeverReachesCommandChannel(t *testing.T) {
	body := patchTouching("a.go", "munsu watch arm")
	encoded := mustJSON(t, map[string]any{
		"tool_name":  "apply_patch",
		"tool_input": map[string]any{"command": body},
	})

	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	go func() {
		w.Write(encoded)
		w.Close()
	}()
	os.Stdin = r
	payload, err := readStdinForToolPayload()
	os.Stdin = oldStdin

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !payload.isPatch {
		t.Fatal("apply_patch payload was not classified as a patch")
	}
	if payload.command != "" {
		t.Fatalf("patch body leaked into the command channel: %q", payload.command)
	}
	if payload.patchBody != body {
		t.Fatalf("patch body not preserved: %q", payload.patchBody)
	}
}

// TestBashPayloadStillUsesCommandChannel guards the other side of the split:
// classification by tool name must not disturb ordinary shell calls.
func TestBashPayloadStillUsesCommandChannel(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	gitDir := initGitRepoForSafety(t, t.TempDir())

	code, stderr := runCodexPayload(t, gitDir, map[string]any{
		"hookEventName": "PreToolUse",
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "munsu watch arm"},
	})
	if code != 2 {
		t.Fatalf("Bash munsu watch arm: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "watcher lifecycle is managed automatically") {
		t.Fatalf("unexpected deny reason for Bash: %q", stderr)
	}
}

// TestApplyPatchTargetsReadsEveryHeaderKind covers the header vocabulary and
// the two ways a patch body can lie about its targets: content lines inside a
// hunk are prefixed, so they can neither forge nor mask a header.
func TestApplyPatchTargetsReadsEveryHeaderKind(t *testing.T) {
	body := "*** Begin Patch\n" +
		"*** Add File: added.go\n" +
		"+package x\n" +
		"*** Update File: updated.go\n" +
		"*** Move to: moved.go\n" +
		"@@\n" +
		"+*** Add File: forged.go\n" +
		"-*** Delete File: masked.go\n" +
		" *** Update File: context.go\n" +
		"*** Delete File: deleted.go\n" +
		"*** End Patch\n"

	targets, err := applyPatchTargets(body)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	want := []string{"added.go", "updated.go", "moved.go", "deleted.go"}
	if strings.Join(targets, ",") != strings.Join(want, ",") {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
}
