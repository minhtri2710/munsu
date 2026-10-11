package orchestrator

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// reportAndClaim files one direct-dispatch report and claims every queued wake
// in the General home under one lease.
func reportAndClaim(t *testing.T, generalHome, identity string, extra ...func()) (*ReportResult, *ClaimResult) {
	t.Helper()
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
	for _, enqueue := range extra {
		enqueue()
	}
	claim, err := ClaimWakes(generalHome, "general", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	return res, claim
}

func wakeEventID(w ClaimedWakeRecord) string { return w.Epoch + ":" + w.Seq }

func uplinkWake(t *testing.T, claim *ClaimResult) ClaimedWakeRecord {
	t.Helper()
	for _, w := range claim.Wakes {
		if w.Kind == "uplink" {
			return w
		}
	}
	t.Fatalf("no uplink wake in %+v", claim.Wakes)
	return ClaimedWakeRecord{}
}

// settleEntries are the two ways a claimed wake leaves its lease; both accept
// the report the wake points at.
var settleEntries = map[string]func(h, lease, event string) ([]UnacceptedReport, error){
	"resolve": func(h, lease, event string) ([]UnacceptedReport, error) {
		return ResolveWake(h, lease, event, "seen")
	},
	"ack": func(h, lease, event string) ([]UnacceptedReport, error) {
		return AckWakes(h, lease, []string{event})
	},
}

// The wake is the General's acknowledgment: resolving or acking it accepts the
// envelope it points at, the next recovery pass retires the sender's pending
// report, and teardown continuity passes with no inbox ack in between.
func TestResolvingOrAckingAnUplinkWakeAcceptsItsReport(t *testing.T) {
	for name, settle := range settleEntries {
		t.Run(name, func(t *testing.T) {
			generalHome, identity := directGeneralHome(t)
			res, claim := reportAndClaim(t, generalHome, identity)
			wake := uplinkWake(t, claim)
			if err := VerifyRetirementContinuity(generalHome, "direct-task"); err == nil {
				t.Fatal("continuity must refuse while the report is unacknowledged")
			}

			unaccepted, err := settle(generalHome, claim.LeaseID, wakeEventID(wake))
			if err != nil || len(unaccepted) != 0 {
				t.Fatalf("settling the wake: unaccepted = %v err = %v", unaccepted, err)
			}
			ack, err := NewStore(generalHome).ReadAck("direct-task", res.MessageID)
			if err != nil || ack == nil || ack.Outcome != OutcomeAccepted {
				t.Fatalf("ack = %+v err = %v, want an accepted ack for message %s", ack, err, res.MessageID)
			}

			transport := &countingTransport{}
			if err := ReconcileCaptainHook(generalHome, false, transport); err != nil {
				t.Fatalf("ReconcileCaptainHook: %v", err)
			}
			if pending, err := NewStore(generalHome).ListPending("direct-task"); err != nil || len(pending) != 0 {
				t.Fatalf("pending after recovery = %d err = %v, want 0", len(pending), err)
			}
			if err := VerifyRetirementContinuity(generalHome, "direct-task"); err != nil {
				t.Fatalf("continuity after the wake settled: %v", err)
			}
		})
	}
}

// A report that cannot be accepted never blocks its wake. Whatever refuses the
// ack, a conflicting outcome on record or a soldier whose task provenance is
// gone, the wake is settled and the report comes back as data with its ref, for
// resolve and ack alike.
func TestSettlingAWakeWhoseReportCannotBeAcceptedStillSettlesIt(t *testing.T) {
	causes := map[string]struct {
		break_ func(t *testing.T, generalHome string, env *Envelope)
		reason string
	}{
		"conflicting outcome": {func(t *testing.T, generalHome string, env *Envelope) {
			if err := NewStore(generalHome).WriteAck(&ProcessingAck{
				MessageID: env.MessageID, SenderRank: env.SenderRank, SenderIdentity: env.SenderIdentity,
				ReceiverRank: env.ReceiverRank, ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
				PayloadHash: env.PayloadHash, Outcome: OutcomeDone, ProcessedAt: time.Now().UnixNano(),
			}); err != nil {
				t.Fatal(err)
			}
		}, "conflicting"},
		"provenance loss": {func(t *testing.T, generalHome string, _ *Envelope) {
			path, err := home.MetaFilePath(generalHome, "direct-task")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}, "underivable"},
	}
	for cause, c := range causes {
		for name, settle := range settleEntries {
			t.Run(cause+"/"+name, func(t *testing.T) {
				generalHome, identity := directGeneralHome(t)
				res, claim := reportAndClaim(t, generalHome, identity)
				wake := uplinkWake(t, claim)
				env, err := NewStore(generalHome).ReadEnvelope("direct-task", res.MessageID)
				if err != nil || env == nil {
					t.Fatalf("ReadEnvelope: %v", err)
				}
				c.break_(t, generalHome, env)

				unaccepted, err := settle(generalHome, claim.LeaseID, wakeEventID(wake))
				if err != nil {
					t.Fatalf("a report that cannot be accepted must not block its wake: %v", err)
				}
				if len(unaccepted) != 1 || unaccepted[0].Ref != wake.Ref || !strings.Contains(unaccepted[0].Reason, c.reason) {
					t.Fatalf("unaccepted = %+v, want one entry with ref %s and a %q reason", unaccepted, wake.Ref, c.reason)
				}
				held, listErr := ListClaimedWakes(generalHome, time.Now())
				if listErr != nil || len(held) != 0 {
					t.Fatalf("claimed wakes = %d err = %v, want the wake settled", len(held), listErr)
				}
			})
		}
	}
}

// The resolve request is trimmed and validated once, before anything is
// accepted, exactly as the wake mutation does it: padded ids still accept the
// report, and a request the mutation would refuse accepts nothing.
func TestResolveNormalizesBeforeAccepting(t *testing.T) {
	t.Run("padded ids accept", func(t *testing.T) {
		generalHome, identity := directGeneralHome(t)
		res, claim := reportAndClaim(t, generalHome, identity)
		wake := uplinkWake(t, claim)
		if _, err := ResolveWake(generalHome, " "+claim.LeaseID+" ", " "+wakeEventID(wake)+" ", " seen "); err != nil {
			t.Fatalf("ResolveWake with padded ids: %v", err)
		}
		if ack, err := NewStore(generalHome).ReadAck("direct-task", res.MessageID); err != nil || ack == nil {
			t.Fatalf("ack = %+v err = %v, want the padded resolve to accept the report", ack, err)
		}
	})
	refused := map[string]func(lease, event string) (string, string, string){
		"empty summary":  func(lease, event string) (string, string, string) { return lease, event, "  " },
		"empty claim id": func(_, event string) (string, string, string) { return "", event, "seen" },
	}
	for name, request := range refused {
		t.Run(name, func(t *testing.T) {
			generalHome, identity := directGeneralHome(t)
			res, claim := reportAndClaim(t, generalHome, identity)
			lease, event, summary := request(claim.LeaseID, wakeEventID(uplinkWake(t, claim)))

			unaccepted, err := ResolveWake(generalHome, lease, event, summary)
			if err == nil || !strings.Contains(err.Error(), "claim-id, event-id, and summary are required") || len(unaccepted) != 0 {
				t.Fatalf("unaccepted = %v err = %v, want the existing required refusal", unaccepted, err)
			}
			if ack, err := NewStore(generalHome).ReadAck("direct-task", res.MessageID); err != nil || ack != nil {
				t.Fatalf("ack = %+v err = %v, want none from a refused request", ack, err)
			}
			if held, _ := ListClaimedWakes(generalHome, time.Now()); len(held) == 0 {
				t.Fatal("the wake must stay held after a refused request")
			}
		})
	}
}

// Only the named wake is settled. A sibling uplink wake in the same lease keeps
// its report unacknowledged, and a wake with no ref writes no ack at all.
func TestSettlingAWakeAcceptsOnlyTheReportItNames(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res, claim := reportAndClaim(t, generalHome, identity, func() {
		if err := EnqueueWake(generalHome, "signal", "direct-task", "needs-decision: x"); err != nil {
			t.Fatal(err)
		}
	})
	var signal ClaimedWakeRecord
	for _, w := range claim.Wakes {
		if w.Kind == "signal" {
			signal = w
		}
	}
	if signal.Kind == "" {
		t.Fatalf("no signal wake in %+v", claim.Wakes)
	}

	if _, err := ResolveWake(generalHome, claim.LeaseID, wakeEventID(signal), "seen"); err != nil {
		t.Fatalf("ResolveWake: %v", err)
	}
	if ack, err := NewStore(generalHome).ReadAck("direct-task", res.MessageID); err != nil || ack != nil {
		t.Fatalf("ack = %+v err = %v, want none: the uplink wake was not settled", ack, err)
	}
}

// A re-report supersedes the first envelope. Its wake still resolves: the
// superseded report has nothing left to accept.
func TestSettlingTheWakeOfASupersededReportSucceeds(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	first := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
	second := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
	claim, err := ClaimWakes(generalHome, "general", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range claim.Wakes {
		if _, err := ResolveWake(generalHome, claim.LeaseID, wakeEventID(w), "seen"); err != nil {
			t.Fatalf("resolving %s: %v", wakeEventID(w), err)
		}
	}
	if ack, err := NewStore(generalHome).ReadAck("direct-task", second.MessageID); err != nil || ack == nil {
		t.Fatalf("the live report must be accepted: ack = %+v err = %v", ack, err)
	}
	if ack, err := NewStore(generalHome).ReadAck("direct-task", first.MessageID); err != nil || ack != nil {
		t.Fatalf("the superseded report must stay unacked: ack = %+v err = %v", ack, err)
	}
}
