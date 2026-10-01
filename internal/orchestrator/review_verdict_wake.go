package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/home"
)

// ReviewVerdictTarget names one working review task generation whose verdict
// file the watcher waits for.
type ReviewVerdictTarget struct {
	TaskID     string `json:"taskId"`
	Generation uint64 `json:"generation"`
}

// ReviewVerdictPort is the Fleet side of the review-verdict process event
// (ADR-0025). WorkingReviews lists the targets. ObserveReviewVerdict is the
// resolver: it reports whether the generation's verdict file exists and returns
// its digest; it writes nothing. RecordReviewVerdict is the action and the same
// record step `delivery record-verdict --reviewer-task` runs; it reads the file,
// refuses one that does not speak for that task, generation and reviewed head,
// and records the verdict, returning a one-line summary. digest pins the file
// content the resolver saw.
type ReviewVerdictPort interface {
	WorkingReviews(homeDir string) ([]ReviewVerdictTarget, error)
	ObserveReviewVerdict(homeDir, taskID string, generation uint64) (bool, []byte, error)
	RecordReviewVerdict(homeDir, taskID string, generation uint64, digest string) (string, error)
}

// reviewVerdictEventPrefix keys the review-verdict process event of one review
// task generation; the event payload is the JSON-encoded ReviewVerdictTarget.
const reviewVerdictEventPrefix = "review-verdict:"

func reviewVerdictEventID(taskID string, generation uint64) string {
	return fmt.Sprintf("%s%s:%d", reviewVerdictEventPrefix, taskID, generation)
}

// registerReviewVerdictEvents registers and evaluates the review-verdict event
// of every working review task. Registration is idempotent per (review task,
// generation): the event id carries both, and a task generation's event is
// registered only when it has no record, so an unacked event is left as it is
// and an acked one is never registered again. A file written after the ack is
// therefore not recorded; the manual `delivery record-verdict --reviewer-task`
// is the path for that. Failures are logged and never fail the cycle.
func registerReviewVerdictEvents(homeDir string, reviews ReviewVerdictPort) {
	if reviews == nil {
		return
	}
	targets, err := reviews.WorkingReviews(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "review-verdict: listing review tasks: %v\n", err)
		return
	}
	for _, target := range targets {
		eventID := reviewVerdictEventID(target.TaskID, target.Generation)
		rec, err := readProcessEventRecord(homeDir, eventID)
		if err == nil && rec == nil {
			payload, _ := json.Marshal(target)
			_, err = RegisterProcessEvent(homeDir, eventID, string(payload))
		}
		if err == nil && (rec == nil || rec.Generation != rec.AckedGeneration) {
			err = EvaluateProcessEvent(context.Background(), homeDir, eventID, func(context.Context) (bool, []byte, error) {
				return reviews.ObserveReviewVerdict(homeDir, target.TaskID, target.Generation)
			})
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "review-verdict event %q: %v\n", eventID, err)
		}
	}
}

// recordReviewVerdictWake is the review-verdict arm of the dispatcher. On a
// valid file it does for the review task what the done path of `munsu report`
// does for a soldier: that path completes only a scout, so the review task is
// not completed, and it sends the uplink report to the parent. The arm adds
// only the verdict record. A file the record step refuses, for any reason, is
// reported once as a failed uplink to the parent, keyed by the file digest so
// a re-entry supersedes instead of duplicating, and then acked: there is no
// retry. An uplink that cannot be written leaves the event unacked, and
// RecoverProcessEvents re-announces it on the next watcher start; the record
// step replaces the verdict, so re-entry is safe.
func recordReviewVerdictWake(homeDir string, reviews ReviewVerdictPort, announced ProcessEventWake, rec *ProcessEventRecord) {
	var target ReviewVerdictTarget
	if err := json.Unmarshal([]byte(rec.Payload), &target); err != nil || announced.EventID != reviewVerdictEventID(target.TaskID, target.Generation) {
		fmt.Fprintf(os.Stderr, "process-event wake %q dropped: not a review-verdict event\n", announced.EventID)
		return
	}
	if reviews == nil {
		fmt.Fprintf(os.Stderr, "process-event wake %q dropped: no review-verdict port\n", announced.EventID)
		return
	}
	digest := string(rec.Result)
	state := "done"
	summary, err := reviews.RecordReviewVerdict(homeDir, target.TaskID, target.Generation, digest)
	if err != nil {
		state, summary = "failed", fmt.Sprintf("review verdict not recorded: %v", err)
	}
	key := reviewVerdictEventPrefix + digest
	if digest == "" {
		key = reviewVerdictEventPrefix + "unreadable"
	}
	if err := reportReviewVerdict(homeDir, target.TaskID, key, state, summary); err != nil && !errors.Is(err, ErrReportDurable) {
		fmt.Fprintf(os.Stderr, "review-verdict uplink for %s failed (re-announced on restart): %v\n", target.TaskID, err)
		return
	}
	if err := AckProcessEvent(homeDir, announced.EventID, announced.Generation); err != nil {
		fmt.Fprintf(os.Stderr, "process-event %q ack failed (re-announced on restart): %v\n", announced.EventID, err)
	}
}

// reportReviewVerdict sends the uplink exactly as the soldier done and failed
// path of `munsu report` does: sender is the task the home hosts, the receiver
// is the home's own identity, and the notification is left queued.
func reportReviewVerdict(homeDir, taskID, key, state, message string) error {
	receiverID, receiverRank, err := ReadHomeIdentity(homeDir)
	if err != nil {
		return fmt.Errorf("deriving receiver identity: %w", err)
	}
	_, err = Report(ReportRequest{
		SenderHome: homeDir, ReceiverHome: homeDir,
		SenderRank: Rank("soldier"), SenderIdentity: home.ReceiverIDForTask(taskID),
		ReceiverRank: receiverRank, ReceiverID: receiverID,
		TaskID: taskID, Key: key, State: state, Message: message,
		Notify: func(NotificationRef) UplinkNotifyResult { return QueuedNotification() },
	})
	return err
}
