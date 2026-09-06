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
	"unicode/utf8"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	records, err := collect(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, record := range records {
		fmt.Printf("%s\t%s\t%s\n", record.identity, record.packageKey, record.file)
	}
}

type record struct {
	identity   string
	packageKey string
	file       string
}

func collect(root string) ([]record, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*_test.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	fset := token.NewFileSet()
	var records []record
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
		packageKey := filepath.ToSlash(filepath.Join(filepath.Dir(name), file.Name.Name))
		names := testingImportNames(file)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isTestDecl(fn, names) {
				continue
			}
			addRecord(&records, record{fn.Name.Name, packageKey, filepath.ToSlash(name)})
			if fn.Type.Params.List[0].Names != nil && len(fn.Type.Params.List[0].Names) == 1 && fn.Type.Params.List[0].Names[0].Name != "_" {
				collectSubtests(fn.Body, fn.Type.Params.List[0].Names[0].Obj, fn.Name.Name, names, packageKey, filepath.ToSlash(name), &records)
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].identity != records[j].identity {
			return records[i].identity < records[j].identity
		}
		if records[i].packageKey != records[j].packageKey {
			return records[i].packageKey < records[j].packageKey
		}
		return records[i].file < records[j].file
	})
	return records, nil
}

func addRecord(records *[]record, value record) {
	for _, existing := range *records {
		if existing == value {
			return
		}
	}
	*records = append(*records, value)
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
	if fn.Recv != nil || fn.Name == nil || !isTestName(fn.Name.Name) || fn.Type.TypeParams != nil || fn.Type.Results != nil && len(fn.Type.Results.List) != 0 {
		return false
	}
	return len(fn.Type.Params.List) == 1 && isTestingType(fn.Type.Params.List[0].Type, names)
}

func isTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	rest := name[len("Test"):]
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
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

func isTestingCallback(fn *ast.FuncLit, names map[string]bool) bool {
	if fn.Type.TypeParams != nil || fn.Type.Results != nil && len(fn.Type.Results.List) != 0 {
		return false
	}
	return len(fn.Type.Params.List) == 1 && isTestingType(fn.Type.Params.List[0].Type, names)
}

func collectSubtests(block *ast.BlockStmt, paramObj *ast.Object, prefix string, names map[string]bool, packageKey, file string, records *[]record) {
	if block == nil || paramObj == nil {
		return
	}
	ast.Inspect(block, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
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
		identity := prefix + "/" + normalize(name)
		addRecord(records, record{identity, packageKey, file})
		if len(fn.Type.Params.List[0].Names) == 1 && fn.Type.Params.List[0].Names[0].Name != "_" {
			collectSubtests(fn.Body, fn.Type.Params.List[0].Names[0].Obj, identity, names, packageKey, file, records)
		}
		return false
	})
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
