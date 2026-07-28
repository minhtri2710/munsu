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
	allowed := map[string]bool{"domain": true, "backend": true, "orchestrator": true, "fleet": true, "config": true, "home": true, "harness": true, "bootstrap": true, "testutil": true, "cli": true}
	for path, p := range packages {
		if strings.HasPrefix(path, root) && !allowed[strings.TrimPrefix(path, root)] {
			t.Errorf("unexpected internal package %s", path)
		}
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, root) && !allowed[strings.TrimPrefix(imp, root)] {
				t.Errorf("%s imports package outside final topology: %s", path, imp)
			}
			if path == root+"orchestrator" && imp == root+"fleet" {
				t.Errorf("orchestrator imports fleet")
			}
			if path == root+"domain" && strings.HasPrefix(imp, root) {
				t.Errorf("domain imports core package %s", imp)
			}
		}
	}

	for pkg := range allowed {
		if _, ok := packages[root+pkg]; !ok {
			t.Errorf("missing required internal package %s", pkg)
		}
	}
}
