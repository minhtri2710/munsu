package fleet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// TestNoDirectDeliveryMetaWritesInCutoverFiles pins the Task 7.5 grep gate:
// the delivery preparation and terminal transition files carry no direct
// authoritative home.WriteMeta / home.CompareAndSwapMeta writes. The
// delivery-prepare and delivered/done terminal writes commit as
// generation-bound Authority records; the .meta identity and delivery_state
// keys are reconciled as post-commit projections (ADR-0007 §7), so a direct
// meta write is only acceptable inside a project* projection helper, and
// home.CompareAndSwapMeta is banned outright (the terminal CAS moved into
// the Authority). The delivery_state CAS transitions in delivery_amend.go /
// delivery_mergeops.go are Task 7.6 and are not part of this gate.
func TestNoDirectDeliveryMetaWritesInCutoverFiles(t *testing.T) {
	for _, file := range []string{"delivery_terminal.go", "delivery_mrcheck.go", "delivery_prcheck.go"} {
		path := file
		fset := token.NewFileSet()
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		parsed, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		enclosing := map[*ast.CallExpr]string{}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				ast.Inspect(fn.Body, func(cn ast.Node) bool {
					if call, ok := cn.(*ast.CallExpr); ok {
						enclosing[call] = fn.Name.Name
					}
					return true
				})
				return false
			}
			return true
		})
		var authoritative []string
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "home" {
				return true
			}
			if sel.Sel.Name == "CompareAndSwapMeta" {
				authoritative = append(authoritative, "CompareAndSwapMeta")
			}
			if sel.Sel.Name == "WriteMeta" && !hasProjectPrefix(enclosing[call]) {
				authoritative = append(authoritative, "WriteMeta")
			}
			return true
		})
		if len(authoritative) > 0 {
			t.Errorf("%s carries authoritative meta mutation(s) %v outside the projection layer; delivery preparation and terminal transitions must route through the composed Task Authority (Task 7.5)", file, authoritative)
		}
	}
}

// hasProjectPrefix reports whether the enclosing function name starts with
// "project" (a post-commit projection helper, ADR-0007 §7).
func hasProjectPrefix(fn string) bool {
	return len(fn) >= len("project") && fn[:len("project")] == "project"
}
