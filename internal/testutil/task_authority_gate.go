package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LegacyTaskAuthoritySymbols are home task aggregate/lifecycle/dispatch
// mutations that the Task Authority migration (ADR-0007) replaces slice by
// slice. New production callers of these symbols are banned by the migration
// gate; the per-package allowlist only shrinks as slices land.
var LegacyTaskAuthoritySymbols = []string{
	"CreateTaskAggregate",
	"UpdateCurrentTaskAggregateState",
	"UpdateCurrentTaskAggregateKind",
	"StartTask",
	"UnblockTask",
	"ReopenTask",
	"BindTaskEndpoint",
	"CheckDispatchHold",
	"CreateDispatchHold",
	"ReleaseDispatchHold",
	"ResolveDispatchDecision",
	"PersistDispatchInterpretation",
}

// AssertNoNewTaskAuthorityCallers fails when any production file under pkgDir
// calls a legacy home task-authority mutation outside the allowlist. The
// allowlist maps a file base name to the symbol names that file may still
// call. It is intentionally exact: stale entries (missing file, empty symbol
// list) fail so the allowlist cannot grow stale while migration proceeds.
func AssertNoNewTaskAuthorityCallers(t *testing.T, pkgDir string, allowed map[string][]string) {
	t.Helper()
	symbols := make(map[string]bool, len(LegacyTaskAuthoritySymbols))
	for _, s := range LegacyTaskAuthoritySymbols {
		symbols[s] = true
	}

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("reading %s: %v", pkgDir, err)
	}
	existing := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		existing[name] = true
		allowedSymbols := map[string]bool{}
		for _, s := range allowed[name] {
			allowedSymbols[s] = true
		}
		var unexpected []string
		for _, call := range taskAuthorityCalls(t, filepath.Join(pkgDir, name)) {
			if symbols[call] && !allowedSymbols[call] {
				unexpected = append(unexpected, call)
			}
		}
		if len(unexpected) > 0 {
			t.Errorf("%s/%s calls legacy task-authority mutation(s) %v outside the migration allowlist", pkgDir, name, unexpected)
		}
	}
	for file, calls := range allowed {
		if len(calls) == 0 {
			t.Errorf("allowlist entry %s/%s has no symbols; remove it", pkgDir, file)
			continue
		}
		if !existing[file] {
			t.Errorf("allowlist names %s/%s but the file no longer exists; remove the entry", pkgDir, file)
		}
	}
}

// taskAuthorityCalls returns the home/mhome selector names invoked in one Go
// source file, ignoring tests and comments via the parser.
func taskAuthorityCalls(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var calls []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name == "home" || ident.Name == "mhome" {
			calls = append(calls, sel.Sel.Name)
		}
		return true
	})
	return calls
}
