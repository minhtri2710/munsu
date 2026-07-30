// Package fleet manages the fleet lifecycle — captains, soldiers, delivery,
// config propagation, and reconciliation.
package fleet

import (
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
//  1. Validates non-empty homes, provenance, and destination path safety
//     before any mutation.
//  2. Copies inherited config surface (inheritable files, general-shared.md,
//     projects.md) via ConfigPushWithResult.
//  3. Mirror-deletes removed settings without escaping the captain home.
//  4. Computes effective digest and compares against current generation.
//  5. If unchanged → returns Changed=false with existing generation,
//     requirementState=reused, notificationState=skipped.
//  6. If changed → advances generation, reconciles legacy evidence, creates
//     durable config-reread requirement, and attempts notification through
//     the injected mailbox adapter.
//
// Generation state and requirement are made durable BEFORE notification is
// attempted. The function does not add idempotency, deferred-success, or
// crash-healing behavior — those belong to #369.
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
	//    ConfigPushWithResult now includes a destination preflight and
	//    handles copying, mirror-deletion, digest computation, and
	//    generation advancement.
	res, err := ConfigPushWithResult(req.ParentHome, req.CaptainHome)
	if err != nil {
		return nil, fmt.Errorf("propagate config: %w", err)
	}

	result := &PropagateConfigResult{
		Changed:    res.Changed,
		Generation: res.Generation,
	}

	// 4. If unchanged → return with reused/skipped.
	if !res.Changed {
		result.RequirementState = RequirementReused
		result.NotificationState = NotificationSkipped
		result.Detail = fmt.Sprintf("generation=%d (unchanged)", res.Generation)
		return result, nil
	}

	// 5. Changed: generation is already advanced durably by ConfigPushWithResult.
	//    Wrap the mailbox sender in a recording decorator so we can determine
	//    the typed notification state after EnsureConfigRereadRequirement.
	recorder := &boundSenderRecorder{actual: req.Mailbox}

	// 6. Reconcile legacy config-reread evidence inside the transaction.
	if legErr := ReconcileLegacyConfigReread(req.ParentHome, req.CaptainHome, recorder); legErr != nil {
		result.Detail = fmt.Sprintf("generation=%d, legacy reconciliation: %v", res.Generation, legErr)
		// Continue — legacy reconciliation is best-effort.
	}

	// 7. Create durable config-reread requirement bound to the changed
	//    generation. The recorder captures the notification outcome.
	if mbErr := EnsureConfigRereadRequirement(req.ParentHome, req.CaptainHome, res.Generation, res.NewDigest, recorder); mbErr != nil {
		result.RequirementState = RequirementFailed
		result.NotificationState = NotificationFailed
		result.Detail = fmt.Sprintf("generation=%d, requirement failed: %v", res.Generation, mbErr)
		return result, nil
	}

	// 8. Derive typed notification state from the recorder.
	result.RequirementState = RequirementCreated
	if recorder.called {
		if recorder.result.Acknowledged {
			result.NotificationState = NotificationSubmitted
			result.Detail = fmt.Sprintf("generation=%d, notified", res.Generation)
		} else {
			result.NotificationState = NotificationDeferred
			result.Detail = fmt.Sprintf("generation=%d, notification deferred (status=%s)", res.Generation, recorder.result.Status)
		}
	} else {
		result.NotificationState = NotificationSkipped
		result.Detail = fmt.Sprintf("generation=%d, no notification (no meta or incomplete meta)", res.Generation)
	}

	return result, nil
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
	if !s.Changed {
		return "inherited config unchanged (no notification sent)", nil
	}
	msg := fmt.Sprintf("inherited config changed: generation=%d, requirement=%s", s.Generation, s.RequirementState)
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
