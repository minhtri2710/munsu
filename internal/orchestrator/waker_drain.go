package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// ConditionCode is a stable machine-readable code for a guard condition.
type ConditionCode string

const (
	ConditionQueuedWakesPending ConditionCode = "queued_wakes_pending"
	ConditionWatcherAbsent      ConditionCode = "watcher_absent"
	ConditionWatcherStale       ConditionCode = "watcher_stale"
	ConditionAgedWakePending    ConditionCode = "aged_wake_pending"

	ConditionClaimedWakeExpired     ConditionCode = "claimed_wake_expired"
	ConditionClaimedWakeStalled     ConditionCode = "claimed_wake_stalled"
	ConditionClaimedWakesUnreadable ConditionCode = "claimed_wakes_unreadable"
)

// MaterialWakeAgeThreshold is the age at which a material (done/failed/needs-decision/blocked)
// wake makes the guard unhealthy rather than indeterminate.
const MaterialWakeAgeThreshold = 5 * time.Minute

// ConditionInfo pairs a stable condition code with its human-readable message.
type ConditionInfo struct {
	Code    ConditionCode
	Message string
}

// GuardResult summarizes the shared guard evaluation for both the CLI
// middleware and the contract guard command.
type GuardResult struct {
	BeatStatus BeatStatus
	InFlight   int
	Conditions []ConditionInfo
}

// EvaluateGuard returns the current guard state combining beat liveness,
// queued-wake status, aged wake threshold, claimed-wake state, and the
// caller-supplied in-flight task count. Aged material wakes (beyond
// MaterialWakeAgeThreshold) produce an unhealthy condition rather than merely
// indeterminate. A claimed wake whose lease expired or whose claimant has not
// acked or resolved it produces a condition of its own; a lease directory that
// cannot be read produces one too, never silence.
// Both the pre-run middleware and the contract guard command use this to
// produce consistent warnings and state.
func EvaluateGuard(homeDir string, inFlight int, now time.Time) GuardResult {
	result := GuardResult{
		InFlight: inFlight,
	}

	result.BeatStatus = ReadBeatStatus(homeDir, now)

	if HasQueuedWakes(homeDir) {
		delivery := "under herdr the watcher delivers each as a prompt once the General is idle; elsewhere claim with munsu wake claim"
		if _, modeErr := ResolveWakeDeliveryMode(homeDir); modeErr != nil {
			delivery = fmt.Sprintf("the watcher cannot deliver them (%s); claim with munsu wake claim", wakeModeUnusableReason(modeErr))
		}
		msg := "QUEUED WAKES PENDING - " + delivery
		if hasAgedMaterialWake(homeDir, now) {
			msg = "QUEUED WAKES PENDING (aged) - material wake beyond threshold, guard unhealthy - " + delivery
			result.Conditions = append(result.Conditions, ConditionInfo{
				Code:    ConditionAgedWakePending,
				Message: msg,
			})
		} else {
			result.Conditions = append(result.Conditions, ConditionInfo{
				Code:    ConditionQueuedWakesPending,
				Message: msg,
			})
		}
	}

	result.Conditions = append(result.Conditions, claimedWakeConditions(homeDir, now)...)

	return result
}

// claimedWakeConditions turns held-but-unacked wakes into guard conditions,
// one per code, citing the first wake in claim order.
func claimedWakeConditions(homeDir string, now time.Time) []ConditionInfo {
	claimed, err := ListClaimedWakes(homeDir, now)
	if err != nil {
		return []ConditionInfo{{
			Code:    ConditionClaimedWakesUnreadable,
			Message: fmt.Sprintf("CLAIMED WAKES UNKNOWN - lease directory unreadable: %v", err),
		}}
	}
	var expired, stalled []ClaimedWake
	for _, c := range claimed {
		switch c.State {
		case mhome.ClaimExpired:
			expired = append(expired, c)
		case mhome.ClaimStalled:
			stalled = append(stalled, c)
		}
	}
	var conditions []ConditionInfo
	if len(expired) > 0 {
		conditions = append(conditions, ConditionInfo{
			Code:    ConditionClaimedWakeExpired,
			Message: fmt.Sprintf("CLAIMED WAKE EXPIRED (%d) - %s - the next munsu wake claim reclaims it", len(expired), expired[0].Evidence),
		})
	}
	if len(stalled) > 0 {
		conditions = append(conditions, ConditionInfo{
			Code:    ConditionClaimedWakeStalled,
			Message: fmt.Sprintf("CLAIMED WAKE STALLED (%d) - %s", len(stalled), stalled[0].Evidence),
		})
	}
	return conditions
}

// ClaimedWake is one unacked wake held in a lease, with the state derived by
// mhome.ClaimState.
type ClaimedWake struct {
	LeaseID   string
	Consumer  string
	ClaimedAt time.Time // zero when the lease header has no claim time
	Wake      mhome.ClaimedWakeRecord
	State     string
	Evidence  string
	Material  bool
}

// ListClaimedWakes returns the unacked wakes held in leases, in claim order.
// A wake with a completed resolution is not unacked and is omitted, and so is
// a wake that is also in the queue (mid-reclaim it is in both; the queued copy
// is the live one). The error is the lease directory's: callers show it as
// unknown, never as none.
func ListClaimedWakes(homeDir string, now time.Time) ([]ClaimedWake, error) {
	leases, err := mhome.ListClaimedLeases(homeDir)
	if err != nil {
		return nil, err
	}
	queued := map[string]bool{}
	if records, err := mhome.PeekWakes(homeDir); err == nil {
		for _, r := range records {
			queued[r.Epoch+":"+r.Seq] = true
		}
	}
	var out []ClaimedWake
	for _, l := range leases {
		var claimedAt time.Time
		if l.ClaimedAt > 0 {
			claimedAt = time.Unix(l.ClaimedAt, 0)
		}
		for _, w := range l.Wakes {
			state, evidence := mhome.ClaimState(l, w, now, MaterialWakeAgeThreshold)
			if state == mhome.ClaimResolved || queued[w.Epoch+":"+w.Seq] {
				continue
			}
			out = append(out, ClaimedWake{
				LeaseID:   l.LeaseID,
				Consumer:  l.Consumer,
				ClaimedAt: claimedAt,
				Wake:      w.ClaimedWakeRecord,
				State:     state,
				Evidence:  evidence,
				Material:  payloadHasMaterialMarker(w.Key, w.Payload),
			})
		}
	}
	return out, nil
}

// hasAgedMaterialWake returns true when the wake queue contains at least one
// material wake (done/failed/needs-decision/blocked) older than
// MaterialWakeAgeThreshold.
func hasAgedMaterialWake(homeDir string, now time.Time) bool {
	epoch, ok := OldestMaterialWake(homeDir)
	return ok && epoch < now.Add(-MaterialWakeAgeThreshold).Unix()
}

// OldestMaterialWake returns the enqueue epoch of the oldest material wake in
// the wake queue, counting a report line no wake carries yet from its status
// file's mtime. ok is false when there is none or the queue cannot be read.
func OldestMaterialWake(homeDir string) (epoch int64, ok bool) {
	if at, found := mhome.OldestUnderivedReport(homeDir); found {
		epoch, ok = at.Unix(), true
	}
	records, err := mhome.PeekWakes(homeDir)
	if err != nil {
		return epoch, ok
	}
	for _, r := range records {
		// r.Key is the taskID for signal/report wakes; payloadHasMaterialMarker
		// uses it to check the signal marker at the anchored "<taskID>: " position.
		if !payloadHasMaterialMarker(r.Key, r.Payload) {
			continue
		}
		var e int64
		if _, err := fmt.Sscanf(r.Epoch, "%d", &e); err != nil || e <= 0 {
			continue
		}
		if !ok || e < epoch {
			epoch, ok = e, true
		}
	}
	return epoch, ok
}

// payloadHasMaterialMarker reports whether a wake queue payload carries a
// material-state marker (done:/failed:/needs-decision:/blocked:) at its
// structural position. Material wakes take two producer shapes: a report wake
// payload is "<taskID>: <state>: <msg> [key=K]" (the marker follows the
// "<key>: " prefix, where key is the taskID) and a watcher signal wake payload
// is the status line itself (the marker is at the start). Checking both
// anchored positions matches both shapes while rejecting a marker that merely
// appears mid-message in a non-material payload. OldestMaterialWake
// applies it for both the guard and watch.
func payloadHasMaterialMarker(key, payload string) bool {
	for _, state := range domain.MaterialVerbs {
		marker := state + ":"
		if strings.HasPrefix(payload, marker) || strings.HasPrefix(payload, key+": "+marker) {
			return true
		}
	}
	return false
}
