package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

const fakeNoMistakesScript = `#!/bin/sh
case "$1" in
--version) echo "no-mistakes version v1.99.0" ;;
axi) echo "status" ;;
esac
`

// gateFixture is a primary checkout and an isolated NM_HOME.
type gateFixture struct {
	primary string
	nmHome  string
}

func newGateFixture(t *testing.T) gateFixture {
	t.Helper()
	f := gateFixture{primary: t.TempDir(), nmHome: t.TempDir()}
	t.Setenv("NM_HOME", f.nmHome)
	gitInWorktree(t, f.primary, "init", "-q", ".")
	return f
}

// remote names the gate repo url of the primary's no-mistakes remote.
func (f gateFixture) remote(t *testing.T, url string) {
	t.Helper()
	gitInWorktree(t, f.primary, "remote", "add", "no-mistakes", url)
}

func (f gateFixture) repos(t *testing.T) string {
	t.Helper()
	repos := filepath.Join(f.nmHome, "repos")
	if err := os.MkdirAll(repos, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalPath(repos)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func requireGateBlocker(t *testing.T, err error, category GateBlockerCategory, detail string) {
	t.Helper()
	var blocker *GateBlockerError
	if !errors.As(err, &blocker) {
		t.Fatalf("error = %v (%T), want a gate blocker", err, err)
	}
	if blocker.Category != category || !strings.Contains(blocker.Detail, detail) {
		t.Fatalf("blocker = %s %q, want %s containing %q", blocker.Category, blocker.Detail, category, detail)
	}
}

func TestProjectGate(t *testing.T) {
	t.Run("primary without a no-mistakes remote", func(t *testing.T) {
		f := newGateFixture(t)
		_, _, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "the primary has no no-mistakes remote")
		var blocker *GateBlockerError
		errors.As(err, &blocker)
		if !strings.Contains(blocker.Guidance, "no-mistakes init") || !strings.Contains(blocker.Guidance, f.primary) {
			t.Fatalf("guidance = %q, want the init command for the primary", blocker.Guidance)
		}
	})

	t.Run("repos directory missing", func(t *testing.T) {
		f := newGateFixture(t)
		f.remote(t, filepath.Join(f.nmHome, "repos", "p.git"))
		_, _, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "the no-mistakes repos directory is unreadable")
	})

	t.Run("remote outside the repos directory", func(t *testing.T) {
		f := newGateFixture(t)
		f.repos(t)
		elsewhere := t.TempDir()
		f.remote(t, elsewhere)
		_, _, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "is not a gate repo under")
	})

	t.Run("remote that does not exist", func(t *testing.T) {
		f := newGateFixture(t)
		repos := f.repos(t)
		f.remote(t, filepath.Join(repos, "missing.git"))
		_, _, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "is not a gate repo under")
	})

	t.Run("remote nested below a repos entry", func(t *testing.T) {
		f := newGateFixture(t)
		nested := filepath.Join(f.repos(t), "a", "b.git")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		f.remote(t, nested)
		_, _, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "is not a gate repo under")
	})

	t.Run("gate repo directly under repos", func(t *testing.T) {
		f := newGateFixture(t)
		repos := f.repos(t)
		gate := filepath.Join(repos, "p.git")
		if err := os.MkdirAll(gate, 0o755); err != nil {
			t.Fatal(err)
		}
		f.remote(t, gate)
		repo, state, err := projectGate(f.primary)
		if err != nil {
			t.Fatalf("projectGate: %v", err)
		}
		if repo != gate {
			t.Fatalf("gate repo = %q, want %q", repo, gate)
		}
		if want := filepath.Join(filepath.Dir(repos), "state.sqlite"); state != want {
			t.Fatalf("gate state = %q, want the database beside the repos directory %q", state, want)
		}
	})
}

func TestDefaultNoMistakesPreflight(t *testing.T) {
	t.Run("unreadable config", func(t *testing.T) {
		f := newGateFixture(t)
		if err := os.Mkdir(filepath.Join(f.nmHome, "config.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		requireGateBlocker(t, defaultNoMistakesPreflight(f.primary), GateBlockerConfigMismatch, "reading no-mistakes config")
	})

	t.Run("no-mistakes missing", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.SetPath(t, t.TempDir())
		requireGateBlocker(t, defaultNoMistakesPreflight(f.primary), GateBlockerCommandFailure, "no-mistakes CLI not runnable")
	})

	t.Run("gate not initialized", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.FakeOnPath(t, "no-mistakes", fakeNoMistakesScript)
		testutil.FakeOnPath(t, "pi", "#!/bin/sh\n")
		requireGateBlocker(t, defaultNoMistakesPreflight(f.primary), GateBlockerNotInitialized, "the primary has no no-mistakes remote")
	})

	t.Run("initialized gate", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.FakeOnPath(t, "no-mistakes", fakeNoMistakesScript)
		testutil.FakeOnPath(t, "pi", "#!/bin/sh\n")
		gate := filepath.Join(f.repos(t), "p.git")
		if err := os.MkdirAll(gate, 0o755); err != nil {
			t.Fatal(err)
		}
		f.remote(t, gate)
		if err := defaultNoMistakesPreflight(f.primary); err != nil {
			t.Fatalf("preflight: %v", err)
		}
	})
}
