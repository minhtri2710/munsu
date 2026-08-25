package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRetiredReportRejectsSymlinkReservation(t *testing.T) {
	homeDir := t.TempDir()
	dataDir := filepath.Join(homeDir, "data", "symlink-reservation")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "report.md"), []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dataDir, "empty-target")
	if err := os.WriteFile(target, nil, 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dataDir, "report-g1.md")
	if err := os.Symlink(target, archive); err != nil {
		t.Fatal(err)
	}
	if _, _, err := archiveRetiredReport(homeDir, "symlink-reservation", 1); err == nil {
		t.Fatal("expected symlink reservation conflict")
	}
	archiveInfo, err := os.Lstat(archive)
	if err != nil {
		t.Fatalf("symlink removed: %v", err)
	}
	if archiveInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("archive entry mode = %v, want symlink", archiveInfo.Mode())
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "current" {
		t.Fatalf("report changed to %q", contents)
	}
}

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
