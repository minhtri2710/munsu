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

func TestTransitionalPackagePolicy(t *testing.T) {
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
	retired := map[string]bool{"session": true, "worktree": true, "hometag": true, "ghurl": true, "glurl": true, "composer": true, "nostatus": true, "event": true, "mailbox": true, "uplink": true, "wakedelivery": true, "waker": true, "turnend": true, "afk": true, "backlog": true, "project": true, "scope": true, "delivery": true, "supervision": true, "teardown": true, "decisionhold": true, "brief": true}
	for path, p := range packages {
		for _, imp := range p.Imports {
			if retired[strings.TrimPrefix(imp, root)] && strings.HasPrefix(imp, root) {
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

	// Workflow packages must not directly import concrete backend/session
	// infrastructure. Transitive dependencies through other packages are
	// expected; only direct imports violate the composition root.
	backendFree := map[string]bool{"captain": true, "teardown": true, "supervision": true}
	for _, p := range packages {
		pkg := strings.TrimPrefix(p.ImportPath, root)
		if !backendFree[pkg] || !strings.HasPrefix(p.ImportPath, root) {
			continue
		}
		for _, imp := range p.Imports {
			if imp == root+"backend" {
				t.Errorf("%s directly imports backend; concrete backend belongs at the composition root", p.ImportPath)
			}
		}
	}
}
