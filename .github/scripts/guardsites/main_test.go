package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDerivesSelfOriginatingGuard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `package fixture

import "errors"

func Refuse(mode string) error {
	if mode != "safe" {
		return errors.New("refused")
	}
	return nil
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := scan(root)
	if err != nil {
		t.Fatalf("scan(%q): %v", root, err)
	}
	if len(rows) != 1 {
		t.Fatalf("scan returned %d sites, want 1: %#v", len(rows), rows)
	}
	if rows[0].File != "main.go" || rows[0].Func != "Refuse" || rows[0].Nth != 1 || rows[0].Predicate != "mode != \"safe\"" {
		t.Fatalf("site = %#v, want the executable refusal guard", rows[0])
	}
}

func TestScanRejectsEmptyTree(t *testing.T) {
	if _, err := scan(t.TempDir()); err == nil {
		t.Fatal("scan of a tree without Go source unexpectedly succeeded")
	}
}
