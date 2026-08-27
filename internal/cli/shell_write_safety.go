package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// shellToken is one word of a command segment, or the redirection operator
// itself. Quoting has to survive tokenization here: `echo "x > f"` writes
// nothing, while `echo x > f` writes f, and the two are indistinguishable once
// the quotes have been dropped.
type shellToken struct {
	text       string
	redirects  bool // an unquoted `>`
	expandable bool // contains `$` or a backtick: the shell decides the value
	start      int
	end        int
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
	ambiguous := false
	for _, interpretation := range interpretations {
		for _, result := range interpretation.targets {
			if result.ambiguous {
				continue
			}
			if !slices.Contains(targets, result.path) {
				targets = append(targets, result.path)
			}
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
	for _, segment := range shellSegments(mode, command) {
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
		for _, target := range segmentWriteTargets(segment) {
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
// resolver (resolveSafetyPath, in git_worktree_safety.go) is the #668 owner and
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

// shellSegments tokenizes command — already stripped of heredoc bodies — at
// unquoted `;`, `&`, `|` and newline, into segments. It mirrors
// splitSafetySegments + splitSafetyWords, but keeps the two facts those drop:
// whether a `>` was quoted, and whether a word carries shell expansion.
//
// Heredoc stripping is the caller's job and is done once, with POSIX delimiter
// rules, before either backslash reading tokenizes: a heredoc body is content,
// not a command line, and the same "one payload, one channel" rule BEO-62
// settled applies to it. Tokenizing it refused a legitimate write whenever the
// content happened to look like a command — this file's own ADR is such a
// document.
func shellSegments(mode backslashMode, command string) [][]shellToken {
	return tokenizeSegments(mode, command)
}

func tokenizeSegments(mode backslashMode, command string) [][]shellToken {
	var segments [][]shellToken
	var segment []shellToken
	var word strings.Builder
	wordStart := -1
	rawStart, rawEnd := -1, -1
	expandable := false
	quote := rune(0)
	escaped := false
	flushWord := func() {
		if word.Len() > 0 {
			segment = append(segment, shellToken{text: word.String(), expandable: expandable, start: rawStart, end: rawEnd})
			word.Reset()
		}
		wordStart, rawStart, rawEnd, expandable = -1, -1, -1, false
	}
	flushSegment := func() {
		flushWord()
		if len(segment) > 0 {
			segments = append(segments, segment)
			segment = nil
		}
	}
	runes := []rune(command)
	touch := func(i int) {
		if rawStart < 0 {
			rawStart = i
		}
		rawEnd = i + 1
	}
	add := func(r rune, i int, literal bool) {
		if wordStart < 0 {
			wordStart = i
		}
		word.WriteRune(r)
		// A `$` or backtick names a shell expansion only when the shell would
		// actually perform one here. Inside single quotes, and behind a POSIX
		// backslash escape (outside quotes or inside double quotes), the
		// character is literal: the path it sits in is one this guard can classify,
		// and calling it expandable would drop a genuine protected-path target
		// (#664).
		if !literal && (r == '$' || r == '`') {
			expandable = true
		}
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			touch(i - 1)
			touch(i)
			add(r, i, true)
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
		if quote == '"' {
			touch(i)
			if r == quote {
				quote = 0
			} else {
				add(r, i, false)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '|':
			if word.Len() == 0 && len(segment) > 0 && segment[len(segment)-1].redirects {
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

// heredocSpec is one pending `<<DELIM` body: the word that ends it, and whether
// `<<-` allows leading tabs on that terminator.
type heredocSpec struct {
	delimiter string
	stripTabs bool
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
	runes := []rune(command)
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
			i = skipRedirectSource(runes, run) - 1
		case '\n':
			out.WriteRune(r)
			if len(pending) > 0 {
				i = skipHeredocBodies(runes, i+1, pending) - 1
				pending = nil
			}
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
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
// they were opened, and returns the index where the command line resumes. A body
// that is never terminated runs to the end of the payload.
func skipHeredocBodies(runes []rune, start int, pending []heredocSpec) int {
	i := start
	for _, spec := range pending {
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
				break
			}
		}
	}
	return i
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
// on evidence it does not have.
func appendTarget(targets []shellToken, token shellToken) []shellToken {
	if token.expandable {
		return targets
	}
	return append(targets, token)
}

// verbWriteTargets returns the write targets of a named write verb, or nil for
// every verb this guard does not claim.
func verbWriteTargets(args []shellToken) []shellToken {
	if len(args) == 0 {
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
