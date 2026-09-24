package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	if command == "" && filePath == "" && (harness == "claude" || harness == "grok" || harness == "codex" || harness == "agy") {
		oldStdin := os.Stdin
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("create stdin pipe: %v", err)
		}
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Fatalf("write stdin payload: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close stdin payload: %v", err)
		}
		os.Stdin = r
		defer func() {
			os.Stdin = oldStdin
			_ = r.Close()
		}()
	}

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
	forwardSlashRooted := strings.ReplaceAll(rooted, `\`, "/")
	assertShapes(t, true, worktree, "echo pwned > "+forwardSlashRooted, "")

	unrelated := filepath.Join(t.TempDir(), "README.md")
	unrelatedRooted := strings.TrimPrefix(unrelated, filepath.VolumeName(unrelated))
	assertShapes(t, false, worktree, "echo ok > "+unrelatedRooted, "")
	forwardSlashUnrelatedRooted := strings.ReplaceAll(unrelatedRooted, `\`, "/")
	assertShapes(t, false, worktree, "echo ok > "+forwardSlashUnrelatedRooted, "")
}

// TestShellWriteRefusedByDriveRelativeSameVolumeWindowsTargetIntoBoundPrimary
// covers the same-volume drive-relative spelling (C:foo): it is relative to the
// current directory on that drive, which is the session's cwd (the base), so it
// must reach the bound primary and be refused (#664 v2).
func TestShellWriteRefusedByDriveRelativeSameVolumeWindowsTargetIntoBoundPrimary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-relative paths are Windows-specific")
	}
	primary, worktree := boundTaskFixture(t, "ship-shell-drv-same")
	target := filepath.Join(primary, "README.md")
	rel, err := filepath.Rel(worktree, target)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	// C: + the relative path from the cwd (worktree) to the target.
	driveRelative := filepath.VolumeName(worktree) + rel

	assertShapes(t, true, worktree, "echo pwned > "+driveRelative, "")
	caseVariant := strings.ToLower(filepath.VolumeName(worktree)) + rel
	if strings.ToLower(filepath.VolumeName(worktree)) == filepath.VolumeName(worktree) {
		caseVariant = strings.ToUpper(filepath.VolumeName(worktree)) + rel
	}
	assertShapes(t, true, worktree, "echo pwned > "+caseVariant, "")
}

// TestShellWriteDriveRelativeDifferentVolumeFailClosedWindows pins refusal for
// a drive-relative path whose volume differs from the cwd (#664 v2).
func TestShellWriteDriveRelativeDifferentVolumeFailClosedWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-relative paths are Windows-specific")
	}
	_, worktree := boundTaskFixture(t, "ship-shell-drv-amb")
	volume := filepath.VolumeName(worktree)
	otherVolume := "D:"
	if strings.EqualFold(volume, otherVolume) {
		otherVolume = "E:"
	}
	command := "echo pwned > " + otherVolume + "shared\\README.md"

	assertShapes(t, true, worktree, command, "")
}

// TestResolveShellWritePathWindows is the unit-level pin of the shell-specific
// resolver across every Windows spelling it must classify: volume-less rooted,
// same-volume drive-relative, different-volume drive-relative (fail closed),
// absolute, UNC and plain relative (#664 v2).
func TestResolveShellWritePathWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics required")
	}
	const base = `C:\worktree`
	cases := []struct {
		path      string
		want      string
		ambiguous bool
	}{
		{`\rooted`, `C:\rooted`, false},
		{`/rooted`, `C:\rooted`, false},
		{`C:rel`, `C:\worktree\rel`, false},
		{`C:rel\sub`, `C:\worktree\rel\sub`, false},
		{`C:\abs`, `C:\abs`, false},
		{`\\server\share`, `\\server\share`, false},
		{`rel`, `C:\worktree\rel`, false},
	}
	for _, tc := range cases {
		if got, ambiguous := resolveShellWritePath(base, filepath.VolumeName(base), tc.path); got != tc.want || ambiguous != tc.ambiguous {
			t.Errorf("resolveShellWritePath(%q,%q) = %q, %v, want %q, %v", base, tc.path, got, ambiguous, tc.want, tc.ambiguous)
		}
	}
	if got, ambiguous := resolveShellWritePath(base, filepath.VolumeName(base), `D:ambiguous`); got != "" || !ambiguous {
		t.Errorf("resolveShellWritePath(%q,%q) = %q, %v, want ambiguous", base, `D:ambiguous`, got, ambiguous)
	}
	if got, ambiguous := resolveShellWritePath(base, filepath.VolumeName(base), `c:rel`); got != `C:\worktree\rel` || ambiguous {
		t.Errorf("case-insensitive same-volume resolution = %q, %v", got, ambiguous)
	}
	if got, ambiguous := resolveShellWritePath(base, `D:`, `\shared`); got != `D:\shared` || ambiguous {
		t.Errorf("active-volume rooted resolution = %q, %v", got, ambiguous)
	}
}

func TestShellWriteTargetsAfterAmbiguousWindowsCd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics required")
	}
	const base = `C:\worktree`

	targets, ambiguous := shellWriteTargets(base, `cd D:docs && echo ok > \scratch\out.txt`)
	if ambiguous {
		t.Fatalf("rooted target remained ambiguous: %v", targets)
	}
	if !slices.Contains(targets, `D:\scratch\out.txt`) {
		t.Errorf("rooted target = %v, want D volume candidate", targets)
	}

	if _, ambiguous := shellWriteTargets(base, `CD /D D:docs && echo pwned > relative.txt`); !ambiguous {
		t.Error("/d drive-relative cd did not preserve ambiguous cwd")
	}
	if _, ambiguous := shellWriteTargets(base, `cd D:docs && echo x \> D:secret`); !ambiguous {
		t.Error("escaped redirect ambiguity was cancelled by the other reading")
	}
	if targets, ambiguous := shellWriteTargets(base, `echo ok > "C:\\scratch\\out.txt"`); ambiguous || !slices.Contains(targets, `C:\\scratch\\out.txt`) {
		t.Errorf("quoted Windows target = %v, ambiguous=%v", targets, ambiguous)
	}
}

// TestShellWriteTrailingBackslashExtendsSpan pins the root-cause span fix (#664):
// the POSIX backslash reading must touch the trailing backslash position so the
// word span covers the whole `\foo\`. Without it the span ends one short and the
// literal (Windows) reading never groups with the POSIX reading, defeating the
// dual-reading classification for trailing-separator rooted targets.
func TestShellWriteTrailingBackslashExtendsSpan(t *testing.T) {
	const word = `\foo\`
	segments := tokenizeSegments(backslashEscapes, word)
	if len(segments) != 1 || len(segments[0]) != 1 {
		t.Fatalf("unexpected tokenization: %#v", segments)
	}
	tok := segments[0][0]
	if tok.text != "foo" {
		t.Fatalf("word text = %q, want foo", tok.text)
	}
	if tok.start != 0 || tok.end != 5 {
		t.Errorf("word span = {%d,%d}, want {0,5}", tok.start, tok.end)
	}
}

// TestShellWriteTrailingBackslashRootedTargetAfterAmbiguousCd pins the span
// alignment fix (#664): a volume-less rooted target with a trailing separator
// such as `\foo\` must be classified as a resolvable D:\\foo write even after
// an ambiguous drive-relative cd. The POSIX reading drops the trailing backslash
// and would otherwise yield a short span that the literal candidate never joins,
// defeating the dual-reading grouping and over-refusing a legitimate write.
func TestShellWriteTrailingBackslashRootedTargetAfterAmbiguousCd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics required")
	}
	const base = `C:\worktree`

	targets, ambiguous := shellWriteTargets(base, `cd D:docs && echo ok > \foo\`)
	if ambiguous {
		t.Fatalf("trailing-separator rooted target remained ambiguous: %v", targets)
	}
	if !slices.Contains(targets, `D:\foo`) {
		t.Errorf("trailing-separator rooted target = %v, want D volume candidate", targets)
	}
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

func TestShellWriteComputedCdDoesNotBecomeLiteralPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("computed Windows cwd semantics are Windows-specific")
	}
	_, worktree := boundTaskFixture(t, "ship-shell-computed-cd")
	targets, ambiguous := shellWriteTargets(worktree, `cd "$PRIMARY" && echo pwned > README.md`)
	if !ambiguous {
		t.Fatal("computed cd did not make dependent relative write ambiguous")
	}
	for _, target := range targets {
		if strings.Contains(target, "$PRIMARY") {
			t.Fatalf("computed cd produced a literal synthetic target: %v", targets)
		}
	}
}

func windowsAmbiguousCdVolumes(t *testing.T, worktree string) (string, string) {
	t.Helper()
	known := filepath.VolumeName(worktree)
	if known == "" {
		t.Fatal("worktree has no Windows volume")
	}
	unknown := "D:"
	if strings.EqualFold(known, unknown) {
		unknown = "E:"
	}
	return known, unknown
}

func TestShellWriteAmbiguousCdOnlyBlocksDependentRelativeWrites(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ambiguous drive-relative cwd is Windows-specific")
	}
	_, worktree := boundTaskFixture(t, "ship-shell-ambiguous-cd")
	known, unknown := windowsAmbiguousCdVolumes(t, worktree)
	cd := "cd " + unknown + "docs"

	// Reads are never targets, so they are unaffected by the unknown cwd.
	assertShapes(t, false, worktree, cd+" && cat file", "")
	// Absolute write to an unrelated path does not resolve against the unknown
	// volume's cwd, so it is allowed (#664).
	assertShapes(t, false, worktree, cd+" && echo ok > "+known+"\\scratch\\out.txt", "")
	// Same-volume drive-relative resolves against the known cwd, so it is allowed.
	assertShapes(t, false, worktree, cd+" && echo ok > "+known+"logs\\out.txt", "")
	// Volume-less rooted anchors to the active (unknown-cwd) volume's root, which
	// is known independently of its cwd, so it is allowed (#664).
	assertShapes(t, false, worktree, cd+" && echo ok > \\scratch\\out.txt", "")
	// Dependent relative write resolves against the unknown cwd: refused.
	assertShapes(t, true, worktree, cd+" && echo pwned > relative.txt", "")
	// A dependent relative cd after the ambiguous drive-relative cd must keep
	// the unknown active drive state: the plain relative write still resolves
	// against the unknowable cwd and is refused (it must not be resolved against
	// the stale cwd of the prior volume, nor may unknownCwd be cleared) (#664).
	assertShapes(t, true, worktree, cd+" && cd .. && echo pwned > relative.txt", "")
	assertShapes(t, true, worktree, "cd /d "+unknown+"docs && cd .. && echo pwned > relative.txt", "")
	// A dependent relative cd by a plain subdir after the ambiguous drive-relative
	// cd keeps the unknown active drive state too: the dependent relative write
	// still resolves against the unknowable cwd and is refused (#664 v8).
	assertShapes(t, true, worktree, cd+" && cd subdir && echo pwned > relative.txt", "")
	assertShapes(t, true, worktree, "cd /d "+unknown+"docs && cd subdir && echo pwned > relative.txt", "")
	// Different-volume drive-relative also resolves against the unknown cwd: refused.
	assertShapes(t, true, worktree, cd+" && echo pwned > "+unknown+"rel\\out.txt", "")
}

// TestShellWriteAmbiguousCdAllowsUnrelatedAbsoluteAndSameVolumeWrites pins the
// Windows shell-aware refusal boundary after an ambiguous drive-relative cd: an
// absolute write (C:\\scratch\\out.txt) and a same-volume drive-relative write
// (C:logs\\out.txt) must be classified because neither resolves against the
// unknown D: cwd, while a dependent relative write and a different-volume
// drive-relative write must be refused. The shell-aware check replaces the old
// global "cwd unknown" flag, which over-refused the C: spellings (#664).
func TestShellWriteAmbiguousCdAllowsUnrelatedAbsoluteAndSameVolumeWrites(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ambiguous drive-relative cwd is Windows-specific")
	}
	_, worktree := boundTaskFixture(t, "ship-shell-ambiguous-rooted")
	known, unknown := windowsAmbiguousCdVolumes(t, worktree)
	cd := "cd " + unknown + "docs"

	// Absolute write to an unrelated path does not resolve against the unknown
	// volume's cwd, so it is allowed.
	assertShapes(t, false, worktree, cd+" && echo ok > "+known+"\\scratch\\out.txt", "")
	// Same-volume drive-relative resolves against the known cwd, so it is allowed.
	assertShapes(t, false, worktree, cd+" && echo ok > "+known+"logs\\out.txt", "")
	// Dependent relative write resolves against the unknown cwd: refused.
	assertShapes(t, true, worktree, cd+" && echo pwned > relative.txt", "")
	// Different-volume drive-relative also resolves against the unknown cwd: refused.
	assertShapes(t, true, worktree, cd+" && echo pwned > "+unknown+"rel\\out.txt", "")
}

// TestShellWriteHeredocDoubleQuotedDelimiterRefusesProtectedWrite pins the
// quoted-delimiter fix: a double-quoted heredoc delimiter applies POSIX quote
// removal, so `<<"TAIL\\END"` ends the body at `TAIL\END` (the doubled
// backslash collapses to one). A protected command after that real terminator
// must stay visible and be refused — reading the doubled backslash literally
// would have let the body run past it and hidden the write (#664).
func TestShellWriteHeredocDoubleQuotedDelimiterRefusesProtectedWrite(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-heredoc-dquote")
	shared := filepath.Join(primary, "README.md")

	// Double-quoted delimiter with a doubled backslash: the terminator the body
	// must end at is `TAIL\END` (one backslash), not `TAIL\\END` (two).
	command := "cat <<\"TAIL\\\\END\" > notes.md\nharmless\nTAIL\\END\necho pwned > " + shared
	assertShapes(t, true, worktree, command, "")

	// Single-quoted delimiter keeps both backslashes literally, so its
	// terminator is `TAIL\\END` (two) — a different document that must still
	// refuse the protected write after it.
	quoted := "cat <<'TAIL\\\\END' > notes.md\nharmless\nTAIL\\\\END\necho pwned > " + shared
	assertShapes(t, true, worktree, quoted, "")

	// Double-quoted delimiter with an escaped quote: `<<"EOF\"X"` ends at
	// `EOF"X`; a protected write after it is still refused.
	quotedQuote := "cat <<\"EOF\\\"X\" > notes.md\nharmless\nEOF\"X\necho pwned > " + shared
	assertShapes(t, true, worktree, quotedQuote, "")
}

// TestShellWriteHeredocDoubleQuotedDelimiterParsing checks the delimiter parsing
// directly: a double-quoted delimiter with a doubled backslash collapses to the
// single-backslash terminator, so the body ends there and a protected write
// after it is still classified instead of being swallowed (#664).
func TestShellWriteHeredocDoubleQuotedDelimiterParsing(t *testing.T) {
	// Use a platform-native base and a relative post-terminator target so the
	// expectation resolves under both POSIX and Windows path semantics instead of
	// hard-coding Unix spellings the Windows resolver never produces (#664 v8).
	// The relative target still proves the double-quoted delimiter collapses
	// `TAIL\\END` to `TAIL\END`, ending the body there and leaving the second
	// write classified rather than swallowed.
	base := "/worktree"
	command := "cat <<\"TAIL\\\\END\" > notes.md\nharmless\nTAIL\\END\necho pwned > protected.md"
	targets, ambiguous := shellWriteTargets(base, command)
	if ambiguous {
		t.Fatalf("shellWriteTargets unexpectedly ambiguous: %q", command)
	}
	if !slices.Contains(targets, filepath.Join(base, "notes.md")) {
		t.Errorf("expected %s target, got %v", filepath.Join(base, "notes.md"), targets)
	}
	if !slices.Contains(targets, filepath.Join(base, "protected.md")) {
		t.Errorf("protected write after terminator was swallowed: targets=%v", targets)
	}
}

func TestShellWriteHeredocQuoteAwareBackslashStripping(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-heredoc-quote")
	command := "printf x 'foo\\' ; cat <<EOF > notes.md\n" +
		"rm -rf " + filepath.Join(primary, "internal") + "\n" +
		"EOF\n" +
		"echo ok > out.txt"

	// Evaluate the parsed targets rather than searching the stripped command: the
	// temporary checkout path can itself contain the old body's `/tmp` prefix.
	targets, ambiguous := shellWriteTargets(worktree, command)
	if ambiguous {
		t.Fatalf("shellWriteTargets(%q) unexpectedly reported ambiguity", command)
	}
	want := []string{
		filepath.Join(worktree, "notes.md"),
		filepath.Join(worktree, "out.txt"),
	}
	if !slices.Equal(targets, want) {
		t.Fatalf("shellWriteTargets(%q) = %v, want %v", command, targets, want)
	}
	if blocked, reason := evaluateWriteTargets(targets); blocked {
		t.Fatalf("shellWriteTargets(%q) included the heredoc body as a protected write: targets=%v reason=%q", command, targets, reason)
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

	// On Windows, absolute paths like D:\shared\README.md contain backslashes and
	// are classified under both interpretations (#664):
	// - POSIX-escape reading dissolves `\`, yielding D:\base\sharedREADME.md
	// - Windows-literal reading treats `\` as path separator, yielding D:\shared\README.md
	// On Unix, absolute paths use forward slashes and both readings produce the same path.
	absTargets := func() []string {
		if runtime.GOOS == "windows" {
			return []string{filepath.Join(base, "sharedREADME.md"), abs}
		}
		return []string{abs}
	}
	absWithPrefix := func(prefix ...string) []string {
		var out []string
		out = append(out, prefix...)
		if runtime.GOOS == "windows" {
			out = append(out, filepath.Join(base, "sharedREADME.md"))
		}
		out = append(out, abs)
		return out
	}
	dirTargets := func() []string {
		if runtime.GOOS == "windows" {
			return []string{filepath.Join(base, "shared"), filepath.Dir(abs)}
		}
		return []string{filepath.Dir(abs)}
	}
	cdElsewhereTargets := func() []string {
		if runtime.GOOS == "windows" {
			return []string{filepath.Join(base, "elsewhere", "out.txt"), filepath.Join(elsewhere, "out.txt")}
		}
		return []string{filepath.Join(elsewhere, "out.txt")}
	}

	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"echo x > " + abs, absTargets()},
		{"echo x >> " + abs, absTargets()},
		{"echo x 2> " + abs, absTargets()},
		{"echo x | tee " + abs, absTargets()},
		{"rm -rf " + abs, absTargets()},
		{"cp a.txt " + abs, absTargets()},
		{"sed -i '' s/a/b/ " + abs, absWithPrefix(base, filepath.Join(base, "s/a/b/"))},
		{"perl -pi -e s/a/b/ " + abs, absWithPrefix(filepath.Join(base, "s/a/b/"))},
		{"dd if=/dev/zero of=" + abs, absTargets()},
		{"echo x > out.txt", []string{filepath.Join(base, "out.txt")}},
		{"cd " + elsewhere + " && echo x > out.txt", cdElsewhereTargets()},

		// Deliberately not claimed.
		{"cat " + abs, nil},
		{"grep -rn foo " + abs, nil},
		{"sed s/a/b/ " + abs, nil},
		{"cp " + abs + " local.txt", []string{filepath.Join(base, "local.txt")}},
		{"echo \"x > " + abs + "\"", nil},
		{"echo x > $(cat target)", nil},
		{"python3 -c open('" + abs + "','w')", nil},

		// `>|` is one clobber operator: splitting at the pipe dropped the target.
		{"echo x >| " + abs, absTargets()},
		{"echo x >|" + abs, absTargets()},

		// `-t DIR` moves the destination out of the last position.
		{"cp -t " + filepath.Dir(abs) + " ./x.md", dirTargets()},
		{"cp --target-directory=" + filepath.Dir(abs) + " ./x.md", dirTargets()},
		{"install -t" + filepath.Dir(abs) + " ./x.md", dirTargets()},
		// rsync's `-t` is `--times`: it takes no operand, so the destination is
		// still the last one and the source stays a source.
		{"rsync -t " + abs + " ./x.md", []string{filepath.Join(base, "x.md")}},
		{"rsync -tv " + abs + " ./x.md", []string{filepath.Join(base, "x.md")}},
		{"rsync -t /etc/hosts " + abs, absTargets()},
		{"rsync -tv /etc/hosts " + abs, absTargets()},

		// A heredoc body is content, not a command line.
		{"cat <<'EOF' > notes.md\nrm -rf " + abs + "\nEOF", []string{filepath.Join(base, "notes.md")}},
		{"cat <<EOF > notes.md\ncd /elsewhere\nEOF\necho ok > out.txt",
			[]string{filepath.Join(base, "notes.md"), filepath.Join(base, "out.txt")}},
		{"cat <<-END > notes.md\n\tEND\nrm -rf " + abs, absWithPrefix(filepath.Join(base, "notes.md"))},
		{"cat <<A > one.md\nrm -rf " + abs + "\nA\ncat <<B > two.md\nrm -rf " + abs + "\nB",
			[]string{filepath.Join(base, "one.md"), filepath.Join(base, "two.md")}},
		// A here-string has no body; the words after it are still a command.
		{"tee " + abs + " <<< text", absTargets()},
		// `\` quotes the delimiter word, so the body still ends at `EOF` and the
		// command after the terminator is still read.
		{"cat <<\\EOF > notes.md\nrm -rf " + abs + "\nEOF\nrm -rf " + abs,
			absWithPrefix(filepath.Join(base, "notes.md"))},
		{"cat <<-\\END > notes.md\n\tEND\nrm -rf " + abs, absWithPrefix(filepath.Join(base, "notes.md"))},

		// A lone backslash is read both ways (#664). munsu does not know which
		// shell will interpret the command, so the Windows reading — `\` is an
		// ordinary path separator — has to yield a candidate even on a platform
		// whose shell would dissolve it, and the POSIX reading has to keep
		// yielding one on a platform whose shell would not. Both appear here on
		// every OS, which is the whole of the guarantee: whichever shell runs
		// the command, the path it actually opens was classified.
		// The Windows reading resolves a volume-less rooted target to the base
		// volume's root; the POSIX reading dissolves the backslashes. Both are
		// asserted through the resolver so the row holds on every OS (#664 v2).
		{"echo x > " + `\shared\README.md`, []string{
			filepath.Join(base, "sharedREADME.md"),
			mustResolveShellWritePath(base, `\shared\README.md`),
		}},
		{"rm -rf " + `\shared\README.md`, []string{
			filepath.Join(base, "sharedREADME.md"),
			mustResolveShellWritePath(base, `\shared\README.md`),
		}},
		// The two readings collapse when there is no backslash to read, so a
		// command that never mentions one produces exactly one target and no
		// duplicate survives the union.
		{"echo x > plain.md", []string{filepath.Join(base, "plain.md")}},
		// POSIX quoting keeps backslashes literal in single quotes, and in double
		// quotes unless they precede a POSIX-special character.
		{"echo x > 'quoted\\out.txt'", []string{filepath.Join(base, `quoted\out.txt`)}},
		{"echo x > \"quoted\\out.txt\"", []string{filepath.Join(base, `quoted\out.txt`)}},
	} {
		got, ambiguous := shellWriteTargets(base, tc.command)
		if ambiguous {
			t.Errorf("%q unexpectedly ambiguous", tc.command)
		}
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

func shellWriteTargetsUnderForTest(mode backslashMode, checkPath, command string) ([]string, bool) {
	var targets []string
	ambiguous := false
	for _, result := range shellWriteTargetsUnderDetailed(mode, checkPath, command) {
		if result.ambiguous {
			ambiguous = true
		} else {
			targets = append(targets, result.path)
		}
	}
	return targets, ambiguous
}

// TestShellWriteHeredocBackslashDelimiterBothReadings pins the heredoc fix:
// a backslash-quoted delimiter (<<\EOF, <<-\END) must end the body at the bare
// delimiter in BOTH backslash readings, so a write named after the terminator —
// including a Windows-path one — is classified by the literal (Windows) reading
// instead of being swallowed (#664 v2). It calls the readings directly because
// the dual union already masks a swallowed literal reading on a POSIX host.
func TestShellWriteHeredocBackslashDelimiterBothReadings(t *testing.T) {
	base := mustAbsTestPath(t, "base")
	target := `\shared\README.md`

	cases := []string{
		"cat <<\\EOF > notes.md\nharmless\nEOF\necho pwned > " + target,
		"cat <<-\\END > notes.md\n\tharmless\n\tEND\necho pwned > " + target,
	}
	for _, command := range cases {
		// Heredoc stripping is POSIX and runs once; the readings tokenize what
		// remains, so this pins that the post-terminator write survives stripping
		// and is classified under both readings (#664 v3).
		stripped := stripHeredocBodies(command)
		escapeTargets, escapeAmbiguous := shellWriteTargetsUnderForTest(backslashEscapes, base, stripped)
		literalTargets, literalAmbiguous := shellWriteTargetsUnderForTest(backslashLiteral, base, stripped)
		if escapeAmbiguous || literalAmbiguous {
			t.Errorf("%q unexpectedly ambiguous", command)
		}
		// The POSIX reading dissolves backslashes, so it sees the mangled target.
		if !slices.Contains(escapeTargets, mustResolveShellWritePath(base, "sharedREADME.md")) {
			t.Errorf("escape reading dropped post-heredoc write: %q → %v", command, escapeTargets)
		}
		// The Windows reading keeps the backslash as a separator and must end
		// the body at EOF/END, so it sees the real Windows path.
		if !slices.Contains(literalTargets, mustResolveShellWritePath(base, target)) {
			t.Errorf("literal reading swallowed post-heredoc write: %q → %v", command, literalTargets)
		}
	}
}

// TestShellWriteHeredocBackslashDelimiterRefusesProtectedWindowsWrite exercises
// the full decision flow: a protected Windows-path write named after a <<\EOF or
// <<-\END terminator is refused on every harness shape, because heredoc bodies
// are stripped once with POSIX rules before the dual readings tokenize (#664 v3).
func TestShellWriteHeredocBackslashDelimiterRefusesProtectedWindowsWrite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("protected Windows paths require Windows filepath semantics")
	}
	primary, worktree := boundTaskFixture(t, "ship-shell-heredoc-win")
	target := filepath.Join(primary, "README.md") // C:\...\primary\README.md

	cases := []string{
		"cat <<\\EOF > notes.md\nharmless\nEOF\necho pwned > " + target,
		"cat <<-\\END > notes.md\n\tharmless\n\tEND\necho pwned > " + target,
	}
	for _, command := range cases {
		assertShapes(t, true, worktree, command, "")
	}
}

// TestShellWriteRefusedIfEitherCandidateIsProtected asserts that if either the
// POSIX-escape or the Windows-literal candidate targets a protected checkout,
// the command is refused even if the other candidate is safe (#664 v4).
func TestShellWriteRefusedIfEitherCandidateIsProtected(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-either-protected")
	primaryTarget := filepath.Join(primary, "README.md")
	worktreeTarget := filepath.Join(worktree, "README.md")

	// Direct evaluateWriteTargets evaluation:
	// If first candidate is safe and second is protected -> refused.
	if block, _ := evaluateWriteTargets([]string{worktreeTarget, primaryTarget}); !block {
		t.Errorf("evaluateWriteTargets([safe, protected]) = false, want true")
	}
	// If first candidate is protected and second is safe -> refused.
	if block, _ := evaluateWriteTargets([]string{primaryTarget, worktreeTarget}); !block {
		t.Errorf("evaluateWriteTargets([protected, safe]) = false, want true")
	}

	// Behavioral assertShapes check from worktree:
	assertShapes(t, true, worktree, "echo pwned > "+primaryTarget, "")

	if runtime.GOOS == "windows" {
		winCommand := "echo pwned > " + primaryTarget
		targets, ambiguous := shellWriteTargets(worktree, winCommand)
		if ambiguous {
			t.Fatalf("shellWriteTargets(%q) unexpectedly ambiguous", winCommand)
		}
		if len(targets) != 2 {
			t.Fatalf("shellWriteTargets(%q) returned %d candidates, want 2: %v", winCommand, len(targets), targets)
		}
		if targets[1] != primaryTarget {
			t.Errorf("targets[1] = %q, want %q", targets[1], primaryTarget)
		}
		assertShapes(t, true, worktree, winCommand, "")
	}
}

// TestShellWriteUnrelatedAllowedOnlyWhenBothCandidatesUnrelated asserts that a
// session standing in an unrelated directory is allowed to write only when every
// candidate target is safe/unrelated. If any candidate lands in the protected
// primary checkout, the call is refused (#664 v4).
func TestShellWriteUnrelatedAllowedOnlyWhenBothCandidatesUnrelated(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-unrelated-both")
	outside := t.TempDir()

	safeTarget := filepath.Join(outside, "notes.md")
	worktreeTarget := filepath.Join(worktree, "notes.md")
	primaryTarget := filepath.Join(primary, "README.md")

	// Both candidates safe in outside temp dir -> allowed.
	assertShapes(t, false, outside, "echo ok > "+safeTarget, "")
	// Both candidates safe in worktree -> allowed.
	assertShapes(t, false, outside, "echo ok > "+worktreeTarget, "")

	// Target in primary checkout -> refused across all shapes.
	assertShapes(t, true, outside, "echo pwned > "+primaryTarget, "")

	// Direct evaluation of candidate lists:
	// [safe, safe] -> allowed
	if block, _ := evaluateWriteTargets([]string{safeTarget, worktreeTarget}); block {
		t.Errorf("evaluateWriteTargets([safe, safe]) = true, want false")
	}
	// [safe, protected] -> refused
	if block, _ := evaluateWriteTargets([]string{safeTarget, primaryTarget}); !block {
		t.Errorf("evaluateWriteTargets([safe, protected]) = false, want true")
	}
	// [protected, safe] -> refused
	if block, _ := evaluateWriteTargets([]string{primaryTarget, safeTarget}); !block {
		t.Errorf("evaluateWriteTargets([protected, safe]) = false, want true")
	}
}

// TestShellWriteNoMalformedEmbeddedVolumeCandidates verifies that resolving
// drive-relative or volume-less paths never emits malformed embedded volume
// prefixes like `\D:` or `C:\base\D:\...` (#664 v4).
func TestShellWriteNoMalformedEmbeddedVolumeCandidates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows volume resolution semantics required")
	}
	const base = `C:\worktree\base`
	paths := []string{
		`C:rel`,
		`C:rel\sub`,
		`c:rel\sub`,
		`\rooted\path`,
		`/rooted/path`,
		`C:\abs\path`,
		`\\server\share\file`,
	}
	for _, p := range paths {
		resolved, _ := resolveShellWritePath(base, filepath.VolumeName(base), p)
		if resolved == "" {
			continue
		}
		if idx := strings.LastIndex(resolved, ":"); idx > 1 {
			t.Errorf("resolveShellWritePath(%q, %q) produced malformed embedded volume: %q", base, p, resolved)
		}
		for _, invalid := range []string{`\C:`, `/C:`, `\c:`, `/c:`, `\D:`, `/D:`, `\d:`, `/d:`} {
			if strings.Contains(resolved, invalid) {
				t.Errorf("resolveShellWritePath(%q, %q) contained invalid substring %q: %q", base, p, invalid, resolved)
			}
		}
	}

	targets, ambiguous := shellWriteTargets(base, `echo x > C:rel\nested\file.txt`)
	if ambiguous {
		t.Errorf("unexpected ambiguous target for same-drive path")
	}
	for _, target := range targets {
		if idx := strings.LastIndex(target, ":"); idx > 1 {
			t.Errorf("shellWriteTargets produced malformed target: %q", target)
		}
		for _, invalid := range []string{`\C:`, `/C:`, `\c:`, `/c:`} {
			if strings.Contains(target, invalid) {
				t.Errorf("shellWriteTargets produced target with %q: %q", invalid, target)
			}
		}
	}
}

// TestShellWriteTargetExactDeduplication verifies that targets produced by
// dual readings are deduplicated across readings without losing ordering or
// emitting duplicate entries (#664 v4).
func TestShellWriteTargetExactDeduplication(t *testing.T) {
	base := mustAbsTestPath(t, "base")

	commands := []struct {
		command string
		want    []string
	}{
		// Path without backslashes produces identical target in both readings; must not duplicate across readings.
		{"echo x > plain.md", []string{filepath.Join(base, "plain.md")}},
		{"rm -rf dir/file.txt", []string{filepath.Join(base, "dir/file.txt")}},
		{"cp a.txt b.txt", []string{filepath.Join(base, "b.txt")}},
	}

	for _, tc := range commands {
		got, ambiguous := shellWriteTargets(base, tc.command)
		if ambiguous {
			t.Errorf("%q unexpectedly ambiguous", tc.command)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q returned %d targets, want %d: %v vs %v", tc.command, len(got), len(tc.want), got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q target[%d] = %q, want %q", tc.command, i, got[i], tc.want[i])
			}
		}
		seen := make(map[string]bool)
		for _, target := range got {
			if seen[target] {
				t.Errorf("%q emitted duplicate target %q", tc.command, target)
			}
			seen[target] = true
		}
	}
}

// mustAbsTestPath returns name as an absolute path rooted at the filesystem
// root on Unix and at the current volume's root on Windows.
func mustResolveShellWritePath(base, path string) string {
	resolved, ambiguous := resolveShellWritePath(base, filepath.VolumeName(base), path)
	if ambiguous {
		panic("unexpected ambiguous shell path")
	}
	return resolved
}

func mustAbsTestPath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(string(os.PathSeparator) + name)
	if err != nil {
		t.Fatalf("Abs(%q): %v", name, err)
	}
	return abs
}

// bindPrimaryAtShellSpecialPath builds a primary checkout whose own path contains
// a shell-special character (special), a detached worktree bound to it, and the
// binding, then points the environment at that binding. The protected checkout
// therefore lives at a path that literally contains special, so a command that
// keeps special literal (single quotes, a POSIX backslash escape outside quotes,
// or a POSIX backslash escape inside double quotes) resolves to the bound
// primary and must be refused (#664).
func bindPrimaryAtShellSpecialPath(t *testing.T, taskID string, special rune) (primary, worktree string) {
	t.Helper()
	base, err := os.MkdirTemp(t.TempDir(), "prim"+string(special)+"*")
	if err != nil {
		t.Fatal(err)
	}
	primary = initGitRepoForSafety(t, base)
	worktree = filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, taskID, primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", taskID)
	return primary, worktree
}

// escapeShellChar quotes special with a POSIX backslash so it stays literal.
func escapeShellChar(t *testing.T, path string, special rune) string {
	t.Helper()
	return strings.ReplaceAll(filepath.ToSlash(path), string(special), "\\"+string(special))
}

// TestShellWriteRefusesLiteralDollarInProtectedPath pins the context-aware
// expansion fix (#664): a write target whose path contains a literal $ kept
// literal by single quotes, a POSIX backslash escape outside quotes, or a POSIX
// backslash escape inside double quotes names the bound primary checkout and
// must be refused. The same $ unescaped and unquoted, or unescaped inside
// double quotes, is a genuine shell expansion the guard cannot classify, so it
// stays omitted and the write is not refused (the narrow claim of ADR-0014 §2 is
// preserved, not relaxed). A bare $ does not trip the separate git-mutation
// command-substitution gate (only $( and backticks do), so these assertions
// exercise the write-target extractor directly.
func TestShellWriteRefusesLiteralDollarInProtectedPath(t *testing.T) {
	primary, worktree := bindPrimaryAtShellSpecialPath(t, "ship-shell-lit-dollar", '$')
	target := filepath.Join(primary, "internal")

	// Literal $: each quoting/escape form keeps special literal, so the target
	// is the bound primary checkout and must be refused.
	assertShapes(t, true, worktree, "rm -rf '"+target+"'", "")
	assertShapes(t, true, worktree, "rm -rf "+escapeShellChar(t, target, '$'), "")
	assertShapes(t, true, worktree, "rm -rf \""+escapeShellChar(t, target, '$')+"\"", "")

	// Unescaped $ is a genuine expansion: omitted, so the write is not refused.
	for _, command := range []string{"rm -rf " + target, "rm -rf \"" + target + "\""} {
		targets, ambiguous := shellWriteTargets(worktree, command)
		if ambiguous || slices.Contains(targets, target) {
			t.Errorf("unescaped-dollar target should be omitted: %v ambiguous=%v", targets, ambiguous)
		}
	}
}

// TestShellWriteRefusesLiteralBacktickInProtectedPath pins the same fix for a
// literal backtick (command substitution when unescaped, literal otherwise): a
// write target whose path contains a literal backtick kept literal by single
// quotes, a POSIX backslash escape outside quotes, or a POSIX backslash escape
// inside double quotes must be retained as a candidate that resolves to the
// bound primary checkout (#664). The separate git-mutation gate blocks any
// command containing a backtick regardless of quoting, so the end-to-end
// refusal below is asserted and the retention below proves the fix
// specifically: an unquoted/un-escaped backtick is still dropped (the narrow
// contract of ADR-0014 §2 is preserved).
func TestShellWriteRefusesLiteralBacktickInProtectedPath(t *testing.T) {
	primary, worktree := bindPrimaryAtShellSpecialPath(t, "ship-shell-lit-backtick", '`')
	target := filepath.Join(primary, "internal")

	for _, command := range []string{
		"rm -rf '" + target + "'",
		"rm -rf " + escapeShellChar(t, target, '`'),
		"rm -rf \"" + escapeShellChar(t, target, '`') + "\"",
	} {
		// The literal backtick is not a shell expansion, so the tokenizer must
		// retain it as a candidate resolving to the bound primary checkout. The
		// candidate may be spelled with either separator set under the dual
		// backslash readings ("C:\\..." vs "C:/..."), so compare by native path
		// semantics rather than raw spelling (#664 v8).
		targets, ambiguous := shellWriteTargets(worktree, command)
		if ambiguous {
			t.Fatalf("%q unexpectedly ambiguous", command)
		}
		targetFound := false
		for _, got := range targets {
			if filepath.Clean(got) == filepath.Clean(target) {
				targetFound = true
				break
			}
		}
		if !targetFound {
			t.Errorf("literal-backtick target dropped: %q -> %v", command, targets)
		}
		// End-to-end the write is refused (git-mutation gate blocks backticks).
		assertShapes(t, true, worktree, command, "")
	}

	// Unescaped backtick is a genuine command substitution the guard cannot
	// classify, so it stays omitted (narrow contract preserved).
	targets, ambiguous := shellWriteTargets(worktree, "rm -rf "+target)
	if ambiguous || slices.Contains(targets, target) {
		t.Errorf("unescaped-backtick target should be omitted: %v ambiguous=%v", targets, ambiguous)
	}
}

// TestTokenizeSegmentsLineContinuationAndANSICQuoting pins how the shared
// tokenizer reads a backslash-newline and a bash `$'...'` word, the two literal
// forms that let git text past both guards before. A POSIX backslash-newline
// outside single quotes is removed and joins the text around it; the Windows
// reading keeps the backslash. `$'...'` is one word in either reading, with its
// escapes decoded, and a word holding an escape the tokenizer does not decode
// is marked undecodable.
func TestTokenizeSegmentsLineContinuationAndANSICQuoting(t *testing.T) {
	for _, tc := range []struct {
		mode        backslashMode
		command     string
		want        [][]string
		undecodable bool
	}{
		{backslashEscapes, "/usr/bin/git \\\npush --force", [][]string{{"/usr/bin/git", "push", "--force"}}, false},
		{backslashEscapes, "git pu\\\nsh --force", [][]string{{"git", "push", "--force"}}, false},
		{backslashEscapes, "\"git pu\\\nsh\"", [][]string{{"git push"}}, false},
		{backslashEscapes, "'a\\\nb'", [][]string{{"a\\\nb"}}, false},
		{backslashLiteral, "git \\\npush", [][]string{{"git", `\`}, {"push"}}, false},
		{backslashEscapes, `bash -c $'git push --force'`, [][]string{{"bash", "-c", "git push --force"}}, false},
		{backslashEscapes, `$'git'x push`, [][]string{{"gitx", "push"}}, false},
		{backslashEscapes, `$'a\tb\nc\\d\'e\"f\?\101\x41\e\E\a\b\f\r\v'`, [][]string{{"a\tb\nc\\d'e\"f?AA\x1b\x1b\a\b\f\r\v"}}, false},
		{backslashLiteral, `$'git\x20push'`, [][]string{{"git push"}}, false},
		{backslashEscapes, `"$'x'"`, [][]string{{"$'x'"}}, false},
		{backslashEscapes, `$'\u0067it'`, [][]string{{`'\u0067it'`}}, true},
		{backslashEscapes, `$'\cA'`, [][]string{{`'\cA'`}}, true},
		{backslashEscapes, `$'\q'`, [][]string{{`'\q'`}}, true},
		{backslashEscapes, `$'\x'`, [][]string{{`'\x'`}}, true},
		{backslashEscapes, `$'\0'`, [][]string{{`'\0'`}}, true},
		{backslashEscapes, `$'\400'`, [][]string{{`'\400'`}}, true},
		{backslashEscapes, `$'\1014'`, [][]string{{"A4"}}, false},
		{backslashEscapes, `$'\x414'`, [][]string{{"A4"}}, false},
		{backslashEscapes, `$'git`, [][]string{{`'git`}}, true},
		{backslashEscapes, `$$'x'`, [][]string{{"$$x"}}, false},
		{backslashEscapes, `a$$'b'`, [][]string{{"a$$b"}}, false},
		{backslashEscapes, `$$'\' ; gi\t push`, [][]string{{`$$\`}, {"git", "push"}}, false},
		{backslashEscapes, `$$$'\x41'`, [][]string{{"$$A"}}, false},
		{backslashEscapes, `$''`, [][]string{{""}}, false},
		{backslashEscapes, `''`, [][]string{{""}}, false},
		{backslashEscapes, `""`, [][]string{{""}}, false},
		{backslashEscapes, `''"" x`, [][]string{{"", "x"}}, false},
		{backslashLiteral, `a '' b`, [][]string{{"a", "", "b"}}, false},
		{backslashEscapes, `x''`, [][]string{{"x"}}, false},
		// bash reads `$"..."` as its double-quoted text; `$$` stays the PID.
		{backslashEscapes, `bash -c $"git push --force"`, [][]string{{"bash", "-c", "git push --force"}}, false},
		{backslashLiteral, `$"a $x"b`, [][]string{{"a $xb"}}, false},
		{backslashEscapes, `$""`, [][]string{{""}}, false},
		{backslashEscapes, `$$"x"`, [][]string{{"$$x"}}, false},
		// An unquoted `${...}` is one word whatever it holds, and an
		// unterminated one is undecodable.
		{backslashEscapes, `echo ${x:-a b} c`, [][]string{{"echo", "${x:-a b}", "c"}}, false},
		{backslashEscapes, `${x:-a;b|c>d} ; y`, [][]string{{"${x:-a;b|c>d}"}, {"y"}}, false},
		{backslashEscapes, `${x:-"}"} ${a:-${b:-c d}} e`, [][]string{{"${x:-}}", "${a:-${b:-c d}}", "e"}}, false},
		{backslashEscapes, `${x y`, [][]string{{"${x", "y"}}, true},
		// A parameter expansion the tokenizer cannot parse is undecodable.
		{backslashEscapes, `${ x; }`, [][]string{{"${ x; }"}}, true},
		{backslashEscapes, `${x["0"]}`, [][]string{{"${x[0]}"}}, true},
		{backslashEscapes, `${x[\}]}`, [][]string{{"${x[}]}"}}, true},
		{backslashEscapes, `${x[$i]} ${x[i+1]} ${x[-1]}`, [][]string{{"${x[$i]}", "${x[i+1]}", "${x[-1]}"}}, false},
		{backslashEscapes, `${x&}`, [][]string{{"${x&}"}}, true},
		{backslashEscapes, `${#x:-a}`, [][]string{{"${#x:-a}"}}, true},
		{backslashEscapes, `${x[0]} ${!x} ${#x} ${x@Q} ${x[@]} ${!p*} ${x:1:2} ${#}`, [][]string{{"${x[0]}", "${!x}", "${#x}", "${x@Q}", "${x[@]}", "${!p*}", "${x:1:2}", "${#}"}}, false},
		// Inside double quotes a `${...}` is one group too, whose quotes nest.
		{backslashEscapes, `"${x:-"a b"}" c`, [][]string{{"${x:-a b}", "c"}}, false},
		{backslashEscapes, `"${x:-"}"}" c`, [][]string{{"${x:-}}", "c"}}, false},
	} {
		segments := tokenizeSegments(tc.mode, tc.command)
		var got [][]string
		undecodable := false
		for _, segment := range segments {
			got = append(got, segmentWords(segment))
			for _, token := range segment {
				undecodable = undecodable || token.undecodable
			}
		}
		if !slices.EqualFunc(got, tc.want, slices.Equal[[]string]) || undecodable != tc.undecodable {
			t.Errorf("tokenizeSegments(%v, %q) = %q undecodable=%v, want %q undecodable=%v", tc.mode, tc.command, got, undecodable, tc.want, tc.undecodable)
		}
	}
	// A word also carries its other literal readings. Each `$'...'` part read
	// as the text between its quotes, undecoded, is one. A parameter
	// expansion whose operator substitutes its word is another: the token
	// with that word, decoded as shell, in place of the expansion.
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`$'gi\t'`, []string{`gi\t`}},
		{`a$'\x67'b $'x'`, []string{`a\x67b`, ""}},
		{"echo # $'\ngi\\t push'", []string{"", "", "\ngi\\t push"}},
		{`$''`, []string{""}},
		{`'\t'`, []string{""}},
		// An unquoted word made only of expansions may also be removed: its
		// absent reading is the empty one after the `|`.
		{`${x:-git}`, []string{"git|"}},
		{`${x:-$'\x67it'}`, []string{`git|\x67it|${x:-\x67it}|`}},
		{`"${x:-$'\x67it'}"`, []string{`git|\x67it|${x:-\x67it}`}},
		{`/usr/bin/${x-g}${y:+i}t`, []string{"/usr/bin/git"}},
		{`${x=git} ${x:=git} ${x+git} ${x:+"g"'it'}`, []string{"git|", "git|", "git|", "git|"}},
		{`${a:-${b:-git}}`, []string{`git|`}},
		{`${x[0]:-git} ${!x-git} ${x[@]:+git}`, []string{"git|", "git|", "git|"}},
		// In double quotes the word is read in its quoting context: its own
		// quotes nest, `$"..."` is the quoted text and `'` is literal.
		{`"${x:-$"git"}"`, []string{"git"}},
		{`"${x:-$"g"it}"`, []string{"git"}},
		{`"${x:-"a b"'c'}"`, []string{"a b'c'"}},
		{`"${x:-git push}"`, []string{"git push"}},
		// Unquoted, the substituted word is split into words, none of them
		// an operator.
		{`${x:-git push}`, []string{"git,push|"}},
		{`${x:-a > f}`, []string{"a,>,f|"}},
		{`${a:-git} ${b:-push}`, []string{"git|", "push|"}},
		{`${x:?git} ${#x} ${x#git} ${x%git} ${x/a/git} ${x^} ${x,} ${x:1} ${x:-git`, []string{"", "", "", "", "", "", "", "", ""}},
	} {
		var got []string
		for _, segment := range tokenizeSegments(backslashEscapes, tc.command) {
			for _, token := range segment {
				var readings []string
				for _, alternate := range token.alternates {
					readings = append(readings, strings.Join(segmentWords(alternate), ","))
				}
				got = append(got, strings.Join(readings, "|"))
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("alternate readings of %q = %q, want %q", tc.command, got, tc.want)
		}
	}
	// Only a word made only of unquoted expansions and quoted `@` forms may
	// be removed, an empty quoted part beside a `@` form included; any other
	// quoted expansion is an empty word.
	for command, want := range map[string]bool{
		`$b`: true, `${b}`: true, `${b:+x}`: true, `$1$b`: true, "$b\\\n": true,
		`x$b`: false, `"$b"`: false, `'$b'`: false, `$(b)`: false, `$`: false,
		`"$@"`: true, `"${@:+x}"`: true, `"${e[@]}"`: true, `"${e[@]:+x}"`: true, `"${e[@]+x}"`: true,
		`"${!pre@}"`: true, `"${!e[@]}"`: true, `"$@""$@"`: true, `"$@"$b`: true, `"$@"""`: true,
		`"$@"''`: true, `"$@"$''`: true, `"$@"$""`: true, `"$@$@"`: true,
		`"$*"`: false, `"${e[*]}"`: false, `"${!pre*}"`: false, `"${e[@]:-}"`: false, `"${e[@]-}"`: false,
		`"${@:-}"`: false, `"${e[@]:=}"`: false, `"${#e[@]}"`: false, `x"$@"`: false, `"$@"x`: false,
		`"x$@"`: false, `""`: false, `""$b`: false, `"$@"'x'`: false, `"$@`: false, `"${@`: false,
	} {
		token := tokenizeSegments(backslashEscapes, command)[0][0]
		absent := slices.ContainsFunc(token.alternates, func(alternate []shellToken) bool { return len(alternate) == 0 })
		if absent != want {
			t.Errorf("%q has an absent reading = %v, want %v", command, absent, want)
		}
	}
	// munsuInvocations reads every word holding a space as shell again; an
	// undecodable word must not read back as itself, or the recursion never
	// ends.
	if got, _ := munsuInvocations("bash -c $'munsu watch \\q'", 0); len(got) != 1 || got[0].subcommand != "watch" {
		t.Errorf("munsuInvocations of an undecodable word = %+v, want the watch invocation", got)
	}
	// An empty quoted word is the --home value, so the bare watch after it
	// is the invocation, and it is not a guard or doctor call.
	if got, _ := munsuInvocations("munsu --home '' watch", 0); len(got) != 1 || got[0].subcommand != "watch" || len(got[0].following) != 0 {
		t.Errorf("munsuInvocations(munsu --home '' watch) = %+v, want a bare watch", got)
	}
	if onlyGuardOrDoctor("munsu '' guard .no-mistakes") {
		t.Errorf("onlyGuardOrDoctor(munsu '' guard) = true, want false")
	}
}

// TestShellWriteTargetsReadEmptyQuotedWords pins the write-guard verdicts an
// empty quoted word moved: it is a word, so it takes its operand position, and
// as a target it names the directory the command runs in (darwin cp writes
// there). Redirects and in-place sed with an empty suffix follow the same rule
// and over-refuse.
func TestShellWriteTargetsReadEmptyQuotedWords(t *testing.T) {
	base := mustAbsTestPath(t, "base")
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`echo x > ''`, []string{base}},
		{`echo x > ""`, []string{base}},
		{`echo x > $''`, []string{base}},
		{`cp a ''`, []string{base}},
		{`cp a ""`, []string{base}},
		{`cp -R a ''`, []string{base}},
		{`cp a $'' b`, []string{filepath.Join(base, "b")}},
		// `>''|` is a redirection and a pipe, not the `>|` clobber operator.
		{`echo x >''| tee f`, []string{base, filepath.Join(base, "f")}},
	} {
		got, ambiguous := shellWriteTargetsUnderForTest(backslashEscapes, base, tc.command)
		if ambiguous || !slices.Equal(got, tc.want) {
			t.Errorf("%q → %v ambiguous=%v, want %v", tc.command, got, ambiguous, tc.want)
		}
	}
}

// TestShellWriteEmptyCopyDestinationIntoBoundPrimaryRefused runs through the
// real hook: darwin cp writes an empty destination into the cwd, so a copy to an
// empty word from inside the shared primary checkout is a write into it.
func TestShellWriteEmptyCopyDestinationIntoBoundPrimaryRefused(t *testing.T) {
	primary, worktree := boundTaskFixture(t, "ship-shell-empty-dest")
	docs := filepath.Join(primary, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"cd " + docs + " && cp ../README.md ''",
		"cd " + docs + ` && cp ../README.md ""`,
		"cd " + docs + " && cp -R ../README.md ''",
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); !block {
			t.Errorf("%q: block=false (%s), want refused", command, reason)
		}
	}
}

// TestShellWriteTargetsReadContinuationAndANSICQuoting pins the write-guard
// verdicts the tokenizer change moved: a continued target names the joined
// path, and a `$'...'` target names its decoded path instead of being dropped
// as an expansion.
func TestShellWriteTargetsReadContinuationAndANSICQuoting(t *testing.T) {
	base := mustAbsTestPath(t, "base")
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"echo x > f\\\noo", []string{filepath.Join(base, "foo")}},
		{`echo x > $'f\x6fo'`, []string{filepath.Join(base, "foo")}},
		{`echo x > $'\u0066'`, nil},
	} {
		got, ambiguous := shellWriteTargetsUnderForTest(backslashEscapes, base, tc.command)
		if ambiguous || !slices.Equal(got, tc.want) {
			t.Errorf("%q → %v ambiguous=%v, want %v", tc.command, got, ambiguous, tc.want)
		}
	}
}

// TestShellWriteTargetsReadParameterExpansionWords pins the write targets of
// a parameter expansion: the words bash substitutes when the parameter is
// unset, split when unquoted, are targets at the expansion's position, and
// the expansion as written is not one. The raw reading of an ANSI-C word is
// not.
func TestShellWriteTargetsReadParameterExpansionWords(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		command string
		want    []string
	}{
		// The removed reading of an expansion that cannot be empty still
		// names a target: fail closed.
		{`cp a ${x:-b c}`, []string{"c", "a"}},
		{`rm ${x:-b c}`, []string{"b", "c"}},
		{`rm "${x:-b c}"`, []string{"b c"}},
		{`echo ${x:-a > f}`, nil},
		{`echo ${x#a > f}`, nil},
		{`echo ${x#a; touch f #}`, nil},
		// Every combination of candidates is read.
		{`${a:-rm} ${b:-c}`, []string{"c"}},
		{`cp ${a:--t} ${b:-d} e`, []string{"e", "d"}},
		{`echo ${x:-a b}`, nil},
		{`rm ${x:-$HOME/f}`, nil},
		{`touch $'\x41'`, []string{"A"}},
	} {
		var want []string
		for _, target := range tc.want {
			want = append(want, filepath.Join(dir, target))
		}
		if targets, ambiguous := shellWriteTargets(dir, tc.command); ambiguous || !slices.Equal(targets, want) {
			t.Errorf("shellWriteTargets(%q) = %q ambiguous=%v, want %q", tc.command, targets, ambiguous, want)
		}
	}
}

// TestShellWriteTargetsRefuseTooManyCandidateReadings pins the bound on the
// write guard's candidate combinations: past it the targets are ambiguous.
// A defaulted expansion has three readings: as written, substituted, absent.
func TestShellWriteTargetsRefuseTooManyCandidateReadings(t *testing.T) {
	dir := t.TempDir()
	command := "rm"
	for i := range 6 {
		command += fmt.Sprintf(" ${x%d:-f%d}", i, i)
	}
	if _, ambiguous := shellWriteTargets(dir, command); !ambiguous {
		t.Errorf("shellWriteTargets(%q): ambiguous=false, want true", command)
	}
	if _, ambiguous := shellWriteTargets(dir, strings.Replace(command, " ${x5:-f5}", "", 1)); ambiguous {
		t.Errorf("shellWriteTargets with 5 expansions: ambiguous=true, want false")
	}
	// Only a segment that can write is read: no candidate here is a write
	// verb or a redirect.
	for _, command := range []string{
		strings.Replace(command, "rm", "echo", 1),
		"echo $a $b $c $d $e $f $g $h $i",
		"rm f; echo $a $b $c $d $e $f $g $h $i",
	} {
		if _, ambiguous := shellWriteTargets(dir, command); ambiguous {
			t.Errorf("shellWriteTargets(%q): ambiguous=true, want false", command)
		}
	}
	for _, command := range []string{"rm $a $b $c $d $e $f $g $h $i", "${v:-rm} $a $b $c $d $e $f $g $h", "echo $a $b $c $d $e $f $g $h > $i"} {
		if _, ambiguous := shellWriteTargets(dir, command); !ambiguous {
			t.Errorf("shellWriteTargets(%q): ambiguous=false, want true", command)
		}
	}
}
