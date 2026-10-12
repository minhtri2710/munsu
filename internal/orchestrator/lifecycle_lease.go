package orchestrator

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

// UnacceptedReportsHint tells the receiver what to do with a report a settle
// or an AFK return could not finish.
const UnacceptedReportsHint = "the report is settled, but completing its scout task failed: run `munsu task done <id>` once the cause is fixed"

// UnacceptedReport is a report whose settlement could not finish its work when
// the wake or digest entry that carried it was settled. The settle itself is
// never refused for it; the report is returned as data so the receiver can
// finish by hand.
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

// AckWakes acknowledges claimed wakes. A report wake settles its report first:
// the wake is the receiver's acknowledgment. The wake is acked whether or not
// its report's work finished; failures are returned.
func AckWakes(h, id string, events []string) ([]UnacceptedReport, error) {
	unaccepted, err := settleReportWakes(h, id, events)
	if err != nil {
		return nil, err
	}
	return unaccepted, home.AckWakes(h, id, events)
}

// ResolveWake resolves one claimed wake and, like AckWakes, settles the report
// a report wake carries without letting a failure block the resolution.
func ResolveWake(h, leaseID, eventID, summary string) ([]UnacceptedReport, error) {
	leaseID, eventID, summary, err := home.NormalizeWakeResolution(leaseID, eventID, summary)
	if err != nil {
		return nil, err
	}
	unaccepted, err := settleReportWakes(h, leaseID, []string{eventID})
	if err != nil {
		return nil, err
	}
	return unaccepted, home.ResolveWake(h, leaseID, eventID, summary)
}

// settleReportWakes runs before the wake leaves the lease, so a crash between
// the two steps settles the report again instead of stranding it. A lease that
// is already gone holds nothing to settle; the wake mutation reports that case.
func settleReportWakes(h, leaseID string, eventIDs []string) ([]UnacceptedReport, error) {
	wakes, err := home.LeaseWakes(h, leaseID)
	if err != nil {
		return nil, err
	}
	var unaccepted []UnacceptedReport
	for _, wake := range wakes {
		if wake.Kind != home.ReportWakeKind || !slices.Contains(eventIDs, wake.Epoch+":"+wake.Seq) {
			continue
		}
		if u := settleReport(h, wake.Key, wake.Payload, wake.Ref); u != nil {
			unaccepted = append(unaccepted, *u)
		}
	}
	return unaccepted, nil
}

// settleReport settles one report its parent received. A scout's done report
// completes the scout's task; then the report stops holding the task's
// teardown. It is the one place a report is settled: the wake resolve and ack
// paths and the AFK return all go through it.
func settleReport(h, taskID, payload, ref string) *UnacceptedReport {
	var reasons []string
	if err := completeScoutOnReport(h, taskID, payload); err != nil {
		reasons = append(reasons, "completing scout task: "+err.Error())
	}
	if err := home.SettleReport(h, taskID, ref); err != nil {
		reasons = append(reasons, "settling report: "+err.Error())
	}
	if len(reasons) == 0 {
		return nil
	}
	return &UnacceptedReport{Key: taskID, Payload: payload, Ref: ref, Reason: strings.Join(reasons, "; ")}
}

// completeScoutOnReport commits a scout's completion when its parent settles
// the scout's done report. Any other report, a task munsu does not track, and a
// scout already terminal complete nothing.
func completeScoutOnReport(h, taskID, payload string) error {
	if domain.LineVerb(strings.TrimPrefix(payload, taskID+": ")) != "done" {
		return nil
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return nil
	}
	hm, err := home.Open(h)
	if errors.Is(err, home.ErrNotInitialized) {
		return nil
	}
	if err != nil {
		return err
	}
	auth, err := taskauthority.NewCanonical(hm)
	if err != nil {
		return err
	}
	agg, err := auth.Get(tid)
	if errors.Is(err, taskauthority.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if agg.Definition.Kind != taskauthority.KindScout || agg.Phase == taskauthority.PhaseDone || agg.Phase == taskauthority.PhaseResolved || agg.Phase == taskauthority.PhaseRetired {
		return nil
	}
	req := taskauthority.CanonicalCompleteRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		To:           taskauthority.PhaseDone, Reason: "report: scout done",
	}
	var nonce [12]byte
	rand.Read(nonce[:])
	opID, err := domain.NewOperationID(fmt.Sprintf("report-scout-done-%x", nonce[:]))
	if err != nil {
		return err
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		return err
	}
	if _, err := auth.Complete(op, req); err != nil {
		return err
	}
	// The .meta mtime is the staleness witness of the task's check artifacts.
	return home.UpdateMeta(h, taskID, func(map[string]string) error { return nil })
}
