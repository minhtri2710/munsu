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
	os.Setenv("MUNSU_HOME", "/tmp/munsu-test-env")
	defer os.Unsetenv("MUNSU_HOME")
	path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/munsu-test-env" {
		t.Errorf("Resolve() = %q, want %q", path, "/tmp/munsu-test-env")
	}
}

func TestResolveFlagOverride(t *testing.T) {
	os.Unsetenv("MUNSU_HOME")
	path, err := Resolve("/tmp/munsu-test-flag")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/munsu-test-flag" {
		t.Errorf("Resolve() = %q, want %q", path, "/tmp/munsu-test-flag")
	}
}

func TestResolveFlagOverridesEnv(t *testing.T) {
	os.Setenv("MUNSU_HOME", "/tmp/munsu-test-env")
	defer os.Unsetenv("MUNSU_HOME")
	path, err := Resolve("/tmp/munsu-test-flag")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/munsu-test-flag" {
		t.Errorf("Resolve() = %q, want %q", path, "/tmp/munsu-test-flag")
	}
}

func TestResolveRootOverride(t *testing.T) {
	os.Unsetenv("MUNSU_HOME")
	os.Setenv("MUNSU_ROOT_OVERRIDE", "/tmp/munsu-root-override")
	defer os.Unsetenv("MUNSU_ROOT_OVERRIDE")
	path, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/munsu-root-override" {
		t.Errorf("Resolve() = %q, want %q", path, "/tmp/munsu-root-override")
	}
}

func TestEnsureDirTree(t *testing.T) {
	tmp := t.TempDir()
	homeDir := filepath.Join(tmp, ".munsu")
	if err := EnsureDirTree(homeDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range HomeDirNames {
		d := filepath.Join(homeDir, name)
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("expected dir %s to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}
}
