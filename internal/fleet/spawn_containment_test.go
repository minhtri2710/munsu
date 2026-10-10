package fleet

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoMistakesYAML_DisableProjectSettingsIsTrue asserts that the repository's
// .no-mistakes.yaml has disable_project_settings: true.
// This is a contract test — it guards against weakening project-instruction isolation.
func TestNoMistakesYAML_DisableProjectSettingsIsTrue(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// Walk up to repo root (spawn/spawn_test.go → spawn/ → internal/ → repo root)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	yamlPath := filepath.Join(repoRoot, ".no-mistakes.yaml")

	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("reading .no-mistakes.yaml: %v", err)
	}

	if !strings.Contains(string(data), "disable_project_settings: true") {
		t.Errorf(".no-mistakes.yaml must preserve disable_project_settings: true; current content:\n%s", string(data))
	}
}
