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
		{"generation-scoped report", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, ReportName(7)), []byte("findings"), 0644)
		}, true},
		{"prefix without a generation number", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, reportNamePrefix+"garbage"+reportNameSuffix), []byte("not a report"), 0644)
		}, false},
		{"generation number below the identity floor", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, reportNamePrefix+"0"+reportNameSuffix), []byte("findings"), 0644)
		}, false},
		{"report symlink", func(dir string) error { return os.Symlink("missing", filepath.Join(dir, ReportName(8))) }, true},
		{"report-shaped directory", func(dir string) error { return os.Mkdir(filepath.Join(dir, ReportName(9)), 0755) }, true},
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
