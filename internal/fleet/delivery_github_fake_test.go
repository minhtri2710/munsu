package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// ghReply is one scripted gh answer: it applies to the first invocation whose
// space-joined argv contains match. stdout is printed, stderr goes to stderr,
// and a non-zero exit makes the invocation fail.
type ghReply struct {
	match  string
	stdout string
	stderr string
	exit   int
}

// fakeGH is a scripted gh on the gh and gh-axi lookups. It logs every
// invocation's argv (one space-joined line each) and refuses any invocation no
// reply matches, so a test sees exactly the gh calls production makes.
type fakeGH struct {
	dir string
}

func installFakeGH(t *testing.T, replies ...ghReply) *fakeGH {
	t.Helper()
	dir := t.TempDir()
	var script strings.Builder
	script.WriteString("#!/bin/sh\nd=$(dirname \"$0\")\necho \"$*\" >> \"$d/argv.log\"\n")
	for i, r := range replies {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("reply-%d.out", i)), []byte(r.stdout), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("reply-%d.err", i)), []byte(r.stderr), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&script, "case \"$*\" in *'%s'*) cat \"$d/reply-%d.out\"; cat \"$d/reply-%d.err\" >&2; exit %d;; esac\n", r.match, i, i, r.exit)
	}
	script.WriteString("echo \"fake gh: unscripted invocation: $*\" >&2\nexit 99\n")
	gh := testutil.WriteFakeExecutable(t, filepath.Join(dir, "gh"), script.String())
	axi := testutil.WriteFakeExecutable(t, filepath.Join(dir, "gh-axi"), "#!/bin/sh\nexit 0\n")
	oldGH, oldAxi := ghCLILookPath, ghAxiLookPath
	t.Cleanup(func() { ghCLILookPath, ghAxiLookPath = oldGH, oldAxi })
	ghCLILookPath = func() (string, error) { return gh, nil }
	ghAxiLookPath = func() (string, error) { return axi, nil }
	return &fakeGH{dir: dir}
}

// calls returns the logged gh invocations, oldest first.
func (f *fakeGH) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "argv.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}
