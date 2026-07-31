// Package fleet manages the fleet lifecycle — captains, soldiers, delivery,
// config propagation, and reconciliation.
package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/home"
)

// PropagateConfigRequest carries all inputs for the PropagateConfig transaction.
type PropagateConfigRequest struct {
	ParentHome  string
	CaptainHome string
	Mailbox     home.BoundSender
}

// RequirementState is the typed outcome of the config-reread requirement step.
type RequirementState string

const (
	RequirementCreated RequirementState = "created"
	RequirementReused  RequirementState = "reused"
	RequirementFailed  RequirementState = "failed"
)

// NotificationState is the typed outcome of the config-reread notification step.
type NotificationState string

const (
	NotificationSubmitted NotificationState = "submitted"
	NotificationDeferred  NotificationState = "deferred"
	NotificationSkipped   NotificationState = "skipped"
	NotificationFailed    NotificationState = "failed"
)

// PropagateConfigResult holds the structured outcome of a PropagateConfig call.
type PropagateConfigResult struct {
	Changed           bool
	Generation        int
	RequirementState  RequirementState
	NotificationState NotificationState
	Detail            string
}

// PropagateConfigSummary is a renderable summary of a PropagateConfigResult.
type PropagateConfigSummary struct {
	Changed           bool
	Generation        int
	RequirementState  string
	NotificationState string
	Detail            string
}

// Summary returns a human-readable summary from the structured result.
func (r *PropagateConfigResult) Summary() PropagateConfigSummary {
	if r == nil {
		return PropagateConfigSummary{
			Detail: "no result (nil)",
		}
	}
	return PropagateConfigSummary{
		Changed:           r.Changed,
		Generation:        r.Generation,
		RequirementState:  string(r.RequirementState),
		NotificationState: string(r.NotificationState),
		Detail:            r.Detail,
	}
}

// PropagateConfig is the high Fleet-owned transaction that propagates a changed
// Captain config end to end. It:
//
//  1. Validates non-empty homes, provenance, and destination path safety
//     before any mutation.
//  2. Copies inherited config surface (inheritable files, general-shared.md,
//     projects.md) via configPushWithResult.
//  3. Mirror-deletes removed settings without escaping the captain home.
//  4. Computes effective digest and compares against current generation.
//  5. Reconciles legacy config-reread evidence in both changed and unchanged
//     paths so that malformed legacy state is surfaced early.
//  6. Ensures or heals the durable config-reread requirement for the current
//     generation and digest. On the unchanged path this detects a crash where
//     the generation was committed but the mailbox requirement was not
//     materialized, or a deferred notification that needs retry.
//  7. Returns the typed PropagateConfigResult with changed/changed state,
//     generation, requirement state, notification state, and detail.
//
// Generation state and requirement are made durable BEFORE notification is
// attempted. Notification rejection or failure after durable commit returns
// a deferred-success summary rather than a transaction error. Hard validation,
// unsafe path, corrupt state, and failed durable writes remain errors.
func PropagateConfig(req PropagateConfigRequest) (*PropagateConfigResult, error) {
	// 1. Validate non-empty homes.
	if req.ParentHome == "" {
		return nil, fmt.Errorf("propagate config: parent home is required")
	}
	if req.CaptainHome == "" {
		return nil, fmt.Errorf("propagate config: captain home is required")
	}
	if req.Mailbox == nil {
		return nil, fmt.Errorf("propagate config: mailbox sender capability is required")
	}

	// 2. Validate provenance (before any mutation).
	if _, err := ValidateProvenance(req.CaptainHome); err != nil {
		return nil, fmt.Errorf("propagate config: %w", err)
	}

	// 3. Run the full config push with generation tracking.
	res, err := configPushWithResult(req.ParentHome, req.CaptainHome)
	if err != nil {
		return nil, fmt.Errorf("propagate config: %w", err)
	}

	result := &PropagateConfigResult{
		Changed:    res.Changed,
		Generation: res.Generation,
	}

	// 4. Reconcile legacy config-reread evidence in both changed and
	//    unchanged paths so incomplete state is healed.
	recorder := &boundSenderRecorder{actual: req.Mailbox}

	if legErr := ReconcileLegacyConfigReread(req.ParentHome, req.CaptainHome, recorder); legErr != nil {
		// Legacy reconciliation failure is best-effort detail.
		result.Detail = fmt.Sprintf("generation=%d, legacy reconciliation: %v", res.Generation, legErr)
	}

	// 5. Determine the digest to use for requirement identity.
	//    On the unchanged path, OldDigest == NewDigest. On the changed path
	//    NewDigest reflects the new content. On first push with unchanged
	//    content (no prior gen), NewDigest is set.
	digest := res.NewDigest
	if digest == "" {
		digest = res.OldDigest
	}

	// 6. Ensure or heal the durable config-reread requirement.
	//    On the unchanged path, this detects a crash where the generation
	//    was committed but the mailbox requirement was not materialized,
	//    or a deferred notification that needs retry.
	reqState, notifState, detail, reqErr := ensureOrHealRequirement(
		req.ParentHome, req.CaptainHome,
		res.Generation, digest, recorder,
	)
	if reqErr != nil {
		// Durable requirement failure is a hard error.
		return nil, fmt.Errorf("propagate config: %w", reqErr)
	}

	result.RequirementState = reqState
	result.NotificationState = notifState
	result.Detail = detail

	return result, nil
}

// ensureOrHealRequirement handles requirement creation, healing, and
// notification retry for a single propagation transaction. It:
//
//  1. Checks whether a durable inbox envelope for (gen, digest) already
//     exists in the captain's mailbox.
//  2. If the envelope exists and is acked → returns (Reused, Skipped).
//  3. If the envelope exists and is not acked → calls
//     EnsureConfigRereadRequirement to retry notification.
//     Returns (Reused, <recorder outcome>).
//  4. If the envelope does not exist → calls
//     EnsureConfigRereadRequirement to create it.
//     Returns (Created, <recorder outcome>).
//
// When the inbox envelope does not exist, this is a crash-healing scenario:
// the generation was committed by configPushWithResult but the mailbox
// requirement (envelope + pending) was not materialized before the crash.
// On the next propagation, we heal by creating it now.
//
// Durable requirement failures (invalid provenance, corrupt state, failed
// writes) return errors.
func ensureOrHealRequirement(
	parentHome, captainHome string,
	gen int, digest string,
	recorder *boundSenderRecorder,
) (RequirementState, NotificationState, string, error) {
	// Derive identities for envelope lookup.
	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		return RequirementFailed, NotificationFailed, "",
			fmt.Errorf("ensure requirement: %w", err)
	}
	senderIdentity, _, err := home.ReadHomeIdentity(parentHome)
	if err != nil {
		return RequirementFailed, NotificationFailed, "",
			fmt.Errorf("ensure requirement: deriving sender identity: %w", err)
	}

	// Compute the expected deterministic envelope ID.
	envelopeID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)

	// Canonicalize the captain home for store access.
	canonCaptain, err := canonicalCaptainHome(captainHome)
	if err != nil {
		return RequirementFailed, NotificationFailed, "",
			fmt.Errorf("ensure requirement: canonicalizing captain home: %w", err)
	}
	captainStore := home.NewStore(canonCaptain)

	// Check if the inbox envelope already exists.
	existingEnv, readErr := captainStore.ReadEnvelope(senderIdentity, envelopeID)
	if readErr != nil {
		return RequirementFailed, NotificationFailed, "",
			fmt.Errorf("ensure requirement: reading envelope: %w", readErr)
	}

	envExists := existingEnv != nil

	// Validate an existing envelope has expected content.
	if envExists {
		if existingEnv.Key != ConfigRereadKey {
			return RequirementFailed, NotificationFailed, "",
				fmt.Errorf("ensure requirement: existing envelope ID %q has unexpected key %q", envelopeID, existingEnv.Key)
		}
	}

	// Check ack state.
	isAcked := captainStore.IsAcked(senderIdentity, envelopeID)
	if envExists && isAcked {
		// Already fully processed → no action needed.
		return RequirementReused, NotificationSkipped,
			fmt.Sprintf("generation=%d (acked)", gen), nil
	}

	// Call EnsureConfigRereadRequirement — idempotent for same envelope ID.
	// This heals:
	//   - generation-only crash (no envelope)
	//   - envelope-only crash (no pending)
	//   - missing pending record
	//   - deferred notification retry
	if reqErr := EnsureConfigRereadRequirement(parentHome, captainHome, gen, digest, recorder); reqErr != nil {
		return RequirementFailed, NotificationFailed, "",
			fmt.Errorf("ensure requirement: %w", reqErr)
	}

	reqState := RequirementReused
	if !envExists {
		// The envelope was not present — this is a crash-healing creation.
		reqState = RequirementCreated
	}

	notifState, detail := deriveNotifyState(recorder, gen)
	return reqState, notifState, detail, nil
}

// deriveNotifyState reads the boundSenderRecorder result and returns the
// typed NotificationState and a descriptive detail string.
func deriveNotifyState(recorder *boundSenderRecorder, gen int) (NotificationState, string) {
	if recorder.called {
		if recorder.result.Acknowledged {
			return NotificationSubmitted,
				fmt.Sprintf("generation=%d, notified", gen)
		}
		return NotificationDeferred,
			fmt.Sprintf("generation=%d, notification deferred (status=%s)", gen, recorder.result.Status)
	}
	return NotificationSkipped,
		fmt.Sprintf("generation=%d, no notification (no meta or incomplete meta)", gen)
}

// boundSenderRecorder wraps a home.BoundSender and records whether Send was
// called and the result. This lets PropagateConfig determine the typed
// notification state without redesigning EnsureConfigRereadRequirement.
type boundSenderRecorder struct {
	actual home.BoundSender
	called bool
	result home.BoundSendResult
}

func (r *boundSenderRecorder) Alive(homeDir string, meta map[string]string) (bool, error) {
	return r.actual.Alive(homeDir, meta)
}

func (r *boundSenderRecorder) Send(homeDir string, meta map[string]string, payload string) home.BoundSendResult {
	r.called = true
	r.result = r.actual.Send(homeDir, meta, payload)
	return r.result
}

// Ensure recordingSender implements home.BoundSender.
var _ home.BoundSender = (*boundSenderRecorder)(nil)

// noopBoundSender is a home.BoundSender that never sends. It returns
// Acknowledged: false with status "deferred" so the pending record
// remains for converge to retry when a real mailbox sender is available.
// Used during seed and update where no running session exists yet.
type noopBoundSender struct{}

func (n *noopBoundSender) Alive(_ string, _ map[string]string) (bool, error) {
	return false, nil
}

func (n *noopBoundSender) Send(_ string, _ map[string]string, _ string) home.BoundSendResult {
	return home.BoundSendResult{
		Status:       "deferred",
		Acknowledged: false,
	}
}

var _ home.BoundSender = (*noopBoundSender)(nil)

// propagateConfigLogPath returns the path to the propagation state directory.
func propagateConfigLogPath(captainHome string) string {
	return filepath.Join(captainHome, "state", "config-push.log")
}

// IsPropagateConfigUnchanged returns true when the inherited config at
// captainHome has not changed since the last propagation. This is a
// read-only check — it computes the digest and compares against the
// stored generation without writing anything.
func IsPropagateConfigUnchanged(captainHome string) (bool, error) {
	newDigest, err := ComputeInheritedConfigDigest(captainHome)
	if err != nil {
		if errors.Is(err, ErrNoPublishedSnapshot) {
			return false, nil
		}
		return false, fmt.Errorf("is-propagate-config-unchanged: computing digest: %w", err)
	}

	_, oldDigest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		return false, fmt.Errorf("is-propagate-config-unchanged: reading gen: %w", err)
	}
	if !found {
		return false, nil // No generation yet → first change.
	}
	return oldDigest == newDigest, nil
}

// PropagateConfigCLI wraps PropagateConfig and returns a summary string
// suitable for CLI output. It handles nil result and common errors.
func PropagateConfigCLI(req PropagateConfigRequest) (string, error) {
	result, err := PropagateConfig(req)
	if err != nil {
		return "", err
	}
	s := result.Summary()

	var msg string
	if !s.Changed {
		msg = fmt.Sprintf("inherited config unchanged: generation=%d, requirement=%s",
			s.Generation, s.RequirementState)
	} else {
		msg = fmt.Sprintf("inherited config changed: generation=%d, requirement=%s",
			s.Generation, s.RequirementState)
	}

	switch s.NotificationState {
	case "submitted":
		msg += ", notification submitted"
	case "deferred":
		msg += ", notification deferred"
	case "skipped":
		msg += ", notification skipped"
	case "failed":
		msg += ", notification failed"
	}
	if s.Detail != "" {
		msg += " (" + s.Detail + ")"
	}
	return msg, nil
}

// Ensure PropagateConfigCLI is usable from the CLI layer.
var _ = PropagateConfigCLI

// ConfigNotificationAdapter wraps a home.BoundSender into a notification
// adapter suitable for use where the generic BoundSender is not directly
// available but a simpler send interface is needed.
type ConfigNotificationAdapter struct {
	Sender func(parentHome, captainHome string, gen int, digest string) (bool, string)
}

// EnsureConfigNotificationAdapter is a convenience constructor.
func ensureConfigPreconditions(captainHome string) error {
	stateDir := filepath.Join(captainHome, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("ensuring state directory: %w", err)
	}
	return nil
}
