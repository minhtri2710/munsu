package orchestrator

import (
	"testing"
	"time"
)

func TestWedgeDetectorMissingBeatStartGrace(t *testing.T) {
	w := NewWedgeDetector(t.TempDir())
	w.SetStaleThreshold(time.Minute)
	if alarm := w.Check(w.createdAt.Add(time.Minute - time.Nanosecond)); alarm != nil {
		t.Fatalf("missing beat before grace = %+v, want nil", alarm)
	}
	alarm := w.Check(w.createdAt.Add(time.Minute))
	if alarm == nil {
		t.Fatal("missing beat after grace must raise an alarm")
	}
	if alarm.Reason != "watcher beat never set" {
		t.Errorf("alarm reason = %q, want 'watcher beat never set'", alarm.Reason)
	}
}

func TestWedgeDetectorRepeatedWakeDuringMissingBeatGrace(t *testing.T) {
	w := NewWedgeDetector(t.TempDir())
	w.SetStaleThreshold(time.Minute)
	for i := 0; i < defaultWakeCountMax; i++ {
		w.FeedWake("same-key")
	}

	alarm := w.Check(w.createdAt.Add(time.Minute - time.Nanosecond))
	if alarm == nil {
		t.Fatal("repeated wake during missing-beat grace must raise an alarm")
	}
	if alarm.WakeKey != "same-key" {
		t.Errorf("alarm wake key = %q, want same-key", alarm.WakeKey)
	}
}
