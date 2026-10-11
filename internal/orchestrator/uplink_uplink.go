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
	"github.com/minhtri2710/munsu/internal/domain"
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

type uplinkNotifyKind uint8

const (
	uplinkNotifyQueued uplinkNotifyKind = iota
	uplinkNotifyAcknowledged
	uplinkNotifyFailed
)

type UplinkNotifyResult struct {
	kind uplinkNotifyKind
	err  error
}

func QueuedNotification() UplinkNotifyResult {
	return UplinkNotifyResult{kind: uplinkNotifyQueued}
}

func AcknowledgedNotification() UplinkNotifyResult {
	return UplinkNotifyResult{kind: uplinkNotifyAcknowledged}
}

func FailedNotification(err error) UplinkNotifyResult {
	if err == nil {
		err = errors.New("notification transport failed")
	}
	return UplinkNotifyResult{kind: uplinkNotifyFailed, err: err}
}

func (r UplinkNotifyResult) Acknowledged() bool { return r.kind == uplinkNotifyAcknowledged }
func (r UplinkNotifyResult) Queued() bool       { return r.kind == uplinkNotifyQueued }
func (r UplinkNotifyResult) Failed() bool       { return r.kind == uplinkNotifyFailed }
func (r UplinkNotifyResult) Failure() error     { return r.err }

type ReportRequest struct {
	SenderHome, ReceiverHome    string
	SenderIdentity, ReceiverID  string
	SenderRank, ReceiverRank    mhome.Rank
	TaskID, Key, State, Message string
	Notify                      func(mhome.NotificationRef) UplinkNotifyResult
}

type ReportResult struct {
	MessageID string
	Notified  bool
	Queued    bool
}

type RecoverRequest struct {
	SenderHome, ReceiverHome string
	SenderIdentity           string
	ReceiverRank             mhome.Rank
	ForceNotify              bool
	Now                      time.Time
	Notify                   func(mhome.NotificationRef) UplinkNotifyResult
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
	if !domain.IsMaterialVerb(req.State) {
		return nil, fmt.Errorf("uplink report: state %q is not material", req.State)
	}
	if req.Key == "" {
		req.Key = "default"
	}
	if _, homeRank, err := mhome.ReadHomeIdentity(req.ReceiverHome); err != nil {
		return nil, fmt.Errorf("uplink report: reading receiver home rank: %w", err)
	} else if req.ReceiverRank != homeRank {
		return nil, fmt.Errorf("uplink report: receiver rank %q cannot be satisfied by receiving home %s of rank %q", req.ReceiverRank, req.ReceiverHome, homeRank)
	}
	oldReports, err := supersededReports(req)
	if err != nil {
		return nil, err
	}

	payload := fmt.Sprintf("%s: %s [task=%s key=%s]", req.State, req.Message, req.TaskID, req.Key)
	env := &mhome.Envelope{
		SenderRank: req.SenderRank, SenderIdentity: req.SenderIdentity,
		ReceiverRank: req.ReceiverRank, ReceiverID: req.ReceiverID,
		Kind: "uplink-report", TaskID: req.TaskID, Key: req.Key, Payload: payload,
	}
	if err := mhome.NewStore(req.ReceiverHome).WriteEnvelope(env); err != nil {
		return nil, fmt.Errorf("uplink report: write envelope: %w", err)
	}
	// The wake lands before the sender's pending record: a crash between them
	// leaves a wake for a report the sender does not track yet, which the
	// receiver can still settle, never a pending report nothing will ever
	// surface to a General (its recovery pass does not re-notify one).
	ref := mhome.NotificationRef{MessageID: env.MessageID, SenderIdentity: req.SenderIdentity}
	if err := mhome.EnqueueRefWake(req.ReceiverHome, "uplink", req.TaskID, ref.Encode(), payload); err != nil {
		return nil, fmt.Errorf("uplink report: enqueue receiver wake: %w", err)
	}
	if err := mhome.NewStore(req.SenderHome).WritePending(env); err != nil {
		return nil, fmt.Errorf("uplink report: write pending: %w", err)
	}
	if err := writeEvidence(openEvidencePath(req.SenderHome, req.TaskID, req.Key), evidence{MessageID: env.MessageID, TaskID: req.TaskID, Key: req.Key, State: req.State, At: time.Now().UnixNano()}); err != nil {
		return nil, fmt.Errorf("uplink report: write open evidence: %w", err)
	}
	if err := retireSuperseded(req.SenderHome, req.ReceiverHome, oldReports); err != nil {
		return nil, fmt.Errorf("%w: message %s landed in %s; retire superseded: %w", ErrReportDurable, env.MessageID, req.ReceiverHome, err)
	}

	result := &ReportResult{MessageID: env.MessageID, Queued: true}
	if req.Notify == nil {
		return result, nil
	}
	nr := req.Notify(ref)
	// Both failures below land after the durable commit, and this one lands
	// after Notify has already run -- the receiver may well have been told.
	// Neither is a report that did not happen, so both say so.
	if err := markNotificationAttempt(req.SenderHome, env.MessageID); err != nil {
		return nil, fmt.Errorf("%w: message %s landed in %s and the notification path already ran; recording the attempt: %w", ErrReportDurable, env.MessageID, req.ReceiverHome, err)
	}
	if nr.Acknowledged() {
		result.Notified, result.Queued = true, false
	} else if nr.Failed() {
		return nil, fmt.Errorf("%w: message %s landed in %s; notify: %w", ErrReportDurable, env.MessageID, req.ReceiverHome, nr.Failure())
	}
	return result, nil
}

func Recover(req RecoverRequest) (*RecoverResult, error) {
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	var pending []*mhome.Envelope
	var err error
	if req.SenderIdentity == "" {
		pending, err = mhome.NewStore(req.SenderHome).ListAllPending()
	} else {
		pending, err = mhome.NewStore(req.SenderHome).ListPending(req.SenderIdentity)
	}
	if err != nil {
		return nil, fmt.Errorf("uplink recover: list pending: %w", err)
	}
	result := &RecoverResult{}
	for _, env := range pending {
		if req.ReceiverRank != "" && env.ReceiverRank != req.ReceiverRank {
			continue
		}
		ack, err := mhome.NewStore(req.ReceiverHome).ReadAck(env.SenderIdentity, env.MessageID)
		if err != nil {
			return nil, err
		}
		if ack != nil {
			if err := mhome.ValidateAck(env, ack); err != nil {
				return nil, fmt.Errorf("uplink recover: invalid ack: %w", err)
			}
			if ack.Outcome != mhome.OutcomeAccepted {
				return nil, fmt.Errorf("uplink recover: ack outcome %q is not accepted", ack.Outcome)
			}
			if err := writeEvidence(acceptedEvidencePath(req.SenderHome, env.TaskID, env.Key), evidence{MessageID: env.MessageID, TaskID: env.TaskID, Key: env.Key, At: req.Now.UnixNano()}); err != nil {
				return nil, err
			}
			_ = os.Remove(openEvidencePath(req.SenderHome, env.TaskID, env.Key))
			if err := mhome.NewStore(req.SenderHome).RemovePendingAfterAck(env.SenderIdentity, env.MessageID, ack); err != nil {
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
		ref := mhome.NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
		nr := req.Notify(ref)
		if err := markNotificationAttempt(req.SenderHome, env.MessageID); err != nil {
			return nil, err
		}
		if nr.Failed() {
			return nil, fmt.Errorf("uplink recover: notifying parent: %w", nr.Failure())
		}
		if nr.Acknowledged() {
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
	pending, err := mhome.NewStore(home).ListAllPending()
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

func supersededReports(req ReportRequest) ([]*mhome.Envelope, error) {
	pending, err := mhome.NewStore(req.SenderHome).ListPending(req.SenderIdentity)
	if err != nil {
		return nil, err
	}
	var old []*mhome.Envelope
	for _, env := range pending {
		if env.TaskID == req.TaskID && normalizedKey(env.Key) == normalizedKey(req.Key) {
			old = append(old, env)
		}
	}
	return old, nil
}

func retireSuperseded(senderHome, receiverHome string, old []*mhome.Envelope) error {
	for _, env := range old {
		if err := mhome.NewStore(receiverHome).MarkSuperseded(env.SenderIdentity, env.MessageID); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(senderHome, "state", mhome.OutboxDir, env.SenderIdentity, env.MessageID+".pending")); err != nil && !os.IsNotExist(err) {
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
	return mhome.AtomicWrite(path, data, 0600)
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

// TargetResolver resolves a receiver's notification target. selfDirected is
// true exactly when senderHome == receiverHome; the default resolver then uses
// receiverHome's own runtime target. Cross-process captain resolution uses
// authoritative parent metadata and fails closed when its pane identity is
// unavailable.
type TargetResolver func(receiverHome string, selfDirected bool, ref mhome.NotificationRef) (TargetResult, error)

type NotificationTransport interface {
	Notify(senderHome string, target TargetResult, payload string) UplinkNotifyResult
}

func NotifyParentWithTransport(senderHome, receiverHome string, ref mhome.NotificationRef, transport NotificationTransport) UplinkNotifyResult {
	return NotifyParentWithTargetResolver(senderHome, receiverHome, ref, resolveReceiverTarget, transport)
}

func NotifyParentWithTargetResolver(senderHome, receiverHome string, ref mhome.NotificationRef, resolveTarget TargetResolver, transport NotificationTransport) UplinkNotifyResult {
	target, err := resolveTarget(receiverHome, senderHome == receiverHome, ref)
	if err != nil {
		return QueuedNotification()
	}
	if target.Handle == "" || target.Source == Unsupported || transport == nil {
		return QueuedNotification()
	}
	return transport.Notify(senderHome, target, ref.Encode())
}

// resolveReceiverTarget names the pane to notify. Only a Captain receiver has
// one: the General's pane is written by the watcher wake dispatch alone, so a
// General-bound report stays queued here and its wake is already in the
// General's queue (Report enqueues it before notifying).
func resolveReceiverTarget(receiverHome string, selfDirected bool, ref mhome.NotificationRef) (TargetResult, error) {
	env, err := mhome.NewStore(receiverHome).ReadEnvelope(ref.SenderIdentity, ref.MessageID)
	if err != nil || env == nil {
		return TargetResult{}, fmt.Errorf("reading receiver envelope: %w", err)
	}
	if env.ReceiverRank != mhome.RankCaptain {
		return TargetResult{Source: Unsupported}, nil
	}
	if selfDirected {
		return ResolveTargetWithSource(receiverHome)
	}
	parentHome, err := config.Get(receiverHome, "parent-home")
	if err != nil {
		return TargetResult{}, fmt.Errorf("captain parent-home unavailable: %w", err)
	}
	meta, err := mhome.ReadMeta(parentHome, "captain:"+env.ReceiverID)
	if err != nil {
		return TargetResult{}, err
	}
	return captainMetaTarget(meta)
}
