package home

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRefusesStatErrors(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "faulted-root")
	sentinel := errors.New("injected lstat failure")
	var statPath string
	_, err := initWithLstat(root, func(path string) (os.FileInfo, error) {
		statPath = path
		return nil, sentinel
	})
	if err == nil {
		t.Fatal("Init accepted an injected stat failure")
	}
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "home: stat root") {
		t.Fatalf("Init error = %v, want stat-root error wrapping sentinel", err)
	}
	abs, absErr := filepath.Abs(root)
	if absErr != nil {
		t.Fatal(absErr)
	}
	if statPath != filepath.Clean(abs) {
		t.Fatalf("stat path = %q, want %q", statPath, filepath.Clean(abs))
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("refused Init created %d entries, want none", len(entries))
	}
}

func TestCanonicalEmpty(t *testing.T) {
	if got := Canonical(""); got != "" {
		t.Fatalf("Canonical(\"\") = %q, want \"\"", got)
	}
}

func TestCanonicalNonexistentFallsBackToAbsolute(t *testing.T) {
	rel := filepath.Join("definitely", "nonexistent", "path")
	got := Canonical(rel)
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	if got != abs {
		t.Fatalf("Canonical(%q) = %q, want absolute fallback %q", rel, got, abs)
	}
}

func TestCanonicalResolvesSymlink(t *testing.T) {
	target := t.TempDir()
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	got := Canonical(link)
	want := Canonical(target)
	if got != want {
		t.Fatalf("Canonical(%q) = %q, want resolved %q", link, got, want)
	}
}

func TestCanonicalIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first := Canonical(dir)
	second := Canonical(first)
	if first != second {
		t.Fatalf("Canonical not idempotent: %q != %q", first, second)
	}
}
