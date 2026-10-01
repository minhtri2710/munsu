package domain

import "testing"

func TestFoldOpenDecisions(t *testing.T) {
	got := FoldOpenDecisions([]string{"needs-decision[key=x]: choose", "resolved[key=x]: done"})
	if len(got) != 0 {
		t.Fatalf("got=%v", got)
	}
}
func TestFoldOpenActivities(t *testing.T) {
	got := FoldOpenActivities([]string{"working[key=x]: work", "done[key=x]: done"})
	if len(got) != 0 {
		t.Fatalf("got=%v", got)
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
		{"working [key=phase1]: Phase 1", "working"},
		{"needs-decision [key=approach]: Pick approach", "needs-decision"},
		{"blocked: waiting", "blocked"},
		{"resolved [key=approach]: Chose React", "resolved"},
		{"paused: waiting", "paused"},
		{"  working [key=x]: note  ", "working"},
		{"no colon here ", "no colon here"},
	}
	for _, tt := range tests {
		got := LineVerb(tt.line)
		if got != tt.want {
			t.Errorf("LineVerb(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}
