package hometag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTagDeterministic(t *testing.T) {
	a := Tag("/home/user/.munsu")
	b := Tag("/home/user/.munsu")
	if a != b {
		t.Errorf("same input -> different tags: %q vs %q", a, b)
	}
}

func TestTagDifferent(t *testing.T) {
	a := Tag("/home/user/.munsu")
	b := Tag("/home/other/.munsu")
	if a == b {
		t.Errorf("different inputs -> same tag: %q", a)
	}
}

func TestTagLength(t *testing.T) {
	tag := Tag("/some/path")
	if len(tag) != 6 {
		t.Errorf("tag length = %d, want 6", len(tag))
	}
}

func TestTagHexChars(t *testing.T) {
	tag := Tag("/another/path")
	for _, c := range tag {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in tag %q", c, tag)
		}
	}
}

func TestTagCanonicalizesExistingPath(t *testing.T) {
	homeDir := t.TempDir()
	if got, want := Tag(homeDir), Tag(Canonical(homeDir)); got != want {
		t.Fatalf("Tag() = %q, want canonical %q", got, want)
	}
}

func TestWorkspaceTagPrimaryKeepsLegacyTag(t *testing.T) {
	homeDir := t.TempDir()
	if got, want := WorkspaceTag(homeDir), Tag(homeDir); got != want {
		t.Fatalf("WorkspaceTag() = %q, want legacy primary tag %q", got, want)
	}
}

func TestWorkspaceTagCaptainIsReadableAndScoped(t *testing.T) {
	homeDir := t.TempDir()
	marker := "munsu-v2\nAPI Supervisor\n" + homeDir + "\n"
	if err := os.WriteFile(filepath.Join(homeDir, ".munsu-captain-home"), []byte(marker), 0644); err != nil {
		t.Fatal(err)
	}
	got := WorkspaceTag(homeDir)
	wantPrefix := "captain-api-supervisor-"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("WorkspaceTag() = %q, want prefix %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, Tag(homeDir)) {
		t.Fatalf("WorkspaceTag() = %q, want hash suffix %q", got, Tag(homeDir))
	}
}

func TestWorkspaceTagIgnoresMalformedCaptainMarker(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeDir, ".munsu-captain-home"), []byte("broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, want := WorkspaceTag(homeDir), Tag(homeDir); got != want {
		t.Fatalf("WorkspaceTag() = %q, want legacy tag %q for malformed marker", got, want)
	}
}

func TestWorkspaceTagCollapsesTmpAliases(t *testing.T) {
	base := t.TempDir()
	// On macOS /tmp is typically a symlink to /private/tmp. Mirror that shape
	// with an explicit symlink so the test is deterministic on Linux too.
	alias := filepath.Join(base, "alias")
	realHome := filepath.Join(base, "real")
	if err := os.MkdirAll(realHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, alias); err != nil {
		t.Fatal(err)
	}
	marker := "munsu-v2\napi-sup\n" + realHome + "\n"
	if err := os.WriteFile(filepath.Join(realHome, ".munsu-captain-home"), []byte(marker), 0644); err != nil {
		t.Fatal(err)
	}
	if got, want := WorkspaceTag(alias), WorkspaceTag(realHome); got != want {
		t.Fatalf("WorkspaceTag(alias)=%q WorkspaceTag(real)=%q", got, want)
	}
}
