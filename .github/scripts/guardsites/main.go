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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// skipped by the leading dot, which also keeps this program from scanning
// itself; `testdata` is skipped by name, matching what the go tool does.
func skipDir(name string) bool {
	if name == "testdata" || name == "vendor" {
		return true
	}
	return len(name) > 1 && (name[0] == '.' || name[0] == '_')
}

func scan(root string) ([]site, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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

	fset := token.NewFileSet()
	var rows []site
	for _, path := range files {
		src, err := os.ReadFile(path)
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
				if !isRefusal(stmt.Body) || !isSelfOriginating(stmt.Init, stmt.Cond) {
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
// The last one is why the init statement is read too: its condition is a bare
// `ok` and says nothing, while the branch is unmistakably error handling.
//
// `if len(failures) > 0`, `if identity != Worktree`, `if stored.HeadRef !=
// snap.HeadRef` mention no error and are predicates over data that is already
// type-valid, so they stay.
//
// The cost of the broad rule, paid deliberately: a genuine guard on a field
// named `Error` (`if resp.Error != ""`) drops out of the set. That direction is
// the safe one -- it shrinks the lower bound rather than filling the lane with
// error handling nobody would call a guard.
func isSelfOriginating(init ast.Stmt, cond ast.Expr) bool {
	self := true
	mentionsErr := func(n ast.Node) bool {
		ast.Inspect(n, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				if errValue(v.Name) {
					self = false
				}
			case *ast.SelectorExpr:
				if errValue(v.Sel.Name) {
					self = false
				}
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
func errValue(name string) bool {
	l := strings.ToLower(name)
	return l == "e" || strings.Contains(l, "err")
}
