package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitRefusesStatErrors(t *testing.T) {
	// A NUL byte makes a root unusable everywhere, but not at the same stage:
	// POSIX carries it as far as the stat and reports "home: stat root", while
	// Windows rejects it during path resolution and reports "home: resolve
	// root". Pinning either string asserts which branch happened to fire, not
	// the contract. The contract is that an unusable root is refused and
	// nothing is left behind for it, so that is what is asserted — on both
	// platforms, and including the side-effect check the stage assertion never
	// made.
	parent := t.TempDir()
	_, err := Init(filepath.Join(parent, "bad\x00root"))
	if err == nil {
		t.Fatal("Init accepted a root containing a NUL byte")
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
