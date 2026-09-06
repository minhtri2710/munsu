package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func identities(records []record) []string {
	result := make([]string, len(records))
	for i, item := range records {
		result[i] = item.identity
	}
	return result
}

func TestCollectsSemanticTestIdentities(t *testing.T) {
	root := t.TempDir()
	src := `package fixture
import ("testing"; tlib "testing")
var _ = "func TestFake(" 
/* func TestComment( */
func Test(t *testing.T) {}
func TestValid(t *testing.T) {
	t.Run("two  words", func(t *testing.T) { t.Run(` + "`raw name`" + `, func(t *testing.T) {}) })
	func() { t.Run("unreachable", func(t *testing.T) {}) }
}
func Testlower(t *testing.T) {}
func TestWrong(t testing.T) {}
func (x fixture) TestMethod(t *testing.T) {}
func TestAlias(t *tlib.T) {}
func TestUnnamed(*testing.T) {}
func TestBlank(_ *testing.T) {}
func TestEmpty(t *testing.T) () {}
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
	want := []string{"Test", "TestAlias", "TestBlank", "TestEmpty", "TestUnnamed", "TestValid", "TestValid/two__words", "TestValid/two__words/raw_name"}
	if !reflect.DeepEqual(identities(got), want) {
		t.Fatalf("identities = %#v, want %#v", identities(got), want)
	}
}

func TestCollectReflectsRemovedSubtestsAndDuplicatePackages(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one", "fixture_test.go")
	second := filepath.Join(root, "two", "fixture_test.go")
	if err := os.MkdirAll(filepath.Dir(first), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(second), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, pkg, body string) {
		if err := os.WriteFile(path, []byte("package "+pkg+"\nimport \"testing\"\n"+body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(first, "one", `func TestParent(t *testing.T) { t.Run("kept", func(t *testing.T) {}); t.Run("removed", func(t *testing.T) {}) }`)
	write(second, "two", `func TestParent(t *testing.T) {}`)
	git(t, root, "init")
	git(t, root, "add", ".")
	got, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].identity != "TestParent" || got[0].packageKey == got[1].packageKey {
		t.Fatalf("records = %#v", got)
	}
	write(first, "one", `func TestParent(t *testing.T) { t.Run("kept", func(t *testing.T) {}) }`)
	got, err = collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(identities(got), []string{"TestParent", "TestParent", "TestParent/kept"}) {
		t.Fatal(identities(got))
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
