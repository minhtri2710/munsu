//go:build windows

package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestScanBoundEndpointsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if err := home.WriteMeta(tmp, "ship:1", map[string]string{"kind": "ship", "window": "@win", "backend": "herdr"}); err != nil {
		t.Fatal(err)
	}
	endpoints, err := scanBoundEndpoints(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("scanBoundEndpoints = %+v, want 1 bound endpoint", endpoints)
	}
	if endpoints[0].handle != "@win" || endpoints[0].backend != "herdr" {
		t.Errorf("scanBoundEndpoints[0] = %+v, want handle @win backend herdr", endpoints[0])
	}
}

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
