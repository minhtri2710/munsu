package home

import (
	"testing"
	"time"
)

// A wake's Ref is stored and handed back through every rewrite of the record:
// claim, a partial ack that rewrites the lease, and reclaim of an expired lease.
func TestWakeRefSurvivesClaimPartialAckAndReclaim(t *testing.T) {
	h := t.TempDir()
	const ref = `{"message_id":"m1","sender_identity":"s1"}`
	if err := EnqueueRefWake(h, "uplink", "task-a", ref, "done: a"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueWake(h, "signal", "task-b", "plain"); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimWakes(h, "general", 60, 10)
	if err != nil || len(claim.Wakes) != 2 {
		t.Fatalf("claim = %+v err = %v, want 2 wakes", claim, err)
	}
	var uplink, plain ClaimedWakeRecord
	for _, w := range claim.Wakes {
		if w.Kind == "uplink" {
			uplink = w
		} else {
			plain = w
		}
	}
	if uplink.Ref != ref || plain.Ref != "" {
		t.Fatalf("claimed refs = %q and %q, want %q and empty", uplink.Ref, plain.Ref, ref)
	}

	if err := AckWakes(h, claim.LeaseID, []string{plain.Epoch + ":" + plain.Seq}); err != nil {
		t.Fatal(err)
	}
	held, err := LeaseWakes(h, claim.LeaseID)
	if err != nil || len(held) != 1 || held[0].Ref != ref {
		t.Fatalf("lease after partial ack = %+v err = %v, want the uplink wake with its ref", held, err)
	}

	if n, err := reclaimExpiredLeasesAt(h, func() time.Time { return time.Now().Add(time.Hour) }); err != nil || n != 1 {
		t.Fatalf("reclaim = %d err = %v, want 1", n, err)
	}
	queued, err := PeekWakes(h)
	if err != nil || len(queued) != 1 || queued[0].Ref != ref {
		t.Fatalf("queue after reclaim = %+v err = %v, want the uplink wake with its ref", queued, err)
	}
}
