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
