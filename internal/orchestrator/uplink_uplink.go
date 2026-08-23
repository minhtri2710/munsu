// Package uplink implements durable one-hop material reports.
package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

const retryInterval = 60 * time.Second

// ErrReportDurable marks a Report failure that happens after the full durable
// record set -- envelope, pending record, open evidence, receiver wake -- has
// committed. Three paths carry it: retiring superseded reports, recording the
// notification attempt, and the notification itself. The report exists, so none
// of them mean "the report did not happen", and re-running report supersedes
// this record rather than adding a second one.
//
// It says nothing about whether the receiver was notified: the notify path may
// have succeeded and the failure landed after it. Of the three, only the notify
// path is reachable from a test today -- the other two need a durable-write
// failure this package has no fault injection for, so they are checked by
// inspection, not by a lane. It is deliberately NOT carried
// by failures inside the durable sequence itself (pending, evidence, wake),
// because supersededReports derives what to retire from the sender's pending
// records -- with that record missing a re-report supersedes nothing and leaves
// two live envelopes in the receiver's inbox, so the advice above would be false.
var ErrReportDurable = errors.New("uplink report: durable")

var materialStates = map[string]bool{
	"done": true, "failed": true, "blocked": true, "needs-decision": true,
}

type UplinkNotifyResult struct {
	Acknowledged bool
	Queued       bool
	// Err carries a notification path whose transport was invoked and failed.
	// A target that cannot be resolved is merely unavailable and remains queued.
	Err error
}

type ReportRequest struct {
	SenderHome, ReceiverHome    string
	SenderIdentity, ReceiverID  string
	SenderRank, ReceiverRank    Rank
	TaskID, Key, State, Message string
	Notify                      func(NotificationRef) UplinkNotifyResult
}

type ReportResult struct {
	MessageID string
	Notified  bool
	Queued    bool
}

type RecoverRequest struct {
	SenderHome, ReceiverHome string
	SenderIdentity           string
	ReceiverRank             Rank
	ForceNotify              bool
	Now                      time.Time
	Notify                   func(NotificationRef) UplinkNotifyResult
}

type RecoverResult struct {
	Accepted int
	Notified int
	Queued   int
}

type evidence struct {
	MessageID string `json:"message_id"`
	TaskID    string `json:"task_id"`
	Key       string `json:"key"`
	State     string `json:"state"`
	At        int64  `json:"at"`
}

func Report(req ReportRequest) (*ReportResult, error) {
	if req.SenderHome == "" || req.ReceiverHome == "" || req.SenderIdentity == "" || req.ReceiverID == "" || req.TaskID == "" || req.Message == "" {
		return nil, fmt.Errorf("uplink report: required identity, home, task, or message is empty")
	}
	if !materialStates[req.State] {
		return nil, fmt.Errorf("uplink report: state %q is not material", req.State)
	}
	if req.Key == "" {
		req.Key = "default"
	}
	if _, homeRank, err := ReadHomeIdentity(req.ReceiverHome); err != nil {
		return nil, fmt.Errorf("uplink report: reading receiver home rank: %w", err)
	} else if req.ReceiverRank != homeRank {
		return nil, fmt.Errorf("uplink report: receiver rank %q cannot be satisfied by receiving home %s of rank %q", req.ReceiverRank, req.ReceiverHome, homeRank)
	}
	oldReports, err := supersededReports(req)
	if err != nil {
		return nil, err
	}

	payload := fmt.Sprintf("%s: %s [task=%s key=%s]", req.State, req.Message, req.TaskID, req.Key)
	env := &Envelope{
		SenderRank: req.SenderRank, SenderIdentity: req.SenderIdentity,
		ReceiverRank: req.ReceiverRank, ReceiverID: req.ReceiverID,
		Kind: "uplink-report", TaskID: req.TaskID, Key: req.Key, Payload: payload,
	}
	if err := NewStore(req.ReceiverHome).WriteEnvelope(env); err != nil {
		return nil, fmt.Errorf("uplink report: write envelope: %w", err)
	}
	if err := NewStore(req.SenderHome).WritePending(env); err != nil {
		return nil, fmt.Errorf("uplink report: write pending: %w", err)
	}
	if err := writeEvidence(openEvidencePath(req.SenderHome, req.TaskID, req.Key), evidence{MessageID: env.MessageID, TaskID: req.TaskID, Key: req.Key, State: req.State, At: time.Now().UnixNano()}); err != nil {
		return nil, fmt.Errorf("uplink report: write open evidence: %w", err)
	}
	if err := EnqueueWake(req.ReceiverHome, "uplink", req.TaskID, payload); err != nil {
		return nil, fmt.Errorf("uplink report: enqueue receiver wake: %w", err)
	}
	if err := retireSuperseded(req.SenderHome, req.ReceiverHome, oldReports); err != nil {
		return nil, fmt.Errorf("%w: message %s landed in %s; retire superseded: %w", ErrReportDurable, env.MessageID, req.ReceiverHome, err)
	}

	result := &ReportResult{MessageID: env.MessageID, Queued: true}
	if req.Notify == nil {
		return result, nil
	}
	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: req.SenderIdentity}
	nr := req.Notify(ref)
	// Both failures below land after the durable commit, and this one lands
	// after Notify has already run -- the receiver may well have been told.
	// Neither is a report that did not happen, so both say so.
	if err := markNotificationAttempt(req.SenderHome, env.MessageID); err != nil {
		return nil, fmt.Errorf("%w: message %s landed in %s and the notification path already ran; recording the attempt: %w", ErrReportDurable, env.MessageID, req.ReceiverHome, err)
	}
	if nr.Err != nil {
		return nil, fmt.Errorf("%w: message %s landed in %s; notify: %w", ErrReportDurable, env.MessageID, req.ReceiverHome, nr.Err)
	}
	if nr.Acknowledged {
		result.Notified, result.Queued = true, false
	}
	return result, nil
}

func Recover(req RecoverRequest) (*RecoverResult, error) {
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	var pending []*Envelope
	var err error
	if req.SenderIdentity == "" {
		pending, err = NewStore(req.SenderHome).ListAllPending()
	} else {
		pending, err = NewStore(req.SenderHome).ListPending(req.SenderIdentity)
	}
	if err != nil {
		return nil, fmt.Errorf("uplink recover: list pending: %w", err)
	}
	result := &RecoverResult{}
	for _, env := range pending {
		if req.ReceiverRank != "" && env.ReceiverRank != req.ReceiverRank {
			continue
		}
		ack, err := NewStore(req.ReceiverHome).ReadAck(env.SenderIdentity, env.MessageID)
		if err != nil {
			return nil, err
		}
		if ack != nil {
			if err := ValidateAck(env, ack); err != nil {
				return nil, fmt.Errorf("uplink recover: invalid ack: %w", err)
			}
			if ack.Outcome != OutcomeAccepted {
				return nil, fmt.Errorf("uplink recover: ack outcome %q is not accepted", ack.Outcome)
			}
			if err := writeEvidence(acceptedEvidencePath(req.SenderHome, env.TaskID, env.Key), evidence{MessageID: env.MessageID, TaskID: env.TaskID, Key: env.Key, At: req.Now.UnixNano()}); err != nil {
				return nil, err
			}
			_ = os.Remove(openEvidencePath(req.SenderHome, env.TaskID, env.Key))
			if err := NewStore(req.SenderHome).RemovePendingAfterAck(env.SenderIdentity, env.MessageID, ack); err != nil {
				return nil, err
			}
			_ = os.Remove(notificationAttemptPath(req.SenderHome, env.MessageID))
			result.Accepted++
			continue
		}
		if req.Notify == nil || (!req.ForceNotify && !notificationDue(req.SenderHome, env.MessageID, req.Now)) {
			result.Queued++
			continue
		}
		ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
		nr := req.Notify(ref)
		if err := markNotificationAttempt(req.SenderHome, env.MessageID); err != nil {
			return nil, err
		}
		// An unacknowledged notification remains queued for a later retry. A
		// resolver may be temporarily unavailable, while a transport failure is
		// surfaced by Report because the transport was actually invoked there.
		if nr.Acknowledged {
			result.Notified++
		} else {
			result.Queued++
		}
	}
	return result, nil
}

// HasAnyOpenReport reports whether any keyed Uplink Report for taskID still
// awaits a Processing Ack.
func HasAnyOpenReport(home, taskID string) bool {
	matches, _ := filepath.Glob(filepath.Join(evidenceDir(home), safe(taskID)+".*.open.json"))
	return len(matches) > 0
}

// HasPendingReport checks sender pending records directly so teardown remains
// fail-closed if a crash occurs before local open evidence is written.
func HasPendingReport(home, taskID string) bool {
	pending, err := NewStore(home).ListAllPending()
	if err != nil {
		return true
	}
	for _, env := range pending {
		if env.TaskID == taskID && env.Kind == "uplink-report" {
			return true
		}
	}
	return false
}

func supersededReports(req ReportRequest) ([]*Envelope, error) {
	pending, err := NewStore(req.SenderHome).ListPending(req.SenderIdentity)
	if err != nil {
		return nil, err
	}
	var old []*Envelope
	for _, env := range pending {
		if env.TaskID == req.TaskID && normalizedKey(env.Key) == normalizedKey(req.Key) {
			old = append(old, env)
		}
	}
	return old, nil
}

func retireSuperseded(senderHome, receiverHome string, old []*Envelope) error {
	for _, env := range old {
		if err := NewStore(receiverHome).MarkSuperseded(env.SenderIdentity, env.MessageID); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(senderHome, "state", OutboxDir, env.SenderIdentity, env.MessageID+".pending")); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = os.Remove(notificationAttemptPath(senderHome, env.MessageID))
	}
	return nil
}

func safe(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_").Replace(s)
}
func normalizedKey(key string) string {
	if key == "" {
		return "default"
	}
	return key
}
func evidenceDir(home string) string { return filepath.Join(home, "state", ".uplink") }
func openEvidencePath(home, taskID, key string) string {
	return filepath.Join(evidenceDir(home), safe(taskID)+"."+safe(normalizedKey(key))+".open.json")
}
func acceptedEvidencePath(home, taskID, key string) string {
	return filepath.Join(evidenceDir(home), safe(taskID)+"."+safe(normalizedKey(key))+".accepted.json")
}
func notificationAttemptPath(home, messageID string) string {
	return filepath.Join(evidenceDir(home), "notify-"+safe(messageID))
}

func writeEvidence(path string, value evidence) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func markNotificationAttempt(home, messageID string) error {
	path := notificationAttemptPath(home, messageID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", time.Now().UnixNano())), 0644)
}
func notificationDue(home, messageID string, now time.Time) bool {
	info, err := os.Stat(notificationAttemptPath(home, messageID))
	return err != nil || now.Sub(info.ModTime()) >= retryInterval
}

// NotifyParent attempts immediate delivery of a NotificationRef to the parent
// agent pane. Durable state must already exist before this adapter is called.
type TargetResolver func(receiverHome string, ref NotificationRef) (TargetResult, error)

type NotificationTransport interface {
	Notify(senderHome string, target TargetResult, payload string) UplinkNotifyResult
}

func NotifyParentWithTransport(senderHome, receiverHome string, ref NotificationRef, transport NotificationTransport) UplinkNotifyResult {
	return NotifyParentWithTargetResolver(senderHome, receiverHome, ref, resolveReceiverTarget, transport)
}

func NotifyParentWithTargetResolver(senderHome, receiverHome string, ref NotificationRef, resolveTarget TargetResolver, transport NotificationTransport) UplinkNotifyResult {
	target, err := resolveTarget(receiverHome, ref)
	if err != nil {
		return UplinkNotifyResult{Queued: true}
	}
	if target.Handle == "" || target.Source == Unsupported || transport == nil {
		return UplinkNotifyResult{Queued: true}
	}
	return transport.Notify(senderHome, target, ref.Encode())
}

func resolveReceiverTarget(receiverHome string, ref NotificationRef) (TargetResult, error) {
	env, err := NewStore(receiverHome).ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil || env == nil {
		return TargetResult{}, fmt.Errorf("reading receiver envelope: %w", err)
	}
	if env.ReceiverRank == RankCaptain {
		parentHome, err := config.Get(receiverHome, "parent-home")
		if err != nil {
			return TargetResult{}, fmt.Errorf("captain parent-home unavailable: %w", err)
		}
		meta, err := mhome.ReadMeta(parentHome, "captain:"+env.ReceiverID)
		if err != nil {
			return TargetResult{}, err
		}
		paneID := strings.TrimSpace(meta["herdr_pane_id"])
		if paneID == "" {
			return TargetResult{}, fmt.Errorf("captain meta has no herdr_pane_id")
		}
		sessionID := strings.TrimSpace(meta["herdr_session"])
		if sessionID == "" {
			sessionID = "default"
		}
		return TargetResult{Source: RuntimeSource, Handle: sessionID + ":" + paneID, Session: sessionID}, nil
	}
	return ResolveTargetWithSource(receiverHome)
}
