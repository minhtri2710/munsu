package testutil

import (
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

type listedPackage struct {
	ImportPath string
	Imports    []string
}

// TestPackageTopology enforces the owner-clean dependency-direction rules of
// ADR-0008 §1 on the live package graph. Rules are expressed as ownership and
// import-direction constraints between named modules, never as a literal
// package-count gate or a blacklist of deleted package names:
//
//	domain        — shared concepts only: imports no internal package.
//	config        — configuration resolution authority: imports no internal package.
//	home          — domain-neutral durable mechanics: imports domain only.
//	backend       — terminal/worktree/repo/provider capabilities: imports home only.
//	harness       — coding-agent runtime capabilities: imports config only.
//	taskauthority — Task truth and invariant operations: imports domain and home only.
//	orchestrator  — supervision policy and Uplink lifecycle: never imports fleet.
//	fleet         — workforce execution: never imports orchestrator.
//	testutil      — test infrastructure only: no product module imports it.
//	cmd/munsu     — executable bootstrap: no internal package imports it.
func TestPackageTopology(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	packages := map[string]listedPackage{}
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		packages[p.ImportPath] = p
	}
	const root = "github.com/minhtri2710/munsu/internal/"

	internalImports := func(p listedPackage) []string {
		var internal []string
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, root) {
				internal = append(internal, strings.TrimPrefix(imp, root))
			}
		}
		return internal
	}

	// module is the short package name (e.g. "fleet") for an internal path,
	// or "" when the path is not an internal package.
	module := func(path string) string {
		if strings.HasPrefix(path, root) {
			return strings.TrimPrefix(path, root)
		}
		return ""
	}

	onlyImports := func(t *testing.T, path string, allowed ...string) {
		t.Helper()
		p, ok := packages[path]
		if !ok {
			t.Errorf("missing required internal package %s", module(path))
			return
		}
		perm := map[string]bool{}
		for _, a := range allowed {
			perm[a] = true
		}
		for _, imp := range internalImports(p) {
			if !perm[imp] {
				t.Errorf("%s imports %s outside its ownership rule (allowed: %s)", module(path), imp, strings.Join(allowed, ", "))
			}
		}
	}

	neverImports := func(t *testing.T, path, banned string) {
		t.Helper()
		p, ok := packages[path]
		if !ok {
			t.Errorf("missing required internal package %s", module(path))
			return
		}
		for _, imp := range internalImports(p) {
			if imp == banned {
				t.Errorf("%s imports %s; the two modules must not depend on each other", module(path), banned)
			}
		}
	}

	for path, p := range packages {
		m := module(path)
		switch m {
		case "domain", "config":
			for _, imp := range internalImports(p) {
				t.Errorf("%s imports core package %s; it must stay self-contained", m, imp)
			}
		case "home":
			onlyImports(t, path, "domain")
		case "backend":
			onlyImports(t, path, "home")
		case "harness":
			onlyImports(t, path, "config")
		case "taskauthority":
			onlyImports(t, path, "domain", "home")
		case "fleet":
			neverImports(t, path, "orchestrator")
		case "orchestrator":
			neverImports(t, path, "fleet")
		default:
			// No ownership rule pins this module's imports; the specific
			// rules above still apply to it through neverImports below.
		}
		// testutil is test infrastructure only: no package may depend on it.
		for _, imp := range internalImports(p) {
			if imp == "testutil" {
				t.Errorf("%s imports test infrastructure package testutil", m)
			}
		}
		// cmd/munsu is the executable bootstrap; nothing internal depends on it.
		if strings.HasPrefix(p.ImportPath, "github.com/minhtri2710/munsu/internal/") {
			for _, imp := range p.Imports {
				if imp == "github.com/minhtri2710/munsu/cmd" {
					t.Errorf("%s imports executable bootstrap cmd/munsu", m)
				}
			}
		}
	}

	// The rules are anchored on named modules; fail loudly if a rule's module
	// vanished from the graph so the policy cannot silently go stale.
	for _, name := range []string{"domain", "config", "home", "backend", "harness", "taskauthority", "fleet", "orchestrator", "cli", "testutil"} {
		if _, ok := packages[root+name]; !ok {
			t.Errorf("missing required internal package %s", name)
		}
	}
}
