package cli

import (
	"os"
	"path/filepath"
	"slices"
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
	text       string
	redirects  bool // an unquoted `>`
	expandable bool // contains `$` or a backtick: the shell decides the value
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
// target, and the argument tokens of a named write verb. A verb that is not on
// the list, an interpreter, a wrapper, or a target the shell computes are all
// open. Reads are never targets: `cat`, `grep -r` and `go build` pointed at the
// shared checkout are legitimate work, and refusing them is the failure mode
// this guard must not have.
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
	// Heredoc syntax is POSIX grammar. Strip bodies once with POSIX delimiter
	// rules, then tokenize the remaining command under both backslash readings,
	// so the literal (Windows) reading differs from the POSIX reading only in how
	// it reads path backslashes — never in where a heredoc ends. Stripping once
	// keeps <<\EOF / <<-\END terminating at the bare delimiter on every OS and
	// stops the literal reading from swallowing a write named after the
	// terminator.
	stripped := stripHeredocBodies(command)
	interpretations := []shellTargetResolution{
		{mode: backslashEscapes},
		{mode: backslashLiteral},
	}
	for i := range interpretations {
		interpretations[i].targets = shellWriteTargetsUnderDetailed(interpretations[i].mode, checkPath, stripped)
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

type shellTargetSpan struct{ start, end int }
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
	var targets []shellTargetResult
	currentPath := checkPath
	activeVolume := filepath.VolumeName(checkPath)
	activeVolumeKnown := true
	// activeVolume is the drive a plain relative path resolves against: the
	// drive left active by the last `cd`. unknownCwd is the volume whose
	// per-drive current directory this pass cannot reconstruct ("" if none).
	// An ambiguous drive-relative `cd D:docs` only makes D:'s cwd unknowable:
	// D-volume targets stay ambiguous, but C-volume and absolute targets resolve
	// against known state and must not be refused for it (ADR-0014 §1, #664).
	unknownCwd := ""
	cwdByVolume := map[string]string{strings.ToLower(activeVolume): currentPath}
	for _, segment := range tokenizeSegments(mode, command) {
		if len(segment) == 0 {
			continue
		}
		if strings.EqualFold(segment[0].text, "cd") && !segment[0].redirects {
			if operand, ok := cdOperand(segment); ok {
				if operand.expandable {
					unknownCwd = filepath.VolumeName(operand.text)
					if unknownCwd == "" {
						unknownCwd = activeVolume
					}
					activeVolume = unknownCwd
					activeVolumeKnown = false
					continue
				}
				base := currentPath
				if volume := filepath.VolumeName(operand.text); volume != "" {
					if known, ok := cwdByVolume[strings.ToLower(volume)]; ok {
						base = known
					}
				}
				// A plain relative cd (no volume, not volume-less rooted) against a
				// drive whose cwd is unknown cannot be resolved: the active drive's
				// current directory is exactly what the prior drive-relative cd left
				// unreconstructable. Keep the unknown state — do not resolve against
				// the stale currentPath of the previous volume or clear unknownCwd.
				if !activeVolumeKnown && filepath.VolumeName(operand.text) == "" && !(len(operand.text) > 0 && os.IsPathSeparator(operand.text[0])) {
					continue
				}
				resolved, pathAmbiguous := resolveShellWritePath(base, activeVolume, operand.text)
				if pathAmbiguous {
					// Different-volume drive-relative cd (D:docs from a C: base): D
					// becomes the active drive with an unknowable cwd. C stays known,
					// so C-volume and absolute targets stay resolvable.
					activeVolume = filepath.VolumeName(operand.text)
					activeVolumeKnown = false
					unknownCwd = activeVolume
				} else {
					currentPath = resolved
					activeVolume = filepath.VolumeName(resolved)
					activeVolumeKnown = true
					cwdByVolume[strings.ToLower(activeVolume)] = resolved
					unknownCwd = ""
				}
			}
			continue
		}
		// Every reading of a segment that can write names targets: a
		// parameter expansion's substituted words take its place, and their
		// targets share its span. A segment where no candidate is a write
		// verb or a redirect names none.
		if !slices.ContainsFunc(candidateTokens(segment), func(token shellToken) bool {
			return token.redirects || slices.Contains(writeVerbs, filepath.Base(token.text))
		}) {
			continue
		}
		readings, ok := writeReadings(segment)
		if !ok {
			targets = append(targets, shellTargetResult{
				span:      shellTargetSpan{start: segment[0].start, end: segment[len(segment)-1].end},
				ambiguous: true,
			})
			continue
		}
		var segmentTargets []shellToken
		for _, reading := range readings {
			segmentTargets = append(segmentTargets, segmentWriteTargets(reading)...)
		}
		for _, target := range segmentTargets {
			base := currentPath
			if volume := filepath.VolumeName(target.text); volume != "" {
				if known, ok := cwdByVolume[strings.ToLower(volume)]; ok {
					base = known
				}
			}
			resolved, targetAmbiguous := resolveShellWritePath(base, activeVolume, target.text)
			if !targetAmbiguous && pathDependsOnUnknownCwd(activeVolume, unknownCwd, target.text) {
				targetAmbiguous = true
			}
			targets = append(targets, shellTargetResult{
				span: shellTargetSpan{start: target.start, end: target.end},
				path: resolved, ambiguous: targetAmbiguous,
			})
		}
	}
	return targets
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
// segments of words. It is the one shell reader for both safety guards, and it
// keeps what a dequote-then-split reader loses: whether a `>` was quoted,
// whether a word carries shell expansion, and a quoted space or operator inside
// one word.
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
	flushWord := func() {
		if word.Len() > 0 || quoted {
			token := shellToken{text: word.String(), expandable: expandable, undecodable: undecodable, splitsAtIFS: splitsAtIFS, start: rawStart, end: rawEnd}
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
		}
		word.Reset()
		raw.Reset()
		sub, subRaw = substitutedReading{}, substitutedReading{}
		vanish = vanishReading{}
		wordStart, rawStart, rawEnd, expandable, undecodable, splitsAtIFS, quoted, substituted, afterDollar = -1, -1, -1, false, false, false, false, false, false
	}
	flushSegment := func() {
		flushWord()
		if len(segment) > 0 {
			segments = append(segments, segment)
			segment = nil
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
	add := func(r rune, i int, literal bool) {
		if wordStart < 0 {
			wordStart = i
		}
		// A `$` or backtick names a shell expansion only when the shell would
		// actually perform one here. Inside single quotes, and behind a POSIX
		// backslash escape (outside quotes or inside double quotes), the
		// character is literal: the path it sits in is one this guard can classify,
		// and calling it expandable would drop a genuine protected-path target
		// (#664).
		expands := !literal && (r == '$' || r == '`')
		expandable = expandable || expands
		afterDollar = expands && r == '$'
		if len(groups) == 0 {
			vanish.add(r, literal, quote == '"')
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
			rawText := string(runes[i+2 : end-1])
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
		if n := len(groups); n > 0 && strings.ContainsRune(" \t\n;&|>", r) {
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
			flushSegment()
		case ';', '&', '\n':
			flushSegment()
		case ' ', '\t':
			flushWord()
		case '>':
			flushWord()
			segment = append(segment, shellToken{text: ">", redirects: true, start: i, end: i + 1})
		default:
			touch(i)
			add(r, i, false)
		}
	}
	flushSegment()
	return segments
}

// vanishReading follows a word as the tokenizer decodes it, outside any
// `${...}` group, for whether it holds nothing but expansions any of which may
// expand to nothing: unquoted ones, and quoted `@` forms, which bash removes
// with the word when they have no elements, beside which an empty quoted part
// may stand. Any other quoted part leaves an empty word. A command
// substitution is not: the tokenizer does not find its end, so the words it
// is split into are not the shell's.
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

// heredocSpec is one pending `<<DELIM` body: the word that ends it, and whether
// `<<-` allows leading tabs on that terminator.
type heredocSpec struct {
	delimiter string
	stripTabs bool
}

// stdinFeed is text a stripped `<<` body or `<<<` word feeds to its command's
// stdin. A here-string's text is still shell-quoted. An unterminated heredoc
// runs to the end of the payload and is reported as not terminated.
type stdinFeed struct {
	text       string
	terminated bool
}

// stripHeredocBodies removes every heredoc body from a command line, leaving the
// command words around it intact. Heredoc syntax is POSIX grammar, so this runs
// once with POSIX delimiter rules; the dual backslash readings tokenize what
// remains and only differ in how they read path backslashes.
//
// Without this, each body line was split at its newline and tokenized as a
// command of its own: a `rm -rf <shared>/...` example inside a document became a
// real write target, and a `cd <shared>` line inside a document moved the
// resolution base for the genuine commands after the terminator. Both refused
// writes that must go through, which is the one failure this guard cannot have.
func stripHeredocBodies(command string) string {
	stripped, _ := splitHeredocBodies(command)
	return stripped
}

// splitHeredocBodies is stripHeredocBodies that also returns what it removed
// from stdin: each heredoc body and here-string word. The git guard reads
// each one as shell.
func splitHeredocBodies(command string) (string, []stdinFeed) {
	runes := []rune(command)
	var feeds []stdinFeed
	var out strings.Builder
	var pending []heredocSpec
	quote := rune(0)
	escaped := false

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if quote == '\'' {
			out.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if quote == '"' && r == '\\' {
			out.WriteRune(r)
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
			out.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			out.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			out.WriteRune(r)
		case '<':
			// Every `<` form reads: `<` a file, `<<<` a string, `<<` a body.
			// None of them names a write target, so the operator and the word
			// it consumes both leave the command line here.
			run := i
			for run < len(runes) && runes[run] == '<' {
				run++
			}
			if run-i == 2 {
				if spec, next, ok := readHeredocRedirect(runes, i); ok {
					pending = append(pending, spec)
					out.WriteRune(' ')
					i = next - 1
					continue
				}
			}
			out.WriteRune(' ')
			end := skipRedirectSource(runes, run)
			if run-i == 3 {
				feeds = append(feeds, stdinFeed{text: string(runes[run:end]), terminated: true})
			}
			i = end - 1
		case '\n':
			out.WriteRune(r)
			if len(pending) > 0 {
				var bodies []stdinFeed
				i, bodies = skipHeredocBodies(runes, i+1, pending)
				i--
				feeds = append(feeds, bodies...)
				pending = nil
			}
		default:
			out.WriteRune(r)
		}
	}
	for range pending {
		feeds = append(feeds, stdinFeed{})
	}
	return out.String(), feeds
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
		if r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '&' || r == '|' || r == '<' || r == '>' {
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
		body := stdinFeed{}
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

func cdOperand(segment []shellToken) (shellToken, bool) {
	if len(segment) < 2 || segment[1].redirects {
		return shellToken{}, false
	}
	index := 1
	if strings.EqualFold(segment[index].text, "/d") {
		index++
	}
	if index >= len(segment) || segment[index].redirects {
		return shellToken{}, false
	}
	return segment[index], true
}

// segmentWriteTargets splits one segment into its redirection targets and the
// write targets of its verb.
func segmentWriteTargets(segment []shellToken) []shellToken {
	var targets []shellToken
	var args []shellToken
	for i := 0; i < len(segment); i++ {
		if !segment[i].redirects {
			args = append(args, segment[i])
			continue
		}
		// `2> err` tokenizes as "2", ">", "err": the fd number belongs to the
		// redirection, not to the verb.
		if n := len(args); n > 0 && isDigits(args[n-1].text) {
			args = args[:n-1]
		}
		for i+1 < len(segment) && segment[i+1].redirects {
			i++
		}
		if i+1 < len(segment) {
			targets = appendTarget(targets, segment[i+1])
			i++
		}
	}
	for _, target := range verbWriteTargets(args) {
		targets = appendTarget(targets, target)
	}
	return targets
}

// appendTarget drops a target whose value the shell computes: an unexpanded
// word is not a path this guard can classify, and guessing would refuse a call
// on evidence it does not have. An empty word stays a target: it resolves to
// the directory the command runs in, where darwin cp writes it.
func appendTarget(targets []shellToken, token shellToken) []shellToken {
	if token.expandable {
		return targets
	}
	return append(targets, token)
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
