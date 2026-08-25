package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasReportEvidence(t *testing.T) {
	dir := t.TempDir()

	if HasReportEvidence(filepath.Join(dir, "missing")) != true {
		t.Fatal("a directory that cannot be read must be reported as holding evidence")
	}
	if HasReportEvidence(dir) {
		t.Fatal("an empty data directory holds no report evidence")
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	if HasReportEvidence(dir) {
		t.Fatal("a brief is input, not report evidence")
	}
	if err := os.WriteFile(filepath.Join(dir, "report-g7.md"), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	if !HasReportEvidence(dir) {
		t.Fatal("a retired generation's archived report is evidence")
	}
}
