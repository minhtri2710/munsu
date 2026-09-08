package orchestrator

import (
	"os"
	"strings"
	"testing"
)

// TestObligations_RoundTripKeyedAndKeyless is the core oracle for the single
// canonical format: an obligation with a key and one without must both survive
// a save/load round-trip with every field intact, including an empty Key. On
// the pre-cut writer the keyless line was 5 fields and the reader recovered it
// only via a space heuristic; this asserts the shape is now unambiguous.
func TestObligations_RoundTripKeyedAndKeyless(t *testing.T) {
	home := t.TempDir()
	want := []Obligation{
		{Kind: ReportRelay, State: StateOpen, Key: "terminal-1", Detail: "relay material terminal report for task/key", CreatedAt: 111, ClosedAt: 0},
		{Kind: Cleanup, State: StateClosed, Key: "", Detail: "clean temp artifacts, stale turnend markers, closed-key hygiene", CreatedAt: 222, ClosedAt: 333},
	}
	if err := SaveTaskObligations(home, "task-a", want); err != nil {
		t.Fatalf("SaveTaskObligations: %v", err)
	}
	got, err := LoadTaskObligations(home, "task-a")
	if err != nil {
		t.Fatalf("LoadTaskObligations: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d obligations, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("obligation %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestObligations_KeylessWritesSixFields pins the on-disk contract: a keyless
// obligation still serializes as six tab fields with an empty key column, not
// the pre-cut five-field line.
func TestObligations_KeylessWritesSixFields(t *testing.T) {
	home := t.TempDir()
	if err := SaveTaskObligations(home, "task-a", []Obligation{
		{Kind: Cleanup, State: StateOpen, Key: "", Detail: "clean", CreatedAt: 1, ClosedAt: 0},
	}); err != nil {
		t.Fatalf("SaveTaskObligations: %v", err)
	}
	p, err := taskObligationsPath(home, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimRight(string(b), "\n")
	fields := strings.Split(line, "\t")
	if len(fields) != 6 {
		t.Fatalf("line has %d fields, want 6: %q", len(fields), line)
	}
	if fields[2] != "" {
		t.Errorf("key field = %q, want empty", fields[2])
	}
}

// writeRawObligations writes literal bytes to a task's obligations file so the
// parser's rejection of malformed records is observable through the public load
// path.
func writeRawObligations(t *testing.T, home, taskID, content string) {
	t.Helper()
	// Seed the obligations directory through the real writer, then overwrite
	// the file with the literal record under test.
	if err := SaveTaskObligations(home, taskID, nil); err != nil {
		t.Fatalf("seeding obligations dir: %v", err)
	}
	p, err := taskObligationsPath(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestParseObligations_RejectsMalformed asserts the parser fails the load,
// rather than silently skipping or zero-filling, for each malformed record.
// Every case is green on the pre-cut parser (short lines were skipped, kind and
// state were force-cast, and parseInt swallowed its error), so these fail
// before the fix and pass after it.
func TestParseObligations_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"too few fields":  "cleanup\topen\tclean temp artifacts, stale markers\t1\t0\n",
		"too many fields": "cleanup\topen\t\tclean\t1\t0\textra\n",
		"unknown kind":    "bogus\topen\t\tclean\t1\t0\n",
		"unknown state":   "cleanup\tpending\t\tclean\t1\t0\n",
		"bad created_at":  "cleanup\topen\t\tclean\tnotanumber\t0\n",
		"bad closed_at":   "cleanup\topen\t\tclean\t1\tnope\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeRawObligations(t, home, "task-a", content)
			if _, err := LoadTaskObligations(home, "task-a"); err == nil {
				t.Errorf("LoadTaskObligations on %q = nil error, want a parse error", content)
			}
		})
	}
}
