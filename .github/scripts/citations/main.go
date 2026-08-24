// Command citations derives every checkable citation out of the documentation
// set and prints one tab-separated row per citation with its resolution status.
//
// Output columns: status <TAB> doc <TAB> kind <TAB> citation
//
//	status    `resolved`, `unresolved` or `unchecked`
//	doc       repo-relative path of the markdown file the citation is in
//	kind      `path` or `symbol` when the citation was judged, `token` when it
//	          was not -- deciding which of the two it is is one of the
//	          judgements that was not available
//	citation  the cleaned citation text, which is the waiver key
//
// `unchecked` is the honest half of the contract. A token this tool cannot
// judge is printed with that status rather than dropped, because a lane that
// silently narrows its own subject is the defect this one exists to end: a
// reader who sees `0 unaccounted` has to be able to see, in the same output,
// what was never in the account. Unchecked rows take no waiver row -- there is
// no claim to waive -- and `citations.sh unchecked` lists them.
//
// Line numbers are deliberately NOT part of a row. A citation written
// `internal/x/y.go:41` is checked as `internal/x/y.go`: the file has to exist,
// and the line is dropped. Checking that the line still holds the cited symbol
// is the useful version and the brittle one -- every insertion above a citation
// would turn the lane red on a diff that changed no citation, and a lane that
// goes red for reasons nobody caused is a lane that gets waived. Dropping the
// line also keeps the waiver keys stable, the same reason .github/deadcode.allow
// drops line:col.
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::citations could not resolve %q: %v\n", root, err)
		os.Exit(1)
	}
	rows, err := scan(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::citations could not scan the tree, so it cannot judge any citation: %v\n", err)
		os.Exit(1)
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for _, r := range rows {
		fmt.Fprintln(out, r)
	}
}

// ---------------------------------------------------------------------------
// the documentation set
// ---------------------------------------------------------------------------

// The covered set: every markdown file under docs/ except docs/plans/, plus the
// root documents that carry citations as their primary evidence.
//
// A walk rather than a list, so a new document joins this lane by existing --
// the same argument the gofmt step in ci.yml makes for passing `.` instead of a
// package list. Not opt-in inside that boundary: a file nobody remembered to
// add is a file nobody checks, which is the failure this lane exists to end.
func containedPath(root, target string) (string, error) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("canonical repository root: %w", err)
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("absolute repository root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path resolves outside repository root")
	}
	return resolved, nil
}

// docs/plans/ is excluded, and the reason is a property of the document class
// rather than of its backlog. A plan is a dated artifact: it records what
// someone intended on a day, its citations were true on that day, and nobody
// maintains it afterwards. The correct state of a June plan that cites a
// since-renamed symbol is UNCHANGED, so a lane over plans produces a burn-down
// that can never legitimately reach zero -- which is how a waiver file turns
// into the graveyard citations.sh's header warns about. When this was measured
// plans were the majority of the seeded waiver on their own; the count is not
// repeated here, because a frozen number in a comment is the shape #553 took
// out of .github/build-tags.manifest. Re-derive it by deleting the SkipDir
// below and running `citations.sh list`. ADRs stay in: CLAUDE.md cites 0017 as
// governing rules soldiers work under today, which makes an ADR a live
// normative document and a stale citation in one a real defect.
//
// Also out of scope, stated here rather than left as an accident of the glob:
// COMMANDS.md, CONTEXT.md, CONTRIBUTING.md and SUPERVISION.md at the root, and
// Go doc comments. Doc comments carry the same class of defect -- a stale
// `munsu delivery pr-check` citation sat in supervision_check.go's doc comment
// (#573) -- but they are a different extractor over a different corpus, and
// widening the set is a one-line change here once someone wants to pay the
// backlog for it.
func docFiles(root string) ([]string, error) {
	var out []string
	// CLAUDE.md is a symlink to AGENTS.md. Both are named because both are
	// cited by name, but the bytes are scanned once, under the name of the file
	// that actually holds them: scanning both would double every row in it and
	// ask for two waiver rows per defect.
	seen := map[string]bool{}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		target, err := filepath.EvalSymlinks(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("covered document %s: %w", name, err)
		}
		if _, err := containedPath(root, filepath.Join(root, name)); err != nil {
			if strings.Contains(err.Error(), "outside repository root") {
				return nil, fmt.Errorf("covered document %s resolves outside repository root", name)
			}
			return nil, fmt.Errorf("covered document %s: %w", name, err)
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, name)
	}
	docs := filepath.Join(root, "docs")
	plans := filepath.Join(docs, "plans")
	info, err := os.Lstat(docs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("covered directory docs/ is missing or symlinked")
	}
	if _, err := containedPath(root, docs); err != nil {
		return nil, fmt.Errorf("covered directory docs/: %w", err)
	}
	err = filepath.WalkDir(docs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == plans {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if targetInfo, statErr := os.Stat(p); statErr == nil && targetInfo.IsDir() {
				return fmt.Errorf("covered documentation directory %s is symlinked", filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator))))
			}
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		if _, err := containedPath(root, p); err != nil {
			if strings.Contains(err.Error(), "outside repository root") {
				return fmt.Errorf("covered document %s resolves outside repository root", filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator))))
			}
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// ---------------------------------------------------------------------------
// inline code spans
// ---------------------------------------------------------------------------

type span struct {
	text  string
	start int // byte offset of the opening backtick run in the line
	end   int // byte offset just past the closing backtick run
}

// Inline code spans on one line. Fenced blocks are handled by the caller.
//
// A span is a run of N backticks closed by the next run of exactly N. Spans
// that open on one line and close on another are skipped rather than guessed
// at: an unterminated run is either a multi-line span (nothing in the covered
// set cites across a line break) or a typo, and inventing a citation out of one
// would put a row in the waiver that no reader can find. Note the `break`
// rather than a `continue`: an unmatched run abandons the rest of its line, so
// a well-formed span after it is dropped too. Counted, with that effect, as one
// of the lane's silent drops in referenceShaped's enumeration, which is the
// complete list.
func inlineSpans(line string) []span {
	var out []span
	i := 0
	for i < len(line) {
		if line[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		j, closed := i+n, false
		for j < len(line) {
			if line[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(line) && line[j+m] == '`' {
				m++
			}
			if m == n {
				closed = true
				break
			}
			j += m
		}
		if !closed {
			break
		}
		out = append(out, span{text: line[i+n : j], start: i, end: j + n})
		i = j + n
	}
	return out
}

// Fenced code blocks are excluded. They hold commands, transcripts and sample
// output -- text that is not asserting anything about this tree -- and a lane
// that read them would spend its whole waiver on `go install
// golang.org/x/tools/...` lines. The cost is real and stated: a citation that
// only ever appears inside a fence is not checked. Counted as one of the lane's
// silent drops in referenceShaped's enumeration, which is the complete list.
func fenceMarker(line string) (byte, int, int, bool) {
	normalized, _, _ := stripMarkdownContainer(line)
	return fenceMarkerNormalized(normalized)
}

func fenceContinuationIndent(line string) (int, bool) {
	start := 0
	for start < len(line) && line[start] == ' ' && start < 4 {
		start++
	}
	markerEnd := start
	if markerEnd < len(line) && (line[markerEnd] == '-' || line[markerEnd] == '*' || line[markerEnd] == '+') {
		markerEnd++
	} else {
		digits := 0
		for markerEnd < len(line) && line[markerEnd] >= '0' && line[markerEnd] <= '9' && digits < 9 {
			markerEnd++
			digits++
		}
		if digits == 0 || markerEnd >= len(line) || (line[markerEnd] != '.' && line[markerEnd] != ')') {
			return 0, false
		}
		markerEnd++
	}
	if markerEnd >= len(line) || (line[markerEnd] != ' ' && line[markerEnd] != '\t') {
		return 0, false
	}
	prefixEnd := markerEnd
	for prefixEnd < len(line) && (line[prefixEnd] == ' ' || line[prefixEnd] == '\t') {
		prefixEnd++
	}
	return markdownColumns(line[:prefixEnd]), true
}

func markdownColumns(text string) int {
	columns := 0
	for _, b := range []byte(text) {
		switch b {
		case ' ':
			columns++
		case '\t':
			columns = (columns/4 + 1) * 4
		default:
			columns++
		}
	}
	return columns
}

func stripBlockquoteContainers(line string) (string, int) {
	start := 0
	depth := 0
	for {
		spaces := 0
		for start < len(line) && line[start] == ' ' && spaces < 4 {
			start++
			spaces++
		}
		if spaces > 3 || start >= len(line) || line[start] != '>' {
			return line[start-spaces:], depth
		}
		start++
		if start < len(line) && (line[start] == ' ' || line[start] == '\t') {
			start++
		}
		depth++
	}
}

func stripFenceContinuation(line string, columns int) (string, bool) {
	current := 0
	start := 0
	for start < len(line) {
		switch line[start] {
		case ' ':
			current++
		case '\t':
			current = (current/4 + 1) * 4
		default:
			return line[start:], current >= columns
		}
		start++
	}
	return line[start:], current >= columns
}

func fenceMarkerNormalized(line string) (byte, int, int, bool) {
	columns := 0
	start := 0
	for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
		if line[start] == ' ' {
			columns++
		} else {
			columns = (columns/4 + 1) * 4
		}
		start++
	}
	if columns > 3 || len(line)-start < 3 || (line[start] != '`' && line[start] != '~') {
		return 0, 0, 0, false
	}
	marker := line[start]
	run := 0
	for start+run < len(line) && line[start+run] == marker {
		run++
	}
	if run < 3 || (marker == '`' && strings.Contains(line[start+run:], "`")) {
		return 0, 0, 0, false
	}
	return marker, run, start, true
}

func stripMarkdownContainer(line string) (string, int, int) {
	start := 0
	consumed := false
	quoteDepth := 0
	for {
		indentStart := start
		for start < len(line) && line[start] == ' ' {
			start++
		}
		if start-indentStart > 3 {
			if consumed {
				return line[indentStart:], indentStart, quoteDepth
			}
			return line, 0, quoteDepth
		}
		if start < len(line) && line[start] == '>' {
			consumed = true
			quoteDepth++
			start++
			if start < len(line) && (line[start] == ' ' || line[start] == '\t') {
				start++
			}
			continue
		}
		markerStart := start
		if start < len(line) && (line[start] == '-' || line[start] == '*' || line[start] == '+') {
			start++
		} else {
			digits := 0
			for start < len(line) && line[start] >= '0' && line[start] <= '9' && digits < 10 {
				start++
				digits++
			}
			if digits == 0 || digits > 9 || start >= len(line) || (line[start] != '.' && line[start] != ')') {
				if consumed {
					return line[markerStart:], markerStart, quoteDepth
				}
				return line, 0, quoteDepth
			}
			start++
		}
		if start == markerStart || start >= len(line) || (line[start] != ' ' && line[start] != '\t') {
			if consumed {
				return line[markerStart:], markerStart, quoteDepth
			}
			return line, 0, quoteDepth
		}
		consumed = true
		for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
			start++
		}
	}
}

func closesFenceNormalized(line string, marker byte, openingRun int) bool {
	gotMarker, run, start, ok := fenceMarkerNormalized(line)
	if !ok || gotMarker != marker || run < openingRun {
		return false
	}
	return strings.TrimSpace(line[start+run:]) == ""
}

func closesFence(line string, marker byte, openingRun, openingQuoteDepth int) bool {
	normalized, _, quoteDepth := stripMarkdownContainer(line)
	if quoteDepth != openingQuoteDepth {
		return false
	}
	gotMarker, run, start, ok := fenceMarkerNormalized(normalized)
	if !ok || gotMarker != marker || run < openingRun {
		return false
	}
	return strings.TrimSpace(normalized[start+run:]) == ""
}

// ---------------------------------------------------------------------------
// classification
// ---------------------------------------------------------------------------

var (
	lineRefRe = regexp.MustCompile(`:\d+(-\d+)?(,\d+)?$`)
	identRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	allCapsRe = regexp.MustCompile(`^[A-Z0-9_]+$`)
	// `(Type).Method` and `(*Type).Method`, the way a method is cited when the
	// receiver matters.
	methodExprRe = regexp.MustCompile(`^\(\*?([A-Za-z_][A-Za-z0-9_]*)\)\.([A-Za-z_][A-Za-z0-9_]*)$`)
	// A trailing argument list on a cited call.
	callSuffixRe = regexp.MustCompile(`\([^()]*\)$`)
)

// The punctuation a reference can never contain, because it belongs to
// executable syntax rather than to a name: quoting, expansion, negation,
// escaping, call and index brackets, and the shell's own separators.
//
// Stated as what a reference CANNOT contain rather than as what it may. The
// inverse -- an allow-list of the characters a path is spelled with -- is what
// this file carried until it dropped `internal/fleet/process_runtime_{unix,\
// windows}.go` on the floor, and an allow-list will always do that: the shapes
// that reach the disclosure rule are by definition the ones nobody anticipated,
// so anything it has not been taught vanishes rather than being declared.
// Glob, brace, placeholder and `KEY=value` notations therefore pass and are
// disclosed, which is right -- they are references the lane cannot judge, which
// is exactly what the register is for.
//
// This gate is narrow on purpose: what it removes is a fragment of a command or
// an expression -- `'[.[]`, `[.labels[].name]`,
// `${XDG_BIN_HOME:-$HOME/.local/bin}` -- which no reader could resolve against
// anything, and a register nobody reads discloses nothing. It is one of the
// three ways a token gets no row at all; referenceShaped's header enumerates
// all three, and this is not the largest.
const codeChars = "\"'`$!\\()[]|;&"

func callResultSelector(tok string) bool {
	expr, err := parser.ParseExpr(tok)
	if err != nil {
		return false
	}
	return selectorRootedInCall(expr)
}

func selectorRootedInCall(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.CallExpr:
		return selectorRootedInCall(t.Fun)
	case *ast.SelectorExpr:
		return callRoot(t.X)
	case *ast.ParenExpr:
		return selectorRootedInCall(t.X)
	}
	return false
}

func callRoot(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.CallExpr:
		return true
	case *ast.SelectorExpr:
		return callRoot(t.X)
	case *ast.IndexExpr:
		return callRoot(t.X)
	case *ast.IndexListExpr:
		return callRoot(t.X)
	case *ast.ParenExpr:
		return callRoot(t.X)
	}
	return false
}

// Trailing prose punctuation, a trailing slash and a `:line` or `:line-line`
// suffix are not part of the citation.
func cleanToken(tok string) string {
	tok = strings.TrimFunc(tok, func(r rune) bool { return r == ' ' || r == '\t' })
	for {
		before := tok
		tok = strings.TrimRight(tok, ".,;:'\"")
		tok = lineRefRe.ReplaceAllString(tok, "")
		if tok == before {
			break
		}
	}
	tok = strings.TrimPrefix(tok, "./")
	if tok != "/" {
		tok = strings.TrimRight(tok, "/")
	}
	return tok
}

// Whether a token is a citation of a path in THIS repository: its first segment
// has to name something that exists at the repo root.
//
// Derived from the tree rather than declared, which is what separates
// `internal/cli/x.go` from `golang.org/x/tools/cmd/deadcode` with no list of
// import-path prefixes to keep up to date, and what makes a new top-level
// directory join this lane by existing.
//
// This is the STRONGEST claim a citation can make and it is held to it exactly:
// a token that starts at a real root entry has to resolve at that path. It is
// not rescued by the by-suffix rule below -- `internal/cli/task_cmd.go` written
// for a file that now lives elsewhere is a wrong path, not a loose one.
//
// A token this rule declines is not thereby unchecked: fileCitation picks up
// anything that names a file by basename or by package-relative tail, and
// whatever neither rule claims is printed with status `unchecked`.
func isRepoPath(root, tok string) bool {
	if tok == "" || strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
		return false
	}
	if strings.ContainsAny(tok, "*?<>|$\\{} \t") || strings.Contains(tok, "://") {
		return false
	}
	first, _, hasSlash := strings.Cut(tok, "/")
	if !hasSlash || first == "" || first == "." || first == ".." {
		return false
	}
	_, err := os.Lstat(filepath.Join(root, first))
	return err == nil
}

func repoPathExists(canonicalRoot, tok string) bool {
	candidate := filepath.Join(canonicalRoot, filepath.FromSlash(tok))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(canonicalRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	_, err = os.Lstat(candidate)
	return err == nil
}

// ---------------------------------------------------------------------------
// the file index
// ---------------------------------------------------------------------------

// Every file in the tree, indexed by every path-component suffix of its
// repo-relative path, plus the set of extensions the tree actually uses.
//
// internal/cli/task_cmd.go is indexed under `task_cmd.go`, `cli/task_cmd.go`
// and `internal/cli/task_cmd.go`, which is what lets a citation written any of
// those three ways resolve against the one file. Documents cite files all three
// ways and the issue's first acceptance clause says every backticked file path,
// not every root-relative one.
type files struct {
	exts          map[string]bool
	suffix        map[string]bool
	canonicalRoot string
}

// The extension set is DERIVED, like everything else this lane judges. It is
// the set of extensions that occur in the tree, not a list of source
// extensions somebody has to remember to extend, and it is what separates a
// file citation from prose that happens to contain a dot or a slash:
// `get/set`, `munsu.task-authority/v1`, `github.com/spf13/cobra` and
// `state/.afk` all end in something the tree never uses as an extension, so
// none of them is read as a claim about a file. A new kind of file in the tree
// teaches this rule about itself.
func buildFiles(root string) (*files, error) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root %s: %w", root, err)
	}
	fi := &files{exts: map[string]bool{}, suffix: map[string]bool{}, canonicalRoot: canonicalRoot}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata and vendor are excluded for the same reason buildIndex
			// excludes them: the weak resolutions below match by name, and a
			// fabricated citation must not be rescued by a fixture that happens
			// to carry that name. Root-relative citations are unaffected --
			// those are answered by Lstat against the real tree, not by this
			// index, so a document may still cite a testdata path by its path.
			if name := d.Name(); p != root && (name == ".git" || name == "testdata" || name == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if _, err := containedPath(root, p); err != nil {
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ext := filepath.Ext(path.Base(rel)); ext != "" {
			fi.exts[ext] = true
		}
		parts := strings.Split(rel, "/")
		for i := range parts {
			fi.suffix[strings.Join(parts[i:], "/")] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// No emptiness guard here on purpose. buildIndex runs first and already
	// fails on a tree with no .go file, so by this point the walk has seen at
	// least that one -- a guard nothing can enter is a branch no test can cover
	// and no reader can trust (ADR-0017).
	return fi, nil
}

// Whether a token is a citation of a file in this tree named by its basename
// (`spawn_runner.go`, `ci.yml`) or by a package-relative tail
// (`cli/git_worktree_safety.go`), the two shapes that are unambiguously file
// path citations in the sense the issue means and that the root-relative rule
// above cannot see. Together they were the largest unchecked group: 192 bare
// and 118 first-segment-unknown tokens on the covered set.
//
// Two shapes are excluded because they name a naming convention rather than a
// file, and both are cited that way in the covered set. A bare `.go` has no
// stem. `_test.go`, `_windows.go` and `_lock_windows.go` are filename SUFFIXES
// -- Go does not build a file whose name starts with `_`, so nothing in a Go
// tree is ever called that, and ADR-0017 cites all three while discussing how
// files are named. Neither is dropped in silence: pathShaped sends both to an
// `unchecked` row.
func fileCitation(fi *files, tok string) bool {
	// unjudged: empty, absolute, home-relative
	if tok == "" || strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
		return false
	}
	// unjudged: notation, url
	if strings.ContainsAny(tok, "*?<>|$\\{} \t") || strings.Contains(tok, "://") {
		return false
	}
	base := path.Base(tok)
	ext := filepath.Ext(base)
	// unjudged: no-extension, all-extension, leading-underscore
	if ext == "" || ext == base || strings.HasPrefix(base, "_") {
		return false
	}
	// unjudged: unused-extension
	return fi.exts[ext]
}

// Whether a token looks like a reference to something -- a file, a package, a
// qualified name -- rather than an English word. This is the last rule to run,
// and everything it catches becomes an `unchecked` row.
//
// It is the answer to the question "what did this lane not look at", and the
// answer has to be a rule rather than a list, because the shapes that reach it
// are exactly the shapes nobody anticipated. Six of them are known:
//
//   - `mailbox.WriteEnvelope`: a qualified name whose QUALIFIER this tree does
//     not declare. Judging it is not available. Nothing syntactic separates a
//     munsu citation whose package was renamed out from under it from
//     `fmt.Errorf`, and resolving on the member name alone would report
//     `Errorf`, `Skip`, `Exit` and `Join` as fabricated -- the false-positive
//     class the qualifier rule exists to prevent, at corpus scale. So this is
//     declared, not decided. It is #566's own defect class when the rename
//     lands on the qualifier, and a reader has to be able to see that the lane
//     did not check it.
//   - `fm-primary-watch-arm.js`, `notes.rst`: a filename whose extension the
//     tree never uses, so the derived rule cannot tell it from prose.
//   - `.gitignore`, `.go`: all extension, no stem. A real dotfile and a bare
//     suffix are the same shape.
//   - `internal/fleet/process_runtime_{unix,windows}.go`, `docs/**`,
//     `state/<task-id>.meta`: brace, glob and placeholder notation. Each stands
//     for a set of paths rather than one, which is why fileCitation refuses it
//     and why it has to be declared -- the brace form above expands to two
//     files that exist today and nothing here would notice if they stopped.
//   - `MUNSU_HOME=/tmp/munsu-dogfood`, `~/.munsu`: a path written as an
//     assignment, or under a home directory this lane cannot see.
//   - `newCaptainRecoverTransaction().Recover`: a selector on a call result;
//     its owner cannot be resolved without type information.
//
// What this function declines gets NO row -- not `unchecked`, nothing. This is
// the one place that enumerates ALL of the lane's silence, which is why the
// summaries in citations.sh and AGENTS.md point here instead of restating it.
// Two of the six ways in are upstream of this function and are repeated here
// on purpose: a limit stated only where it is implemented is a limit the
// reader has to already know about to find.
//
//  1. a span inside a fenced code block, dropped by the scanner.
//     A citation that only ever appears in a fence is never checked.
//  2. everything to the right of an unmatched backtick run, dropped by
//     inlineSpans, which abandons the rest of the line rather than resuming
//     after the run. So the silence is wider than the typo that causes it: a
//     correctly written, properly closed citation later in the same sentence
//     goes with it, in a span markdown renders as code. Nothing here cites
//     across a line break, so the run itself is a typo and guessing at it
//     would seed the waiver with a citation no reader can find -- but the
//     collateral is the part worth knowing, and no line in the covered set
//     has backticks to the right of an unmatched run today.
//  3. empty, or a URL: nothing about this tree is being claimed.
//  4. executable punctuation, the codeChars set above: a fragment of a command
//     or an expression -- `'[.[]`, `[.labels[].name]`,
//     `${XDG_BIN_HOME:-$HOME/.local/bin}` -- that no reader could resolve
//     against anything.
//  5. the final return below: a token with no slash, no dot in its basename
//     and no leading underscore. This is where every backticked English word
//     goes, and with it every bare capitalised word symbolName declined for
//     having no interior capital: `Report`, `Retire`, `open` and `Digester`
//     produce no row at all, while `SetTargetSafety` in the same sentence
//     produces one. It is the largest of the six by far and the one a reader
//     is most likely to mistake for coverage.
//
// The count has been wrong twice, in the same direction each time. It read
// "the one silent drop" while symbolName's header already stated a second one
// forty lines away; it then read "the whole of the lane's silence" over three
// token-level cases while (1) and (2) sat honestly documented at their own
// sites. Both times the defect was a summary written from the code in front of
// it. Add a silent drop anywhere in this file and its line belongs here, not
// only where it happens.
func referenceShaped(tok string) bool {
	if tok == "" || strings.Contains(tok, "://") {
		return false
	}
	if callResultSelector(tok) {
		return true
	}
	// A cited call is the same reference as the name it calls, so `time.Now()`
	// is disclosed rather than swallowed by the parens rule below. This is the
	// same normalisation symbolName applies, for the same reason.
	tok = callSuffixRe.ReplaceAllString(tok, "")
	if methodExprRe.MatchString(tok) {
		return true
	}
	if tok == "" || strings.ContainsAny(tok, codeChars) {
		return false
	}
	base := path.Base(tok)
	return strings.Contains(tok, "/") || strings.Contains(base, ".") || strings.HasPrefix(base, "_")
}

// Whether a span is a citation of a Go identifier, and the name to resolve.
//
// The rule accepts the six declaration forms the issue named -- func, method,
// type, const, var and struct field -- because the index it resolves against
// carries all six. What it has to get right is the other direction: which
// backticked words are claims about the Go tree at all. Two shapes qualify:
//
//   - qualified (`home.Init`, `PR.CanMerge`, `(*Store).WriteEnvelope`), where
//     the qualifier names a package or type declared in this repo. That
//     qualifier test is what keeps `fmt.Errorf`, `t.Skip` and `go/parser` out:
//     they are qualified by something this tree does not declare, so no claim
//     about this tree is being made.
//   - unqualified with an interior capital (`ResolveDeliveryMode`,
//     `newSpawnCmd`, `ParentHome`, `ReportRelay`). The hump is what separates an
//     identifier from a backticked English word; without it `open`, `fixed` and
//     `main` would all be symbol citations.
//
// The fail-open that follows, stated rather than discovered: a single
// capitalised word with no interior capital (`Report`, `Retire`) is not checked.
// Nothing distinguishes it from prose, and guessing would put the lane's error
// budget where its evidence is weakest. That one is silent, and it is not
// silent alone: it lands in referenceShaped's third case, with every other
// backticked English word. Every OTHER shape
// this function declines -- above all a qualified name whose qualifier this
// tree does not declare, which is #566's defect class when the rename lands on
// the qualifier -- is caught by referenceShaped and declared as `unchecked`,
// because a citation that vanishes is indistinguishable from one that passed.
func symbolName(idx *index, text string) (bool, bool) {
	tok := strings.TrimSpace(text)
	tok = cleanToken(callSuffixRe.ReplaceAllString(tok, ""))
	if m := methodExprRe.FindStringSubmatch(tok); m != nil {
		typeID, ok := uniqueQualifier(idx.typeQualifiers, m[1])
		if !ok {
			return false, false
		}
		return true, idx.typeMembers[typeID][m[2]]
	}
	if tok == "" || !identRe.MatchString(tok) {
		return false, false
	}
	if i := strings.IndexByte(tok, '.'); i >= 0 {
		qualifier, name := tok[:i], tok[i+1:]
		if strings.Contains(name, ".") {
			return false, false
		}
		var candidates []struct {
			members map[string]bool
		}
		if ids := idx.pkgQualifiers[qualifier]; len(ids) == 1 {
			for id := range ids {
				candidates = append(candidates, struct{ members map[string]bool }{idx.pkgMembers[id]})
			}
		}
		if ids := idx.typeQualifiers[qualifier]; len(ids) == 1 {
			for id := range ids {
				candidates = append(candidates, struct{ members map[string]bool }{idx.typeMembers[id]})
			}
		}
		if len(candidates) != 1 {
			return false, false
		}
		return true, candidates[0].members[name]
		// unjudged: unknown-qualifier
		return false, false
	}
	if allCapsRe.MatchString(tok) {
		return false, false
	}
	return strings.IndexFunc(tok[1:], func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0, idx.names[tok]
}

// ---------------------------------------------------------------------------
// the declaration index
// ---------------------------------------------------------------------------

type index struct {
	names          map[string]bool
	pkgQualifiers  map[string]map[string]bool
	typeQualifiers map[string]map[string]bool
	pkgMembers     map[string]map[string]bool
	typeMembers    map[string]map[string]bool
}

// Every name declared anywhere in the Go tree, by parsing every .go file.
//
// go/parser, not go/types or x/tools: the question is whether a name is
// declared, which is a syntactic question, and answering it syntactically buys
// two things a type-checked answer does not. It reads files under every build
// tag and every GOOS at once -- a parser applies no build constraints -- so a
// `_darwin.go` declaration resolves on a linux runner, which is the hole
// deadcode.sh has to run one analysis per GOOS to close. And it needs no module
// download, which is what keeps this lane hermetic.
//
// testdata trees are excluded: they hold fixture sources for other lanes, and a
// fabricated symbol must not resolve against a fixture.
func buildIndex(root string) (*index, error) {
	idx := &index{
		names:          map[string]bool{},
		pkgQualifiers:  map[string]map[string]bool{},
		typeQualifiers: map[string]map[string]bool{},
		pkgMembers:     map[string]map[string]bool{},
		typeMembers:    map[string]map[string]bool{},
	}
	fset := token.NewFileSet()
	seen := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Leading-dot directories and testdata hold no product Go source:
			// .github carries this lane's own instrument and guardsites', and
			// testdata carries fixture sources for both. A documentation symbol
			// must not resolve against either -- the same exclusion, for the
			// same reason, that guardsites makes.
			if name := d.Name(); p != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		if _, err := containedPath(root, p); err != nil {
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return err
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if perr != nil {
			// Fail closed. A file this tool cannot parse is a file whose
			// declarations are missing from the index, and a missing
			// declaration reads as a fabricated citation -- a red lane for a
			// reason that is not the author's.
			return fmt.Errorf("parse %s: %w", p, perr)
		}
		seen++
		relDir, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		pkg := packageIdentity(filepath.ToSlash(relDir), f.Name.Name)
		addQualifier(idx.pkgQualifiers, f.Name.Name, pkg)
		if idx.pkgMembers[pkg] == nil {
			idx.pkgMembers[pkg] = map[string]bool{}
		}
		for _, decl := range f.Decls {
			idx.collect(pkg, decl)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if seen == 0 {
		return nil, fmt.Errorf("no .go files found under %s -- an empty index would report every symbol citation as fabricated", root)
	}
	return idx, nil
}

func packageIdentity(dir, pkg string) string {
	return dir + "::" + pkg
}

func addQualifier(index map[string]map[string]bool, name, identity string) {
	if index[name] == nil {
		index[name] = map[string]bool{}
	}
	index[name][identity] = true
}

func uniqueQualifier(index map[string]map[string]bool, name string) (string, bool) {
	ids := index[name]
	if len(ids) != 1 {
		return "", false
	}
	for id := range ids {
		return id, true
	}
	return "", false
}

func (idx *index) collect(pkg string, decl ast.Decl) {
	addPackageMember := func(name string) {
		idx.names[name] = true
		idx.pkgMembers[pkg][name] = true
	}
	switch d := decl.(type) {
	case *ast.FuncDecl:
		idx.names[d.Name.Name] = true
		if d.Recv == nil {
			addPackageMember(d.Name.Name)
		} else if receiver := receiverName(d.Recv); receiver != "" {
			idx.addTypeMember(packageIdentityForType(pkg, receiver), receiver, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				idx.names[s.Name.Name] = true
				addPackageMember(s.Name.Name)
				typeID := packageIdentityForType(pkg, s.Name.Name)
				addQualifier(idx.typeQualifiers, s.Name.Name, typeID)
				idx.collectMembers(typeID, s.Type)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					addPackageMember(n.Name)
				}
			}
		}
	}
}

func packageIdentityForType(pkg, typeName string) string {
	return pkg + "::" + typeName
}

func (idx *index) addTypeMember(typeID, typeName, member string) {
	idx.names[member] = true
	if idx.typeMembers[typeID] == nil {
		idx.typeMembers[typeID] = map[string]bool{}
	}
	idx.typeMembers[typeID][member] = true
	addQualifier(idx.typeQualifiers, typeName, typeID)
}

func receiverName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) != 1 {
		return ""
	}
	var receiver ast.Expr = fields.List[0].Type
	for {
		switch t := receiver.(type) {
		case *ast.StarExpr:
			receiver = t.X
		case *ast.Ident:
			return t.Name
		case *ast.IndexExpr:
			receiver = t.X
		case *ast.IndexListExpr:
			receiver = t.X
		default:
			return ""
		}
	}
}

// Direct struct fields, embedded fields and interface methods are three of the
// six accepted forms. They are the forms a `^func`/`^type` grep cannot see:
// `ParentHome` is a struct field and the reviewer's prototype reported it as
// fabricated (#573).
func (idx *index) collectMembers(typeID string, expr ast.Expr) {
	typeName := typeID[strings.LastIndex(typeID, "::")+2:]
	var fields *ast.FieldList
	switch t := expr.(type) {
	case *ast.StructType:
		fields = t.Fields
	case *ast.InterfaceType:
		fields = t.Methods
	default:
		return
	}
	for _, f := range fields.List {
		for _, name := range f.Names {
			idx.addTypeMember(typeID, typeName, name.Name)
		}
		if len(f.Names) == 0 {
			if _, isInterface := expr.(*ast.InterfaceType); !isInterface {
				if name := embeddedName(f.Type); name != "" {
					idx.addTypeMember(typeID, typeName, name)
				}
			}
		}
		idx.collectNestedMemberNames(f.Type)
	}
}

func (idx *index) collectNestedMemberNames(expr ast.Expr) {
	ast.Inspect(expr, func(node ast.Node) bool {
		var fields *ast.FieldList
		switch t := node.(type) {
		case *ast.StructType:
			fields = t.Fields
		case *ast.InterfaceType:
			fields = t.Methods
		default:
			return true
		}
		for _, f := range fields.List {
			for _, name := range f.Names {
				idx.names[name.Name] = true
			}
			if len(f.Names) == 0 {
				if _, isInterface := node.(*ast.InterfaceType); !isInterface {
					if name := embeddedName(f.Type); name != "" {
						idx.names[name] = true
					}
				}
			}
		}
		return true
	})
}

func embeddedName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return embeddedName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return embeddedName(t.X)
	case *ast.IndexListExpr:
		return embeddedName(t.X)
	}
	return ""
}

func declarationExprName(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.SelectorExpr:
		left, ok := declarationExprName(t.X)
		if !ok {
			return "", false
		}
		return left + "." + t.Sel.Name, true
	case *ast.IndexExpr:
		return declarationExprName(t.X)
	case *ast.IndexListExpr:
		return declarationExprName(t.X)
	case *ast.ParenExpr:
		return declarationExprName(t.X)
	case *ast.StarExpr:
		name, ok := declarationExprName(t.X)
		if !ok {
			return "", false
		}
		return "(*" + name + ")", true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// the scan
// ---------------------------------------------------------------------------

func scan(root string) ([]string, error) {
	idx, err := buildIndex(root)
	if err != nil {
		return nil, err
	}
	fi, err := buildFiles(root)
	if err != nil {
		return nil, err
	}
	docs, err := docFiles(root)
	if err != nil {
		return nil, err
	}
	rows := map[string]bool{}
	for _, doc := range docs {
		body, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			return nil, err
		}
		var fenceMarkerByte byte
		var fenceRun int
		var fenceQuoteDepth int
		var fenceContinuationColumns int
		for _, line := range strings.Split(string(body), "\n") {
			if fenceRun > 0 {
				if fenceContinuationColumns == 0 {
					if closesFence(line, fenceMarkerByte, fenceRun, fenceQuoteDepth) {
						fenceMarkerByte, fenceRun, fenceQuoteDepth, fenceContinuationColumns = 0, 0, 0, 0
						continue
					}
					_, _, quoteDepth := stripMarkdownContainer(line)
					if quoteDepth == fenceQuoteDepth {
						continue
					}
					fenceMarkerByte, fenceRun, fenceQuoteDepth, fenceContinuationColumns = 0, 0, 0, 0
				}
				normalized, quoteDepth := stripBlockquoteContainers(line)
				if quoteDepth < fenceQuoteDepth {
					fenceMarkerByte, fenceRun, fenceQuoteDepth, fenceContinuationColumns = 0, 0, 0, 0
				} else if quoteDepth != fenceQuoteDepth {
					continue
				} else {
					if fenceContinuationColumns > 0 {
						var ok bool
						normalized, ok = stripFenceContinuation(normalized, fenceContinuationColumns)
						if !ok {
							if strings.TrimSpace(line) != "" {
								fenceMarkerByte, fenceRun, fenceQuoteDepth, fenceContinuationColumns = 0, 0, 0, 0
							} else {
								continue
							}
						}
					}
					if fenceRun > 0 && closesFenceNormalized(normalized, fenceMarkerByte, fenceRun) {
						fenceMarkerByte, fenceRun, fenceQuoteDepth, fenceContinuationColumns = 0, 0, 0, 0
						continue
					}
					if fenceRun > 0 {
						continue
					}
				}
			}
			if marker, run, _, ok := fenceMarker(line); ok {
				normalized, quoteDepth := stripBlockquoteContainers(line)
				fenceQuoteDepth = quoteDepth
				fenceContinuationColumns, _ = fenceContinuationIndent(normalized)
				fenceMarkerByte, fenceRun = marker, run
				continue
			}
			for _, s := range inlineSpans(line) {
				for _, row := range classify(root, idx, fi, doc, s.text) {
					rows[row] = true
				}
			}
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no citations extracted from %d documents -- the extractor no longer matches, so this proves nothing", len(docs))
	}
	out := make([]string, 0, len(rows))
	for row := range rows {
		out = append(out, row)
	}
	sort.Strings(out)
	return out, nil
}

// One span can hold several citations: `.github/scripts/build-tags.sh packages
// <tag>` is a command whose first word is a path. Path classification therefore
// runs per whitespace-separated token, and the "first segment exists at the
// root" rule is what makes that safe. Symbol classification runs on the whole
// span only: a bare word inside a command line is a subcommand or a flag, not a
// claim about a Go declaration.
func classify(root string, idx *index, fi *files, doc, text string) []string {
	var rows []string
	expressionText := strings.TrimRight(strings.TrimSpace(text), ".,;:'\"")
	if expr, err := parser.ParseExpr(expressionText); err == nil {
		if selectorRootedInCall(expr) {
			return []string{strings.Join([]string{"unchecked", doc, "token", expressionText}, "\t")}
		}
		if call, ok := expr.(*ast.CallExpr); ok {
			if callee, ok := declarationExprName(call.Fun); ok {
				judged, resolved := symbolName(idx, callee)
				if judged {
					status := "unresolved"
					if resolved {
						status = "resolved"
					}
					return []string{strings.Join([]string{status, doc, "symbol", expressionText}, "\t")}
				}
				if referenceShaped(callee) {
					return []string{strings.Join([]string{"unchecked", doc, "token", expressionText}, "\t")}
				}
			}
		}
	}
	fields := strings.Fields(text)
	claimed := false
	var unjudged []string
	for _, tok := range fields {
		tok = cleanToken(tok)
		status := ""
		switch {
		case isRepoPath(root, tok):
			// Root-relative: held to the exact path it names.
			status = "unresolved"
			if repoPathExists(fi.canonicalRoot, tok) {
				status = "resolved"
			}
		case fileCitation(fi, tok):
			// Bare or package-relative: held to naming a file that exists
			// somewhere. Deliberately weaker than the case above -- the
			// citation itself made a weaker claim.
			status = "unresolved"
			if fi.suffix[tok] {
				status = "resolved"
			}
		default:
			unjudged = append(unjudged, tok)
			continue
		}
		claimed = true
		rows = append(rows, strings.Join([]string{status, doc, "path", tok}, "\t"))
	}
	// A span already read as a file is not also a symbol: `home.md` would
	// otherwise be split into a package qualifier and a name and reported twice
	// for one citation.
	if len(fields) == 1 && !claimed {
		if judged, resolved := symbolName(idx, expressionText); judged {
			status := "unresolved"
			if resolved {
				status = "resolved"
			}
			rows = append(rows, strings.Join([]string{status, doc, "symbol", expressionText}, "\t"))
			unjudged = nil
		}
	}
	// Whatever no rule above could judge, declared rather than dropped. The
	// kind is `token` and not `path`: deciding it was a path is one of the
	// judgements that was not available.
	for _, tok := range unjudged {
		if referenceShaped(tok) {
			rows = append(rows, strings.Join([]string{"unchecked", doc, "token", tok}, "\t"))
		}
	}
	return rows
}
