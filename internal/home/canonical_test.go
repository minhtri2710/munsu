package home

import (
	"os"
	"path/filepath"
	"testing"
)

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
