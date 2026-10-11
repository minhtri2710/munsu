package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestCanonicalDelegatesToHome(t *testing.T) {
	existing := t.TempDir()
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link")
	if err := os.Symlink(existing, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	cases := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"existing", existing},
		{"nonexistent", filepath.Join("definitely", "gone")},
		{"symlink", link},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := Canonical(tc.path), home.Canonical(tc.path); got != want {
				t.Fatalf("Canonical(%q) = %q, home.Canonical = %q", tc.path, got, want)
			}
		})
	}
}

func TestTagDeterministic(t *testing.T) {
	a := Hometag("/home/user/.munsu")
	b := Hometag("/home/user/.munsu")
	if a != b {
		t.Errorf("same input -> different tags: %q vs %q", a, b)
	}
}

func TestTagDifferent(t *testing.T) {
	a := Hometag("/home/user/.munsu")
	b := Hometag("/home/other/.munsu")
	if a == b {
		t.Errorf("different inputs -> same tag: %q", a)
	}
}

func TestTagLength(t *testing.T) {
	tag := Hometag("/some/path")
	if len(tag) != 6 {
		t.Errorf("tag length = %d, want 6", len(tag))
	}
}

func TestTagHexChars(t *testing.T) {
	tag := Hometag("/another/path")
	for _, c := range tag {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in tag %q", c, tag)
		}
	}
}

func TestTagCanonicalizesExistingPath(t *testing.T) {
	homeDir := t.TempDir()
	if got, want := Hometag(homeDir), Hometag(Canonical(homeDir)); got != want {
		t.Fatalf("Hometag() = %q, want canonical %q", got, want)
	}
}

func TestWorkspaceTagPrimaryKeepsLegacyHometag(t *testing.T) {
	homeDir := t.TempDir()
	if got, want := WorkspaceTag(homeDir), Hometag(homeDir); got != want {
		t.Fatalf("WorkspaceTag() = %q, want legacy primary tag %q", got, want)
	}
}

// TestWorkspaceTagMarkerShapes pins the label for every shape of captain
// marker: only a marker ValidateCaptainProvenance reads as naming a captain, or
// one naming another home (a copied captain), changes the hash-only label.
func TestWorkspaceTagMarkerShapes(t *testing.T) {
	tests := []struct {
		name   string
		marker func(home string) string // "" writes no marker
		prefix string                   // "" keeps the hash-only label
	}{
		{"valid", func(h string) string { return "munsu-v2\nAPI Supervisor\n" + h + "\n" }, "captain-api-supervisor-"},
		{"copied home", func(h string) string { return "munsu-v2\napi-sup\n/elsewhere/home\n" }, "captain-api-sup-"},
		{"two lines", func(h string) string { return "munsu-v2\napi-sup\n" }, ""},
		{"four lines", func(h string) string { return "munsu-v2\napi-sup\n" + h + "\nextra\n" }, ""},
		{"wrong version", func(h string) string { return "munsu-v1\napi-sup\n" + h + "\n" }, ""},
		{"blank id", func(h string) string { return "munsu-v2\n  \n" + h + "\n" }, ""},
		{"id with no label characters", func(h string) string { return "munsu-v2\n!!!\n" + h + "\n" }, ""},
		{"malformed", func(h string) string { return "broken\n" }, ""},
		{"no marker", func(h string) string { return "" }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			if marker := tt.marker(Canonical(homeDir)); marker != "" {
				if err := os.WriteFile(filepath.Join(homeDir, ".munsu-captain-home"), []byte(marker), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want := Hometag(homeDir)
			if tt.prefix != "" {
				want = tt.prefix + want
			}
			if got := WorkspaceTag(homeDir); got != want {
				t.Fatalf("WorkspaceTag() = %q, want %q", got, want)
			}
		})
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
