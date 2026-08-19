// Command guardsites derives the set of self-originating refusal sites from the
// source tree and prints one tab-separated row per site.
//
// This is the derivation half of the refusal-branch coverage lane (BEO-63,
// tier 2). Nothing in the repo declares which branches are guards: the set is
// read out of the tree, and the committed file next to it lists only the
// waivers. Adding a guard therefore cannot fail open, because there is no
// register to forget to update.
//
// Output columns: file <TAB> func <TAB> nth <TAB> predicate <TAB> block
//
//	file       repo-relative path of the non-test .go file
//	func       enclosing top-level function, `Type.Method` for methods
//	nth        1-based occurrence of this exact predicate in this function
//	predicate  the `if` condition, source text, whitespace collapsed
//	block      `line.col` of the body's opening brace
//
// The first four columns are the identity of a site and are what the baseline
// file stores. `block` is deliberately NOT part of that identity: it is the key
// used to look the site up in a coverage profile on this exact revision, and it
// moves every time an unrelated edit shifts a function down a few lines. A
// baseline keyed on it would churn on diffs that changed no guard -- the same
// reason .github/deadcode.allow drops line:col from its keys.
//
// `nth` is what makes the remaining three columns unique. It is not decoration:
// nine functions in this repo refuse twice on the same predicate (`!ok` twice in
// Canonical.DeliveryCurrency), and without it those pairs would collapse into
// one key, so waiving either would waive both. It is assigned in source order,
// so it only moves when a sibling with the *same* predicate is added or removed
// -- which is a guard change, exactly when the baseline should move.
//
// `block` is the position of `Body.Lbrace`, which is exactly where cmd/cover
// opens the counter for an `if` body (`addCounters(s.Body.Lbrace, ...)`), so a
// site matches a profile block by start position alone. Matching the end
// position too would be wrong, not merely redundant: cover ends the block at the
// closing brace for a `return` body and at the last statement for a `panic` one,
// so half the sites would silently stop matching.
//
// What counts as a site, and why the definition is this narrow: a guard's
// defining property is that on valid input it contributes nothing, so no
// happy-path test can observe whether it exists. Error propagation (`if err !=
// nil { return fmt.Errorf(...) }`) has the opposite property -- it is on the
// path every failing call already takes -- and it outnumbers real guards several
// to one, so a lane that flagged it would be noise.
//
// The recognizer is a heuristic and this number is a LOWER BOUND. It does not
// see: a guard written as a `switch` default, a guard that returns early with a
// bare `nil`, a guard wrapped in a helper call (`mustBeWorktree(p)`), or a guard
// at the *function* level -- a complete refusal that nothing calls. That last
// shape is not a gap here, it is the reachability lane's job (deadcode.sh).
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	rows, err := scan(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::guardsites could not scan the tree, so it cannot derive the guard set: %v\n", err)
		os.Exit(1)
	}
	out := &bytes.Buffer{}
	for _, r := range rows {
		fmt.Fprintf(out, "%s\t%s\t%d\t%s\t%s\n", r.File, r.Func, r.Nth, r.Predicate, r.Block)
	}
	os.Stdout.Write(out.Bytes())
}

type site struct {
	File      string
	Func      string
	Nth       int
	Predicate string
	Block     string
	line      int
	col       int
}

// Directories that never hold production Go source. `.git` and `.github` are
// skipped by the leading dot, which is what keeps a normal repository-root scan
// from including this program's own source; `testdata` is skipped by name,
// matching what the go tool does. The leading dot is NOT a guarantee: a caller
// who aims the walk at the tool's own directory (any root inside .github/scripts)
// defeats it, so scan() also fails closed on the walked set containing this
// source (see the self-measure guard in scan).
func skipDir(name string) bool {
	if name == "testdata" || name == "vendor" {
		return true
	}
	return len(name) > 1 && (name[0] == '.' || name[0] == '_')
}

func scan(root string) ([]site, error) {
	// Absolutise the root ONCE, before it reaches the walk, the re-parser and
	// packages.Config.Dir. The resolver keys type info by filepath.Abs on the
	// filenames go/packages hands back and the walk keys by its own paths; the
	// two sides only agree when both resolve against the same base. With a
	// relative root, a go list that answers in relative filenames keys the type
	// info against one directory while the walk keys against another, every
	// span lookup misses, and each type-dependent guard silently falls back to
	// the name heuristic -- the fail-open this function exists to prevent.
	// Documenting "pass an absolute root" is not the fix; the instrument's
	// value is that it fails closed no matter how it is invoked.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = absRoot

	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// An empty walk is a failure, not an empty answer: this repo has Go source
	// today, so finding none means the walk stopped matching, and a derivation
	// that silently derives nothing waives everything.
	if len(files) == 0 {
		return nil, fmt.Errorf("no non-test .go files under %s", root)
	}

	// The tool must not measure itself. Every scan of this repository skips
	// .github by the leading-dot rule, so the tool's own source can appear in
	// the walked set only because the caller aimed the walk at the tool's own
	// directory (`go run . .` from here, or any root inside .github/scripts).
	// Such a run measures a one-package tree, prints a handful of sites and
	// exits 0 -- an "all clear" that has measured no repository. The lane's
	// whole value is deriving the REPOSITORY's refusal set, so this is the same
	// mis-invocation class as an unreadable tree and fails closed like one.
	_, self, _, _ := runtime.Caller(0)
	selfClean := filepath.Clean(self)
	for _, path := range files {
		if filepath.Clean(path) == selfClean {
			return nil, fmt.Errorf("root %s contains the tool's own source (%s); the refusal set is derived for the repository, not for guardsites itself, so this run would report all clear having measured nothing", root, self)
		}
	}

	// Type info is the whole point of the fix: it lets the recognizer tell an
	// error VALUE from a variable that merely carries a name that looks like
	// one. go/packages only type-checks the files that build for one GOOS/GOARCH
	// at a time, so loadTypes runs it once per GOOS and unions the results; a
	// GOOS-gated file that builds under none of typeCheckGOOS stays out of the
	// loaded set and is handled by the legacy name heuristic below.
	resolver, err := loadTypes(root)
	if err != nil {
		return nil, fmt.Errorf("type-checking the tree: %w", err)
	}

	// Type-checking RAN, but the question is whether its output is usable for
	// THIS walk. The old guard (`typeChecked == 0`) only proved that files were
	// loaded somewhere; it could not tell a run whose type info keys line up
	// with the walk's paths from one where every lookup misses. If none of the
	// walked files resolves to a recorded span under the key the walk computes,
	// the resolver and the walk disagree about where the tree lives and every
	// type-dependent guard silently falls back to name matching -- a derived set
	// that is not just incomplete, it is unusable, and the run must be fatal
	// rather than silent.
	//
	// The rule is deliberately "all of them", not "any". A walked file can be
	// without spans for reasons that are the lane's designed "unmeasured" state,
	// not a broken instrument: a file no loaded GOOS pass compiles (an
	// untargeted build tag, a nested module `./...` never descends into), and a
	// doc-only file with no identifiers at all. This tree has both kinds today.
	// Counting them keeps the legacy name heuristic -- the lane treats them as
	// unmeasured rather than wrong -- and a rule that fired on any empty file
	// would take the lane down on a tree with nothing wrong. Only a total miss
	// leaves the instrument measuring this tree not at all, and the defect this
	// guard exists to catch is total: a base mismatch shifts EVERY resolver key
	// away from every walk key (the two sides resolve against different
	// directories), so no loaded file ever lines up. Recognising the
	// should-have-resolved file from the legitimately-excluded one would mean
	// reimplementing go/build's file selection -- name suffixes, build
	// constraints, cgo, nested modules -- exactly as go list applies it, and a
	// single disagreement would break the lane on a tree with nothing wrong. The
	// total-miss signal is the honest fail-closed boundary.
	if zero := countFilesWithoutSpans(files, resolver); zero == len(files) {
		return nil, fmt.Errorf("type info resolved no spans for any of the %d files the walk found, so the resolver's output is unusable for this tree and every type-dependent guard would silently fall back to the name heuristic", zero)
	}

	fset := token.NewFileSet()
	var rows []site
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		// ParseComments is off and build constraints are not evaluated on
		// purpose: a `_windows.go` guard is a guard, and dropping it here would
		// put it permanently outside the lane instead of into the baseline with
		// a reason. Its lookup will find no block on a linux profile, which the
		// comparison treats as "not proven covered" rather than as absent.
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			owner := funcName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				stmt, ok := n.(*ast.IfStmt)
				if !ok {
					return true
				}
				if !isRefusal(stmt.Body) || !isSelfOriginating(resolver, abs, fset, stmt.Init, stmt.Cond) {
					return true
				}
				pos := fset.Position(stmt.Body.Lbrace)
				rows = append(rows, site{
					File:      rel,
					Func:      owner,
					Predicate: exprText(src, fset, stmt.Cond),
					Block:     fmt.Sprintf("%d.%d", pos.Line, pos.Column),
					line:      pos.Line,
					col:       pos.Column,
				})
				return true
			})
		}
	}
	// Ordinals in source order, so `nth` names the same branch on every machine
	// and does not renumber when ast.Inspect visit order changes under it.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].File != rows[j].File {
			return rows[i].File < rows[j].File
		}
		if rows[i].line != rows[j].line {
			return rows[i].line < rows[j].line
		}
		return rows[i].col < rows[j].col
	})
	seen := map[string]int{}
	for i := range rows {
		key := rows[i].File + "\t" + rows[i].Func + "\t" + rows[i].Predicate
		seen[key]++
		rows[i].Nth = seen[key]
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].File != rows[j].File {
			return rows[i].File < rows[j].File
		}
		if rows[i].Func != rows[j].Func {
			return rows[i].Func < rows[j].Func
		}
		if rows[i].Predicate != rows[j].Predicate {
			return rows[i].Predicate < rows[j].Predicate
		}
		return rows[i].Nth < rows[j].Nth
	})
	return rows, nil
}

// `Type.Method` for methods, bare name for plain functions -- the same shape
// .github/deadcode.allow uses, so a name means one thing in both files. A guard
// inside a closure is attributed to the enclosing top-level function: a closure
// has no stable name, and `func1` renumbers when a sibling closure is added.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	switch v := t.(type) {
	case *ast.Ident:
		return v.Name + "." + fn.Name.Name
	case *ast.IndexExpr: // generic receiver, `Store[T]`
		if id, ok := v.X.(*ast.Ident); ok {
			return id.Name + "." + fn.Name.Name
		}
	case *ast.IndexListExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return id.Name + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

// Source text of the condition with every run of whitespace collapsed to one
// space. The output is tab-separated, so a condition spanning lines or holding
// a tab would otherwise split into columns that are not columns.
func exprText(src []byte, fset *token.FileSet, e ast.Expr) string {
	start := fset.Position(e.Pos()).Offset
	end := fset.Position(e.End()).Offset
	return strings.Join(strings.Fields(string(src[start:end])), " ")
}

// A refusal action: the branch does not fall through, and it ends the operation
// by naming a reason rather than by returning a value the caller uses.
//
// The LAST statement decides, so a branch that logs before it refuses still
// counts. `return nil` and `return err` do not: the first is an early exit with
// no refusal, the second is propagation of somebody else's.
func isRefusal(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	switch s := body.List[len(body.List)-1].(type) {
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			if constructsError(r) {
				return true
			}
		}
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			return fn.Name == "panic"
		case *ast.SelectorExpr:
			pkg, ok := fn.X.(*ast.Ident)
			return ok && pkg.Name == "os" && fn.Sel.Name == "Exit"
		}
	}
	return false
}

// Whether an expression produces an error value that did not exist before this
// branch ran. Three shapes, and no type information is used -- go/types would
// answer this exactly but costs a full load of every package, which is the
// budget the whole lane has.
//
//	fmt.Errorf(...) / errors.New(...) / anything named *Err*  a constructed error
//	&fooError{...} / fooError{...}                            a literal error value
//	ErrNotFound / errNotWorktree                              a sentinel
//
// `err` and `e` themselves are excluded by requiring something after the prefix:
// those name a value that arrived from elsewhere.
func constructsError(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		return errorish(calleeName(v.Fun))
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return constructsError(v.X)
		}
	case *ast.CompositeLit:
		return errorish(typeName(v.Type))
	case *ast.Ident:
		return sentinel(v.Name)
	case *ast.SelectorExpr:
		return sentinel(v.Sel.Name)
	}
	return false
}

func calleeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if pkg, ok := v.X.(*ast.Ident); ok {
			return pkg.Name + "." + v.Sel.Name
		}
		return v.Sel.Name
	case *ast.IndexExpr:
		return calleeName(v.X)
	}
	return ""
}

func typeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.IndexExpr:
		return typeName(v.X)
	}
	return ""
}

// A name that says "error" anywhere in it, qualifier included. The qualifier has
// to count: `errors.New` is the second most common way to build one in this
// repo and its bare selector says nothing at all.
func errorish(name string) bool {
	return strings.Contains(strings.ToLower(name), "err")
}

// A package-level sentinel: `ErrNotFound`, `errNotWorktree`. The suffix must be
// non-empty, which is what keeps the local `err` out.
func sentinel(name string) bool {
	for _, p := range []string{"Err", "err"} {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return true
		}
	}
	return false
}

// Whether the branch starts the refusal itself rather than reacting to an error
// somebody else produced. This is the property that makes the derived set
// bounded: it removes the `if err != nil` population, which is several times
// larger than the guard population and is already on the path of every failing
// call -- a lane that flagged it would be reporting ordinary error handling.
//
// One rule, applied to the condition AND the `if`'s init statement: neither may
// mention an error value. That covers the four shapes that showed up when this
// was first run over the tree, which a narrower `X != nil` rule let through:
//
//	if err != nil                                     the plain case
//	if os.IsNotExist(err)                             classification by helper
//	if errors.Is(err, fs.ErrNotExist)                 classification by errors
//	if ee, ok := err.(*exec.ExitError); ok            classification by assertion
//
// `if len(failures) > 0`, `if identity != Worktree`, `if stored.HeadRef !=
// snap.HeadRef` mention no error and are predicates over data that is already
// type-valid, so they stay.
//
// Identifiers are judged by their resolved TYPE, not their name. go/packages
// type-checks the whole tree up front (see loadTypes), so a string field named
// `Error` (`if resp.Error != ""`) is data and stays in the set, while an
// `error`-typed field or parameter is propagation and drops out no matter what
// it is called. The old rule judged names, which is why a validator whose
// parameter was literally named `e` lost every branch: a launch-argument
// validator is not an error value just because its parameter is called `e`.
//
// Three edges. An identifier in a type-loaded file whose type cannot be
// resolved fails CLOSED to "error" (the safe direction -- shrink the lower
// bound rather than guess a branch is a guard). A GOOS-gated file that never
// builds under any GOOS in typeCheckGOOS is never type-loaded at all; those
// unmeasured files keep the legacy name heuristic so their guards stay visible
// to the coverage lane, exactly as they always were. And the `_` blank is
// never an error value.
func isSelfOriginating(r *resolver, file string, fset *token.FileSet, init ast.Stmt, cond ast.Expr) bool {
	self := true
	mentionsErr := func(n ast.Node) bool {
		ast.Inspect(n, func(n ast.Node) bool {
			if expr, ok := n.(ast.Expr); ok && r.errish(file, fset, expr) {
				self = false
			}
			return self
		})
		return self
	}
	if init != nil && !mentionsErr(init) {
		return false
	}
	return mentionsErr(cond)
}

// A name that holds or classifies an error value: the local `err`/`e`, a field
// or helper whose name says error. Unlike sentinel() this DOES accept a bare
// `err` -- here the question is what the value is, not where it came from.
//
// This is now only the fallback for GOOS-gated files that go/packages cannot
// type-check under any GOOS in typeCheckGOOS; type-loaded files are judged by
// the real type of each identifier.
func errValue(name string) bool {
	l := strings.ToLower(name)
	return l == "e" || strings.Contains(l, "err")
}

// A span is a node's byte range within its file: (start offset, end offset).
// The start offset alone is not a key. In `ctx.Err()` the CallExpr, the
// SelectorExpr and the identifier `ctx` all begin at the same byte, so keying
// on the start would let one overwrite the others and the call's `error` result
// would be indistinguishable from the receiver's type. The end offset separates
// them. Offsets, not *ast.Node pointers, are the key at all because the
// recognizer re-parses each file with its own FileSet: byte offsets in the same
// source agree across independent parses, while AST pointers do not.
type span struct{ off, end int }

// A resolver maps every typed node in a type-loaded file to its resolved type,
// so the recognizer can tell a genuine error value from a data variable no
// matter what it is named.
//
// go/packages type-checks only the files that build for one GOOS/GOARCH at a
// time, so a single load cannot see platform-gated siblings. loadTypes unions
// GOOS=linux, darwin and windows, which between them cover every build-gated
// file in this tree except one gated to a GOOS the repo never targets; that
// file stays unloaded and keeps the legacy name heuristic.
type resolver struct {
	byFile map[string]map[span]types.Type // abs path -> node span -> type
	loaded map[string]bool                // abs paths type-checked under some GOOS
}

// The three GOOS values whose union covers this tree's build-gated files.
// `.github/build-tags.manifest` is the authority on which files are gated and
// how; keep these in step with its goos-vet rows.
var typeCheckGOOS = []string{"linux", "darwin", "windows"}

// loadTypes type-checks the tree under root once per GOOS and records, for
// every loaded file, the resolved type of each identifier and each typed
// expression, keyed by span.
//
// Recording expressions and not just identifiers is what makes `ctx.Err() != nil`
// read as error propagation: the identifier `ctx` is a context, the selector
// `ctx.Err` is a `func() error`, and neither is an error value -- only the call
// itself has type `error`.
func loadTypes(root string) (*resolver, error) {
	r := &resolver{byFile: map[string]map[span]types.Type{}, loaded: map[string]bool{}}
	for _, goos := range typeCheckGOOS {
		cfg := &packages.Config{
			Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
			Dir:  root,
			// Appending is the documented way to override one variable:
			// packages.Config.Env says "only the last value in the slice for
			// each environment key is used" and gives this exact idiom. An
			// inherited GOOS earlier in the slice does not win, so this does
			// not need filtering. Reviewed once on the opposite premise and
			// measured: with GOOS=darwin exported, the type-not-name fixture
			// still yields b_windows.go, which only resolves when the windows
			// pass type-checks. Do not "fix" this without re-running that.
			Env: append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0"),
		}
		pkgs, err := packages.Load(cfg, "./...")
		if err != nil {
			return nil, fmt.Errorf("GOOS=%s: %v", goos, err)
		}
		for _, p := range pkgs {
			if len(p.Errors) > 0 {
				return nil, fmt.Errorf("GOOS=%s: package %s did not type-check: %v", goos, p.PkgPath, p.Errors[0])
			}
			for _, f := range p.Syntax {
				if abs, err := filepath.Abs(p.Fset.Position(f.Pos()).Filename); err == nil {
					r.loaded[abs] = true
				}
			}
			for id, obj := range p.TypesInfo.Uses {
				if obj != nil && obj.Type() != nil {
					r.record(p.Fset, id, obj.Type())
				}
			}
			for id, obj := range p.TypesInfo.Defs {
				if obj != nil && obj.Type() != nil {
					r.record(p.Fset, id, obj.Type())
				}
			}
			for expr, tv := range p.TypesInfo.Types {
				if tv.Type != nil {
					r.record(p.Fset, expr, tv.Type)
				}
			}
		}
	}
	return r, nil
}

// The number of walked files under which the resolver recorded no type span,
// keyed by the walk's own absolute path. The walk and the resolver must key by
// the same path for type info to be usable; a base mismatch between the two
// shows up here as every file resolving to nothing.
func countFilesWithoutSpans(files []string, r *resolver) int {
	n := 0
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil || len(r.byFile[abs]) == 0 {
			n++
		}
	}
	return n
}

// record stores n's type under its own file and span. Nodes seen under more
// than one GOOS are written more than once with the same key and type, so the
// union is order-independent.
func (r *resolver) record(fset *token.FileSet, n ast.Node, t types.Type) {
	pos := fset.Position(n.Pos())
	abs, err := filepath.Abs(pos.Filename)
	if err != nil {
		return
	}
	m := r.byFile[abs]
	if m == nil {
		m = map[span]types.Type{}
		r.byFile[abs] = m
	}
	m[span{pos.Offset, fset.Position(n.End()).Offset}] = t
}

// errish reports whether expr in file is an error value. Type info wins where
// it exists; the open cases are stated in isSelfOriginating's doc comment.
func (r *resolver) errish(file string, fset *token.FileSet, expr ast.Expr) bool {
	if id, ok := expr.(*ast.Ident); ok && id.Name == "_" {
		return false
	}
	if r == nil || !r.loaded[file] {
		// Unmeasured under every GOOS we load: the real type is unknowable
		// here, so keep exactly the name heuristic these files always used.
		id, ok := expr.(*ast.Ident)
		return ok && errValue(id.Name)
	}
	t, ok := r.byFile[file][span{fset.Position(expr.Pos()).Offset, fset.Position(expr.End()).Offset}]
	if !ok {
		// An identifier in a type-loaded file with no resolved type fails
		// CLOSED to "error": shrinking the set is the safe direction, the same
		// one the package doc pays for elsewhere. Other expression nodes
		// legitimately carry no recorded type, and reading that absence as an
		// error value would empty the register instead of shrinking it.
		_, isIdent := expr.(*ast.Ident)
		return isIdent
	}
	return isErrorType(t)
}

// The universe `error` interface, resolved once. types.Implements against it
// answers "is this an error value?" for concrete types, interfaces, named
// alias errors, and `error` itself.
var errorInterface = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

func isErrorType(t types.Type) bool {
	return types.Implements(t, errorInterface)
}
