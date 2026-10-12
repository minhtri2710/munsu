package home

import (
	"os"
	"slices"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// The report mark rides after the key, where the status-line parsers already
// stop reading: the verb, the key and the message of a report line are those
// of the same line unmarked.
func TestReportMarkIsInvisibleToStatusParsers(t *testing.T) {
	for _, tc := range []struct {
		name               string
		line               string
		report             bool
		verb, message, key string
	}{
		{"report line", domain.ReportStatusLine("needs-decision", "pick a db", "db"), true, "needs-decision", "needs-decision: pick a db", "db"},
		{"unmarked line", "needs-decision: pick a db [key=db]", false, "needs-decision", "needs-decision: pick a db", "db"},
		{"mark with no key", "needs-decision: pick a db [report]", false, "needs-decision", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.IsReportLine(tc.line); got != tc.report {
				t.Errorf("IsReportLine(%q) = %v, want %v", tc.line, got, tc.report)
			}
			if got := domain.LineVerb(tc.line); got != tc.verb {
				t.Errorf("LineVerb(%q) = %q, want %q", tc.line, got, tc.verb)
			}
			if tc.key == "" {
				return
			}
			if message, key := ParseStatusKey(tc.line); message != tc.message || key != tc.key {
				t.Errorf("ParseStatusKey(%q) = %q, %q, want %q, %q", tc.line, message, key, tc.message, tc.key)
			}
		})
	}
}

func reportWakePayloads(t *testing.T, h string) []string {
	t.Helper()
	queued, err := PeekWakes(h)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, w := range queued {
		if w.Kind == ReportWakeKind {
			out = append(out, w.Payload)
		}
	}
	return out
}

func appendReport(t *testing.T, h, id, state, message string) string {
	t.Helper()
	line := domain.ReportStatusLine(state, message, "default")
	if err := AppendStatus(h, id, line); err != nil {
		t.Fatal(err)
	}
	return line
}

func deriveReports(t *testing.T, h string) {
	t.Helper()
	if err := DeriveReportWakes(h); err != nil {
		t.Fatal(err)
	}
}

// Each material report line derives one report wake, once, whichever of the
// queue's writers reads it first.
func TestDeriveReportWakes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps func(t *testing.T, h string) []string
		want  []string
	}{
		{"material report", func(t *testing.T, h string) []string {
			appendReport(t, h, "t1", "done", "shipped")
			deriveReports(t, h)
			return reportWakePayloads(t, h)
		}, []string{"t1: done: shipped [key=default]"}},
		{"derived twice", func(t *testing.T, h string) []string {
			appendReport(t, h, "t1", "done", "shipped")
			deriveReports(t, h)
			deriveReports(t, h)
			return reportWakePayloads(t, h)
		}, []string{"t1: done: shipped [key=default]"}},
		{"unmarked material line", func(t *testing.T, h string) []string {
			if err := AppendStatus(h, "t1", "done: shipped [key=default]"); err != nil {
				t.Fatal(err)
			}
			deriveReports(t, h)
			return reportWakePayloads(t, h)
		}, nil},
		{"non-material report", func(t *testing.T, h string) []string {
			appendReport(t, h, "t1", "working", "building")
			deriveReports(t, h)
			return reportWakePayloads(t, h)
		}, nil},
		{"rewritten history", func(t *testing.T, h string) []string {
			appendReport(t, h, "t1", "blocked", "one")
			deriveReports(t, h)
			p, err := StatusFilePath(h, "t1")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			appendReport(t, h, "t1", "blocked", "two")
			appendReport(t, h, "t1", "failed", "three")
			deriveReports(t, h)
			return reportWakePayloads(t, h)
		}, []string{"t1: blocked: one [key=default]", "t1: failed: three [key=default]"}},
		{"claim with no watcher", func(t *testing.T, h string) []string {
			appendReport(t, h, "t1", "failed", "red")
			claim, err := ClaimWakes(h, "general", 60, 10)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, w := range claim.Wakes {
				got = append(got, w.Payload)
			}
			return append(got, reportWakePayloads(t, h)...)
		}, []string{"t1: failed: red [key=default]"}},
		{"AFK drain with no watcher", func(t *testing.T, h string) []string {
			appendReport(t, h, "t1", "failed", "red")
			records, err := DrainWakesExcludingKind(h, "process-event")
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range records {
				got = append(got, r.Payload)
			}
			return append(got, reportWakePayloads(t, h)...)
		}, []string{"t1: failed: red [key=default]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.steps(t, t.TempDir()); !slices.Equal(got, tc.want) {
				t.Fatalf("report wakes = %q, want %q", got, tc.want)
			}
		})
	}
}

// A report holds its task until its wake is settled, and a guard with no
// watcher sees it queued before anything derives it.
func TestUnsettledReports(t *testing.T) {
	for _, tc := range []struct {
		name   string
		steps  func(t *testing.T, h string)
		want   int
		queued bool
	}{
		{"underived report", func(t *testing.T, h string) {
			appendReport(t, h, "t1", "done", "shipped")
		}, 1, true},
		{"derived report", func(t *testing.T, h string) {
			appendReport(t, h, "t1", "done", "shipped")
			deriveReports(t, h)
			if _, err := DrainWakesOfKind(h, ReportWakeKind); err != nil {
				t.Fatal(err)
			}
		}, 1, false},
		{"settled report", func(t *testing.T, h string) {
			line := appendReport(t, h, "t1", "done", "shipped")
			deriveReports(t, h)
			if err := SettleReport(h, "t1", reportRef(0, line)); err != nil {
				t.Fatal(err)
			}
		}, 0, true},
		{"another report settled", func(t *testing.T, h string) {
			first := appendReport(t, h, "t1", "blocked", "waiting")
			appendReport(t, h, "t1", "done", "shipped")
			deriveReports(t, h)
			if err := SettleReport(h, "t1", reportRef(0, first)); err != nil {
				t.Fatal(err)
			}
		}, 1, true},
		{"unmarked material line", func(t *testing.T, h string) {
			if err := AppendStatus(h, "t1", "done: shipped [key=default]"); err != nil {
				t.Fatal(err)
			}
		}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := t.TempDir()
			tc.steps(t, h)
			got, err := UnsettledReports(h, "t1")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Errorf("UnsettledReports = %q, want %d", got, tc.want)
			}
			if q := HasQueuedWakes(h); q != tc.queued {
				t.Errorf("HasQueuedWakes = %v, want %v", q, tc.queued)
			}
		})
	}
}
