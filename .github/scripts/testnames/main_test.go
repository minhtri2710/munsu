package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectsTopLevelTestDeclarationsOnly(t *testing.T) {
	root := t.TempDir()
	src := `package fixture
var _ = "func TestFake("
/* func TestComment( */
func Test(t *testing.T) {}
func Testlower(a, b int) int { return 0 }
func TestWrong(*NotTesting) {}
func TestBlank(_ *testing.T) {}
func TestResult(t *testing.T) error { return nil }
func (fixture) TestMethod(t *testing.T) {}
func TestParent(t *testing.T) { t.Run("subtest", func(t *testing.T) {}) }
`
	path := filepath.Join(root, "fixture_test.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil { t.Fatal(err) }
	git(t, root, "init")
	git(t, root, "add", ".")
	got, err := collect(root)
	if err != nil { t.Fatal(err) }
	want := []record{
		{identity: "Test", packageKey: "fixture", file: "fixture_test.go"},
		{identity: "TestBlank", packageKey: "fixture", file: "fixture_test.go"},
		{identity: "TestParent", packageKey: "fixture", file: "fixture_test.go"},
		{identity: "TestResult", packageKey: "fixture", file: "fixture_test.go"},
		{identity: "TestWrong", packageKey: "fixture", file: "fixture_test.go"},
		{identity: "Testlower", packageKey: "fixture", file: "fixture_test.go"},
	}
	if !reflect.DeepEqual(got, want) { t.Fatalf("records = %#v, want %#v", got, want) }
}

func TestRetainsDuplicateDeclarationsAndFiles(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "one", "a_test.go"),
		filepath.Join(root, "one", "b_test.go"),
		filepath.Join(root, "two", "c_test.go"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
		pkg := filepath.Base(filepath.Dir(path))
		if err := os.WriteFile(path, []byte("package "+pkg+"\nfunc TestDuplicate() {}\n"), 0o600); err != nil { t.Fatal(err) }
	}
	git(t, root, "init")
	git(t, root, "add", ".")
	got, err := collect(root)
	if err != nil { t.Fatal(err) }
	if len(got) != 3 || got[0].file != "one/a_test.go" || got[1].file != "one/b_test.go" || got[2].file != "two/c_test.go" { t.Fatalf("records = %#v", got) }
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("git %v: %v\n%s", args, err, out) }
}
