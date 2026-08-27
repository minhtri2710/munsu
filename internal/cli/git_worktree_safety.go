package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type gitCommandSafety struct {
	isGit       bool
	mutating    bool
	verb        string
	args        []string
	targetPath  string
	gitDir      string
	workTree    string
	branchName  string
	pushRefspec string
}

func evaluateGitMutationSafety(checkPath, command string) (bool, string) {
	homeDir := strings.TrimSpace(os.Getenv("MUNSU_HOME"))
	taskID := strings.TrimSpace(os.Getenv("MUNSU_TASK_ID"))
	segments := splitSafetySegments(command)
	currentPath := checkPath
	for _, rawSegment := range segments {
		segment := strings.TrimSpace(rawSegment)
		if segment == "" {
			continue
		}
		if hasGitCommandSubstitution(segment) {
			return true, "compound shell command with command substitution is not allowed for git mutation"
		}
		if nextPath, ok := cdSegmentPath(currentPath, segment); ok {
			currentPath = nextPath
			continue
		}
		parsed, err := parseGitSafetyCommand(currentPath, segment)
		if err != nil {
			return true, err.Error()
		}
		if !parsed.isGit || !parsed.mutating {
			continue
		}
		if homeDir == "" || taskID == "" {
			return true, "git mutation requires active munsu task worktree binding"
		}
		// The worktree binding is read from the canonical Task Authority
		// (current-truth only): the v1 aggregate store is gone, and an
		// uninitialized home fails closed. Current truth never carries a
		// stale generation's binding, so a mutation target on a superseded
		// generation is refused by the binding checks below.
		auth, err := taskAuthorityForRead(homeDir)
		if err != nil {
			return true, "git mutation worktree binding unavailable: " + err.Error()
		}
		tid, err := domain.NewTaskID(taskID)
		if err != nil {
			return true, "git mutation worktree binding unavailable: " + err.Error()
		}
		agg, err := auth.Get(tid)
		if err != nil {
			return true, "git mutation worktree binding unavailable: " + err.Error()
		}
		if agg.Worktree == nil {
			return true, "git mutation requires active worktree binding"
		}
		binding := agg.Worktree
		if reason := validateGitTargetBinding(parsed, binding); reason != "" {
			return true, reason
		}
		if reason := validateGitMutationAuthority(homeDir, taskID, parsed, binding); reason != "" {
			return true, reason
		}
	}
	return false, ""
}

// validateGitMutationAuthority enforces the default Ship authority allowlist:
// task-local branch, add, commit, and normal push. The git authorization
// layer (amendment/retirement context tiers, force-with-lease authorization)
// was removed with the legacy delivery path (#414 B); unrestricted force,
// branch deletion, rewrite operations, and push --delete are unconditionally
// denied.
func validateGitMutationAuthority(homeDir, taskID string, g gitCommandSafety, binding *taskauthority.WorktreeBinding) string {
	taskBranch := "mu/" + taskID
	currentBranch, err := gitSafetyOutput(binding.Path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "git mutation branch unavailable: " + err.Error()
	}
	currentBranch = strings.TrimSpace(currentBranch)
	if currentBranch == "HEAD" {
		if g.verb == "branch" && branchOpAllowed(taskBranch, g.args) {
			if head, err := gitSafetyOutput(binding.Path, "rev-parse", "HEAD"); err != nil || head != binding.Head {
				return "unexpected head: bound worktree is not at the recorded base HEAD"
			}
			return ""
		}
		if (g.verb == "checkout" || g.verb == "switch") && createsBranch(g.args) && g.branchName == taskBranch {
			if head, err := gitSafetyOutput(binding.Path, "rev-parse", "HEAD"); err != nil || head != binding.Head {
				return "unexpected head: bound worktree is not at the recorded base HEAD"
			}
			return ""
		}
		return "unexpected head: bound worktree is detached from the task-local branch"
	}
	if currentBranch != taskBranch {
		return "unexpected head: bound worktree is not on the task-local branch"
	}

	switch g.verb {
	case "add", "commit":
		return ""
	case "branch":
		if branchOpAllowed(taskBranch, g.args) {
			return ""
		}
		return "default Ship authority permits only task-local branch, add, commit, and normal push"
	case "checkout", "switch":
		if createsBranch(g.args) && g.branchName == taskBranch {
			return ""
		}
		return "default Ship authority permits only task-local branch, add, commit, and normal push"
	case "push":
		if len(g.args) == 0 || g.args[0] != "origin" {
			return "default Ship authority permits only task-local branch, add, commit, and normal push"
		}
		if g.pushRefspec == taskBranch || g.pushRefspec == "HEAD:refs/heads/"+taskBranch || g.pushRefspec == "HEAD:"+taskBranch || g.pushRefspec == "HEAD" {
			return ""
		}
		return "default Ship authority permits only task-local branch, add, commit, and normal push"
	default:
		return "default Ship authority permits only task-local branch, add, commit, and normal push"
	}
}

func parseGitSafetyCommand(checkPath, command string) (gitCommandSafety, error) {
	return parseGitSafetyCommandWithMode(checkPath, command, gitSafetyBackslashMode())
}

func parseGitSafetyCommandWithMode(checkPath, command string, mode backslashMode) (gitCommandSafety, error) {
	segments := splitSafetySegmentsWithMode(mode, command)
	if len(segments) == 0 {
		return gitCommandSafety{}, nil
	}
	for _, segment := range segments {
		args := splitSafetyWordsWithMode(mode, segment)
		if len(args) == 0 {
			continue
		}
		idx := -1
		for i, arg := range args {
			base := filepath.Base(arg)
			if base == "git" || strings.HasSuffix(base, "/git") {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		g := gitCommandSafety{isGit: true, targetPath: checkPath}
		gitArgs := args[idx+1:]
		for len(gitArgs) > 0 {
			arg := gitArgs[0]
			switch {
			case arg == "-C" && len(gitArgs) > 1:
				g.targetPath = resolveSafetyPathWithMode(g.targetPath, gitArgs[1], mode)
				gitArgs = gitArgs[2:]
			case strings.HasPrefix(arg, "-C") && len(arg) > 2:
				g.targetPath = resolveSafetyPathWithMode(g.targetPath, arg[2:], mode)
				gitArgs = gitArgs[1:]
			case arg == "--git-dir" && len(gitArgs) > 1:
				g.gitDir = resolveSafetyPathWithMode(g.targetPath, gitArgs[1], mode)
				gitArgs = gitArgs[2:]
			case strings.HasPrefix(arg, "--git-dir="):
				g.gitDir = resolveSafetyPathWithMode(g.targetPath, strings.TrimPrefix(arg, "--git-dir="), mode)
				gitArgs = gitArgs[1:]
			case arg == "--work-tree" && len(gitArgs) > 1:
				g.workTree = resolveSafetyPathWithMode(g.targetPath, gitArgs[1], mode)
				g.targetPath = g.workTree
				gitArgs = gitArgs[2:]
			case strings.HasPrefix(arg, "--work-tree="):
				g.workTree = resolveSafetyPathWithMode(g.targetPath, strings.TrimPrefix(arg, "--work-tree="), mode)
				g.targetPath = g.workTree
				gitArgs = gitArgs[1:]
			case strings.HasPrefix(arg, "-"):
				gitArgs = gitArgs[1:]
			default:
				g.verb = arg
				g.args = gitArgs[1:]
				fillGitCommandDetails(&g)
				return g, nil
			}
		}
	}
	return gitCommandSafety{}, nil
}

func splitSafetyWordsWithMode(mode backslashMode, segment string) []string {
	var args []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			args = append(args, b.String())
			b.Reset()
		}
	}
	for _, r := range segment {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && mode == backslashEscapes {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return args
}

func fillGitCommandDetails(g *gitCommandSafety) {
	switch g.verb {
	case "add", "commit", "checkout", "switch", "push", "merge", "rebase", "reset", "restore", "rm", "mv", "clean", "tag", "cherry-pick", "revert", "worktree":
		g.mutating = true
	case "branch":
		g.mutating = branchCommandWrites(g.args)
	}
	if g.verb == "checkout" || g.verb == "switch" {
		for i := 0; i < len(g.args); i++ {
			if (g.args[i] == "-b" || g.args[i] == "-B" || g.args[i] == "-c" || g.args[i] == "-C") && i+1 < len(g.args) {
				g.branchName = g.args[i+1]
				return
			}
		}
		for _, arg := range g.args {
			if !strings.HasPrefix(arg, "-") {
				g.branchName = arg
				return
			}
		}
	}
	if g.verb == "push" {
		for i := len(g.args) - 1; i >= 0; i-- {
			arg := g.args[i]
			if strings.HasPrefix(arg, "-") || arg == "origin" {
				continue
			}
			g.pushRefspec = arg
			return
		}
	}
}

func validateGitExplicitTargetBinding(g gitCommandSafety, binding *taskauthority.WorktreeBinding) string {
	gitDir := ""
	if g.gitDir != "" {
		gitDir = canonicalSafetyPathRuntime(g.gitDir)
	}
	workTree := ""
	if g.workTree != "" {
		workTree = canonicalSafetyPathRuntime(g.workTree)
	}
	return validateCanonicalGitExplicitTargetBinding(gitDir, workTree, binding)
}

func validateCanonicalGitExplicitTargetBinding(gitDir, workTree string, binding *taskauthority.WorktreeBinding) string {
	if gitDir != "" && gitDir != binding.GitDir {
		return "wrong repository: --git-dir does not match binding"
	}
	if workTree != "" && workTree != binding.Path {
		return "git mutation --work-tree does not match bound worktree path"
	}
	return ""
}

func validateGitTargetBinding(g gitCommandSafety, binding *taskauthority.WorktreeBinding) string {
	identity, gitDir, commonDir, err := gitSafetyIdentity(g.targetPath)
	if err != nil {
		return "git mutation target unavailable: " + err.Error()
	}
	if identity == "primary" {
		return "primary checkout refused for git mutation"
	}
	if identity != "worktree" {
		return "git mutation target is not the bound worktree"
	}
	root, err := gitSafetyOutput(g.targetPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "git mutation target unavailable: " + err.Error()
	}
	root = canonicalSafetyPathRuntime(root)
	if root != binding.Path {
		if commonDir != binding.CommonDir {
			return "wrong repository: git mutation target does not match bound repository"
		}
		return "git mutation target does not match bound worktree path"
	}
	if gitDir != binding.GitDir || commonDir != binding.CommonDir {
		return "wrong repository: git mutation git-dir/common-dir do not match binding"
	}
	if reason := validateGitExplicitTargetBinding(g, binding); reason != "" {
		return reason
	}
	if binding.RepositoryIdentity != binding.CommonDir {
		return "wrong repository: repository identity does not match binding"
	}
	return ""
}

func branchCommandWrites(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if arg == "-d" || arg == "-D" || arg == "-m" || arg == "-M" || arg == "-c" || arg == "-C" {
			return true
		}
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return true
		}
	}
	return false
}

func branchOpAllowed(taskBranch string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "-d" || args[0] == "-D" || args[0] == "-m" || args[0] == "-M" {
		return false
	}
	for _, arg := range args {
		if arg == taskBranch {
			return true
		}
	}
	return false
}

func createsBranch(args []string) bool {
	for _, arg := range args {
		if arg == "-b" || arg == "-B" || arg == "-c" || arg == "-C" {
			return true
		}
	}
	return false
}

// gitSafetyIdentity classifies the git mutation target. Classification itself
// has one owner — fleet.ClassifyIdentity (ADR-0009) — so this only renders the
// verdict as the string the safety gate compares against, and the git-dir and
// common-dir it returns are canonicalized by exactly the same code that
// produced the binding it is checked against.
func gitSafetyIdentity(path string) (string, string, string, error) {
	identity, gitDir, commonDir, err := fleet.ClassifyIdentity(path)
	if err != nil {
		return "", "", "", err
	}
	if identity != fleet.Primary && identity != fleet.Worktree {
		return identity.String(), "", "", nil
	}
	return identity.String(), gitDir, commonDir, nil
}

func gitSafetyOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func resolveSafetyPath(base, path string) string {
	return resolveSafetyPathWithMode(base, path, gitSafetyBackslashMode())
}

func resolveSafetyPathWithMode(base, path string, mode backslashMode) string {
	if filepath.IsAbs(path) || (mode == backslashLiteral && isWindowsAbsolutePath(path)) {
		return path
	}
	return filepath.Join(base, path)
}

func isWindowsAbsolutePath(path string) bool {
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\\\`)
}

func canonicalSafetyPathRuntime(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(resolved)
}

func gitSafetyBackslashMode() backslashMode {
	if runtime.GOOS == "windows" {
		return backslashLiteral
	}
	return backslashEscapes
}

func splitSafetySegments(command string) []string {
	return splitSafetySegmentsWithMode(gitSafetyBackslashMode(), command)
}

func splitSafetySegmentsWithMode(mode backslashMode, command string) []string {
	segments := []string{}
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			segments = append(segments, s)
		}
		b.Reset()
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && mode == backslashEscapes {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ';', '&', '|', '\n':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return segments
}

func hasGitCommandSubstitution(command string) bool {
	return strings.Contains(command, "$(") || strings.Contains(command, "`")
}

func cdSegmentPath(currentPath, segment string) (string, bool) {
	return cdSegmentPathWithMode(gitSafetyBackslashMode(), currentPath, segment)
}

func cdSegmentPathWithMode(mode backslashMode, currentPath, segment string) (string, bool) {
	fields := splitSafetyWordsWithMode(mode, segment)
	if len(fields) == 0 || fields[0] != "cd" {
		return "", false
	}
	if len(fields) < 2 {
		return currentPath, true
	}
	return resolveSafetyPath(currentPath, fields[1]), true
}
