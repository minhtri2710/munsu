package testutil

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

type listedPackage struct {
	ImportPath string
	Imports    []string
}

func TestTransitionalPackagePolicy(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	packages := map[string]listedPackage{}
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatal(err)
		}
		packages[p.ImportPath] = p
	}
	const root = "github.com/minhtri2710/munsu/internal/"
	for path, p := range packages {
		for _, imp := range p.Imports {
			if imp == root+"session" || imp == root+"worktree" || imp == root+"hometag" {
				t.Errorf("%s imports retired package %s", path, imp)
			}
			if path == root+"fleet" && imp == root+"orchestrator" {
				t.Errorf("fleet imports orchestrator")
			}
			if path == root+"orchestrator" && imp == root+"fleet" {
				t.Errorf("orchestrator imports fleet")
			}
			if path == root+"domain" && strings.HasPrefix(imp, root) {
				t.Errorf("domain imports core package %s", imp)
			}
		}
	}
}
