package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	ids, err := collect(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, id := range ids {
		fmt.Println(id)
	}
}

func collect(root string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*_test.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	fset := token.NewFileSet()
	ids := map[string]bool{}
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		names := testingImportNames(file)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isTestDecl(fn, names) {
				continue
			}
			ids[fn.Name.Name] = true
			p := fn.Type.Params.List[0].Names[0]
			collectSubtests(fn.Body, p.Obj, fn.Name.Name, names, ids)
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func testingImportNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		name := "testing"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "." {
			names["T"] = true
		} else if name != "_" {
			names[name] = true
		}
	}
	return names
}

func isTestDecl(fn *ast.FuncDecl, names map[string]bool) bool {
	if fn.Recv != nil || fn.Name == nil || len(fn.Name.Name) <= 4 || !strings.HasPrefix(fn.Name.Name, "Test") || unicode.IsLower([]rune(fn.Name.Name[4:])[0]) || fn.Type.TypeParams != nil || fn.Type.Results != nil {
		return false
	}
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	field := fn.Type.Params.List[0]
	if len(field.Names) != 1 || field.Names[0].Name == "_" {
		return false
	}
	return isTestingType(field.Type, names)
}
func isTestingType(expr ast.Expr, names map[string]bool) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	if id, ok := star.X.(*ast.Ident); ok {
		return id.Name == "T" && names[id.Name]
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && names[pkg.Name]
}

func collectSubtests(block *ast.BlockStmt, paramObj *ast.Object, prefix string, names map[string]bool, ids map[string]bool) {
	if block == nil {
		return
	}
	ast.Inspect(block, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Obj != paramObj {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil || name == "" {
			return true
		}
		fn, ok := call.Args[1].(*ast.FuncLit)
		if !ok || !isTestingCallback(fn, names) {
			return true
		}
		child := prefix + "/" + normalize(name)
		ids[child] = true
		collectSubtests(fn.Body, fn.Type.Params.List[0].Names[0].Obj, child, names, ids)
		return false
	})
}
func isTestingCallback(fn *ast.FuncLit, names map[string]bool) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	field := fn.Type.Params.List[0]
	return len(field.Names) == 1 && isTestingType(field.Type, names)
}
func normalize(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsSpace(r) {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
