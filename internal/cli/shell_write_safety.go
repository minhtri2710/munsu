package cli

import (
	"path/filepath"
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
// Tokenization cannot fail, so this channel has no unparseable state and
// inherits none of the fail-closed obligation applyPatchTargets carries.
func shellWriteTargets(checkPath, command string) []string {
	var targets []string
	currentPath := checkPath
	for _, segment := range shellSegments(command) {
		if len(segment) == 0 {
			continue
		}
		if segment[0].text == "cd" && !segment[0].redirects {
			if len(segment) > 1 && !segment[1].redirects {
				currentPath = resolveSafetyPath(currentPath, segment[1].text)
			}
			continue
		}
		for _, target := range segmentWriteTargets(segment) {
			targets = append(targets, resolveSafetyPath(currentPath, target))
		}
	}
	return targets
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

// shellSegments splits a command at unquoted `;`, `&`, `|` and newline, and
// tokenizes each segment. It mirrors splitSafetySegments + splitSafetyWords,
// but keeps the two facts those drop: whether a `>` was quoted, and whether a
// word carries shell expansion.
//
// Heredoc bodies are removed before splitting: a heredoc body is content, not a
// command line, and the same "one payload, one channel" rule BEO-62 settled
// applies to it. Tokenizing it refused a legitimate write whenever the content
// happened to look like a command — this file's own ADR is such a document.
func shellSegments(command string) [][]shellToken {
	return tokenizeSegments(stripHeredocBodies(command))
}

func tokenizeSegments(command string) [][]shellToken {
	var segments [][]shellToken
	var segment []shellToken
	var word strings.Builder
	expandable := false
	quote := rune(0)
	escaped := false

	flushWord := func() {
		if word.Len() > 0 {
			segment = append(segment, shellToken{text: word.String(), expandable: expandable})
			word.Reset()
		}
		expandable = false
	}
	flushSegment := func() {
		flushWord()
		if len(segment) > 0 {
			segments = append(segments, segment)
			segment = nil
		}
	}

	for _, r := range command {
		if escaped {
			word.WriteRune(r)
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
				word.WriteRune(r)
				if r == '$' || r == '`' {
					expandable = true
				}
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '|':
			// `>|` is one clobber operator, not a redirect followed by a pipe:
			// splitting there dropped the target word entirely.
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
			segment = append(segment, shellToken{text: ">", redirects: true})
		default:
			word.WriteRune(r)
			if r == '$' || r == '`' {
				expandable = true
			}
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
// command words around it intact.
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
		if r == '\\' {
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
			j++
			if j < len(runes) {
				delimiter.WriteRune(runes[j])
				j++
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote := r
			j++
			for j < len(runes) && runes[j] != quote {
				delimiter.WriteRune(runes[j])
				j++
			}
			if j < len(runes) {
				j++
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

// segmentWriteTargets splits one segment into its redirection targets and the
// write targets of its verb.
func segmentWriteTargets(segment []shellToken) []string {
	var targets []string
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
func appendTarget(targets []string, token shellToken) []string {
	if token.expandable {
		return targets
	}
	return append(targets, token.text)
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
