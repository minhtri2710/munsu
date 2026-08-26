package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// safetyShapes is every output shape runSafetyCheck renders. The gap BEO-73
// closes was measured on all six, because all six route through runSafetyCheck;
// the acceptance directions below are therefore asserted on all six too.
var safetyShapes = []string{"claude", "codex", "grok", "opencode", "agy", "pi"}

// runSafetyShape runs safety-check in one harness shape and reports whether the
// call was refused, in that shape's own terms. agy denies with exit 0 and a
// stdout `decision` field, and pi denies through `block` / `gate_refused` in its
// JSON contract — reading the exit code for either would silently pass.
func runSafetyShape(t *testing.T, harness, checkPath, command, filePath string) (bool, string) {
	t.Helper()
	exitCode := 0
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	cmd := &cobra.Command{}
	cmd.SetErr(io.Discard)
	cmd.Flags().String("output", OutputJSON, "")

	var contract strings.Builder
	cmd.SetOut(&contract)

	stdout, stderr := captureBoth(func() {
		if err := runSafetyCheck(cmd, checkPath, command, filePath, harness); err != nil {
			t.Fatalf("%s: runSafetyCheck: %v", harness, err)
		}
	})

	switch harness {
	case "agy":
		var payload struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
			t.Fatalf("agy: stdout is not the decision contract: %v (stdout=%q)", err, stdout)
		}
		return payload.Decision == "deny", payload.Reason
	case "pi":
		var response struct {
			Data SafetyCheckData `json:"data"`
		}
		if err := json.Unmarshal([]byte(contract.String()), &response); err != nil {
			t.Fatalf("pi: stdout is not the safety-check contract: %v (stdout=%q)", err, contract.String())
		}
		// The pi extension gates on both flags (integration_pi.go:372).
		reason := response.Data.Reason
		if reason == "" {
			reason = response.Data.Error
		}
		return response.Data.Block || response.Data.GateRefused, reason
	default:
		return exitCode == 2, stderr + stdout
	}
}

// assertShapes runs one call through every output shape and requires the same
// verdict from each.
func assertShapes(t *testing.T, wantBlocked bool, checkPath, command, filePath string) {
	t.Helper()
	for _, harness := range safetyShapes {
		blocked, detail := runSafetyShape(t, harness, checkPath, command, filePath)
		if blocked != wantBlocked {
			t.Errorf("%s: command=%q file=%q cwd=%s → blocked=%v, want %v (detail=%q)",
				harness, command, filePath, checkPath, blocked, wantBlocked, detail)
		}
	}
}

// TestShellWriteRefusedByAbsoluteTargetIntoBoundPrimary is the direction BEO-73
// exists for: the session stands in its own valid worktree, so the cwd ladder
// has nothing to refuse, and the target is the shared checkout.
func TestShellWriteRefusedByVolumeLessRootedWindowsTargetIntoBoundPrimary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("volume-less rooted paths are Windows-specific")
	}
	primary, worktree := boundTaskFixture(t, "ship-shell-volume-less-root")
	target := filepath.Join(primary, "README.md")
	rooted := strings.TrimPrefix(target, filepath.VolumeName(target))

	assertShapes(t, true, worktree, "echo pwned > "+rooted, "")

	unrelated := filepath.Join(t.TempDir(), "README.md")
	unrelatedRooted := strings.TrimPrefix(unrelated, filepath.VolumeName(unrelated))
	assertShapes(t, false, worktree, "echo ok > "+unrelatedRooted, "")
}

func TestShellWriteRefusedByAbsoluteTargetIntoBoundPrimary(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-target")

	target := filepath.Join(primary, "README.md")
	for _, command := range []string{
		"echo pwned > " + target,
		"echo pwned >> " + target,
		"echo pwned >" + target,
		"echo pwned | tee " + target,
		"sed -i '' s/a/b/ " + target,
		"cp /etc/hosts " + target,
		"mv /etc/hosts " + target,
		"rm -rf " + filepath.Join(primary, "internal"),
		"touch " + target,
		"truncate -s 0 " + target,
		"dd if=/dev/zero of=" + target,
		"chmod 777 " + target,
	} {
		assertShapes(t, true, worktree, command, "")
	}
}

// TestShellWriteRefusedAfterCdIntoBoundPrimary covers the variant a plain
// absolute-path scan would miss: `cd` inside the command line moves the base
// every relative target resolves against.
func TestShellWriteRefusedAfterCdIntoBoundPrimary(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-cd")

	for _, command := range []string{
		"cd " + primary + " && echo pwned > README.md",
		"cd " + primary + " && rm -rf internal",
	} {
		assertShapes(t, true, worktree, command, "")
	}
}

// TestShellReadsIntoBoundPrimaryAllowed is the false-positive direction that
// decides whether this guard is usable at all: reading the shared checkout as a
// reference is ordinary work, and refusing it would stall every run.
func TestShellReadsIntoBoundPrimaryAllowed(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-read")

	for _, command := range []string{
		"cat " + filepath.Join(primary, "README.md"),
		"grep -rn munsu " + primary,
		"ls -la " + primary,
		"go build " + filepath.Join(primary, "..."),
		"diff " + filepath.Join(primary, "README.md") + " " + filepath.Join(worktree, "README.md"),
		"cp " + filepath.Join(primary, "README.md") + " " + filepath.Join(worktree, "README.md"),
	} {
		assertShapes(t, false, worktree, command, "")
	}
}

// TestShellWriteInsideWorktreeAllowed is the normal soldier flow.
func TestShellWriteInsideWorktreeAllowed(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-shell-ok")

	for _, command := range []string{
		"echo ok > README.md",
		"echo ok > " + filepath.Join(worktree, "README.md"),
		"sed -i '' s/a/b/ " + filepath.Join(worktree, "README.md"),
		"rm -rf " + filepath.Join(worktree, "build"),
	} {
		assertShapes(t, false, worktree, command, "")
	}
}

// TestShellWriteIntoUnrelatedRepositoryAllowed pins the BEO-50 lesson on this
// path: `Primary` means gitDir == commonDir, which every scratch repo an agent
// creates satisfies. Only the bound repository's checkout is refused.
func TestShellWriteIntoUnrelatedRepositoryAllowed(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-shell-scratch")

	scratch := initGitRepoForSafety(t, t.TempDir())
	for _, command := range []string{
		"echo notes > " + filepath.Join(scratch, "notes.md"),
		"echo notes > " + filepath.Join(t.TempDir(), "notes.md"),
	} {
		assertShapes(t, false, worktree, command, "")
	}
}

// TestShellWriteIntoPrimaryAllowedOutsideTaskRun holds the whitelist boundary:
// without a task run there is no bound repository, so nothing is refused.
func TestShellWriteIntoPrimaryAllowedOutsideTaskRun(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "")

	assertShapes(t, false, primary, "echo hi > "+filepath.Join(primary, "README.md"), "")
}

// TestShellWriteIntoSiblingWorktreeAllowed pins ADR-0014 §4: a sibling task's
// worktree is deliberately outside this guard's scope. It is asserted so that a
// later change of that policy shows up as a failing test rather than as drift.
func TestShellWriteIntoSiblingWorktreeAllowed(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-sibling")

	sibling := filepath.Join(t.TempDir(), "sibling")
	runGitForSafety(t, primary, "worktree", "add", "--detach", sibling)

	target := filepath.Join(sibling, "README.md")
	assertShapes(t, false, worktree, "echo pwned > "+target, "")
	assertShapes(t, false, worktree, "", target)
}

// TestUnrelatedCwdAllowedWhenEveryTargetIsSafe is the narrowing of ADR-0014 §3:
// a call whose targets are provably outside the bound primary checkout is not
// shared-state access just because the session stands outside any repository.
func TestUnrelatedCwdAllowedWhenEveryTargetIsSafe(t *testing.T) {
	_, worktree := boundTaskFixture(t, "ship-unrelated-ok")
	outside := t.TempDir()

	target := filepath.Join(worktree, "README.md")
	assertShapes(t, false, outside, "sed -i '' s/a/b/ "+target, "")
	assertShapes(t, false, outside, "", target)
}

// TestUnrelatedCwdStillRefusedWithoutWriteTarget is bound 1 of the narrowing:
// the cwd rule itself is unchanged for calls that name no target.
func TestUnrelatedCwdStillRefusedWithoutWriteTarget(t *testing.T) {
	boundTaskFixture(t, "ship-unrelated-bare")
	outside := t.TempDir()

	assertShapes(t, true, outside, "ls -la", "")
	assertShapes(t, true, outside, "", "")
}

// TestUnrelatedCwdStillRefusedWhenTargetIsBoundPrimary keeps the two rules from
// cancelling each other out: an unsafe target refuses on its own terms.
func TestUnrelatedCwdStillRefusedWhenTargetIsBoundPrimary(t *testing.T) {
	primary, _ := boundTaskFixture(t, "ship-unrelated-bad")
	outside := t.TempDir()

	target := filepath.Join(primary, "README.md")
	assertShapes(t, true, outside, "echo pwned > "+target, "")
	assertShapes(t, true, outside, "", target)
}

// TestUnrelatedCwdRefusalUnchangedWithoutBinding is bound 2: no binding to
// compare against, no relaxation.
func TestUnrelatedCwdRefusalUnchangedWithoutBinding(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	t.Setenv("MUNSU_TASK_ID", "ship-unbound-unrelated")

	outside := t.TempDir()
	target := filepath.Join(t.TempDir(), "notes.md")
	assertShapes(t, true, outside, "echo hi > "+target, "")
}

// TestHeredocBodyIsContentNotCommand is the false-positive direction that took
// this guard down once: every line of a heredoc body used to be tokenized as its
// own command, so a document that quotes a command was refused, and a `cd` line
// inside a document moved the resolution base for the real commands after the
// terminator. Both writes below go to the worktree and must run.
func TestHeredocBodyIsContentNotCommand(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-heredoc")

	for _, command := range []string{
		// F2: the body quotes a destructive command aimed at the shared checkout.
		"cat <<'EOF' > notes.md\nrm -rf " + filepath.Join(primary, "internal") + "\nEOF",
		// H1: the body contains a `cd`, and the real write comes after it.
		"cat <<'EOF' > notes.md\ncd " + primary + "\nEOF\necho ok > out.txt",
		// The same document written with an unquoted and a tab-stripping marker.
		"cat <<EOF > notes.md\nrm -rf " + filepath.Join(primary, "internal") + "\nEOF",
		"cat <<-EOF > notes.md\n\trm -rf " + filepath.Join(primary, "internal") + "\n\tEOF",
		// A backslash-quoted marker is the same document.
		"cat <<\\EOF > notes.md\nrm -rf " + filepath.Join(primary, "internal") + "\nEOF",
	} {
		assertShapes(t, false, worktree, command, "")
	}
}

// TestHeredocDoesNotHideRealTargets keeps the fix from becoming a bypass: only
// the body is content. A write named outside the body still decides the call,
// and an unterminated heredoc must not swallow the rest of the payload.
func TestHeredocDoesNotHideRealTargets(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-heredoc-bypass")

	shared := filepath.Join(primary, "README.md")
	for _, command := range []string{
		"cat <<'EOF' > " + shared + "\nharmless\nEOF",
		"cat <<'EOF' > notes.md\nharmless\nEOF\nrm -rf " + filepath.Join(primary, "internal"),
		"cat <<'EOF' > notes.md\nharmless\nEOF\ncd " + primary + " && echo pwned > README.md",
		// A backslash quotes the delimiter word, so these bodies end at `EOF`
		// too. Reading the backslash into the delimiter matched no line and
		// hid every command after the terminator.
		"cat <<\\EOF > notes.md\nharmless\nEOF\necho pwned > " + shared,
		"cat <<-\\EOF > notes.md\n\tharmless\n\tEOF\necho pwned > " + shared,
	} {
		assertShapes(t, true, worktree, command, "")
	}
}

// TestClobberRedirectRefused covers `>|`, the clobber form used when noclobber
// is set. It is the same redirection as `>` and was never in the open list.
func TestClobberRedirectRefused(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-clobber")

	shared := filepath.Join(primary, "README.md")
	assertShapes(t, true, worktree, "echo pwned >| "+shared, "")
	assertShapes(t, true, worktree, "echo pwned >|"+shared, "")
	assertShapes(t, false, worktree, "echo ok >| "+filepath.Join(worktree, "README.md"), "")
	// A real pipe still splits, so the reader on the right is still a reader.
	assertShapes(t, false, worktree, "cat "+shared+" | wc -l", "")
}

// TestTargetDirectoryFlagRefused covers `-t DIR`, which names the destination of
// a copy verb up front instead of last.
func TestTargetDirectoryFlagRefused(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-target-dir")

	for _, command := range []string{
		"cp -t " + primary + " ./x.md",
		"cp --target-directory=" + primary + " ./x.md",
		"mv -t " + primary + " ./x.md",
		"install -t" + primary + " ./x.md",
	} {
		assertShapes(t, true, worktree, command, "")
	}
	// Copying out of the shared checkout into the worktree stays a read.
	assertShapes(t, false, worktree, "cp -t "+worktree+" "+filepath.Join(primary, "README.md"), "")

	// rsync spells `-t` as `--times`, so it never asks the question above: its
	// source stays a source, and its destination is still the last operand.
	assertShapes(t, false, worktree, "rsync -t "+filepath.Join(primary, "README.md")+" ./x.md", "")
	assertShapes(t, false, worktree, "rsync -tv "+filepath.Join(primary, "README.md")+" ./x.md", "")
	assertShapes(t, true, worktree, "rsync -t /etc/hosts "+filepath.Join(primary, "README.md"), "")
	assertShapes(t, true, worktree, "rsync -tv /etc/hosts "+filepath.Join(primary, "README.md"), "")
}

// TestShellWriteTargetExtraction pins the narrow claim of coverage directly,
// without a git fixture: what counts as a write target and what deliberately
// does not (ADR-0014 §2).
func TestShellWriteTargetExtraction(t *testing.T) {
	// A leading separator is an absolute path on Unix but only a
	// current-drive-relative one on Windows, where filepath.IsAbs wants a
	// volume. These stand in for absolute paths, so they have to be absolute
	// on both platforms or the resolution under test never happens.
	base := mustAbsTestPath(t, "base")
	abs := filepath.Join(mustAbsTestPath(t, "shared"), "README.md")
	elsewhere := mustAbsTestPath(t, "elsewhere")

	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"echo x > " + abs, []string{abs}},
		{"echo x >> " + abs, []string{abs}},
		{"echo x 2> " + abs, []string{abs}},
		{"echo x | tee " + abs, []string{abs}},
		{"rm -rf " + abs, []string{abs}},
		{"cp a.txt " + abs, []string{abs}},
		{"sed -i '' s/a/b/ " + abs, []string{filepath.Join(base, "s/a/b/"), abs}},
		{"perl -pi -e s/a/b/ " + abs, []string{filepath.Join(base, "s/a/b/"), abs}},
		{"dd if=/dev/zero of=" + abs, []string{abs}},
		{"echo x > out.txt", []string{filepath.Join(base, "out.txt")}},
		{"cd " + elsewhere + " && echo x > out.txt", []string{filepath.Join(elsewhere, "out.txt")}},

		// Deliberately not claimed.
		{"cat " + abs, nil},
		{"grep -rn foo " + abs, nil},
		{"sed s/a/b/ " + abs, nil},
		{"cp " + abs + " local.txt", []string{filepath.Join(base, "local.txt")}},
		{"echo \"x > " + abs + "\"", nil},
		{"echo x > $(cat target)", nil},
		{"python3 -c open('" + abs + "','w')", nil},

		// `>|` is one clobber operator: splitting at the pipe dropped the target.
		{"echo x >| " + abs, []string{abs}},
		{"echo x >|" + abs, []string{abs}},

		// `-t DIR` moves the destination out of the last position.
		{"cp -t " + filepath.Dir(abs) + " ./x.md", []string{filepath.Dir(abs)}},
		{"cp --target-directory=" + filepath.Dir(abs) + " ./x.md", []string{filepath.Dir(abs)}},
		{"install -t" + filepath.Dir(abs) + " ./x.md", []string{filepath.Dir(abs)}},
		// rsync's `-t` is `--times`: it takes no operand, so the destination is
		// still the last one and the source stays a source.
		{"rsync -t " + abs + " ./x.md", []string{filepath.Join(base, "x.md")}},
		{"rsync -tv " + abs + " ./x.md", []string{filepath.Join(base, "x.md")}},
		{"rsync -t /etc/hosts " + abs, []string{abs}},

		// A heredoc body is content, not a command line.
		{"cat <<'EOF' > notes.md\nrm -rf " + abs + "\nEOF", []string{filepath.Join(base, "notes.md")}},
		{"cat <<EOF > notes.md\ncd /elsewhere\nEOF\necho ok > out.txt",
			[]string{filepath.Join(base, "notes.md"), filepath.Join(base, "out.txt")}},
		{"cat <<-END > notes.md\n\tEND\nrm -rf " + abs, []string{filepath.Join(base, "notes.md"), abs}},
		{"cat <<A > one.md\nrm -rf " + abs + "\nA\ncat <<B > two.md\nrm -rf " + abs + "\nB",
			[]string{filepath.Join(base, "one.md"), filepath.Join(base, "two.md")}},
		// A here-string has no body; the words after it are still a command.
		{"tee " + abs + " <<< text", []string{abs}},
		// `\` quotes the delimiter word, so the body still ends at `EOF` and the
		// command after the terminator is still read.
		{"cat <<\\EOF > notes.md\nrm -rf " + abs + "\nEOF\nrm -rf " + abs,
			[]string{filepath.Join(base, "notes.md"), abs}},
		{"cat <<-\\END > notes.md\n\tEND\nrm -rf " + abs, []string{filepath.Join(base, "notes.md"), abs}},

		// A lone backslash is read both ways (#664). munsu does not know which
		// shell will interpret the command, so the Windows reading — `\` is an
		// ordinary path separator — has to yield a candidate even on a platform
		// whose shell would dissolve it, and the POSIX reading has to keep
		// yielding one on a platform whose shell would not. Both appear here on
		// every OS, which is the whole of the guarantee: whichever shell runs
		// the command, the path it actually opens was classified.
		{"echo x > " + `\shared\README.md`, []string{
			filepath.Join(base, "sharedREADME.md"),
			filepath.Join(base, `\shared\README.md`),
		}},
		{"rm -rf " + `\shared\README.md`, []string{
			filepath.Join(base, "sharedREADME.md"),
			filepath.Join(base, `\shared\README.md`),
		}},
		// The two readings collapse when there is no backslash to read, so a
		// command that never mentions one produces exactly one target and no
		// duplicate survives the union.
		{"echo x > plain.md", []string{filepath.Join(base, "plain.md")}},
	} {
		got := shellWriteTargets(base, tc.command)
		if len(got) != len(tc.want) {
			t.Errorf("%q → %v, want %v", tc.command, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q → %v, want %v", tc.command, got, tc.want)
				break
			}
		}
	}
}

// mustAbsTestPath returns name as an absolute path rooted at the filesystem
// root on Unix and at the current volume's root on Windows.
func mustAbsTestPath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(string(os.PathSeparator) + name)
	if err != nil {
		t.Fatalf("Abs(%q): %v", name, err)
	}
	return abs
}
