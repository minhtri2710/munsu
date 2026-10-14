//go:build windows

package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestFormatCaptainStatusLinesRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := home.AppendStatus(tmp, "captain:domain", "working: processing"); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, "captain:domain", "done: phase-1 complete"); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, "captain:infra", "working: healthy"); err != nil {
		t.Fatal(err)
	}
	lines := formatCaptainStatusLines(home.StateDir(tmp))
	if len(lines) != 2 {
		t.Fatalf("formatCaptainStatusLines = %v, want 2 captain status lines", lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"domain", "infra", "done: phase-1 complete", "!"} {
		if !strings.Contains(joined, want) {
			t.Errorf("formatCaptainStatusLines output missing %q (got %q)", want, joined)
		}
	}
	if strings.Contains(joined, "captain%3A") {
		t.Errorf("formatCaptainStatusLines surfaced encoded stem (got %q)", joined)
	}
}
