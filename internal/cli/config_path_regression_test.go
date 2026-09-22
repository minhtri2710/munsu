package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigCommandRejectsUnknownPathKeysWithoutWriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	for _, key := range []string{"../../x", "base.json"} {
		for _, operation := range []string{"get", "set"} {
			t.Run(operation+"/"+key, func(t *testing.T) {
				root := NewRootCommand()
				root.SetOut(new(bytes.Buffer))
				root.SetErr(new(bytes.Buffer))
				args := []string{"config", operation, key}
				if operation == "set" {
					args = append(args, "pwned")
				}
				root.SetArgs(args)
				if err := root.Execute(); err == nil {
					t.Fatalf("config %s %q succeeded; want unknown-key refusal", operation, key)
				}
			})
		}
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "x")); !os.IsNotExist(err) {
		t.Fatalf("traversal key wrote outside config directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "config", "base.json")); !os.IsNotExist(err) {
		t.Fatalf("base.json key wrote inside config directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "config")); !os.IsNotExist(err) {
		t.Fatalf("unknown-key commands created config directory: %v", err)
	}
}
