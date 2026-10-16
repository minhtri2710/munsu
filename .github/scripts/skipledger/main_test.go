package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lane = `{"Action":"run","Package":"m/internal/a","Test":"TestNeedsTmux"}
{"Action":"output","Package":"m/internal/a","Test":"TestNeedsTmux","Output":"    session_test.go:7: tmux not on PATH\n"}
{"Action":"skip","Package":"m/internal/a","Test":"TestNeedsTmux","Elapsed":0}
{"Action":"pass","Package":"m/internal/a","Test":"TestRuns"}
{"Action":"skip","Package":"m/internal/a"}
{"Action":"skip","Package":"m/internal/a","Test":"TestParent/sub"}
{"Action":"fail","Package":"m/internal/b","Test":"TestBroken"}
`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSkipsRecordsTestSkipsWithTheirMessage(t *testing.T) {
	skips := map[key]*skip{}
	if err := readSkips(strings.NewReader(lane), "integration", skips); err != nil {
		t.Fatal(err)
	}
	if len(skips) != 2 {
		t.Fatalf("want 2 test skips, got %d: %v", len(skips), skips)
	}
	got := skips[key{"m/internal/a", "TestNeedsTmux"}]
	if got == nil || got.reason != "tmux not on PATH" || !got.lanes["integration"] {
		t.Fatalf("TestNeedsTmux: got %+v", got)
	}
	if sub := skips[key{"m/internal/a", "TestParent/sub"}]; sub == nil || sub.reason != "(no message)" {
		t.Fatalf("subtest without a message: got %+v", sub)
	}
}

func TestReadSkipsRejectsMalformedJSON(t *testing.T) {
	err := readSkips(strings.NewReader(`{"Action":"skip"`), "default", map[key]*skip{})
	if err == nil {
		t.Fatal("truncated stream read without error: a lane that observed nothing would read as clean")
	}
}

func TestReadAllowKeepsReasonsAndRefusesMalformedRows(t *testing.T) {
	ok := writeFile(t, "ok.allow", "# header\n\nm/a TestX  a reason with spaces\n")
	rows, err := readAllow(ok)
	if err != nil {
		t.Fatal(err)
	}
	if r := rows[key{"m/a", "TestX"}]; r.reason != "a reason with spaces" {
		t.Fatalf("reason: got %q", r.reason)
	}

	for name, content := range map[string]string{
		"no reason": "m/a TestX\n",
		"duplicate": "m/a TestX r\nm/a TestX r\n",
	} {
		if _, err := readAllow(writeFile(t, name+".allow", content)); err == nil {
			t.Errorf("%s: read without error", name)
		}
	}
}

func TestCheckFailsOnEitherDirectionAndPassesAFullyListedLane(t *testing.T) {
	listed := "m/internal/a TestNeedsTmux listed\nm/internal/a TestParent/sub listed\n"
	for _, tc := range []struct {
		name, allow string
		wantErr     bool
		want        []string
	}{
		{"unlisted skip fails", "m/internal/a TestNeedsTmux listed\n", true, []string{
			"unlisted skips (no row in skips.allow): 1",
			"  m/internal/a TestParent/sub [integration] (no message)",
			"stale rows (skipped by no lane): 0",
		}},
		{"stale row fails", listed + "m/internal/a TestGone no longer skipped\n", true, []string{
			"unlisted skips (no row in skips.allow): 0",
			"stale rows (skipped by no lane): 1",
			"  m/internal/a TestGone no longer skipped",
		}},
		{"fully listed passes", listed, false, []string{
			"every skip is listed and every row is skipped",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run([]string{"check", writeFile(t, "skips.allow", tc.allow), writeFile(t, "integration.json", lane)}, &out)
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "1 finding(s) against skips.allow")) {
				t.Fatalf("want the findings to fail the run, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("clean ledger failed the run: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("report missing %q:\n%s", w, out.String())
				}
			}
			if strings.Contains(out.String(), "  m/internal/a TestNeedsTmux [") {
				t.Errorf("a listed skip reported as unlisted:\n%s", out.String())
			}
		})
	}
}

func TestRunRefusesMissingArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"check"}, {"check", "skips.allow"}, {"bogus", "x"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("run(%q) returned no error", args)
		}
	}
}
