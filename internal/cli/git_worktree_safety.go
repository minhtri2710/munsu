package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
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
		agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, taskID)
		if err != nil {
			return true, "git mutation worktree binding unavailable: " + err.Error()
		}
		if !ok || agg.Worktree == nil {
			if staleBindingExists(homeDir, taskID, agg) {
				return true, "stale generation: worktree binding is not on current task generation"
			}
			return true, "git mutation requires active worktree binding"
		}
		binding := agg.Worktree
		if binding.TaskGeneration != agg.Generation {
			return true, fmt.Sprintf("stale generation: binding=%s current=%s", binding.TaskGeneration, agg.Generation)
		}
		if !home.TaskWorktreeLeaseActive(homeDir, taskID, *binding) {
			return true, "recycled lease: worktree binding lease no longer matches active task"
		}
		if reason := validateGitTargetBinding(parsed, binding); reason != "" {
			return true, reason
		}
		if reason := validateGitMutationAuthority(homeDir, taskID, parsed, binding); reason != "" {
			return true, reason
		}
	}
	return false, ""
}

func staleBindingExists(homeDir, taskID string, current *home.TaskAggregate) bool {
	aggregates, err := home.ListTaskAggregates(homeDir)
	if err != nil {
		return false
	}
	currentGeneration := ""
	if current != nil {
		currentGeneration = current.Generation
	}
	for _, agg := range aggregates {
		if agg.TaskID == taskID && agg.Generation != currentGeneration && agg.Worktree != nil {
			return true
		}
	}
	return false
}

func validateGitMutationAuthority(homeDir, taskID string, g gitCommandSafety, binding *home.TaskWorktreeBinding) string {
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
		if containsGitForce(g.args) {
			return "force push is not allowed"
		}
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
	segments := splitSafetySegments(command)
	if len(segments) == 0 {
		return gitCommandSafety{}, nil
	}
	for _, segment := range segments {
		args := splitSafetyWords(segment)
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
				g.targetPath = resolveSafetyPath(g.targetPath, gitArgs[1])
				gitArgs = gitArgs[2:]
			case strings.HasPrefix(arg, "-C") && len(arg) > 2:
				g.targetPath = resolveSafetyPath(g.targetPath, arg[2:])
				gitArgs = gitArgs[1:]
			case arg == "--git-dir" && len(gitArgs) > 1:
				g.gitDir = resolveSafetyPath(g.targetPath, gitArgs[1])
				gitArgs = gitArgs[2:]
			case strings.HasPrefix(arg, "--git-dir="):
				g.gitDir = resolveSafetyPath(g.targetPath, strings.TrimPrefix(arg, "--git-dir="))
				gitArgs = gitArgs[1:]
			case arg == "--work-tree" && len(gitArgs) > 1:
				g.workTree = resolveSafetyPath(g.targetPath, gitArgs[1])
				g.targetPath = g.workTree
				gitArgs = gitArgs[2:]
			case strings.HasPrefix(arg, "--work-tree="):
				g.workTree = resolveSafetyPath(g.targetPath, strings.TrimPrefix(arg, "--work-tree="))
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

func splitSafetyWords(segment string) []string {
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
		if r == '\\' {
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

func validateGitTargetBinding(g gitCommandSafety, binding *home.TaskWorktreeBinding) string {
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
	if g.gitDir != "" && canonicalSafetyPathRuntime(g.gitDir) != binding.GitDir {
		return "wrong repository: --git-dir does not match binding"
	}
	if g.workTree != "" && canonicalSafetyPathRuntime(g.workTree) != binding.Path {
		return "git mutation --work-tree does not match bound worktree path"
	}
	if binding.RepositoryIdentity != binding.CommonDir {
		return "wrong repository: repository identity does not match binding"
	}
	return ""
}

func shipGitCommandAllowed(taskID string, g gitCommandSafety) bool {
	taskBranch := "mu/" + taskID
	switch g.verb {
	case "add", "commit":
		return true
	case "branch":
		return branchOpAllowed(taskBranch, g.args)
	case "checkout", "switch":
		return createsBranch(g.args) && g.branchName == taskBranch
	case "push":
		if containsGitForce(g.args) {
			return false
		}
		if len(g.args) == 0 || g.args[0] != "origin" {
			return false
		}
		return g.pushRefspec == taskBranch || g.pushRefspec == "HEAD:refs/heads/"+taskBranch || g.pushRefspec == "HEAD:"+taskBranch || g.pushRefspec == "HEAD"
	default:
		return false
	}
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

func containsGitForce(args []string) bool {
	for _, arg := range args {
		if arg == "--force" || arg == "-f" || strings.HasPrefix(arg, "--force-") {
			return true
		}
	}
	return false
}

func gitSafetyIdentity(path string) (string, string, string, error) {
	gitDir, err := gitSafetyOutput(path, "rev-parse", "--git-dir")
	if err != nil {
		return "", "", "", err
	}
	commonDir, err := gitSafetyOutput(path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", "", err
	}
	gitDir = canonicalSafetyPathRuntime(resolveSafetyPath(path, gitDir))
	commonDir = canonicalSafetyPathRuntime(resolveSafetyPath(path, commonDir))
	if gitDir == commonDir {
		return "primary", gitDir, commonDir, nil
	}
	return "worktree", gitDir, commonDir, nil
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
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
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

func splitSafetySegments(command string) []string {
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
		if r == '\\' {
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
	fields := strings.Fields(segment)
	if len(fields) == 0 || fields[0] != "cd" {
		return "", false
	}
	if len(fields) < 2 {
		return currentPath, true
	}
	return resolveSafetyPath(currentPath, fields[1]), true
}
