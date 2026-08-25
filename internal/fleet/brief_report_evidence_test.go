package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasReportEvidence(t *testing.T) {
	if HasReportEvidence(filepath.Join(t.TempDir(), "missing")) != true {
		t.Fatal("a directory that cannot be read must be reported as holding evidence")
	}

	tests := []struct {
		name  string
		setup func(string) error
		want  bool
	}{
		{"empty", func(string) error { return nil }, false},
		{"brief", func(dir string) error { return os.WriteFile(filepath.Join(dir, "brief.md"), []byte("brief"), 0644) }, false},
		{"valid archive", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "report-g7.md"), []byte("findings"), 0644)
		}, true},
		{"malformed archive", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "report-garbage.md"), []byte("not a report"), 0644)
		}, false},
		{"dangling archive symlink", func(dir string) error { return os.Symlink("missing", filepath.Join(dir, "report-g8.md")) }, true},
		{"archive-shaped directory", func(dir string) error { return os.Mkdir(filepath.Join(dir, "report-g9.md"), 0755) }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tt.setup(dir); err != nil {
				t.Fatal(err)
			}
			if got := HasReportEvidence(dir); got != tt.want {
				t.Fatalf("HasReportEvidence = %v, want %v", got, tt.want)
			}
		})
	}
}
