package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectsSemanticTestIdentities(t *testing.T) {
	root := t.TempDir()
	src := `package fixture
import ("testing"; tlib "testing")
var _ = "func TestFake(" 
/* func TestComment( */
func TestValid(t *testing.T) {
	t.Run("two  words", func(t *testing.T) { t.Run(` + "`raw name`" + `, func(t *testing.T) {}) })
	func() {
		t := 1
		_ = t
		t.Run("shadowed", func(t *testing.T) {})
	}
}
func Testlower(t *testing.T) {}
func TestWrong(t testing.T) {}
func (x fixture) TestMethod(t *testing.T) {}
func TestAlias(t *tlib.T) {}
`
	path := filepath.Join(root, "fixture_test.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init")
	git(t, root, "add", ".")
	got, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"TestAlias", "TestValid", "TestValid/two__words", "TestValid/two__words/raw_name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identities = %#v, want %#v", got, want)
	}
}

func TestCollectReflectsRemovedSubtests(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fixture_test.go")
	write := func(body string) {
		if err := os.WriteFile(path, []byte("package fixture\nimport \"testing\"\n"+body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`func TestParent(t *testing.T) { t.Run("kept", func(t *testing.T) {}); t.Run("removed", func(t *testing.T) {}) }`)
	git(t, root, "init")
	git(t, root, "add", ".")
	got, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"TestParent", "TestParent/kept", "TestParent/removed"}) {
		t.Fatal(got)
	}
	write(`func TestParent(t *testing.T) { t.Run("kept", func(t *testing.T) {}) }`)
	got, err = collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"TestParent", "TestParent/kept"}) {
		t.Fatal(got)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
