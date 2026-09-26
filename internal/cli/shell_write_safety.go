package cli

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// shellToken is one word of a command segment, or the redirection operator
// itself. Quoting has to survive tokenization here: `echo "x > f"` writes
// nothing, while `echo x > f` writes f, and the two are indistinguishable once
// the quotes have been dropped.
type shellToken struct {
	text      string
	redirects bool // an unquoted `>`
	// subshell is an unquoted `(` or `)` bash reads as an operator: it opens
	// or closes a subshell, and is a segment of its own.
	subshell   bool
	expandable bool // contains `$` or a backtick: the shell decides the value
	// plain is a word with no quoting, escape or expansion, which bash may
	// read as a reserved word or a function name.
	plain bool
	// apart, on a segment's first token, is a segment bash runs in a child:
	// a pipeline's part, a command sent to the background, or one of a
	// compound command or a function definition, head included, that a
	// pipeline or `&` runs as a whole.
	apart bool
	// body is a synthetic subshell operator around a function's or a
	// coproc's body of a compound command other than a subshell.
	body bool
	// simpleFunctionBody marks a parenthesized function head with a
	// simple-command body, which some named shells accept and
	// bash rejects. Both guards refuse it rather than treating the body as
	// a command in the surrounding shell.
	simpleFunctionBody bool
	// unfinished is the one token of a segment standing for a case the
	// command line opens and never ends with `esac`, from `case` on, or for
	// a substitution substitutionEnd cannot end, from its start on.

	unfinished bool
	// undecodable is a `$'...'` word holding an escape the tokenizer does not
	// decode, or one left unterminated, whose text is the raw word without
	// `$`; or a word holding an unterminated `${`.
	undecodable bool
	// splitsAtIFS is a word holding an unquoted `${...}` that substitutes
	// its word, which bash splits at IFS.
	splitsAtIFS bool
	// alternates are the word's other literal readings, each distinct from
	// text and each a run of words spanning this token. The first ones are
	// the word with each parameter expansion that substitutes its word (`-`,
	// `=`, `+`, with or without `:`) replaced by that word, nested ones
	// included, split where bash splits it. The last is the word with each
	// `$'...'` part read as the text between its quotes, undecoded: where
	// bash does not start ANSI-C quoting (a comment, say), the decoded text
	// is not what runs.
	alternates [][]shellToken
	// undecoded marks the token of that last, raw reading.
	undecoded bool
	start     int
	end       int
}

// shellWriteTargets returns the paths a shell command names as write targets,
// resolved against the directory the command runs in.
//
// The shell channel used to decide on session location alone, so a soldier
// standing in its own worktree wrote the shared checkout through an absolute
// path unopposed (BEO-73). Targets are classified by evaluateFileWriteSafety —
// the same owner the native write channel uses — so no path comparison is
// invented here (ADR-0014 §1).
//
// The claim of coverage is deliberately narrow (ADR-0014 §2): a `>` redirection
// target, and the argument tokens of a named write verb at command position
// (commandPosition), in the command line or in a payload a named shell runs
// (shellPayloads). A verb that is not on the list, a runner outside
// wrapperVerbs, an interpreter other than those shells, or a target the shell
// computes are all open. Reads are never targets: `cat`, `grep -r` and
// `go build` pointed at the shared checkout are legitimate work, and refusing
// them is the failure mode this guard must not have.
//
// Only a named consumer's payload is read, unlike the git guard, which reads
// every word that reads as more than itself. The asymmetry is deliberate: a
// refused git mutation costs a retry, a refused file write costs the run, and
// under the git guard's rule `git commit -m "rm <shared>/x"` would be refused.
//
// A lone backslash has no single meaning here, so the command is resolved twice
// (#664). munsu never runs this string: the only call site is the harness hook
// in integrate_cmd.go, which inspects a command the harness proposes to run.
// Which shell interprets it depends on the harness and the platform, and
// neither is on the wire — so the guard cannot know whether `\` quotes the next
// character and disappears, as POSIX shells read it, or is an ordinary path
// separator, as Windows shells do. Reading it only as an escape deleted every
// separator in a Windows absolute path: the target stopped naming the shared
// checkout, and the write proceeded unopposed.
//
// Both readings are therefore extracted and their resolved candidates unioned;
// ambiguity is decided independently for each target span. There is no platform
// gate, because the platform is not what decides this. Over-refusal stays bounded
// by the narrow claim above: only a redirection target and a named write verb's
// arguments are targets at all, so the candidate the second reading adds can only
// ever be one more write path, never a read.
func shellWriteTargets(checkPath, command string) ([]string, bool) {
	interpretations := []shellTargetResolution{
		{mode: backslashEscapes},
		{mode: backslashLiteral},
	}
	for i := range interpretations {
		interpretations[i].targets = shellWriteTargetsUnderDetailed(interpretations[i].mode, checkPath, command)
	}
	bySpan := make(map[shellTargetSpan][]shellTargetResult)
	for _, interpretation := range interpretations {
		for _, result := range interpretation.targets {
			bySpan[result.span] = append(bySpan[result.span], result)
		}
	}
	var targets []string
	seen := make(map[string]bool)
	ambiguous := false
	for _, interpretation := range interpretations {
		for _, result := range interpretation.targets {
			if result.ambiguous || seen[result.path] {
				continue
			}
			seen[result.path] = true
			targets = append(targets, result.path)
		}
	}
	for _, results := range bySpan {
		resolved := false
		for _, result := range results {
			if !result.ambiguous {
				resolved = true
				break
			}
		}
		if !resolved {
			ambiguous = true
		}
	}
	return targets, ambiguous
}

// shellTargetSpan is where a target stands: its word's runes in the command
// line, or in a payload, where within names the spans of the payloads around
// it, outermost first.
type shellTargetSpan struct {
	start, end int
	within     string
}
type shellTargetResult struct {
	span      shellTargetSpan
	path      string
	ambiguous bool
}
type shellTargetResolution struct {
	mode    backslashMode
	targets []shellTargetResult
}

// backslashMode is one reading of a lone backslash while tokenizing.
type backslashMode int

const (
	// backslashEscapes is the POSIX shell reading: `\` quotes the character
	// after it and is itself removed.
	backslashEscapes backslashMode = iota
	// backslashLiteral is the Windows shell reading: `\` is an ordinary
	// character that quotes nothing, and separates path components.
	backslashLiteral
)

func shellWriteTargetsUnderDetailed(mode backslashMode, checkPath, command string) []shellTargetResult {
	volume := filepath.VolumeName(checkPath)
	walk := &shellWriteWalk{
		mode:      mode,
		payloads:  new(int),
		functions: &shellFunctions{},
		cwd: shellCwd{
			path:              checkPath,
			activeVolume:      volume,
			activeVolumeKnown: true,
			byVolume:          map[string]string{strings.ToLower(volume): checkPath},
		},
	}
	walk.walk(command, 0, "")
	return walk.targets
}

// shellWriteWalk reads one command line under one backslash reading, and the
// payloads it reaches. payloads counts the payloads read for the whole
// command, by every walk it starts.
type shellWriteWalk struct {
	mode     backslashMode
	cwd      shellCwd
	payloads *int
	targets  []shellTargetResult
	// moves counts the directory moves this walk read outside a closed
	// function body, and functions the functions the shell defined so far:
	// a call to one whose body moves the directory leaves it where the body
	// left it.
	moves     int
	functions *shellFunctions
}

// shellFunctions records every function body read in this command. moving is
// monotone: once a name may move the directory, no later definition, unset or
// scope close removes it. calls keeps each name's accumulated call edges so a
// function is resolved against names that may move when it is called.
type shellFunctions struct {
	moving      map[string]bool
	calls       map[string][]string
	commandCall []string
	memo        map[string]bool
	work        int
}

// shellFunction is one definition body: whether it moves the directory
// directly, and the function names its command positions call.
type shellFunction struct {
	moves bool
	calls []string
}

// define adds a definition's call edges and marks its name if the body moves
// directly or reaches a name that may move. Previous definitions remain in the
// graph because this walk cannot prove every conditional definition ran.
func (f *shellFunctions) define(name string, function shellFunction) {
	if f.calls == nil {
		f.calls = map[string][]string{}
	}
	f.calls[name] = append(f.calls[name], function.calls...)
	if function.moves || slices.ContainsFunc(function.calls, f.moves) {
		f.addMoving(name)
	}
}

func (f *shellFunctions) addMoving(name string) {
	if f.moving == nil {
		f.moving = map[string]bool{}
	}
	if f.moving[name] {
		return
	}
	f.moving[name] = true
	f.memo = nil
}

// finishDefinition saves the active body calls, removes them from the enclosing
// body's call stream, and registers the function immediately. Nested function
// bodies therefore do not become calls made by their enclosing body.
func (f *shellFunctions) finishDefinition(name string, callsAt int, function shellFunction) {
	function.calls = slices.Clone(f.commandCall[callsAt:])
	f.commandCall = f.commandCall[:callsAt]
	f.define(name, function)
}

// moves reports whether a call to name moves the directory by resolving its
// accumulated calls against the monotone set. A resolution past the command's
// budget fails closed. Memoized results are invalidated whenever that set grows.
func (f *shellFunctions) moves(name string) bool {
	if f.moving[name] {
		return true
	}
	if moves, ok := f.memo[name]; ok {
		return moves
	}
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(name string) bool {
		if f.moving[name] {
			return true
		}
		if moves, ok := f.memo[name]; ok {
			return moves
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		for _, call := range f.calls[name] {
			if f.work++; f.work > maxWriteReadings || visit(call) {
				return true
			}
		}
		return false
	}
	moves := visit(name)
	if moves {
		f.addMoving(name)
	}
	if f.memo == nil {
		f.memo = map[string]bool{}
	}
	if moves {
		f.memo[name] = true
	} else {
		for name := range seen {
			f.memo[name] = false
		}
	}
	return moves
}

// call logs segment's command word and reports whether any decoded candidate
// names a function that may move the directory.
func (f *shellFunctions) call(segment []shellToken) bool {
	if definesFunction(segment) {
		return false
	}
	names := functionCalls(segment)
	f.commandCall = append(f.commandCall, names...)
	return slices.ContainsFunc(names, f.moves)
}

// shellCwd is the directory a command line runs in. activeVolume is the drive
// a plain relative path resolves against: the drive left active by the last
// `cd`. unknownCwd is the volume whose per-drive current directory this pass
// cannot reconstruct ("" if none). An ambiguous drive-relative `cd D:docs`
// only makes D:'s cwd unknowable: D-volume targets stay ambiguous, but
// C-volume and absolute targets resolve against known state and must not be
// refused for it (ADR-0014 §1, #664). lost is a directory a payload or a
// wrapper moved to that this reading cannot know: every relative target is
// ambiguous until an absolute `cd`. previous is the directory `cd -` returns
// to, nil when this reading did not see the shell leave one; stack is the
// directory stack pushd built, which a stack this does not follow empties.
type shellCwd struct {
	path              string
	activeVolume      string
	activeVolumeKnown bool
	unknownCwd        string
	byVolume          map[string]string
	lost              bool
	previous          *shellCwd
	stack             *dirStack[shellCwd]
}

// dirStack is a directory stack, top first. A push shares the stack under it,
// so no move or subshell copies the stack.
type dirStack[T any] struct {
	top  T
	next *dirStack[T]
}

func (c shellCwd) clone() shellCwd {
	c.byVolume = maps.Clone(c.byVolume)
	return c
}

func (c shellCwd) same(other shellCwd) bool {
	return c.path == other.path && c.activeVolume == other.activeVolume && c.activeVolumeKnown == other.activeVolumeKnown &&
		c.unknownCwd == other.unknownCwd && c.lost == other.lost
}

// move applies what a segment's cd, pushd or popd does (segmentDirMove). A
// directory it cannot follow, `cd -` with no previous directory and popd with
// an empty stack among them, leaves c lost.
func (c *shellCwd) move(move dirMove, operand shellToken) {
	old := c.clone()
	old.previous, old.stack = nil, nil
	restore := func(to shellCwd) {
		stack := c.stack
		*c = to.clone()
		c.stack = stack
	}
	switch move {
	case moveCd:
		c.cd(operand)
	case movePush:
		c.stack = &dirStack[shellCwd]{top: old, next: c.stack}
		c.cd(operand)
	case movePrevious:
		if c.previous == nil {
			c.lost = true
		} else {
			restore(*c.previous)
		}
	case movePop:
		if c.stack == nil {
			c.lost = true
		} else {
			top := c.stack.top
			c.stack = c.stack.next
			restore(top)
		}
	case moveStack:
		c.stack = nil
		return
	case moveUnknown:
		c.lost, c.stack = true, nil
	case moveCalled:
		c.lost, c.stack, c.previous = true, nil, nil
		return
	}
	c.previous = &old
}

// cd moves c to operand.
func (c *shellCwd) cd(operand shellToken) {
	if operand.expandable {
		c.unknownCwd = filepath.VolumeName(operand.text)
		if c.unknownCwd == "" {
			c.unknownCwd = c.activeVolume
		}
		c.activeVolume = c.unknownCwd
		c.activeVolumeKnown = false
		return
	}
	base := c.path
	if volume := filepath.VolumeName(operand.text); volume != "" {
		if known, ok := c.byVolume[strings.ToLower(volume)]; ok {
			base = known
		}
	}
	// A plain relative cd (no volume, not volume-less rooted) against a
	// drive whose cwd is unknown cannot be resolved: the active drive's
	// current directory is exactly what the prior drive-relative cd left
	// unreconstructable. Keep the unknown state — do not resolve against
	// the stale path of the previous volume or clear unknownCwd.
	if !c.activeVolumeKnown && filepath.VolumeName(operand.text) == "" && !(len(operand.text) > 0 && os.IsPathSeparator(operand.text[0])) {
		return
	}
	resolved, pathAmbiguous := resolveShellWritePath(base, c.activeVolume, operand.text)
	if pathAmbiguous {
		// Different-volume drive-relative cd (D:docs from a C: base): D
		// becomes the active drive with an unknowable cwd. C stays known,
		// so C-volume and absolute targets stay resolvable.
		c.activeVolume = filepath.VolumeName(operand.text)
		c.activeVolumeKnown = false
		c.unknownCwd = c.activeVolume
		return
	}
	c.path = resolved
	c.activeVolume = filepath.VolumeName(resolved)
	c.activeVolumeKnown = true
	c.byVolume[strings.ToLower(c.activeVolume)] = resolved
	c.unknownCwd = ""
	c.lost = c.lost && !filepath.IsAbs(operand.text)
}

// resolve resolves target against c, and reports whether it is ambiguous.
func (c shellCwd) resolve(target string) (string, bool) {
	base := c.path
	if volume := filepath.VolumeName(target); volume != "" {
		if known, ok := c.byVolume[strings.ToLower(volume)]; ok {
			base = known
		}
	}
	resolved, ambiguous := resolveShellWritePath(base, c.activeVolume, target)
	if !ambiguous && (pathDependsOnUnknownCwd(c.activeVolume, c.unknownCwd, target) || (c.lost && !filepath.IsAbs(target))) {
		ambiguous = true
	}
	return resolved, ambiguous
}

// under returns c moved into each directory chdirs names, in order, or false
// under a changed root, where no target is knowable. A directory the shell
// computes leaves it lost.
func (c shellCwd) under(chdirs []shellToken, chroot bool) (shellCwd, bool) {
	c = c.clone()
	for _, dir := range chdirs {
		if dir.expandable {
			c.lost = true
			continue
		}
		c.cd(dir)
	}
	return c, !chroot
}

// walk reads command, a payload depth layers deep whose span is within.
func (w *shellWriteWalk) walk(command string, depth int, within string) {
	// Heredoc syntax is POSIX grammar. Bodies are split off once with POSIX
	// delimiter rules, before either backslash reading tokenizes, so the
	// literal (Windows) reading differs from the POSIX reading only in how it
	// reads path backslashes — never in where a heredoc ends. That keeps
	// <<\EOF / <<-\END terminating at the bare delimiter on every OS and stops
	// the literal reading from swallowing a write named after the terminator.
	stripped, feeds := splitHeredocBodies(command)
	segments := tokenizeSegments(w.mode, stripped)
	fed := segmentFeeds([]rune(stripped), segments, feeds)
	// subshells are the directories to return to at each open subshell's
	// `)`, with the function-body head and the move/call marks at its start.
	type subshell struct {
		cwd   shellCwd
		head  []shellToken
		moves int
		calls int
	}
	var subshells []subshell
	for i, segment := range segments {
		if segment[0].simpleFunctionBody {
			w.targets = append(w.targets, shellTargetResult{
				span:      shellTargetSpan{start: segment[0].start, end: segment[0].end, within: within},
				ambiguous: true,
			})
			continue
		}
		if segment[0].unfinished {
			w.targets = append(w.targets, shellTargetResult{span: shellTargetSpan{start: segment[0].start, end: segment[0].end, within: within}, ambiguous: true})
			continue
		}
		if i > 0 && definesFunction(segments[i-1]) && !segment[0].body {
			w.functions.finishDefinition(functionName(segments[i-1]), len(w.functions.commandCall), shellFunction{})
		}
		if segment[0].subshell {
			if segment[0].text == "(" {
				opened := subshell{cwd: w.cwd.clone(), moves: w.moves, calls: len(w.functions.commandCall)}
				if segment[0].body && i > 0 && definesFunction(segments[i-1]) {
					opened.head = segments[i-1]
				}
				subshells = append(subshells, opened)
			} else if n := len(subshells); n > 0 {
				closed := subshells[n-1]
				w.cwd, subshells = closed.cwd, subshells[:n-1]
				if closed.head != nil {
					w.functions.finishDefinition(functionName(closed.head), closed.calls, shellFunction{moves: w.moves != closed.moves})
					w.moves = closed.moves
				}
			}
			continue
		}
		move, operand := segmentDirMove(segment)
		if w.functions.call(segment) && move == moveNone {
			move = moveCalled
		}
		if move != moveNone {
			// bash opens the segment's redirections before it moves.
			_, redirects := withoutRedirections(segment)
			for _, target := range redirects {
				w.target(w.cwd, target, within)
			}
			w.cwd.move(move, operand)
			w.moves++
			continue
		}
		// Every reading of a segment that can write names targets: a
		// parameter expansion's substituted words take its place, and their
		// targets share its span. A segment where no candidate is a write
		// verb, a redirect or a command that runs a payload names none.
		if !slices.ContainsFunc(candidateTokens(segment), func(token shellToken) bool {
			base := filepath.Base(token.text)
			return token.redirects || slices.Contains(writeVerbs, base) || slices.Contains(shellConsumers, base) || base == "eval" || base == "env" || base == "time"
		}) {
			continue
		}
		span := shellTargetSpan{start: segment[0].start, end: segment[len(segment)-1].end, within: within}
		readings, ok := writeReadings(segment)
		if !ok {
			w.targets = append(w.targets, shellTargetResult{span: span, ambiguous: true})
			continue
		}
		var payloads []shellPayload
		for _, reading := range readings {
			redirects, command := segmentWriteTargets(w.mode, reading)
			for _, target := range redirects {
				w.target(w.cwd, target, within)
			}
			cwd, known := w.cwd.under(command.chdirs, command.chroot)
			for _, target := range slices.Concat(command.outputs, verbWriteTargets(command.args)) {
				if !known {
					w.targets = append(w.targets, shellTargetResult{span: shellTargetSpan{start: target.start, end: target.end, within: within}, ambiguous: true})
					continue
				}
				w.target(cwd, target, within)
			}
			for _, payload := range shellPayloads(w.mode, command, fed[i]) {
				if !slices.ContainsFunc(payloads, payload.same) {
					payloads = append(payloads, payload)
				}
			}
		}
		w.read(payloads, span, depth)
	}
}

// target adds target, resolved against cwd. A word whose value the shell
// computes is not a target: it is not a path this guard can classify, and
// guessing would refuse a call on evidence it does not have. An empty word
// stays a target: it resolves to the directory the command runs in, where
// darwin cp writes it.
func (w *shellWriteWalk) target(cwd shellCwd, target shellToken, within string) {
	if target.expandable {
		return
	}
	path, ambiguous := cwd.resolve(target.text)
	w.targets = append(w.targets, shellTargetResult{
		span: shellTargetSpan{start: target.start, end: target.end, within: within},
		path: path, ambiguous: ambiguous,
	})
}

// read reads the payloads of the segment at span. eval runs its payload in
// this shell, so it is read as part of this walk and its `cd` moves the
// segments after it; a shell runs its payload in a child that starts in the
// directory its wrappers name and whose `cd` does not return. When the
// segment's readings give eval more than one payload, each is read from here
// and a `cd` in any leaves the directory lost. A payload past
// maxShellPayloadDepth, or past maxWriteReadings payloads in the command, is
// not read: its segment is ambiguous.
func (w *shellWriteWalk) read(payloads []shellPayload, span shellTargetSpan, depth int) {
	inline := 0
	for _, payload := range payloads {
		if payload.inline {
			inline++
		}
	}
	for _, payload := range payloads {
		*w.payloads++
		if depth+1 > maxShellPayloadDepth || *w.payloads > maxWriteReadings {
			w.targets = append(w.targets, shellTargetResult{span: span, ambiguous: true})
			continue
		}
		within := span.within + strconv.Itoa(payload.start) + "-" + strconv.Itoa(payload.end) + "/"
		if payload.inline && inline == 1 {
			w.walk(payload.text, depth+1, within)
			continue
		}
		cwd, known := w.cwd.under(payload.chdirs, payload.chroot)
		if !known {
			w.targets = append(w.targets, shellTargetResult{span: span, ambiguous: true})
			continue
		}
		if !payload.inline {
			// A shell starts with no directory stack or previous directory.
			cwd.previous, cwd.stack = nil, nil
		}
		child := &shellWriteWalk{mode: w.mode, cwd: cwd, payloads: w.payloads, functions: w.functions}
		calls := len(w.functions.commandCall)
		child.walk(payload.text, depth+1, within)
		if !payload.inline {
			w.functions.commandCall = w.functions.commandCall[:calls]
		}
		w.targets = append(w.targets, child.targets...)
		if payload.inline && (child.moves > 0 || !child.cwd.same(w.cwd)) {
			w.cwd.lost = true
			w.moves++
		}
	}
}

// shellConsumers are the shells whose `-c` operand, or stdin when they have
// neither `-c` nor a script operand (or have `-s`), the write guard reads as
// a command line. With eval they are the only payloads it reads (ADR-0014 §2).
var shellConsumers = []string{"bash", "sh", "zsh", "dash", "ksh"}

// shellPayload is a command line a command runs, at runes start to end of the
// text around it: eval's, inline in this shell, or a shell's, in a child in
// the directories its wrappers name.
type shellPayload struct {
	text       string
	start, end int
	inline     bool
	chdirs     []shellToken
	chroot     bool
}

func (p shellPayload) same(other shellPayload) bool {
	return p.text == other.text && p.inline == other.inline && p.chroot == other.chroot &&
		slices.EqualFunc(p.chdirs, other.chdirs, func(a, b shellToken) bool { return a.text == b.text && a.expandable == b.expandable })
}

// shellPayloads returns the payloads command runs, feeds being the stdin its
// segment's heredocs and here-strings give it. eval runs its words joined by
// single spaces, as bash does. A payload the shell computes reads as an
// expansion, which names no target.
func shellPayloads(mode backslashMode, command shellCommand, feeds []stdinFeed) []shellPayload {
	child := func(text string, start, end int) shellPayload {
		return shellPayload{text: text, start: start, end: end, chdirs: command.chdirs, chroot: command.chroot}
	}
	if len(command.args) == 0 {
		return nil
	}
	args := command.args[1:]
	switch base := filepath.Base(command.args[0].text); {
	case base == "eval":
		if len(args) > 0 && args[0].text == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return nil
		}
		return []shellPayload{{text: strings.Join(segmentWords(args), " "), start: args[0].start, end: args[len(args)-1].end, inline: true}}
	case slices.Contains(shellConsumers, base):
		var payloads []shellPayload
		stdin := false
		for _, reading := range shellOptions(shellGrammars[base], args) {
			switch {
			case reading.dashC && reading.operand < len(args):
				operand := args[reading.operand]
				payloads = append(payloads, child(operand.text, operand.start, operand.end))
			case !reading.dashC && (reading.operand == len(args) || reading.dashS):
				stdin = true
			}
		}
		if stdin {
			for _, feed := range feeds {
				text := feed.text
				if feed.hereString {
					text = strings.Join(segmentWords(slices.Concat(tokenizeSegments(mode, text)...)), " ")
				}
				payloads = append(payloads, child(text, feed.at, feed.at+1))
			}
		}
		return payloads
	}
	return nil
}

// evalPayloads returns the distinct payloads eval runs in segment, in every
// reading of its words (writeReadings), and false past maxWriteReadings
// readings. The git guard reads them in its own shell.
func evalPayloads(mode backslashMode, segment []shellToken) ([]shellPayload, bool) {
	if !slices.ContainsFunc(candidateTokens(segment), func(token shellToken) bool { return filepath.Base(token.text) == "eval" }) {
		return nil, true
	}
	readings, ok := writeReadings(segment)
	if !ok {
		return nil, false
	}
	var payloads []shellPayload
	for _, reading := range readings {
		_, command := segmentWriteTargets(mode, reading)
		for _, payload := range shellPayloads(mode, command, nil) {
			if payload.inline && !slices.ContainsFunc(payloads, payload.same) {
				payloads = append(payloads, payload)
			}
		}
	}
	return payloads, true
}

// shellGrammar is how a named shell reads its options, as a probe of bash
// 3.2 and 5.3, zsh 5.9, dash and ksh 93u+ on macOS measured: the option
// letters that are flags and those that take the next word, and the long
// options that are flags and those that take the next word. zsh and ksh
// also read `--NAME` for each of their option names, which are not listed:
// those read as unknown.
type shellGrammar struct {
	flags, values    string
	long, longValues []string
}

var shellGrammars = map[string]shellGrammar{
	"bash": {
		flags: "abcefhiklmnprstuvxBCDEHIPT", values: "oO",
		long:       []string{"--debugger", "--dump-po-strings", "--dump-strings", "--help", "--login", "--noediting", "--noprofile", "--norc", "--posix", "--pretty-print", "--protected", "--restricted", "--verbose", "--version", "--wordexp"},
		longValues: []string{"--rcfile", "--init-file"},
	},
	"sh":   {flags: "abcefhiklmnprstuvxBCDEHIPT", values: "oO"},
	"zsh":  {flags: "abcdefghiklmnprstuvwxyBCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", values: "o", long: []string{"--help", "--version"}, longValues: []string{"--emulate"}},
	"dash": {flags: "abceflimnsuvxCEIV", values: "o"},
	"ksh":  {flags: "abcefhiklmnprstuvxBCDEGH", values: "oRT"},
}

// shellReading is one reading of a shell's options: the index of its first
// operand (len(args) when it has none), and whether `c` and `s` were given.
type shellReading struct {
	operand      int
	dashC, dashS bool
}

// shellOptions reads the options of the shell grammar names and returns
// every reading of them. Options combine (`-lc`, `+xc`) and continue past
// `-c` (`-c -e`); each shell reads `+c` and `+s` as `-c` and `-s`, and a
// value option takes the next word; `--` and `-` end them. An option the
// grammar does not know is read both as a flag and as taking the next word,
// so a payload either reading finds is read. sh is read as bash, which it
// is on macOS.
func shellOptions(grammar shellGrammar, args []shellToken) []shellReading {
	var readings []shellReading
	seen := map[shellReading]bool{}
	var read func(shellReading)
	read = func(at shellReading) {
		if seen[at] {
			return
		}
		seen[at] = true
		i := at.operand
		if i >= len(args) {
			readings = append(readings, shellReading{len(args), at.dashC, at.dashS})
			return
		}
		word := args[i].text
		next := func(skip int) {
			read(shellReading{min(i+1+skip, len(args)), at.dashC, at.dashS})
		}
		switch {
		case word == "--" || word == "-":
			readings = append(readings, shellReading{i + 1, at.dashC, at.dashS})
		case strings.HasPrefix(word, "--"):
			name, _, valued := strings.Cut(word, "=")
			switch {
			case valued || slices.Contains(grammar.long, name):
				next(0)
			case slices.Contains(grammar.longValues, name):
				next(1)
			default:
				next(0)
				next(1)
			}
		case len(word) < 2 || (word[0] != '-' && word[0] != '+'):
			readings = append(readings, at)
		default:
			values, unknown := 0, 0
			for _, r := range word[1:] {
				switch {
				case r == 'c':
					at.dashC = true
				case r == 's':
					at.dashS = true
				}
				switch {
				case strings.ContainsRune(grammar.values, r):
					values++
				case !strings.ContainsRune(grammar.flags, r):
					unknown++
				}
			}
			for extra := 0; extra <= unknown; extra++ {
				next(values + extra)
			}
		}
	}
	read(shellReading{})
	return readings
}

// segmentFeeds returns the stdin feeds of each segment, by index: a feed
// belongs to the segment its operator stood in, which a separator after the
// segment's last word ends.
func segmentFeeds(runes []rune, segments [][]shellToken, feeds []stdinFeed) map[int][]stdinFeed {
	fed := make(map[int][]stdinFeed)
	if len(segments) == 0 {
		return fed
	}
	for _, feed := range feeds {
		k := max(sort.Search(len(segments), func(k int) bool { return segments[k][0].start >= feed.at })-1, 0)
		if last := segments[k][len(segments[k])-1].end; k+1 < len(segments) && last >= 0 && last < feed.at && strings.ContainsAny(string(runes[last:feed.at]), ";&|\n") {
			k++
		}
		fed[k] = append(fed[k], feed)
	}
	return fed
}

// shellCommand is what the words of a segment run: args from the command
// word on, in the directories wrappers name (chdirs, in order), under a root a
// wrapper changed (chroot).
type shellCommand struct {
	args   []shellToken
	chdirs []shellToken
	chroot bool
	// outputs are the files a wrapper writes itself: `time -o FILE`.
	outputs []shellToken
}

// reservedWords are the words bash reads before a command word, besides
// assignments and `time`. A coproc runs its command in a child.
var reservedWords = []string{"!", "{", "if", "then", "else", "elif", "while", "until", "do", "coproc"}

// wrapperGrammar is how a wrapper reads its options: the short options
// (letters) and long options that take a value, those of them that name the
// command's directory, root, split string or output file, and the letters after which it
// runs nothing. assignments reports that NAME=value words may follow its
// options; operands is how many words it reads before the command.
type wrapperGrammar struct {
	values      string
	long        []string
	chdir       []string
	chroot      []string
	split       []string
	output      []string
	none        string
	assignments bool
	operands    int
}

// wrapperVerbs are the commands that run the rest of their argv as a command,
// with the option grammar a bash 3.2 and 5.3 probe on macOS measured, and
// GNU's where macOS has no such command (timeout, setsid) or fewer options
// (env, nice, stdbuf, time). The list is fixed: a runner outside it (arch,
// caffeinate, xcrun, doas, ionice, chronic, xargs, find -exec, parallel,
// watch) stays open (ADR-0014 §2).
var wrapperVerbs = map[string]wrapperGrammar{
	"command": {none: "vV"},
	"builtin": {},
	"exec":    {values: "a"},
	"env": {
		values: "uCPS", long: []string{"--unset", "--chdir", "--split-string"},
		chdir: []string{"-C", "--chdir"}, split: []string{"-S", "--split-string"}, assignments: true,
	},
	"nohup":   {},
	"time":    {values: "fo", long: []string{"--format", "--output"}, output: []string{"-o", "--output"}},
	"nice":    {values: "n", long: []string{"--adjustment"}},
	"timeout": {values: "ks", long: []string{"--kill-after", "--signal"}, operands: 1},
	"sudo": {
		values: "CDgpRTUurt",
		long:   []string{"--close-from", "--chdir", "--group", "--host", "--prompt", "--chroot", "--command-timeout", "--other-user", "--user", "--role", "--type"},
		chdir:  []string{"-D", "--chdir"}, chroot: []string{"-R", "--chroot"}, assignments: true,
	},
	"setsid": {},
	"stdbuf": {values: "ioe", long: []string{"--input", "--output", "--error"}},
}

// commandPosition returns what args run: the command word is the first word
// after every assignment and reserved word bash reads before it, and every
// wrapper with its options. `command -v` and `-V` run nothing. The string
// `env -S` splits, read here as shell, stands in its place as words, each
// argument after it staying one word.
func commandPosition(mode backslashMode, args []shellToken) shellCommand {
	var command shellCommand
	prefix := true
	for len(args) > 0 {
		if prefix && (isAssignment(args[0].text) || slices.Contains(reservedWords, args[0].text)) {
			args = args[1:]
			continue
		}
		grammar, ok := wrapperVerbs[filepath.Base(args[0].text)]
		if !ok {
			break
		}
		prefix = args[0].text == "time"
		args = args[1:]
		for len(args) > 0 && strings.HasPrefix(args[0].text, "-") {
			option := args[0]
			args = args[1:]
			if option.text == "--" {
				break
			}
			name, value, valued := option.text, option, false
			if strings.HasPrefix(option.text, "--") {
				name, value.text, valued = strings.Cut(option.text, "=")
				if !valued && slices.Contains(grammar.long, name) && len(args) > 0 {
					value, args, valued = args[0], args[1:], true
				}
			} else {
				for k := 1; k < len(option.text); k++ {
					if strings.IndexByte(grammar.none, option.text[k]) >= 0 {
						return shellCommand{}
					}
					if strings.IndexByte(grammar.values, option.text[k]) < 0 {
						continue
					}
					name = option.text[:1] + option.text[k:k+1]
					if k+1 < len(option.text) {
						value.text, valued = option.text[k+1:], true
					} else if len(args) > 0 {
						value, args, valued = args[0], args[1:], true
					}
					break
				}
			}
			switch {
			case !valued:
			case slices.Contains(grammar.chdir, name):
				command.chdirs = append(command.chdirs, value)
			case slices.Contains(grammar.chroot, name):
				command.chroot = true
			case slices.Contains(grammar.output, name):
				command.outputs = append(command.outputs, value)
			case slices.Contains(grammar.split, name):
				split := slices.Concat(tokenizeSegments(mode, value.text)...)
				for k := range split {
					split[k].start += value.start
					split[k].end += value.start
				}
				args = slices.Concat(split, args)
			}
		}
		if grammar.assignments {
			for len(args) > 0 && strings.Contains(args[0].text, "=") {
				args = args[1:]
			}
		}
		args = args[min(grammar.operands, len(args)):]
	}
	command.args = args
	return command
}

// builtinCommand returns the words of segment, its redirections dropped, from
// the word bash runs as a command in this shell on: past assignments,
// reserved words, `time -p`, and the `builtin` and `command` prefixes, which
// still run a builtin such as cd here. It returns nil when `command -v` or
// `-V` runs nothing, or a coproc runs the command in a child.
func builtinCommand(segment []shellToken) []shellToken {
	segment, _ = withoutRedirections(segment)
	for len(segment) > 0 {
		word := segment[0].text
		switch {
		case word == "coproc":
			return nil
		case isAssignment(word) || slices.Contains(reservedWords, word) || word == "builtin":
		case word == "time" || word == "command":
			for len(segment) > 1 && strings.HasPrefix(segment[1].text, "-") && segment[1].text != "-" {
				if word == "command" && strings.ContainsAny(segment[1].text, "vV") {
					return nil
				}
				segment = segment[1:]
			}
		default:
			return segment
		}
		segment = segment[1:]
	}
	return nil
}

// isAssignment reports whether word is a NAME=value or NAME+=value word, NAME
// being an identifier with an optional bracket-balanced `[subscript]`.
func isAssignment(word string) bool {
	rest := word[identifierLen(word):]
	if len(rest) == len(word) {
		return false
	}
	if strings.HasPrefix(rest, "[") {
		depth := 0
		for k, r := range rest {
			if r == '[' {
				depth++
			} else if r == ']' {
				depth--
			}
			if depth == 0 {
				rest = rest[k+1:]
				break
			}
		}
		if depth > 0 {
			return false
		}
	}
	return strings.HasPrefix(strings.TrimPrefix(rest, "+"), "=")
}

// identifierLen returns the length of the identifier word starts with.
func identifierLen(word string) int {
	for i, r := range word {
		if r != '_' && !('a' <= r && r <= 'z') && !('A' <= r && r <= 'Z') && !(i > 0 && '0' <= r && r <= '9') {
			return i
		}
	}
	return len(word)
}

// maxWriteReadings bounds the combinations writeReadings reads.
const maxWriteReadings = 256

// writeReadings returns every combination of the readings of segment's
// tokens: each token as written or as one of its alternates. The raw reading
// of an ANSI-C word is not one: a target is decoded. It returns false past
// maxWriteReadings combinations.
func writeReadings(segment []shellToken) ([][]shellToken, bool) {
	readings := [][]shellToken{nil}
	for _, token := range segment {
		options := [][]shellToken{{token}}
		for _, alternate := range token.alternates {
			if !slices.ContainsFunc(alternate, func(word shellToken) bool { return word.undecoded }) {
				options = append(options, alternate)
			}
		}
		if len(readings)*len(options) > maxWriteReadings {
			return nil, false
		}
		if len(options) == 1 {
			for r := range readings {
				readings[r] = append(readings[r], token)
			}
			continue
		}
		var next [][]shellToken
		for _, reading := range readings {
			for _, option := range options {
				next = append(next, slices.Concat(reading, option))
			}
		}
		readings = next
	}
	return readings, true
}

// pathDependsOnUnknownCwd reports whether resolving target against the session's
// shell state depends on the current directory of the volume unknownCwd names —
// the only path context a drive-relative `cd` can leave unknowable. Absolute
// paths, volume-less rooted paths (\foo, anchored to the active volume's root
// rather than its cwd) and same-volume drive-relative paths (C:foo, resolved
// against that volume's known cwd) do not, so they stay classified instead of
// being swept into the refusal. filepath.IsAbs disagrees on the volume-less
// rooted case (it reports false on Windows for a separator-led path), and a
// shell-aware check is required there (#664).
func pathDependsOnUnknownCwd(activeVolume, unknownCwd, target string) bool {
	if unknownCwd == "" {
		return false
	}
	if filepath.IsAbs(target) {
		return false
	}
	if vol := filepath.VolumeName(target); vol != "" {
		// Drive-relative/drive-absolute on some volume. Only the volume whose cwd
		// is unknown makes it ambiguous; resolveShellWritePath already reports any
		// other unknown volume as ambiguous on its own.
		return strings.EqualFold(vol, unknownCwd)
	}
	if len(target) > 0 && os.IsPathSeparator(target[0]) {
		// Volume-less rooted: anchored to the active volume's root, not its cwd.
		return false
	}
	// Plain relative: resolved against the active drive's cwd.
	return strings.EqualFold(activeVolume, unknownCwd)
}

// resolveShellWritePath resolves a write target against the directory the
// command runs in, under the Windows path spellings the shell channel must
// classify (#664). It is shell-specific on purpose: the native write channel's
// resolver (resolveSafetyPathWithMode, in git_worktree_safety.go) is the #668 owner and
// must not grow this logic, so the dual-reading guard stays the single owner of
// its own comparison (ADR-0014 §1).
//
// Three cases sit beyond the plain relative join:
//
//   - Volume-less rooted (\foo): rooted at the current volume's root, with no
//     volume in the target. It is anchored to the shell's active volume, so
//     \foo with an active C: drive becomes C:\foo.
//   - Same-volume drive-relative (C:foo): relative to the current directory on
//     that drive. The session's cwd is the base, so the relative part is joined
//     to it.
//   - Different-volume drive-relative (D:foo) when the base is on C:: the
//     per-drive current directory of D cannot be reconstructed here, so that
//     candidate is reported as ambiguous. The enclosing shell command refuses
//     only when no other backslash interpretation resolves the same target span;
//     an independently resolved candidate is still classified.
func resolveShellWritePath(base, activeVolume, path string) (string, bool) {
	if filepath.IsAbs(path) {
		return path, false
	}
	pathVolume := filepath.VolumeName(path)
	baseVolume := filepath.VolumeName(base)
	if pathVolume == "" && activeVolume != "" && len(path) > 0 && os.IsPathSeparator(path[0]) {
		// Volume-less rooted: anchor to the active volume's root.
		rooted := activeVolume + path
		if filepath.IsAbs(rooted) {
			return filepath.Clean(rooted), false
		}
	}
	if pathVolume != "" {
		if !strings.EqualFold(pathVolume, baseVolume) {
			// Different volume: the per-drive cwd is unknowable.
			return "", true
		}
		// Same volume: resolve the relative part against the base directory.
		return filepath.Join(base, strings.TrimPrefix(path, pathVolume)), false
	}
	return filepath.Join(base, path), false
}

// evaluateWriteTargets refuses the first target that lands in the shared
// checkout of the bound repository.
func evaluateWriteTargets(targets []string) (bool, string) {
	for _, target := range targets {
		if block, reason := evaluateFileWriteSafety(target); block {
			return true, reason
		}
	}
	return false, ""
}

// tokenizeSegments splits command at unquoted `;`, `&`, `|` and newline into
// segments of words. An unquoted `(` that starts a word, but not a `>(`,
// opens a subshell, and an unquoted `)` that closes no `(` read inside a word
// (`$(`, `<(`, `>(`, `$((`, `@(`) closes one: each is a segment of its own. It is the one shell reader for both safety guards, and it
// keeps what a dequote-then-split reader loses: whether a `>` was quoted,
// whether a word carries shell expansion, and a quoted space or operator inside
// one word.
//
// It reads the compound commands whose grammar decides what a paren or brace
// is. A case's pattern list ends at `)`, may open with `(`, and returns after
// `;;`, `;&` or `;;&`: neither pattern paren is a subshell, and `case WORD in`
// and each pattern list are segments of their own. A case never ended with
// `esac` leaves a last segment holding one unfinished token, as does a
// substitution substitutionEnd cannot end. The body of a

// function definition (`name()`, `name ()`, `function name`, `function
// name()`) and a coproc's brace group run apart from the commands around
// them: such a `{ ... }`, or an if, while, until, for, select or case body, is
// enclosed in a body subshell pair of its own, so both guards read it from the
// directory the definition stands in and a `cd` in it does not return. A
// segment a `|` feeds or ends, or a lone `&` ends, is apart; `>&N`, `>&-`
// and `&>` are redirections, not that `&`.
//
// Heredoc stripping is the caller's job and is done once, with POSIX delimiter
// rules, before either backslash reading tokenizes: a heredoc body is content,
// not a command line, and the same "one payload, one channel" rule BEO-62
// settled applies to it. Tokenizing it refused a legitimate write whenever the
// content happened to look like a command — this file's own ADR is such a
// document.
func tokenizeSegments(mode backslashMode, command string) [][]shellToken {
	var segments [][]shellToken
	var segment []shellToken
	runes := []rune(command)
	var word, raw strings.Builder
	// sub and subRaw read the word with each parameter expansion that
	// substitutes its word replaced by that word, split where bash splits
	// it; subRaw keeps each `$'...'` part undecoded, as raw does.
	var sub, subRaw substitutedReading
	substituted := false
	wordStart := -1
	rawStart, rawEnd := -1, -1
	expandable := false
	undecodable := false
	splitsAtIFS := false
	// afterDollar is an expanding `$` just read: bash removes a line
	// continuation before it reads the expansion, which this does not, so a
	// continuation there or inside a `${...}` group makes the word undecodable.
	afterDollar := false
	// quoted records that the word held quoting, so an empty quoted word
	// (`''`, `""`, `$''`) is still a word, as it is to the shell.
	quoted := false
	// vanish reads the word as it is decoded, line continuations and
	// escapes already removed, for whether it may expand to nothing.
	var vanish vanishReading
	quote := rune(0)
	escaped := false
	// groups are the `${...}` groups open at the current rune, innermost
	// last: bash keeps a group in one word whatever blanks, operators or
	// quotes it holds, inside double quotes too.
	var groups []braceGroup
	// wordParens counts the `(` read inside words and not yet closed; it
	// spans words and segments, as a `$(...)` does.
	wordParens := 0
	// literal records a character the word took literally.
	literal := false
	// constructs are the compound commands open at the current rune,
	// innermost last. headPending is a function definition's head just
	// ended by a newline, whose body may follow.
	var constructs []shellConstruct
	headPending := false
	// lead counts the leading words of segment bash reads before its command
	// word (readsBeforeCommand), and assigned the assignments after them.
	// Both count a prefix of segment, so a shorter segment clamps them.
	lead, assigned := 0, 0
	// broken is where a substitution this cannot end starts, -1 if none.
	broken := -1
	// comment is an unquoted `#` at a word's start read, up to its newline:
	// its words are tokenized, but nothing in it is grammar.
	comment := false
	// detach marks the segment being ended as detached, and piped the one
	// after it, which a pipe feeds.
	detach, piped := false, false
	// closing is the first segment of a construct just closed, -1 if none:
	// the closing segment, or the construct's first, detached makes every
	// segment from it on apart.
	closing := -1
	var readWord func(token shellToken)
	flushWord := func() {
		var token shellToken
		appended := false
		if word.Len() > 0 || quoted {
			token = shellToken{text: word.String(), expandable: expandable, plain: !quoted && !expandable && !literal, undecodable: undecodable, splitsAtIFS: splitsAtIFS, start: rawStart, end: rawEnd}
			var readings [][]shellToken
			if substituted {
				readings = append(readings, sub.finish(false), subRaw.finish(true))
			}
			readings = append(readings, []shellToken{{text: raw.String(), expandable: expandable, undecoded: true}})
			for _, reading := range readings {
				if len(reading) == 1 && reading[0].text == token.text {
					continue
				}
				if slices.ContainsFunc(token.alternates, func(seen []shellToken) bool {
					return slices.Equal(segmentWords(seen), segmentWords(reading))
				}) {
					continue
				}
				for i := range reading {
					reading[i].start, reading[i].end = rawStart, rawEnd
				}
				token.alternates = append(token.alternates, reading)
			}
			// A word made only of expansions can expand to nothing, and the
			// shell then removes it: its absent reading is empty.
			if rawStart >= 0 && vanish.finish(quote) && !slices.ContainsFunc(token.alternates, func(seen []shellToken) bool { return len(seen) == 0 }) {
				token.alternates = append(token.alternates, []shellToken{})
			}
			segment = append(segment, token)
			appended = true
		}
		word.Reset()
		raw.Reset()
		sub, subRaw = substitutedReading{}, substitutedReading{}
		vanish = vanishReading{}
		wordStart, rawStart, rawEnd, expandable, undecodable, splitsAtIFS, quoted, substituted, afterDollar, literal = -1, -1, -1, false, false, false, false, false, false, false
		if appended {
			readWord(token)
		}
	}
	flushSegment := func() {
		flushWord()
		if len(segment) > 0 {
			segment[0].apart = detach || piped
			piped = false
			headPending = !comment && definesFunction(segment)
			segments = append(segments, segment)
			segment = nil
		}
		if closing >= 0 && (detach || segments[closing][0].apart) {
			for _, apart := range segments[closing:] {
				apart[0].apart = true
			}
		}
		closing = -1
		detach = false
	}
	// scope ends the segment before token, the last word of segment, with a
	// subshell operator of its own.
	scope := func(token shellToken, operator string) {
		segment = segment[:len(segment)-1]
		flushSegment()
		segments = append(segments, []shellToken{{text: operator, subshell: true, body: true, start: token.start, end: token.start}})
		segment = []shellToken{token}
		lead, assigned = 0, 0
		if readsBeforeCommand(segment, 0) {
			lead = 1
		}
	}
	// split ends the segment before token, the last word of segment, so a
	// definition head after reserved words (`{ g() {`, `then function g`)
	// starts a segment of its own, as a head does at a line's start.
	split := func(token shellToken) {
		segment = segment[:len(segment)-1]
		fed := piped
		flushSegment()
		piped = fed
		segment = []shellToken{token}
		lead, assigned = 0, 0
	}
	// closeConstruct ends the innermost construct at token, its last word.
	closeConstruct := func(token shellToken) {
		top := len(constructs) - 1
		if constructs[top].scoped {
			scope(token, ")")
		}
		closing = constructs[top].from
		constructs = constructs[:top]
	}
	readWord = func(token shellToken) {
		prefix := segment[:len(segment)-1]
		n := len(prefix)
		lead = min(lead, n)
		assigned = min(assigned, n-lead)
		command := lead == n
		if command && readsBeforeCommand(segment, n) {
			lead++
		} else if lead+assigned == n && isAssignment(token.text) {
			assigned++
		}
		top := len(constructs) - 1
		if top >= 0 && constructs[top].kind == constructCase {
			switch c := &constructs[top]; c.phase {
			case caseWord:
				c.phase = caseIn
				return
			case caseIn:
				if token.plain && token.text == "in" {
					c.phase = casePattern
					flushSegment()
				} else {
					constructs = constructs[:top]
				}
				return
			case casePattern:
				if token.plain && token.text == "esac" && len(prefix) == 0 {
					closeConstruct(token)
				}
				return
			}
		}
		if !token.plain || comment {
			return
		}
		if command && n > 0 && (token.text == "function" || definesFunction([]shellToken{token})) && !slices.ContainsFunc(prefix, func(word shellToken) bool { return word.text == "coproc" }) {
			split(token)
			return
		}
		kind, compound := compoundOpeners[token.text]
		switch {
		case compound && (command || opensScope(prefix, headPending)):
			// A compound command a function or a coproc runs as its body is
			// scoped as a brace body is.
			scoped := opensScope(prefix, headPending)
			if scoped {
				scope(token, "(")
			}
			constructs = append(constructs, shellConstruct{kind: kind, scoped: scoped, start: token.start, from: constructFrom(segments, scoped)})
		case command && top >= 0 && compoundClosers[constructs[top].kind] == token.text:
			closeConstruct(token)
		case token.text == "{" && opensScope(prefix, headPending):
			scope(token, "(")
			constructs = append(constructs, shellConstruct{kind: constructBrace, scoped: true, from: constructFrom(segments, true)})
		case command && token.text == "{":
			constructs = append(constructs, shellConstruct{kind: constructBrace, from: len(segments)})
		case command && token.text == "}" && top >= 0 && constructs[top].kind == constructBrace:
			closeConstruct(token)
		}
	}
	touch := func(i int) {
		if rawStart < 0 {
			rawStart = i
		}
		rawEnd = i + 1
	}
	markQuoted := func() {
		quoted = true
		sub.open, subRaw.open = true, true
	}
	add := func(r rune, i int, isLiteral bool) {
		if wordStart < 0 {
			wordStart = i
		}
		literal = literal || isLiteral
		// A `$` or backtick names a shell expansion only when the shell would
		// actually perform one here. Inside single quotes, and behind a POSIX
		// backslash escape (outside quotes or inside double quotes), the
		// character is literal: the path it sits in is one this guard can classify,
		// and calling it expandable would drop a genuine protected-path target
		// (#664).
		expands := !isLiteral && (r == '$' || r == '`')
		expandable = expandable || expands
		afterDollar = expands && r == '$'
		if len(groups) == 0 {
			vanish.add(r, isLiteral, quote == '"')
		}
		word.WriteRune(r)
		raw.WriteRune(r)
		sub.add(string(r), expands)
		subRaw.add(string(r), expands)
	}
	// openGroup reads the `${` at runes[i]. A group whose parameter and
	// operator do not parse is undecodable; an unterminated one is too, and
	// is not a group.
	openGroup := func(i int) int {
		end, ok := closingBrace(runes, i+2)
		if !ok {
			if len(groups) == 0 {
				vanish.unterminated(quote == '"')
			}
			undecodable = true
			touch(i)
			add('$', i, false)
			return i
		}
		body, substitutes, atForm, parsed := parameterExpansion(runes, i+2, end)
		if !parsed || (mode == backslashEscapes && strings.Contains(string(runes[i:end]), "\\\n")) {
			undecodable = true
		}
		if len(groups) == 0 {
			vanish.group(quote == '"', atForm)
		}
		splitsAtIFS = splitsAtIFS || (substitutes && quote != '"')
		if !substitutes {
			body = i + 2
		}
		groups = append(groups, braceGroup{end: end, substitutes: substitutes, quoted: quote == '"'})
		substituted = substituted || substitutes
		for k := i; k < body; k++ {
			touch(k)
			if substitutes {
				word.WriteRune(runes[k])
				raw.WriteRune(runes[k])
			} else {
				add(runes[k], k, false)
			}
		}
		expandable = true
		return body - 1
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if n := len(groups); n > 0 && i == groups[n-1].end {
			touch(i)
			if groups[n-1].substitutes {
				word.WriteRune(r)
				raw.WriteRune(r)
			} else {
				add(r, i, false)
			}
			groups = groups[:n-1]
			continue
		}
		if comment && !strings.ContainsRune(" \t\n;&|>", r) {
			touch(i)
			add(r, i, false)
			continue
		}
		if escaped {
			touch(i - 1)
			touch(i)
			// A backslash-newline is a line continuation: the shell removes
			// both and joins the text around them.
			if r != '\n' {
				add(r, i, true)
			} else if afterDollar {
				undecodable = true
			}
			escaped = false
			continue
		}
		if quote == '\'' {
			touch(i)
			if r == quote {
				quote = 0
			} else {
				add(r, i, true)
			}
			continue
		}
		if quote == '"' && r == '\\' && mode == backslashEscapes {
			if i+1 < len(runes) {
				next := runes[i+1]
				if next == '$' || next == '`' || next == '"' || next == '\\' || next == '\n' {
					touch(i)
					touch(i + 1)
					i++
					if next != '\n' {
						add(next, i, true)
					} else if afterDollar {
						undecodable = true
					}
					continue
				}
			}
			touch(i)
			add(r, i, false)
			continue
		}
		if r == '\\' && mode == backslashEscapes {
			touch(i)
			escaped = true
			continue
		}
		// A command substitution is one piece of the word it stands in,
		// unquoted, inside double quotes or inside a `${...}` group: nothing
		// in it is grammar here. One this cannot end is the rest of the
		// command, which the POSIX reading leaves unfinished: the literal
		// reading reads a POSIX-escaped backtick as one.
		if (r == '$' && i+1 < len(runes) && runes[i+1] == '(') || r == '`' {
			end, ok := substitutionEnd(runes, i)
			if !ok {
				if broken < 0 && mode == backslashEscapes {
					broken = i
				}
				end = len(runes)
			}
			for k := i; k < end; k++ {
				touch(k)
				add(runes[k], k, false)
			}
			i = end - 1
			continue
		}
		// Inside a `${...}` opened within double quotes, bash nests quotes:
		// a `"` opens or closes an inner quote rather than the outer one,
		// and outside that inner quote `$"..."` and `$'...'` are read as
		// they are unquoted.
		var group *braceGroup
		if n := len(groups); n > 0 && groups[n-1].quoted && quote == '"' {
			group = &groups[n-1]
		}
		nested := group != nil && !group.inner
		if quote == '"' && group != nil && r == '"' {
			touch(i)
			group.inner = !group.inner
			markQuoted()
			continue
		}
		if quote == '"' && !(nested && r == '$' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\'')) {
			if r == '$' && i+1 < len(runes) && runes[i+1] == '{' {
				i = openGroup(i)
				continue
			}
			touch(i)
			if r == quote {
				quote = 0
				if len(groups) == 0 {
					vanish.closeQuote()
				}
			} else {
				add(r, i, false)
			}
			continue
		}
		// `$` and a special-parameter character are one expansion (`$$` is
		// the PID), so the `$` after them never starts ANSI-C quoting.
		if r == '$' && i+1 < len(runes) && strings.ContainsRune("$?!#-@*0123456789", runes[i+1]) {
			touch(i)
			add(r, i, false)
			i++
			touch(i)
			add(runes[i], i, false)
			continue
		}
		// bash reads `$"..."` (locale translation) as the double-quoted text.
		if r == '$' && i+1 < len(runes) && runes[i+1] == '"' {
			touch(i)
			continue
		}
		if r == '$' && i+1 < len(runes) && runes[i+1] == '\'' {
			markQuoted()
			text, end, ok := readANSICQuote(runes, i+2)
			touch(i)
			touch(end - 1)
			rawText := string(runes[i+2 : max(end-1, i+2)])
			if len(groups) == 0 {
				vanish.ansiC(text, ok)
			}
			if !ok {
				// The shell's value is unknown, so the word fails closed. Its
				// text is the raw word without the `$`, which is shorter than
				// the raw word, so a caller that reads a word as shell again
				// never gets the same word back.
				text = string(runes[i+1 : end])
				rawText = text
				expandable, undecodable = true, true
			}
			word.WriteString(text)
			raw.WriteString(rawText)
			sub.add(text, false)
			subRaw.add(rawText, false)
			i = end - 1
			continue
		}
		if r == '$' && i+1 < len(runes) && runes[i+1] == '{' {
			i = openGroup(i)
			continue
		}
		if n := len(groups); n > 0 && strings.ContainsRune(" \t\n;&|>()", r) {
			touch(i)
			// Unquoted, the word an expansion substitutes is split at blanks;
			// the result of an expansion is never an operator.
			if groups[n-1].substitutes && strings.ContainsRune(" \t\n", r) {
				word.WriteRune(r)
				raw.WriteRune(r)
				sub.split()
				subRaw.split()
			} else {
				add(r, i, false)
			}
			continue
		}
		switch r {
		case '\'', '"':
			touch(i)
			quote = r
			markQuoted()
			if len(groups) == 0 {
				vanish.openQuote()
			}
		case '|':
			if word.Len() == 0 && !quoted && len(segment) > 0 && segment[len(segment)-1].redirects {
				continue
			}
			pipe := (i == 0 || runes[i-1] != '|') && (i+1 == len(runes) || runes[i+1] != '|')
			detach = pipe
			flushSegment()
			piped = pipe
		case '&':
			if i+1 < len(runes) && runes[i+1] == '>' {
				// `&>` and `&>>` redirect: the `>` that follows is the operator.
				continue
			}
			if n := len(segment); i > 0 && runes[i-1] == '>' && n > 0 && segment[n-1].redirects {
				// `>&` duplicates a descriptor: `>&2`, `>&-` and `>&2-` name
				// no file, so the operator and the fd before it (redirectPrefix)
				// leave the segment; `>&word` writes word, which stays its target.
				j := i + 1
				for j < len(runes) && '0' <= runes[j] && runes[j] <= '9' {
					j++
				}
				if j < len(runes) && runes[j] == '-' {
					j++
				}
				if j > i+1 && (j == len(runes) || strings.ContainsRune(" \t\n;&|()<>", runes[j])) {
					fd := n > 1 && redirectPrefix(segment[n-2], segment[n-1])
					segment = segment[:n-1]
					if fd {
						segment = segment[:n-2]
					}

					i = j - 1
				}
				continue
			}
			detach = (i == 0 || !strings.ContainsRune("&<|", runes[i-1])) && (i+1 == len(runes) || runes[i+1] != '&')
			flushSegment()
		case ';', '\n':
			flushSegment()
			if r == '\n' {
				comment = false
			}
			if n := len(constructs); r == ';' && !comment && n > 0 &&
				constructs[n-1].kind == constructCase && constructs[n-1].phase == caseBody &&
				i+1 < len(runes) && (runes[i+1] == ';' || runes[i+1] == '&') {
				i++
				if runes[i] == ';' && i+1 < len(runes) && runes[i+1] == '&' {
					i++
				}
				constructs[n-1].phase = casePattern
			}
		case ' ', '\t':
			flushWord()
		case '#':
			comment = len(groups) == 0 && word.Len() == 0 && !quoted && rawStart < 0
			touch(i)
			add(r, i, false)
		case '[':
			// At assignment position, `NAME[` opens a subscript bash reads
			// to its `]` as part of the word, blanks and operators included.
			lead = min(lead, len(segment))
			assigned = min(assigned, len(segment)-lead)
			top := len(constructs) - 1
			end, ok := subscriptWordEnd(runes, i)
			if ok && word.Len() > 0 && !quoted && !expandable && !literal && identifierLen(word.String()) == word.Len() &&
				lead+assigned == len(segment) && (top < 0 || constructs[top].kind != constructCase || constructs[top].phase == caseBody) {
				for k := i; k <= end; k++ {
					touch(k)
					add(runes[k], k, false)
				}
				i = end
				continue
			}
			touch(i)
			add(r, i, false)
		case '>':
			flushWord()
			if m := len(segment); m > 0 && segment[m-1].plain && segment[m-1].end == i && descriptorName(segment[m-1].text) &&
				!slices.ContainsFunc(segment[m-1].alternates, func(seen []shellToken) bool { return len(seen) == 0 }) {
				segment[m-1].alternates = append(segment[m-1].alternates, []shellToken{})
			}
			segment = append(segment, shellToken{text: ">", redirects: true, start: i, end: i + 1})
		case '(', ')':
			if r == '(' && (word.Len() > 0 || quoted || (i > 0 && runes[i-1] == '>')) {
				wordParens++
				touch(i)
				add(r, i, false)
				continue
			}
			if r == ')' && wordParens > 0 {
				wordParens--
				touch(i)
				add(r, i, false)
				continue
			}
			flushWord()
			if r == '(' && len(segment) > 0 {
				// `name ()` is a function definition's head, read as `name()`.
				j := i + 1
				for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
					j++
				}
				head := slices.Clone(segment)
				head[len(head)-1].text += "()"
				if j < len(runes) && runes[j] == ')' && definesFunction(head) {
					segment = head
					lead = min(lead, len(segment)-1)
					assigned = min(assigned, len(segment)-1-lead)
					i = j
					continue
				}
			}
			flushSegment()
			n := len(constructs)
			if r == '(' {
				if n > 0 && constructs[n-1].kind == constructCase && constructs[n-1].phase == casePattern {
					continue
				}
				from := len(segments)
				if from > 0 && definesFunction(segments[from-1]) {
					from--
				}
				constructs = append(constructs, shellConstruct{kind: constructSubshell, from: from})
			} else if n > 0 && constructs[n-1].kind == constructCase {
				if constructs[n-1].phase == casePattern {
					constructs[n-1].phase = caseBody
					continue
				}
				if constructs[n-1].phase != caseBody {
					constructs, n = constructs[:n-1], n-1
				}
			}
			if r == ')' && n > 0 && constructs[n-1].kind == constructSubshell {
				closing = constructs[n-1].from
				constructs = constructs[:n-1]
			}
			segments = append(segments, []shellToken{{text: string(r), subshell: true, start: i, end: i + 1}})
		default:
			touch(i)
			add(r, i, false)
		}
	}
	flushSegment()
	if broken >= 0 {
		segments = append(segments, []shellToken{{unfinished: true, start: broken, end: len(runes)}})
	}
	for _, c := range constructs {

		if c.kind == constructCase && c.phase >= casePattern {
			segments = append(segments, []shellToken{{unfinished: true, start: c.start, end: len(runes)}})
			break
		}
	}
	for i, segment := range segments {
		if simpleFunctionBody(segment) {
			segments[i][0].simpleFunctionBody = true
		}
		if i+1 < len(segments) && definesFunction(segment) &&
			strings.HasSuffix(segment[len(segment)-1].text, "()") && simpleFunctionBody(slices.Concat(segment, segments[i+1])) {
			start, end := segment[len(segment)-1].end, segments[i+1][0].start
			if start >= 0 && end >= start && end <= len(runes) {
				gap := string(runes[start:end])
				if strings.ContainsRune(gap, '\n') && strings.TrimSpace(gap) == "" {
					segments[i+1][0].simpleFunctionBody = true
				}
			}
		}
	}
	return segments
}

// shellConstruct is a compound command open while tokenizing: a subshell, a
// brace group, an if, a loop or a case in one of its phases, which started at
// rune start; scoped when a function or a coproc runs it apart.
type shellConstruct struct {
	kind   int
	phase  int
	scoped bool
	start  int
	// from is the construct's first segment: a scoped body's head, or the
	// segment it opens.
	from int
}

// constructFrom is the first segment of a construct opened at the end of
// segments: the head before a scoped body's operator, or the next segment.
func constructFrom(segments [][]shellToken, scoped bool) int {
	if scoped {
		return len(segments) - 2
	}
	return len(segments)
}

const (
	constructSubshell = iota
	constructBrace
	constructCase
	constructIf
	constructLoop
)

// compoundOpeners are the reserved words that open a compound command ended
// by a reserved word, and compoundClosers the word that ends each. `[[` holds
// no command, so no body of it moves the directory.
var (
	compoundOpeners = map[string]int{"case": constructCase, "if": constructIf, "while": constructLoop, "until": constructLoop, "for": constructLoop, "select": constructLoop}
	compoundClosers = map[int]string{constructCase: "esac", constructIf: "fi", constructLoop: "done"}
)

// The phases of a case: its word, `in`, a pattern list, and a body.
const (
	caseWord = iota
	caseIn
	casePattern
	caseBody
)

// readsBeforeCommand reports whether bash reads words[k] before the command
// word of a segment whose earlier words it read so too: a reserved word, time,
// or time's -p. A word after only such words stands where bash reads a
// reserved word.
func readsBeforeCommand(words []shellToken, k int) bool {
	token := words[k]
	return token.plain && (slices.Contains(reservedWords, token.text) || token.text == "time" || token.text == "-p" && k > 0 && words[k-1].text == "time")
}

// simpleFunctionBody reports a parenthesized function head whose body does
// not begin with a compound command bash accepts as a function body. The
// guards refuse these bodies rather than interpreting them as commands in
// the surrounding shell.
func simpleFunctionBody(segment []shellToken) bool {
	if len(segment) == 0 {
		return false
	}
	headLen := 1
	if len(segment) > 1 && segment[0].plain && segment[0].text == "function" {
		headLen = 2
	}
	if len(segment) <= headLen || !definesFunction(segment[:headLen]) ||
		!strings.HasSuffix(segment[headLen-1].text, "()") {
		return false
	}
	body, _ := withoutRedirections(segment[headLen:])
	if len(body) == 0 {
		return false
	}
	first := body[0]
	if first.body {
		return false
	}
	if first.subshell && first.text == "(" {
		return false
	}
	return !(first.plain && slices.Contains([]string{"{", "if", "while", "until", "for", "select", "case", "[["}, first.text))
}

// definesFunction reports whether segment is a function definition's head:
// `name()`, `function name` or `function name()`.
func definesFunction(segment []shellToken) bool {
	switch len(segment) {
	case 1:
		name, ok := strings.CutSuffix(segment[0].text, "()")
		return ok && name != "" && !strings.ContainsAny(name, "=()") && segment[0].plain
	case 2:
		return segment[0].plain && segment[0].text == "function" && segment[1].plain
	}
	return false
}

// opensScope reports whether a `{` after prefix opens a body bash runs apart
// from the commands around it: a function's, prefix being its head or empty
// after a head a newline ended (headPending), or a coproc's.
func opensScope(prefix []shellToken, headPending bool) bool {
	n := len(prefix)
	switch {
	case n == 0:
		return headPending
	case definesFunction(prefix):
		return true
	}
	return prefix[n-1].plain && prefix[n-1].text == "coproc" || n >= 2 && prefix[n-2].plain && prefix[n-2].text == "coproc"
}

// vanishReading follows a word as the tokenizer decodes it, outside any
// `${...}` group, for whether it holds nothing but expansions any of which may
// expand to nothing: unquoted ones, and quoted `@` forms, which bash removes
// with the word when they have no elements, beside which an empty quoted part
// may stand. Any other quoted part leaves an empty word. A command
// substitution is read as one that may not: its output is not read here.

type vanishReading struct {
	not    bool
	quoted bool
	atForm bool
	partAt bool
	open   bool
	// dollar is 1 after an expanding `$` and 2 inside the name after it.
	dollar int
}

// add reads a decoded rune, inside double quotes when dquoted.
func (v *vanishReading) add(r rune, literal, dquoted bool) {
	nameRune := r == '_' || unicode.IsLetter(r)
	switch {
	case v.open || v.not:
	case literal:
		v.not = true
	case dquoted && v.dollar == 1 && r == '@':
		v.dollar, v.partAt = 0, true
	case dquoted && v.dollar == 0 && r == '$':
		v.dollar = 1
	case dquoted:
		v.not = true
	case v.dollar == 1 && strings.ContainsRune("$?!#-@*0123456789", r):
		v.dollar = 0
	case r == '$':
		v.dollar = 1
	case v.dollar == 1 && nameRune:
		v.dollar = 2
	case v.dollar == 2 && (nameRune || unicode.IsDigit(r)):
	default:
		v.not = true
	}
}

// group reads a terminated `${...}` group; inside double quotes only a `@`
// form may vanish.
func (v *vanishReading) group(dquoted, atForm bool) {
	switch {
	case dquoted && !atForm:
		v.not = true
	case dquoted:
		v.partAt = true
	}
	v.dollar = 0
}

// unterminated reads a `${` with no closing brace: unquoted, the word may
// vanish whatever follows.
func (v *vanishReading) unterminated(dquoted bool) {
	if dquoted {
		v.not = true
	}
	if !v.not {
		v.open = true
	}
}

func (v *vanishReading) openQuote() {
	v.quoted, v.dollar = true, 0
}

func (v *vanishReading) closeQuote() {
	if v.dollar == 1 {
		v.not = true
	}
	v.atForm = v.atForm || v.partAt
	v.partAt, v.dollar = false, 0
}

// ansiC reads a `$'...'` part decoded to text, ok false when it is not.
func (v *vanishReading) ansiC(text string, ok bool) {
	v.openQuote()
	if !ok || text != "" {
		v.not = true
	}
}

// finish reports whether the word may vanish, quote being the quote left open
// at its end.
func (v *vanishReading) finish(quote rune) bool {
	if v.open {
		return true
	}
	if v.not || quote != 0 || v.dollar == 1 {
		return false
	}
	return !v.quoted || v.atForm
}

// braceGroup is one `${...}` group open while tokenizing: the index of its
// closing `}`, whether its operator substitutes its word, and whether it
// opened inside double quotes, where inner reports an open nested quote.
type braceGroup struct {
	end         int
	substitutes bool
	quoted      bool
	inner       bool
}

// substitutedReading collects the words of a word read with each
// substituting parameter expansion replaced by its word.
type substitutedReading struct {
	words      []shellToken
	current    strings.Builder
	open       bool
	expandable bool
}

func (s *substitutedReading) add(text string, expandable bool) {
	s.current.WriteString(text)
	s.open = s.open || text != ""
	s.expandable = s.expandable || expandable
}

func (s *substitutedReading) split() {
	if s.open {
		s.words = append(s.words, shellToken{text: s.current.String(), expandable: s.expandable})
	}
	s.current.Reset()
	s.open, s.expandable = false, false
}

// finish returns the reading's words, marked undecoded when the reading
// keeps `$'...'` parts undecoded.
func (s *substitutedReading) finish(undecoded bool) []shellToken {
	s.split()
	words := slices.Clone(s.words)
	for i := range words {
		words[i].undecoded = undecoded
	}
	return words
}

// segmentCandidates returns the positions a classifier reads in segment. A
// position holds a candidate word from each reading of a token that is long
// enough to reach it: the token as written and each alternate, nested
// defaults already flattened. A step names the position after its word,
// len(nodes) past the last. A token with an empty reading can be skipped:
// a classifier that reaches its first position reaches skip too (zero when
// it cannot). The slices
// the nodes and steps hold share backing arrays, so the graph grows with
// tokens times alternates.
func segmentCandidates(segment []shellToken) []candidateNode {
	first := make([]int, len(segment)+1)
	for i, token := range segment {
		longest := 1
		for _, alternate := range token.alternates {
			longest = max(longest, len(alternate))
		}
		first[i+1] = first[i] + longest
	}
	nodes := make([]candidateNode, first[len(segment)])
	for i, token := range segment {
		if slices.ContainsFunc(token.alternates, func(alternate []shellToken) bool { return len(alternate) == 0 }) {
			nodes[first[i]].skip = first[i+1]
		}
		for _, reading := range slices.Concat([][]shellToken{{token}}, token.alternates) {
			for k, candidate := range reading {
				next := first[i+1]
				if k+1 < len(reading) {
					next = first[i] + k + 1
				}
				nodes[first[i]+k].steps = append(nodes[first[i]+k].steps, candidateStep{token: candidate, rest: reading[k+1:], from: i + 1, next: next})
			}
		}
	}
	// A first position's words are followed in one array by those of each
	// first position a skip reaches from it; any other position's words
	// stand alone.
	var firstWords, otherWords []string
	firstEnd := make([]int, len(segment)+1)
	for i := range segment {
		for n := first[i]; n < first[i+1]; n++ {
			for _, step := range nodes[n].steps {
				if n == first[i] {
					firstWords = append(firstWords, step.token.text)
				} else {
					otherWords = append(otherWords, step.token.text)
				}
			}
		}
	}
	firstEnd[len(segment)] = len(firstWords)
	for i, start := len(segment)-1, len(firstWords); i >= 0; i-- {
		start -= len(nodes[first[i]].steps)
		firstEnd[i] = start + len(nodes[first[i]].steps)
		if nodes[first[i]].skip > 0 {
			firstEnd[i] = firstEnd[i+1]
		}
		nodes[first[i]].following = firstWords[start:firstEnd[i]]
	}
	for n := len(nodes) - 1; n >= 0; n-- {
		if nodes[n].following == nil {
			otherWords, nodes[n].following = otherWords[:len(otherWords)-len(nodes[n].steps)], otherWords[len(otherWords)-len(nodes[n].steps):]
		}
	}
	return nodes
}

// candidateNode is one position of segmentCandidates. following is the words
// that can stand at it, the next positions a skip reaches included.
type candidateNode struct {
	steps     []candidateStep
	skip      int
	following []string
}

// candidateStep is one candidate word at a position: rest is the words after
// it in its reading and from the index of the token after that reading, so
// the words after it as written are rest and then segment[from:]. next is the
// position after it.
type candidateStep struct {
	token shellToken
	rest  []shellToken
	from  int
	next  int
}

// candidateTokens returns every token of segment and every token of each of
// its alternates.
func candidateTokens(segment []shellToken) []shellToken {
	var tokens []shellToken
	for _, token := range segment {
		tokens = append(tokens, token)
		for _, alternate := range token.alternates {
			tokens = append(tokens, alternate...)
		}
	}
	return tokens
}

// parameterExpansion parses the parameter and operator of a `${...}` group
// whose body is runes[i:end], runes[end] being its `}`. It returns where the
// word of an operator that substitutes it starts, and false when the group
// does not parse: bash either rejects it or reads it in a way this does not.
// The parameter is a name, digits or a special character, after an optional
// `!` (indirection) or `#` (length), with one optional `[subscript]`
// (subscriptEnd). It reports whether the group is a `@` form, which bash
// removes inside double quotes when it has no elements: of `@`, `name[@]` or
// `!prefix@`, whose operator does not substitute a default (`-`, `=`).
func parameterExpansion(runes []rune, i, end int) (int, bool, bool, bool) {
	nameStart := func(r rune) bool { return r == '_' || unicode.IsLetter(r) }
	j := i
	indirect, length := false, false
	if j+1 < end && (nameStart(runes[j+1]) || unicode.IsDigit(runes[j+1])) {
		indirect, length = runes[j] == '!', runes[j] == '#'
	} else if j+1 < end && runes[j] == '#' && strings.ContainsRune("@*", runes[j+1]) {
		length = true
	}
	if indirect || length {
		j++
	}
	atForm := false
	switch {
	case j < end && nameStart(runes[j]):
		for j < end && (nameStart(runes[j]) || unicode.IsDigit(runes[j])) {
			j++
		}
	case j < end && unicode.IsDigit(runes[j]):
		for j < end && unicode.IsDigit(runes[j]) {
			j++
		}
	case j < end && strings.ContainsRune("@*#?-$!", runes[j]):
		atForm = runes[j] == '@'
		j++
	default:
		return 0, false, false, false
	}
	if j < end && runes[j] == '[' {
		close, ok := subscriptEnd(runes, j, end)
		if !ok {
			return 0, false, false, false
		}
		atForm = string(runes[j:close+1]) == "[@]"
		j = close + 1
	}
	atForm = atForm && !length
	switch {
	case j == end:
		return 0, false, atForm, true
	case length:
		return 0, false, false, false
	case indirect && j+1 == end && (runes[j] == '*' || runes[j] == '@'):
		return 0, false, runes[j] == '@', true
	case runes[j] == ':' && j+1 < end && strings.ContainsRune("-=+", runes[j+1]):
		return j + 2, true, atForm && runes[j+1] == '+', true
	case runes[j] == ':' && j+1 < end:
		return 0, false, atForm, true
	case strings.ContainsRune("-=+", runes[j]):
		return j + 1, true, atForm && runes[j] == '+', true
	case strings.ContainsRune("?#%/^,", runes[j]):
		return 0, false, atForm, true
	case runes[j] == '@' && j+2 == end && strings.ContainsRune("QEPAKaUuLk", runes[j+1]):
		return 0, false, atForm, true
	}
	return 0, false, false, false
}

// subscriptEnd returns the index of the `]` that closes the `[` at
// runes[j], before end. A subscript is bracket-balanced text with no quote
// and no `}`.
func subscriptEnd(runes []rune, j, end int) (int, bool) {
	depth := 0
	for k := j; k < end; k++ {
		switch runes[k] {
		case '\'', '"', '}':
			return 0, false
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return k, true
			}
		}
	}
	return 0, false
}

// closingBrace returns the index of the `}` that closes a `${` whose body
// starts at runes[i], reading quotes, escapes, `$'...'` and nested `${`.
func closingBrace(runes []rune, i int) (int, bool) {
	depth := 0
	quote := rune(0)
	for k := i; k < len(runes); k++ {
		switch r := runes[k]; {
		case quote == '\'':
			if r == quote {
				quote = 0
			}
		case r == '\\':
			k++
		case (r == '$' && k+1 < len(runes) && runes[k+1] == '(') || r == '`':
			end, ok := substitutionEnd(runes, k)
			if !ok {
				return 0, false
			}
			k = end - 1
		case quote == 0 && r == '$' && k+1 < len(runes) && runes[k+1] == '\'':
			_, end, _ := readANSICQuote(runes, k+2)
			k = end - 1
		case r == '"' || (quote == 0 && r == '\''):
			if quote == r {
				quote = 0
			} else if quote == 0 {
				quote = r
			}
		case quote == 0 && r == '$' && k+1 < len(runes) && runes[k+1] == '{':
			depth++
			k++
		case quote == 0 && r == '}':
			if depth == 0 {
				return k, true
			}
			depth--
		}
	}
	return 0, false
}

// substitutionEnd returns the index just past the command substitution at
// runes[i]: a `$(...)` or `$((...))`, which ends at the `)` that balances its
// `(`, or a backtick one, which ends at the next unescaped backtick. In a
// `$(...)` it reads escapes, quotes, `$'...'`, `${...}`, nested substitutions,
// comments and heredoc bodies, none of whose parens count. It returns false
// when the substitution is unterminated, or when a `case` stands at a command
// word in a `$(...)`, whose pattern parens bash versions read apart.
func substitutionEnd(runes []rune, i int) (int, bool) {
	if runes[i] == '`' {
		for k := i + 1; k < len(runes); k++ {
			switch runes[k] {
			case '\\':
				k++
			case '`':
				return k + 1, true
			}
		}
		return 0, false
	}
	depth := 0
	// command is whether the word starting at word stands at a command word.
	command, word := true, -1
	var pending []heredocSpec
	endWord := func(k int) bool {
		if word < 0 {
			return true
		}
		text := string(runes[word:k])
		word = -1
		if command && text == "case" {
			return false
		}
		command = command && (slices.Contains(reservedWords, text) || text == "time")
		return true
	}
	for k := i + 1; k < len(runes); k++ {
		r := runes[k]
		switch {
		case r == '\\':
			if word < 0 {
				word = k
			}
			k++
		case r == '\'' || r == '"' || r == '`' || r == '$' && k+1 < len(runes) && strings.ContainsRune("'({", runes[k+1]):
			end, ok := quotedEnd(runes, k)
			if !ok {
				return 0, false
			}
			if word < 0 {
				word = k
			}
			k = end - 1
		case r == '#' && word < 0:
			for k+1 < len(runes) && runes[k+1] != '\n' {
				k++
			}
		case r == '<' && k+2 < len(runes) && runes[k+1] == '<' && runes[k+2] != '<':
			if !endWord(k) {
				return 0, false
			}
			spec, next, ok := readHeredocRedirect(runes, k)
			if !ok {
				return 0, false
			}
			pending = append(pending, spec)
			k = next - 1
		case strings.ContainsRune(" \t\n;&|()<>", r):
			if !endWord(k) {
				return 0, false
			}
			switch r {
			case '(':
				depth++
				command = true
			case ')':
				depth--
				if depth == 0 {
					return k + 1, true
				}
			case '\n':
				command = true
				if len(pending) > 0 {
					next, bodies := skipHeredocBodies(runes, k+1, pending)
					if slices.ContainsFunc(bodies, func(body stdinFeed) bool { return !body.terminated }) {
						return 0, false
					}

					k, pending = next-1, nil
				}
			case ';', '&', '|':
				command = true
			}
		default:
			if word < 0 {
				word = k
			}
		}
	}
	return 0, false
}

// quotedEnd returns the index just past the quoted or expanded piece of a
// word at runes[i]: a single, double or ANSI-C quote, a `${...}` group or a
// command substitution. It returns false when the piece is unterminated.
func quotedEnd(runes []rune, i int) (int, bool) {
	switch {
	case runes[i] == '`' || runes[i] == '$' && runes[i+1] == '(':
		return substitutionEnd(runes, i)
	case runes[i] == '"':
		for k := i + 1; k < len(runes); k++ {
			switch r := runes[k]; {
			case r == '\\':
				k++
			case r == '"':
				return k + 1, true
			case r == '`' || r == '$' && k+1 < len(runes) && (runes[k+1] == '(' || runes[k+1] == '{'):
				end, ok := quotedEnd(runes, k)
				if !ok {
					return 0, false
				}
				k = end - 1
			}
		}
		return 0, false
	case runes[i] == '$' && runes[i+1] == '{':
		end, ok := closingBrace(runes, i+2)

		return end + 1, ok
	}
	// A single or ANSI-C quote ends at its next unescaped `'`; only ANSI-C
	// reads a backslash.
	ansiC := runes[i] == '$'
	start := i + 1
	if ansiC {
		start++
	}
	for k := start; k < len(runes); k++ {
		switch {
		case ansiC && runes[k] == '\\':
			k++
		case runes[k] == '\'':
			return k + 1, true
		}
	}
	return 0, false
}

// subscriptWordEnd returns the index of the `]` that balances the `[` at
// runes[i], reading quotes and escapes, or false when none does.
func subscriptWordEnd(runes []rune, i int) (int, bool) {
	depth := 0
	for k := i; k < len(runes); k++ {
		switch runes[k] {
		case '\\':
			k++
		case '\'', '"':
			end := slices.Index(runes[k+1:], runes[k])
			if end < 0 {
				return 0, false
			}
			k += end + 1
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return k, true
			}
		}
	}
	return 0, false
}

// readANSICQuote reads a bash `$'...'` word whose body starts at runes[i]. It

// returns the decoded text and the index just past the closing quote, or false
// when the word is unterminated or holds an escape it does not decode. It
// decodes \a \b \e \E \f \n \r \t \v \\ \' \" \?, octal \NNN (one to three
// digits) and hex \xHH (one or two digits) as bash does, for byte values 1 to
// 0377. Everything else, including \c, \u, \U, a NUL byte and an unknown
// escape, is refused.
func readANSICQuote(runes []rune, i int) (string, int, bool) {
	end := i
	for end < len(runes) && runes[end] != '\'' {
		if runes[end] == '\\' {
			end++
		}
		end++
	}
	if end >= len(runes) {
		return "", len(runes), false
	}
	var out []byte
	for j := i; j < end; j++ {
		if runes[j] != '\\' {
			out = utf8.AppendRune(out, runes[j])
			continue
		}
		j++
		var value, digits int
		switch r := runes[j]; r {
		case 'a':
			value = '\a'
		case 'b':
			value = '\b'
		case 'e', 'E':
			value = 0x1b
		case 'f':
			value = '\f'
		case 'n':
			value = '\n'
		case 'r':
			value = '\r'
		case 't':
			value = '\t'
		case 'v':
			value = '\v'
		case '\\', '\'', '"', '?':
			value = int(r)
		case 'x':
			value, digits = escapeDigits(runes[j+1:end], 16, 2)
			j += digits
		default:
			value, digits = escapeDigits(runes[j:end], 8, 3)
			j += digits - 1
		}
		if value < 1 || value > 0377 {
			return "", end + 1, false
		}
		out = append(out, byte(value))
	}
	return string(out), end + 1, true
}

// escapeDigits reads up to max leading digits of base from runes and returns
// their value and count. No digit reads as value zero, which the caller refuses.
func escapeDigits(runes []rune, base, max int) (int, int) {
	value, n := 0, 0
	for ; n < max && n < len(runes); n++ {
		d, err := strconv.ParseUint(string(runes[n]), base, 8)
		if err != nil {
			break
		}
		value = value*base + int(d)
	}
	return value, n
}

// heredocSpec is one pending `<<DELIM` body: the word that ends it, whether
// `<<-` allows leading tabs on that terminator, and where its operator stood.
type heredocSpec struct {
	delimiter string
	stripTabs bool
	at        int
}

// stdinFeed is text a stripped `<<` body or `<<<` word feeds to its command's
// stdin. A here-string's text is still shell-quoted. An unterminated heredoc
// runs to the end of the payload and is reported as not terminated. at is the
// rune the operator left in the stripped command line.
type stdinFeed struct {
	text       string
	terminated bool
	hereString bool
	at         int
}

// splitHeredocBodies removes every heredoc body and here-string word from a
// command line, leaving the command words around it intact, and returns what
// it removed from stdin. Heredoc syntax is POSIX grammar, so this runs once
// with POSIX delimiter rules; the dual backslash readings tokenize what
// remains and only differ in how they read path backslashes. The git guard
// reads each feed as shell; the write guard reads only the ones a named shell
// reads as its script.
//
// Without this, each body line was split at its newline and tokenized as a
// command of its own: a `rm -rf <shared>/...` example inside a document became a
// real write target, and a `cd <shared>` line inside a document moved the
// resolution base for the genuine commands after the terminator. Both refused
// writes that must go through, which is the one failure this guard cannot have.
func splitHeredocBodies(command string) (string, []stdinFeed) {
	runes := []rune(command)
	var feeds []stdinFeed
	var out []rune
	var pending []heredocSpec
	quote := rune(0)
	escaped := false

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			out = append(out, r)
			escaped = false
			continue
		}
		if quote == '\'' {
			out = append(out, r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if quote == '"' && r == '\\' {
			out = append(out, r)
			if i+1 < len(runes) {
				next := runes[i+1]
				if next == '$' || next == '`' || next == '"' || next == '\\' || next == '\n' {
					escaped = true
				}
			}
			continue
		}
		if quote == 0 && r == '\\' {
			// POSIX heredoc scanning: a backslash quotes the next character outside
			// quotes. The surviving command text keeps the backslash so the dual
			// path readings see it; stripping is POSIX-only and runs once before
			// they tokenize.
			out = append(out, r)
			escaped = true
			continue
		}
		// A comment holds no heredoc, quote or substitution.
		if quote == 0 && r == '#' && (len(out) == 0 || strings.ContainsRune(" \t\n;&|()", out[len(out)-1])) {
			for i < len(runes) && runes[i] != '\n' {
				out = append(out, runes[i])
				i++
			}
			i--
			continue
		}
		// A substitution or a `${...}` group holds no heredoc and no
		// redirection of this command line: it is kept whole.

		if r == '`' || r == '$' && i+1 < len(runes) && (runes[i+1] == '(' || runes[i+1] == '{') {
			if end, ok := quotedEnd(runes, i); ok {
				out = append(out, runes[i:end]...)
				i = end - 1
				continue
			}
		}
		if quote != 0 {
			out = append(out, r)
			if r == quote {

				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			out = append(out, r)
		case '<':
			// Every `<` form reads: `<` a file, `<<<` a string, `<<` a body.
			// None of them names a write target, so the operator, the fd
			// number written before it and the word it consumes all leave
			// the command line here. A `{name}` before it is kept with a
			// `>&-` in the operator's place, which names no file, so the
			// tokenizer reads it both ways (descriptorName).
			prefix := len(out)
			for prefix > 0 && !strings.ContainsRune(" \t\n;&|()", out[prefix-1]) {
				prefix--
			}
			if word := string(out[prefix:]); isDigits(word) {
				out = out[:prefix]
			} else if descriptorName(word) {
				out = append(out, '>', '&', '-')
			}
			run := i
			for run < len(runes) && runes[run] == '<' {
				run++
			}
			if run-i == 2 {
				if spec, next, ok := readHeredocRedirect(runes, i); ok {
					spec.at = len(out)
					pending = append(pending, spec)
					out = append(out, ' ')
					i = next - 1
					continue
				}
			}
			out = append(out, ' ')
			// A `<(...)` source ends at its `(`: the process substitution
			// stays in the command line for the tokenizer to read.
			end := skipRedirectSource(runes, run)
			if run-i == 3 {
				feeds = append(feeds, stdinFeed{text: string(runes[run:end]), terminated: true, hereString: true, at: len(out) - 1})
			}
			i = end - 1
		case '\n':
			out = append(out, r)
			if len(pending) > 0 {
				var bodies []stdinFeed
				i, bodies = skipHeredocBodies(runes, i+1, pending)
				i--
				feeds = append(feeds, bodies...)
				pending = nil
			}
		default:
			out = append(out, r)
		}
	}
	for _, spec := range pending {
		feeds = append(feeds, stdinFeed{at: spec.at})
	}
	return string(out), feeds
}

// skipRedirectSource returns the index just past the word a read redirection
// consumes: the file `<` reads, or the text `<<<` feeds to stdin.
func skipRedirectSource(runes []rune, j int) int {
	for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
		j++
	}
	quote := rune(0)
	for j < len(runes) {
		r := runes[j]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			j++
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			j++
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '&' || r == '|' || r == '<' || r == '>' || r == '(' || r == ')' {
			break
		}
		j++
	}
	return j
}

// readHeredocRedirect reads a `<<DELIM` / `<<-DELIM` operator starting at i and
// returns the index just past the delimiter word.
func readHeredocRedirect(runes []rune, i int) (heredocSpec, int, bool) {
	j := i + 2
	spec := heredocSpec{}
	if j < len(runes) && runes[j] == '-' {
		spec.stripTabs = true
		j++
	}
	for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
		j++
	}
	var delimiter strings.Builder
	for j < len(runes) {
		r := runes[j]
		// A backslash quotes the character after it, so `<<\EOF` opens a body
		// that ends at `EOF`. Reading the backslash into the delimiter made it
		// match nothing and swallowed the rest of the payload — every command
		// after the terminator, including ones this guard claims to cover.
		if r == '\\' {
			// A backslash quotes the next character in the delimiter word, so
			// `<<\EOF` opens a body that ends at `EOF`. Both backslash readings
			// must agree on the delimiter, or the literal (Windows) reading would
			// read the backslash into the delimiter, never match the bare
			// terminator, and swallow every command named after it (#664 v2).
			j++
			if j < len(runes) {
				delimiter.WriteRune(runes[j])
				j++
			}
			continue
		}
		if r == '\'' {
			// Single-quoted delimiter: literal until the closing quote,
			// backslashes included. `<<'EOF'` opens a body ending at `EOF`.
			j++
			for j < len(runes) && runes[j] != '\'' {
				delimiter.WriteRune(runes[j])
				j++
			}
			if j < len(runes) {
				j++ // consume closing quote
			}
			continue
		}
		if r == '"' {
			// Double-quoted delimiter: POSIX quote removal applies, so `\\`
			// collapses to one backslash and `\"` to a literal quote. These change
			// the delimiter: `<<'DOC\\X'` and `<<"DOC\\X"` are different
			// terminators, and reading the doubled backslash literally would let
			// the body run past the real terminator and hide a protected command
			// after it (#664).
			j++
			for j < len(runes) && runes[j] != '"' {
				c := runes[j]
				if c == '\\' && j+1 < len(runes) {
					switch runes[j+1] {
					case '\\', '"', '$', '`':
						delimiter.WriteRune(runes[j+1])
						j += 2
					case '\n':
						j += 2 // line continuation: emit nothing
					default:
						// Backslash before a non-special char is kept literally.
						delimiter.WriteRune('\\')
						j++
					}
					continue
				}
				delimiter.WriteRune(c)
				j++
			}
			if j < len(runes) {
				j++ // consume closing quote
			}
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '&' || r == '|' || r == '<' || r == '>' {
			break
		}
		delimiter.WriteRune(r)
		j++
	}
	if delimiter.Len() == 0 {
		return heredocSpec{}, 0, false
	}
	spec.delimiter = delimiter.String()
	return spec, j, true
}

// skipHeredocBodies consumes the bodies of every pending heredoc, in the order
// they were opened, and returns the index where the command line resumes and
// each body. A body that is never terminated runs to the end of the payload.
func skipHeredocBodies(runes []rune, start int, pending []heredocSpec) (int, []stdinFeed) {
	i := start
	bodies := make([]stdinFeed, 0, len(pending))
	for _, spec := range pending {
		body := stdinFeed{at: spec.at}
		var lines []string
		for i < len(runes) {
			lineEnd := i
			for lineEnd < len(runes) && runes[lineEnd] != '\n' {
				lineEnd++
			}
			line := string(runes[i:lineEnd])
			if lineEnd < len(runes) {
				lineEnd++
			}
			i = lineEnd
			if spec.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if strings.TrimRight(line, "\r") == spec.delimiter {
				body.terminated = true
				break
			}
			lines = append(lines, line)
		}
		body.text = strings.Join(lines, "\n")
		bodies = append(bodies, body)
	}
	return i, bodies
}

// dirMove is what a segment does to the directory the shell stands in.
type dirMove int

const (
	moveNone dirMove = iota
	// moveCd is a cd to its operand.
	moveCd
	// movePrevious is `cd -`: back to the directory the shell last left.
	movePrevious
	// movePush is `pushd DIR`: DIR, pushing the directory it leaves.
	movePush
	// movePop is popd: the directory on top of the stack, which it removes.
	movePop
	// moveStack is `pushd -n DIR`: the stack changes and the shell stays.
	moveStack
	// moveUnknown is a move this does not follow: cd with no directory or a
	// `~` one, pushd with none, and either with `+N`, `-N` or popd's `-n`.
	moveUnknown
	// moveCalled is a call to a function whose body moves
	// the directory: where it leaves the shell is not followed.
	moveCalled
)

// functionName is the name a definition head (definesFunction) defines.
func functionName(head []shellToken) string {
	return strings.TrimSuffix(head[len(head)-1].text, "()")
}

// functionCalls are the names segment's command word may run, past reserved
// words, time and assignments, any of which may name a function: the word as
// decoded and the first word of each of its readings, and the next word's
// names too when a reading is empty. After an assignment bash reads no
// reserved word. builtin and command never call one.
func functionCalls(segment []shellToken) []string {
	args, _ := withoutRedirections(segment)
	assigned := false
	var names []string
	for k, token := range args {
		switch {
		case isAssignment(token.text):
			// A quoted value is still an assignment (`X="a b" f`).
			assigned = true
			continue
		case !assigned && readsBeforeCommand(args, k):
			continue
		}
		names = append(names, token.text)
		if strings.ContainsRune(token.text, '\\') {
			for _, candidate := range tokenizeSegments(backslashEscapes, token.text) {
				if len(candidate) > 0 && !candidate[0].subshell {
					names = append(names, candidate[0].text)
					break
				}
			}
		}
		vanishes := false
		for _, alternate := range token.alternates {
			if len(alternate) == 0 {
				vanishes = true
			} else {
				names = append(names, alternate[0].text)
			}
		}
		if !vanishes {
			break
		}
	}
	return names
}

// segmentDirMove reads the cd, pushd or popd segment runs in this shell
// (builtinCommand), and the directory it names. cd reads bash's options
// (`-L`, `-P`, `-e`, `-@`) and `--` before its operand, and cmd's `/d`; a
// cd with an option bash rejects is not read, and one with no operand or a
// `~` operand moves where this does not follow. Both
// guards read a segment's directory change through it.
func segmentDirMove(segment []shellToken) (dirMove, shellToken) {
	args := builtinCommand(segment)
	if len(args) == 0 {
		return moveNone, shellToken{}
	}
	verb, rest := strings.ToLower(args[0].text), args[1:]
	switch verb {
	case "cd":
		for len(rest) > 0 {
			word := rest[0].text
			if word == "--" || strings.EqualFold(word, "/d") {
				rest = rest[1:]
				break
			}
			if len(word) < 2 || word[0] != '-' {
				break
			}
			if strings.Trim(word[1:], "LPe@") != "" {
				return moveNone, shellToken{}
			}
			rest = rest[1:]
		}
		switch {
		case len(rest) == 0 || strings.HasPrefix(rest[0].text, "~"):
			// cd alone goes to $HOME, and a tilde expands to a home.
			return moveUnknown, shellToken{}
		case rest[0].text == "-" && !rest[0].expandable:
			return movePrevious, shellToken{}
		}
		return moveCd, rest[0]
	case "pushd":
		stays := false
		for len(rest) > 0 && len(rest[0].text) > 1 && strings.ContainsRune("-+", rune(rest[0].text[0])) {
			word := rest[0].text
			rest = rest[1:]
			if word == "--" {
				break
			}
			if word != "-n" {
				return moveUnknown, shellToken{}
			}
			stays = true
		}
		switch {
		case len(rest) == 0 || rest[0].text == "-" || strings.HasPrefix(rest[0].text, "~"):
			return moveUnknown, shellToken{}
		case stays:
			return moveStack, shellToken{}
		}
		return movePush, rest[0]
	case "popd":
		if len(rest) > 0 {
			return moveUnknown, shellToken{}
		}
		return movePop, shellToken{}
	}
	return moveNone, shellToken{}
}

// withoutRedirections splits segment into the words bash passes as argv and
// the targets of its `>` redirections. A redirection is its operator, the fd
// number written before it (redirectPrefix), and the word after it; a `<` form
// left the command line, prefix and word, in splitHeredocBodies. A `{name}`
// stays an argument, whose absent reading the tokenizer adds. Every argv
// reader reads the words it returns.
func withoutRedirections(segment []shellToken) ([]shellToken, []shellToken) {
	var args, targets []shellToken
	for i := 0; i < len(segment); i++ {
		if !segment[i].redirects {
			args = append(args, segment[i])
			continue
		}
		// `2> err` tokenizes as "2", ">", "err": the fd number belongs to the
		// redirection, not to the verb.
		if n := len(args); n > 0 && redirectPrefix(args[n-1], segment[i]) {
			args = args[:n-1]
		}
		for i+1 < len(segment) && segment[i+1].redirects {
			i++
		}
		if i+1 < len(segment) {
			targets = append(targets, segment[i+1])
			i++
		}
	}
	return args, targets
}

// redirectPrefix reports whether word names the descriptor of the redirection
// operator op: an unquoted fd number that touches it. A spaced, quoted or
// escaped one is an argument.
func redirectPrefix(word, op shellToken) bool {
	return word.plain && word.end == op.start && isDigits(word.text)
}

// descriptorName reports whether word is a `{name}` bash 5.3 reads as the
// descriptor of a redirection it touches, and bash 3.2 as an argument: the
// tokenizer keeps it as an argument with an absent reading.
func descriptorName(word string) bool {
	name, ok := strings.CutPrefix(word, "{")
	name, closed := strings.CutSuffix(name, "}")
	return ok && closed && name != "" && identifierLen(name) == len(name)
}

// segmentWriteTargets splits one segment into its redirection targets and
// the command its words run.
func segmentWriteTargets(mode backslashMode, segment []shellToken) ([]shellToken, shellCommand) {
	args, targets := withoutRedirections(segment)
	return targets, commandPosition(mode, args)
}

// writeVerbs are the verbs verbWriteTargets claims.
var writeVerbs = []string{"rm", "rmdir", "unlink", "shred", "truncate", "touch", "mkdir", "tee", "mv", "cp", "install", "ln", "rsync", "chmod", "chown", "chgrp", "sed", "perl", "dd"}

// verbWriteTargets returns the write targets of a named write verb, or nil for
// every verb this guard does not claim.
func verbWriteTargets(args []shellToken) []shellToken {
	if len(args) == 0 || !slices.Contains(writeVerbs, filepath.Base(args[0].text)) {
		return nil
	}
	rest := nonFlagArgs(args[1:])
	switch filepath.Base(args[0].text) {
	case "rm", "rmdir", "unlink", "shred", "truncate", "touch", "mkdir", "tee":
		return rest
	case "mv", "cp", "install", "ln":
		// Sources are reads; only the destination is written. `-t DIR` names
		// that destination up front, which moves it out of the last position.
		// Only these four verbs: rsync spells `-t` as `--times`, so asking it
		// the same question reads a source as a destination.
		if target, ok := targetDirectoryFlag(args[1:]); ok {
			return []shellToken{target}
		}
		if len(rest) > 0 {
			return rest[len(rest)-1:]
		}
	case "rsync":
		if len(rest) > 0 {
			return rest[len(rest)-1:]
		}
	case "chmod", "chown", "chgrp":
		// The first operand is the mode or the owner, not a path.
		if len(rest) > 1 {
			return rest[1:]
		}
	case "sed", "perl":
		if hasInPlaceFlag(args[1:]) {
			return rest
		}
	case "dd":
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg.text, "of=") {
				arg.text = strings.TrimPrefix(arg.text, "of=")
				return []shellToken{arg}
			}
		}
	}
	return nil
}

// targetDirectoryFlag returns the destination a copy/move verb was given as
// `-t DIR`, `-tDIR` or `--target-directory[=]DIR`. Only `cp`, `mv`, `ln` and
// `install` may ask: they are the verbs whose `-t` takes a directory operand.
func targetDirectoryFlag(args []shellToken) (shellToken, bool) {
	for i, arg := range args {
		switch {
		case arg.text == "-t" || arg.text == "--target-directory":
			if i+1 < len(args) {
				return args[i+1], true
			}
		case strings.HasPrefix(arg.text, "--target-directory="):
			arg.text = strings.TrimPrefix(arg.text, "--target-directory=")
			return arg, true
		case strings.HasPrefix(arg.text, "-t") && len(arg.text) > 2 && !strings.HasPrefix(arg.text, "--"):
			arg.text = arg.text[2:]
			return arg, true
		}
	}
	return shellToken{}, false
}

func nonFlagArgs(args []shellToken) []shellToken {
	var out []shellToken
	for _, arg := range args {
		if strings.HasPrefix(arg.text, "-") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// hasInPlaceFlag reports whether an editing verb was asked to rewrite its
// operands. `sed -i`, `perl -i -pe` and `perl -pi -e` all qualify.
func hasInPlaceFlag(args []shellToken) bool {
	for _, arg := range args {
		text := arg.text
		if text == "--in-place" || strings.HasPrefix(text, "--in-place=") {
			return true
		}
		if strings.HasPrefix(text, "--") || !strings.HasPrefix(text, "-") {
			continue
		}
		if strings.ContainsRune(text, 'i') {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
