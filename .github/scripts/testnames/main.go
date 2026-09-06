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
	"strings"
)

type record struct {
	identity   string
	packageKey string
	file       string
}

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
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			records = append(records, record{fn.Name.Name, packageKey, filepath.ToSlash(name)})
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
