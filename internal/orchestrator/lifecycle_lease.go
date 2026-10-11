package orchestrator

import (
	"fmt"
	"slices"

	"github.com/minhtri2710/munsu/internal/home"
)

type ClaimedWakeRecord = home.ClaimedWakeRecord
type ClaimResult = home.ClaimResult

func ClaimWakes(h, c string, seconds, limit int) (*ClaimResult, error) {
	result, err := home.ClaimWakes(h, c, seconds, limit)
	if err == nil {
		logWakeClaimObservation(result)
	}
	return result, err
}

// UnacceptedReportsHint tells the receiver what to do with the reports a settle
// or an AFK return could not accept.
const UnacceptedReportsHint = "the reports stay pending: run `munsu inbox ack '<ref>'` once the cause is fixed, or `munsu teardown <id> --force` to retire the task past them"

// UnacceptedReport is an uplink report whose envelope could not be accepted when
// the wake or digest entry that named it was settled. The settle itself is never
// refused for it; the report is returned as data so the receiver can retry.
type UnacceptedReport struct {
	Key     string `json:"key"`
	Payload string `json:"payload,omitempty"`
	Ref     string `json:"ref"`
	Reason  string `json:"reason"`
}

func (u UnacceptedReport) String() string {
	if u.Payload == "" {
		return fmt.Sprintf("%s: %s: %s", u.Key, u.Reason, u.Ref)
	}
	return fmt.Sprintf("%s: %s: %s: %s", u.Key, u.Payload, u.Reason, u.Ref)
}

// AckWakes acknowledges claimed wakes. A wake that points at an uplink report
// also accepts that report: the wake is the receiver's acknowledgment, so the
// sender's pending report retires on its next recovery pass without a separate
// inbox ack. The wake is acked whether or not its report could be accepted;
// failed acceptances are returned.
func AckWakes(h, id string, events []string) ([]UnacceptedReport, error) {
	unaccepted, err := acceptUplinkReports(h, id, events)
	if err != nil {
		return nil, err
	}
	return unaccepted, home.AckWakes(h, id, events)
}

// ResolveWake resolves one claimed wake and, like AckWakes, accepts the uplink
// report the wake points at without letting a failed acceptance block the
// resolution.
func ResolveWake(h, leaseID, eventID, summary string) ([]UnacceptedReport, error) {
	leaseID, eventID, summary, err := home.NormalizeWakeResolution(leaseID, eventID, summary)
	if err != nil {
		return nil, err
	}
	unaccepted, err := acceptUplinkReports(h, leaseID, []string{eventID})
	if err != nil {
		return nil, err
	}
	return unaccepted, home.ResolveWake(h, leaseID, eventID, summary)
}

// acceptUplinkReports runs before the wake leaves the lease, so a crash between
// the two steps replays the idempotent ack instead of stranding the report. A
// lease that is already gone holds nothing to accept; the wake mutation reports
// that case. An acceptance failure is data, never an error.
func acceptUplinkReports(h, leaseID string, eventIDs []string) ([]UnacceptedReport, error) {
	wakes, err := home.LeaseWakes(h, leaseID)
	if err != nil {
		return nil, err
	}
	var unaccepted []UnacceptedReport
	acceptor := uplinkAcceptor{home: h}
	for _, wake := range wakes {
		if wake.Ref == "" || !slices.Contains(eventIDs, wake.Epoch+":"+wake.Seq) {
			continue
		}
		if err := acceptor.accept(wake.Ref); err != nil {
			unaccepted = append(unaccepted, UnacceptedReport{Key: wake.Key, Payload: wake.Payload, Ref: wake.Ref, Reason: err.Error()})
		}
	}
	return unaccepted, nil
}

// uplinkAcceptor accepts the uplink reports a General (or Captain) home has
// received, by the NotificationRef a wake or digest entry carries. It is the one
// place a wake counts as the receiver's acknowledgment: the wake resolve and ack
// paths and the AFK return all go through it.
type uplinkAcceptor struct {
	home     string
	receiver *Receiver
}

// accept acks the envelope ref names with Receiver.Ack. A superseded report is
// skipped: its replacement has its own wake. Every Ack failure, a conflicting
// outcome included, is returned for the caller to report.
func (a *uplinkAcceptor) accept(rawRef string) error {
	ref, err := ParseNotificationRef(rawRef)
	if err != nil {
		return err
	}
	if NewStore(a.home).IsSuperseded(ref.SenderIdentity, ref.MessageID) {
		return nil
	}
	if a.receiver == nil {
		if a.receiver, err = NewReceiver(a.home); err != nil {
			return fmt.Errorf("creating receiver: %w", err)
		}
	}
	if _, err := a.receiver.Ack(ref); err != nil {
		return fmt.Errorf("accepting uplink report %s: %w", ref.MessageID, err)
	}
	return nil
}
