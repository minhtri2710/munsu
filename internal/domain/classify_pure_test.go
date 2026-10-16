package domain

import (
	"slices"
	"testing"
)

func TestFoldOpenDecisions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  []Decision
	}{
		{"keyed decisions resolve independently", []string{
			ReportStatusLine("needs-decision", "which db?", "db"),
			ReportStatusLine("blocked", "redis?", "cache"),
			"resolved: postgres [key=db]",
		}, []Decision{{Key: "cache", Verb: "blocked", Summary: "redis?"}}},
		{"unkeyed decision resolves default", []string{"needs-decision: choose", "resolved: done"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FoldOpenDecisions(tc.lines); !slices.Equal(got, tc.want) {
				t.Fatalf("FoldOpenDecisions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFoldOpenActivities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  []Activity
	}{
		{"keyed phases close independently", []string{
			ReportStatusLine("working", "phase one", "p1"),
			ReportStatusLine("working", "phase two", "p2"),
			ReportStatusLine("done", "phase one shipped", "p1"),
		}, []Activity{{Key: "p2", Verb: "working", Summary: "phase two"}}},
		{"unkeyed phase is default", []string{"working: work"}, []Activity{{Key: "default", Verb: "working", Summary: "work"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FoldOpenActivities(tc.lines); !slices.Equal(got, tc.want) {
				t.Fatalf("FoldOpenActivities = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyAbsorb(t *testing.T) {
	if ClassifyAbsorb("paused: wait") != Paused {
		t.Fatal("paused")
	}
	if ClassifyAbsorb("working: go") != Working {
		t.Fatal("working")
	}
}

func TestIsValidStatusState(t *testing.T) {
	valid := []string{"working", "review-ready", "amending", "needs-decision", "blocked", "paused", "resolved", "done", "failed"}
	for _, s := range valid {
		if !IsValidStatusState(s) {
			t.Errorf("%q should be a valid status state", s)
		}
	}

	invalid := []string{"", "unknown", "pending", "in-progress", "started"}
	for _, s := range invalid {
		if IsValidStatusState(s) {
			t.Errorf("%q should not be a valid status state", s)
		}
	}
}

func TestValidStatusStates(t *testing.T) {
	expected := []string{
		"working", "review-ready", "amending", "needs-decision", "blocked", "paused",
		"awaiting_approval", "resolved", "done", "failed", "delivered",
	}
	if len(ValidStatusStates) != len(expected) {
		t.Fatalf("ValidStatusStates length = %d, want %d", len(ValidStatusStates), len(expected))
	}
	for i, s := range expected {
		if ValidStatusStates[i] != s {
			t.Errorf("ValidStatusStates[%d] = %q, want %q", i, ValidStatusStates[i], s)
		}
	}
}

func TestIsMaterialVerb(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"done", true},
		{"failed", true},
		{"needs-decision", true},
		{"blocked", true},
		{"working", false},
		{"paused", false},
		{"resolved", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		got := IsMaterialVerb(tt.state)
		if got != tt.want {
			t.Errorf("IsMaterialVerb(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestLineVerb(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"done: task complete", "done"},
		{"failed: something broke", "failed"},
		{"working: Phase 1 [key=phase1]", "working"},
		{"needs-decision: Pick approach [key=approach] [report]", "needs-decision"},
		{"blocked: waiting", "blocked"},
		{"resolved: Chose React [key=approach]", "resolved"},
		{"paused: waiting", "paused"},
		{"  working: note [key=x]  ", "working"},
		{"no colon here ", "no colon here"},
	}
	for _, tt := range tests {
		got := LineVerb(tt.line)
		if got != tt.want {
			t.Errorf("LineVerb(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestGeneralRelevantIsOnlyWhatNeedsTheHuman(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"needs-decision: pick a schema", true},
		{"blocked: missing auth", true},
		{"failed: CI red", true},
		{"blocked: waiting [key=x]", true},
		{"task-2 blocked: missing auth", true},
		{"done: shipped", false},
		{"done: PR ready, checks green, merged", false},
		{"review-ready: PR ready", false},
		{"ready in branch feature", false},
		{"PR merged", false},
		{"working: prose says failed: and blocked:", false},
		{"resolved: was blocked: yesterday", false},
		{"paused: blocked: waiting", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := GeneralRelevant(tc.line); got != tc.want {
			t.Errorf("GeneralRelevant(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestSplitStatusKey(t *testing.T) {
	for _, tc := range []struct {
		line, rest, key string
	}{
		{"working: started", "working: started", ""},
		{"needs-decision: pick approach [key=approach]", "needs-decision: pick approach", "approach"},
		{ReportStatusLine("done", "all done", "task-1"), "done: all done", "task-1"},
		{"needs-decision: see [key=fake] in docs", "needs-decision: see [key=fake] in docs", ""},
		{"key only [key=]", "key only [key=]", ""},
		{"bad key [key=a b]", "bad key [key=a b]", ""},
		{"needs-decision: pick [key=a] ", "needs-decision: pick", "a"},
		{"resolved:[key=other]", "resolved:[key=other]", ""},
	} {
		if rest, key := SplitStatusKey(tc.line); rest != tc.rest || key != tc.key {
			t.Errorf("SplitStatusKey(%q) = %q, %q, want %q, %q", tc.line, rest, key, tc.rest, tc.key)
		}
	}
}
