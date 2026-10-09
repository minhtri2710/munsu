package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type gitCommandSafety struct {
	isGit      bool
	mutating   bool
	verb       string
	args       []string
	targetPath string
	gitDir     string
	workTree   string
	branchName string
}

// evaluateGitMutationSafety is the shell-string entry to the git fence. It
// reads the command with the same tokenizer as the write guard: heredoc bodies
// are stripped, then segments and words keep their quoting, so a quoted space
// stays inside one word and a quoted `&&` never ends a segment.
//
// Command substitution is checked on the raw command, before stripping: a
// heredoc with an unquoted delimiter still runs `$(...)` and backticks in its
// body, and the tokenizer does not parse substitutions into words.
//
// The guard does not guess which command consumes a string. Every string
// payload — a quoted or escaped word, a heredoc body, a here-string — is read
// as shell by this same guard, whatever receives it, so git text handed to
// any interpreter, script runner or file is refused as if typed directly. A
// payload it cannot recover is refused.
func evaluateGitMutationSafety(checkPath, command string) (bool, string) {
	homeDir := strings.TrimSpace(os.Getenv("MUNSU_HOME"))
	taskID := strings.TrimSpace(os.Getenv("MUNSU_TASK_ID"))
	mode := gitSafetyBackslashMode()
	return evaluateGitScriptSafety(homeDir, taskID, command, 0, namesIFS(mode, command, 0), &gitShell{path: checkPath, functions: &shellFunctions{}})
}

// namesIFS reports whether any decoded word of command names IFS, at every
// payload depth the git guard reads: every candidate word of each token
// (both readings of an ANSI-C word and each substituted word included), and
// every heredoc body, here-string and word read again as shell. An IFS set
// at one depth reaches a payload the same shell runs (eval, source), so the
// guard reads it once for the whole command. A name built from a variable's
// value is not read.
func namesIFS(mode backslashMode, command string, depth int) bool {
	stripped, feeds := splitHeredocBodies(command)
	var payloads []string
	for _, feed := range feeds {
		payloads = append(payloads, feed.text)
	}
	for _, segment := range tokenizeSegments(mode, stripped) {
		for _, token := range candidateTokens(segment) {
			if strings.Contains(token.text, "IFS") {
				return true
			}
			if readsAsMoreThanItself(mode, token) {
				payloads = append(payloads, token.text)
			}
		}
	}
	if depth < maxShellPayloadDepth {
		for _, payload := range payloads {
			if namesIFS(mode, payload, depth+1) {
				return true
			}
		}
	}
	return false
}

// maxShellPayloadDepth bounds how many payload layers the git guard reads
// through before it refuses.
const maxShellPayloadDepth = 4

// ifs reports that the whole command names IFS (namesIFS): bash then splits
// an unquoted parameter expansion's word at an IFS the guard cannot know.
//
// shell is the shell command runs in, from shell.path. eval runs its payload
// in this shell, so the payload shares shell: its directory moves and its
// definitions reach the segments after it. Every other payload, each word
// read again as shell among them, starts from the directory it stands in; its
// directory moves do not return, but its definitions join the command-wide
// function set (evaluateGitPayloadSafety). A call to a
// function whose body moves the directory leaves it unknown.
func evaluateGitScriptSafety(homeDir, taskID, command string, depth int, ifs bool, shell *gitShell) (bool, string) {
	mode := gitSafetyBackslashMode()
	stripped, feeds := splitHeredocBodies(command)
	var payloads []string
	for _, feed := range feeds {
		if !feed.terminated {
			return true, "heredoc body is unterminated; git mutation cannot be checked"
		}
		payloads = append(payloads, feed.text)
	}
	// A heredoc body runs wherever its command runs, so it is read from every
	// directory the command line visits. A directory the guard cannot follow
	// is "": a git mutation there, or in a payload read from it, is refused.
	paths := []string{shell.path}
	// subshells are the states to return to at each open subshell's `)`.
	type shellDir struct {
		path, previous string
		stack          *dirStack[string]
		head           []shellToken
		moves          int
		calls          int
	}
	var subshells []shellDir
	segments := tokenizeSegments(mode, stripped)
	for _, segment := range segments {
		if segment[0].unmodeledFunctionHead {
			return true, "function name is not modeled; git mutation cannot be checked"
		}
	}
	if hasGitCommandSubstitution(command) {
		return true, "compound shell command with command substitution is not allowed for git mutation"
	}
	for _, segment := range segments {
		if segment[0].invalidFunctionBody {
			return true, "function head has no valid bash body; git mutation cannot be checked"
		}
		if segment[0].unfinished {
			return true, "shell case is unfinished; git mutation cannot be checked"
		}
		if segment[0].subshell {
			if segment[0].text == "(" {
				opened := shellDir{shell.path, shell.previous, shell.stack, nil, shell.moves, len(shell.functions.commandCall)}
				if segment[0].functionBodyHead != nil {
					opened.head = segment[0].functionBodyHead
				}
				subshells = append(subshells, opened)
			} else if n := len(subshells); n > 0 {
				closed := subshells[n-1]
				shell.path, shell.previous, shell.stack = closed.path, closed.previous, closed.stack
				subshells = subshells[:n-1]
				if closed.head != nil {
					shell.functions.finishDefinition(functionName(closed.head), closed.calls, shellFunction{moves: shell.moves != closed.moves})
					shell.moves = closed.moves
				}
			}
			continue
		}
		for _, token := range segment {
			if token.undecodable || (ifs && token.splitsAtIFS) {
				return true, "shell word cannot be decoded; git mutation cannot be checked"
			}
		}
		// Every candidate word of a token is read again as shell, and the
		// verb classifier reads every candidate at each position: the
		// tokenizer cannot tell whether bash starts ANSI-C quoting at a `$'`,
		// or which word a parameter expansion substitutes.
		reread := make(map[string]bool)
		for _, token := range candidateTokens(segment) {
			if reread[token.text] || !readsAsMoreThanItself(mode, token) {
				continue
			}
			reread[token.text] = true
			if blocked, reason := evaluateGitPayloadSafety(homeDir, taskID, shell.path, token.text, depth, ifs, shell); blocked {
				return true, reason
			}
		}
		for _, command := range segmentGitCommands(shell.path, segment, mode) {
			if shell.path == "" {
				return true, "shell directory cannot be determined; git mutation cannot be checked"
			}
			if command.ambiguous {
				return true, "git mutation target cannot be determined"
			}
			if blocked, reason := evaluateParsedGitMutation(homeDir, taskID, command.g); blocked {
				return true, reason
			}
		}
		evals, ok := evalPayloads(mode, segment)
		if !ok {
			return true, "shell word has too many readings; git mutation cannot be checked"
		}
		if blocked, reason := evaluateGitEvalSafety(homeDir, taskID, evals, depth, ifs, shell); blocked {
			return true, reason
		}
		move, operand := segmentDirMove(segment)
		if shell.functions.call(segment) && move == moveNone {
			move = moveCalled
		}
		if move != moveNone {
			shell.moves++
		}
		switch move {
		case moveNone:
			if len(evals) == 0 {
				continue
			}
		case moveCd, movePush:
			if move == movePush {
				shell.stack = &dirStack[string]{top: shell.path, next: shell.stack}
			}
			shell.previous, shell.path = shell.path, gitCdPath(mode, shell.path, operand.text)
		case movePrevious:
			shell.previous, shell.path = shell.path, shell.previous
		case movePop:
			next := ""
			if shell.stack != nil {
				next, shell.stack = shell.stack.top, shell.stack.next
			}
			shell.previous, shell.path = shell.path, next
		case moveStack:
			shell.stack = nil
			continue
		case moveUnknown:
			shell.previous, shell.path, shell.stack = shell.path, "", nil
		case moveCalled:
			shell.previous, shell.path, shell.stack = "", "", nil
		}
		paths = append(paths, shell.path)
	}
	for _, payload := range payloads {
		for _, path := range paths {
			if blocked, reason := evaluateGitPayloadSafety(homeDir, taskID, path, payload, depth, ifs, shell); blocked {
				return true, reason
			}
		}
	}
	return false, ""
}

// gitShell is the shell state a git guard walk reads a command line in: the
// directory it stands in ("" when unknown), the one `cd -` returns to and the
// stack pushd built, the directory moves read, and the functions defined.
type gitShell struct {
	path, previous string
	stack          *dirStack[string]
	moves          int
	functions      *shellFunctions
}

// evaluateGitPayloadSafety reads payload as a shell of its own that starts in
// path, with no previous directory or stack, sharing shell's command-wide function set.
func evaluateGitPayloadSafety(homeDir, taskID, path, payload string, depth int, ifs bool, shell *gitShell) (bool, string) {
	if depth+1 > maxShellPayloadDepth {
		return true, "shell payload nesting is too deep; git mutation cannot be checked"
	}
	calls := len(shell.functions.commandCall)
	defer func() { shell.functions.commandCall = shell.functions.commandCall[:calls] }()
	return evaluateGitScriptSafety(homeDir, taskID, payload, depth+1, ifs, &gitShell{path: path, functions: shell.functions})
}

// evaluateGitEvalSafety reads the payloads eval runs in shell. One payload
// is read in shell itself. When the segment's readings give eval more than
// one, each is read from a copy of the directory state, and one that moves
// the directory leaves it unknown.
func evaluateGitEvalSafety(homeDir, taskID string, evals []shellPayload, depth int, ifs bool, shell *gitShell) (bool, string) {
	if len(evals) > 0 && depth+1 > maxShellPayloadDepth {
		return true, "shell payload nesting is too deep; git mutation cannot be checked"
	}
	if len(evals) == 1 {
		return evaluateGitScriptSafety(homeDir, taskID, evals[0].text, depth+1, ifs, shell)
	}
	moved := false
	for _, eval := range evals {
		copied := *shell
		if blocked, reason := evaluateGitScriptSafety(homeDir, taskID, eval.text, depth+1, ifs, &copied); blocked {
			return true, reason
		}
		moved = moved || copied.moves != shell.moves || copied.path != shell.path
	}
	if moved {
		shell.previous, shell.path, shell.stack = "", "", nil
		shell.moves++
	}
	return false, ""
}

// readsAsMoreThanItself reports whether token, read again as shell, is
// anything but the one plain word it is: quoting or escaping hid a space, an
// operator or another quote inside it, or a literal word holds a parameter
// expansion that substitutes a word. An expandable word is not read again for
// its expansions, which are already its alternates. A plain word ends the
// recursion.
func readsAsMoreThanItself(mode backslashMode, token shellToken) bool {
	segments := tokenizeSegments(mode, token.text)
	// A word holding a substitution that does not end reads again as itself
	// and its unfinished rest.
	if n := len(segments); n == 2 && segments[1][0].unfinished {
		segments = segments[:1]
	}
	if len(segments) != 1 || len(segments[0]) != 1 || segments[0][0].text != token.text {
		return true
	}
	return !token.expandable && len(segments[0][0].alternates) > 0
}

// segmentWords returns the text of each token in a segment.
func segmentWords(segment []shellToken) []string {
	words := make([]string, len(segment))
	for i, token := range segment {
		words[i] = token.text
	}
	return words
}

// evaluateGitArgvSafety is the argv entry to the same fence the string path
// enforces, reached by the git shim on the soldier/captain launch PATH. The
// shell has already expanded and word-split the command, so there is no shell
// front-end here: the shim hands the git arguments (the tokens after the git
// executable) straight to the token walk. This is the reason the shim closes
// the shell-wrapper residual the hook path leaves open — `sh -c 'git push
// --force'` reaches the real git through the shim's PATH entry, so its argv is
// evaluated like any other. MUNSU_HOME/MUNSU_TASK_ID come from the environment
// the launch script exported.
func evaluateGitArgvSafety(checkPath string, gitArgs []string) (bool, string) {
	homeDir := strings.TrimSpace(os.Getenv("MUNSU_HOME"))
	taskID := strings.TrimSpace(os.Getenv("MUNSU_TASK_ID"))
	parsed := walkGitArgs(checkPath, gitArgs, gitSafetyBackslashMode())
	if !parsed.mutating {
		return false, ""
	}
	return evaluateParsedGitMutation(homeDir, taskID, parsed)
}

// evaluateParsedGitMutation is the shared core reached once a mutating git
// invocation has been parsed to a gitCommandSafety. Both entries reach it: the
// string path (evaluateGitMutationSafety, after the shell front-end) and the
// argv path (evaluateGitArgvSafety, from the git shim). The worktree binding is
// read from the canonical Task Authority (current-truth only): the v1 aggregate
// store is gone, and an uninitialized home fails closed. Current truth never
// carries a stale generation's binding, so a mutation target on a superseded
// generation is refused by the binding checks below.
func evaluateParsedGitMutation(homeDir, taskID string, parsed gitCommandSafety) (bool, string) {
	if homeDir == "" || taskID == "" {
		return true, "git mutation requires active munsu task worktree binding"
	}
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
	if reason := validateGitMutationAuthority(homeDir, taskID, parsed, binding, auth, tid); reason != "" {
		return true, reason
	}
	return false, ""
}

// validateGitMutationAuthority enforces branch, task-local edits, and exact-head
// granted pushes. Force/delete/rewrite forms remain unconditionally denied.
func validateGitMutationAuthority(homeDir, taskID string, g gitCommandSafety, binding *taskauthority.WorktreeBinding, auth *taskauthority.Canonical, tid domain.TaskID) string {
	taskBranch := "mu/" + taskID
	currentBranch, err := gitSafetyOutput(binding.Path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "git mutation branch unavailable: " + err.Error()
	}
	currentBranch = strings.TrimSpace(currentBranch)
	if currentBranch == "HEAD" {
		if g.verb == "branch" && branchOpAllowed(taskBranch, g.args) {
			if head, err := gitSafetyOutput(binding.Path, "rev-parse", "HEAD"); err != nil || head != binding.BaseHead {
				return "unexpected head: bound worktree is not at the recorded base HEAD"
			}
			return ""
		}
		if (g.verb == "checkout" || g.verb == "switch") && createsBranch(g.args) && g.branchName == taskBranch {
			if head, err := gitSafetyOutput(binding.Path, "rev-parse", "HEAD"); err != nil || head != binding.BaseHead {
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
	case "checkout", "switch":
		if createsBranch(g.args) && g.branchName == taskBranch {
			return ""
		}
	case "push":
		remote, refspec, allowed := pushTargetAllowed(taskBranch, g.args)
		if !allowed {
			return "default Ship authority permits only task-local branch, add, commit, and granted push"
		}
		if remote == "no-mistakes" {
			if _, err := gitSafetyOutput(binding.Path, "config", "--get", "remote.no-mistakes.url"); err != nil {
				return "no-mistakes push target unavailable: remote.no-mistakes.url is not configured"
			}
			if refspec == "" {
				var err error
				refspec, err = noMistakesPushRefspec(binding.Path, taskBranch)
				if err != nil {
					return "no-mistakes push target unavailable: " + err.Error()
				}
			}
		}
		head, err := pushedCommit(binding.Path)
		if err != nil {
			return "git push commit unavailable: " + err.Error()
		}
		granted, err := auth.HasPushGrant(tid, head)
		if err != nil {
			return fmt.Sprintf("push grant lookup failed for task %s commit %s: %v; publication is blocked", taskID, head, err)
		}
		if granted {
			return ""
		}
		return fmt.Sprintf("push of task %s to %s commit %s has no matching Human grant; run `munsu report needs-decision \"push %s\"` and stop; wait for the General to record the grant before you retry", taskID, remote, head, head)
	}
	return "default Ship authority permits only task-local branch, add, commit, and granted push"
}

func noMistakesPushRefspec(worktree, taskBranch string) (string, error) {
	configured, err := gitSafetyOutput(worktree, "config", "--get-all", "remote.no-mistakes.push")
	if err != nil {
		return "", fmt.Errorf("remote.no-mistakes.push must explicitly name the task branch")
	}
	if strings.Contains(configured, "\n") || !pushRefspecAllowed(taskBranch, configured) {
		return "", fmt.Errorf("remote.no-mistakes.push must name only the current task branch")
	}
	return configured, nil
}

func pushedCommit(worktree string) (string, error) {
	head, err := gitSafetyOutput(worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !taskauthority.IsFullGitSHA(head) {
		return "", fmt.Errorf("could not resolve full pushed commit SHA")
	}
	return strings.ToLower(head), nil
}

// gitCommandPath is a git command the classifier read on some path through
// a segment's candidates. ambiguous reports that paths reaching it selected
// different targets.
type gitCommandPath struct {
	g         gitCommandSafety
	ambiguous bool
}

// gitWalkState is where the classifier is at a position: looking for the
// git executable, reading git's global options, or reading the value of
// option.
type gitWalkState struct {
	phase  int
	option string
}

const (
	gitSeekExecutable = iota
	gitGlobalOptions
	gitOptionValue
)

// segmentGitCommands returns the mutating git commands the verb classifier
// reads in segment when each position may hold any of its candidate words
// (segmentCandidates). The first git executable on a path starts git's
// arguments, as it does for the words as written. A verb is read with the
// words after it as written and with every candidate after it, so a
// mutation in any candidate refuses. Paths that reach a position in the
// same state with different targets merge into one ambiguous path, so the
// walk grows with positions, never with the paths through them, and both
// readings of the words after a verb are ranges of shared arrays.
func segmentGitCommands(checkPath string, segment []shellToken, mode backslashMode) []gitCommandPath {
	// A redirection is no git argument, wherever it stands (`git 2>/dev/null push`).
	segment, _ = withoutRedirections(segment)
	nodes := segmentCandidates(segment)
	written := segmentWords(segment)
	// candidates[starts[n]:] is every candidate word at position n and after.
	var candidates []string
	starts := make([]int, len(nodes)+1)
	for n, node := range nodes {
		starts[n] = len(candidates)
		for _, step := range node.steps {
			candidates = append(candidates, step.token.text)
		}
	}
	starts[len(nodes)] = len(candidates)
	type reached struct {
		state gitWalkState
		path  gitCommandPath
	}
	states := make([][]reached, len(nodes))
	reach := func(n int, state gitWalkState, path gitCommandPath) {
		if n == len(nodes) {
			return
		}
		i := slices.IndexFunc(states[n], func(r reached) bool { return r.state == state })
		if i < 0 {
			states[n] = append(states[n], reached{state, path})
			return
		}
		seen := &states[n][i].path
		if path.ambiguous || seen.g.targetPath != path.g.targetPath || seen.g.gitDir != path.g.gitDir || seen.g.workTree != path.g.workTree {
			seen.ambiguous = true
		}
	}
	reach(0, gitWalkState{phase: gitSeekExecutable}, gitCommandPath{g: gitCommandSafety{isGit: true, targetPath: checkPath}})
	var commands []gitCommandPath
	for n, node := range nodes {
		if node.skip > 0 {
			for _, r := range states[n] {
				reach(node.skip, r.state, r.path)
			}
		}
		after := candidates[starts[n+1]:]
		for _, r := range states[n] {
			for _, step := range node.steps {
				word := step.token.text
				path := r.path
				switch r.state.phase {
				case gitSeekExecutable:
					if base := filepath.Base(word); base == "git" || strings.HasSuffix(base, "/git") {
						reach(step.next, gitWalkState{phase: gitGlobalOptions}, path)
					} else {
						reach(step.next, r.state, path)
					}
				case gitOptionValue:
					applyGitOptionValue(&path.g, r.state.option, word, mode)
					reach(step.next, gitWalkState{phase: gitGlobalOptions}, path)
				case gitGlobalOptions:
					takesValue, isVerb := gitGlobalOption(&path.g, word, mode)
					switch {
					case isVerb:
						tail := written[step.from:]
						if len(step.rest) > 0 {
							tail = slices.Concat(segmentWords(step.rest), tail)
						}
						for k, args := range [][]string{tail, after} {
							command := path
							command.g.verb, command.g.args = word, args
							fillGitCommandDetails(&command.g)
							if command.g.mutating && (k == 0 || !slices.Equal(tail, after)) {
								commands = append(commands, command)
							}
						}
					case takesValue && step.next < len(nodes):
						reach(step.next, gitWalkState{phase: gitOptionValue, option: word}, path)
					default:
						reach(step.next, r.state, path)
					}
				}
			}
		}
	}
	return commands
}

// walkGitArgs interprets the git arguments that follow the git executable (the
// tokens after `git`) into a gitCommandSafety. It applies no shell semantics:
// the tokens are final. The git shim reaches it with the argv the real shell
// already expanded and split; the string path reads the same options through
// segmentGitCommands. It consumes the -C/--git-dir/--work-tree target
// selectors and the leading global options that take a separate value, so the
// value is not mis-read as the git verb, then records the verb and whether it
// mutates.
func walkGitArgs(checkPath string, gitArgs []string, mode backslashMode) gitCommandSafety {
	g := gitCommandSafety{isGit: true, targetPath: checkPath}
	for len(gitArgs) > 0 {
		arg := gitArgs[0]
		takesValue, isVerb := gitGlobalOption(&g, arg, mode)
		switch {
		case isVerb:
			g.verb = arg
			g.args = gitArgs[1:]
			fillGitCommandDetails(&g)
			return g
		case takesValue && len(gitArgs) > 1:
			applyGitOptionValue(&g, arg, gitArgs[1], mode)
			gitArgs = gitArgs[2:]
		default:
			gitArgs = gitArgs[1:]
		}
	}
	return g
}

// gitGlobalOption reads arg in git's global-option position. It applies an
// option that carries its value in the same word, and reports whether arg
// takes the next word as its value or is the verb.
func gitGlobalOption(g *gitCommandSafety, arg string, mode backslashMode) (bool, bool) {
	switch {
	case arg == "-C" || arg == "--git-dir" || arg == "--work-tree":
		return true, false
	case arg == "-c" || arg == "--config-env" || arg == "--namespace" || arg == "--super-prefix" || arg == "--attr-source":
		// These global options take a separate value argument. Consume
		// both tokens so the value is not mis-read as the git verb:
		// `git -c user.name=x push …` must not let `user.name=x` shadow
		// `push` and slip the mutation through as an unknown verb.
		return true, false
	case strings.HasPrefix(arg, "-C") && len(arg) > 2:
		g.targetPath = resolveSafetyPathWithMode(g.targetPath, arg[2:], mode)
	case strings.HasPrefix(arg, "--git-dir="):
		g.gitDir = resolveSafetyPathWithMode(g.targetPath, strings.TrimPrefix(arg, "--git-dir="), mode)
	case strings.HasPrefix(arg, "--work-tree="):
		g.workTree = resolveSafetyPathWithMode(g.targetPath, strings.TrimPrefix(arg, "--work-tree="), mode)
		g.targetPath = g.workTree
	case strings.HasPrefix(arg, "-"):
	default:
		return false, true
	}
	return false, false
}

// applyGitOptionValue applies value as the separate value of the global
// option gitGlobalOption reported takes one.
func applyGitOptionValue(g *gitCommandSafety, option, value string, mode backslashMode) {
	switch option {
	case "-C":
		g.targetPath = resolveSafetyPathWithMode(g.targetPath, value, mode)
	case "--git-dir":
		g.gitDir = resolveSafetyPathWithMode(g.targetPath, value, mode)
	case "--work-tree":
		g.workTree = resolveSafetyPathWithMode(g.targetPath, value, mode)
		g.targetPath = g.workTree
	}
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

func pushTargetAllowed(taskBranch string, args []string) (string, string, bool) {
	remote := ""
	refspec := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			if !pushFlagAllowed(arg) {
				return "", "", false
			}
			continue
		}
		if remote == "" {
			if arg != "origin" && arg != "no-mistakes" {
				return "", "", false
			}
			remote = arg
			continue
		}
		if refspec != "" || !pushRefspecAllowed(taskBranch, arg) {
			return "", "", false
		}
		refspec = arg
	}
	return remote, refspec, remote != "" && (refspec != "" || remote == "no-mistakes")
}

func pushFlagAllowed(flag string) bool {
	switch flag {
	case "-u", "--set-upstream", "-q", "--quiet", "-v", "--verbose", "--porcelain":
		return true
	}
	return false
}

func pushRefspecAllowed(taskBranch, refspec string) bool {
	if strings.HasPrefix(refspec, "+") {
		return false
	}
	switch refspec {
	case taskBranch, "HEAD:refs/heads/" + taskBranch, "HEAD:" + taskBranch, "HEAD":
		return true
	}
	return false
}

func branchCommandWrites(args []string) bool {
	if len(args) == 0 || (len(args) == 1 && args[0] == "--show-current") {
		return false
	}
	return true
}

func branchOpAllowed(taskBranch string, args []string) bool {
	return len(args) == 1 && args[0] == taskBranch
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

func resolveSafetyPathWithMode(base, path string, mode backslashMode) string {
	if filepath.IsAbs(path) {
		// Under the escape reading a Windows drive/UNC path must not be
		// short-circuited to absolute on a Windows host: backslashes stay
		// shell escapes, so the path is relative and joined under base. This
		// keeps the function's result determined by mode, not by GOOS.
		if mode == backslashEscapes && isWindowsAbsolutePath(path) {
			return filepath.Join(base, path)
		}
		return path
	}
	if mode == backslashLiteral && isWindowsAbsolutePath(path) {
		return path
	}
	return filepath.Join(base, path)
}

func isWindowsAbsolutePath(path string) bool {
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
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

// hasGitCommandSubstitution reports a command, process or bash 5.3
// function substitution (`${ cmd; }`, `${| cmd; }`) anywhere in command,
// read with each line continuation removed, as bash joins `$\<newline>(`.
// Removing one that bash keeps only finds more.
func hasGitCommandSubstitution(command string) bool {
	command = strings.ReplaceAll(command, "\\\n", "")
	return strings.Contains(command, "$(") || strings.Contains(command, "`") ||
		strings.Contains(command, "<(") || strings.Contains(command, ">(") ||
		strings.Contains(command, "${ ") || strings.Contains(command, "${\t") ||
		strings.Contains(command, "${\n") || strings.Contains(command, "${|")
}

// gitCdPath returns the directory a cd to operand moves to from currentPath,
// "" when currentPath is unknown and operand is relative to it.
func gitCdPath(mode backslashMode, currentPath, operand string) string {
	if currentPath == "" && !filepath.IsAbs(operand) {
		return ""
	}
	return resolveSafetyPathWithMode(currentPath, operand, mode)
}
