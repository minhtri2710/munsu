package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDefault(t *testing.T) {
	// Ensure no env override
	os.Unsetenv("MUNSU_HOME")
	path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, DefaultDirName)
	if path != want {
		t.Errorf("Resolve() = %q, want %q", path, want)
	}
}

func TestResolveEnvOverride(t *testing.T) {
	envHome := absTestPath(t, "tmp", "munsu-test-env")
	os.Setenv("MUNSU_HOME", envHome)
	defer os.Unsetenv("MUNSU_HOME")
	path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if path != envHome {
		t.Errorf("Resolve() = %q, want %q", path, envHome)
	}
}

func TestResolveFlagOverride(t *testing.T) {
	flagHome := absTestPath(t, "tmp", "munsu-test-flag")
	os.Unsetenv("MUNSU_HOME")
	path, err := Resolve(flagHome)
	if err != nil {
		t.Fatal(err)
	}
	if path != flagHome {
		t.Errorf("Resolve() = %q, want %q", path, flagHome)
	}
}

func TestResolveFlagOverridesEnv(t *testing.T) {
	envHome := absTestPath(t, "tmp", "munsu-test-env")
	flagHome := absTestPath(t, "tmp", "munsu-test-flag")
	os.Setenv("MUNSU_HOME", envHome)
	defer os.Unsetenv("MUNSU_HOME")
	path, err := Resolve(flagHome)
	if err != nil {
		t.Fatal(err)
	}
	if path != flagHome {
		t.Errorf("Resolve() = %q, want %q", path, flagHome)
	}
}

// absTestPath joins name under the filesystem root and makes it absolute.
// Resolve returns filepath.Abs of its override, so an input that is already
// absolute is returned unchanged and the assertion stays about which override
// wins rather than about path shape. A leading separator alone is not enough:
// on Windows it names the current drive's root and filepath.Abs still rewrites
// it to a volume-qualified path.
func absTestPath(t *testing.T, name ...string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(append([]string{string(os.PathSeparator)}, name...)...))
	if err != nil {
		t.Fatalf("Abs(%v): %v", name, err)
	}
	return abs
}

func TestResolveRootOverrideIgnored(t *testing.T) {
	// MUNSU_ROOT_OVERRIDE is deprecated; it should NOT affect resolution.
	os.Unsetenv("MUNSU_HOME")
	os.Setenv("MUNSU_ROOT_OVERRIDE", "/tmp/munsu-root-override")
	defer os.Unsetenv("MUNSU_ROOT_OVERRIDE")
	path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	// Should resolve to default ~/.munsu, not MUNSU_ROOT_OVERRIDE.
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, DefaultDirName)
	if path != want {
		t.Errorf("Resolve() = %q, want %q (MUNSU_ROOT_OVERRIDE should be ignored)", path, want)
	}
}
