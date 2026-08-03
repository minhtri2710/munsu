package taskauthority

import (
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// Issue link definition records live inside the Aggregate, bound to the Task
// Generation: the issue links themselves (the definition record) and the
// provider reconciliation evidence committed with them. Only implementation
// issues may carry automatic closure policy; parent and related links are
// never promoted to automatic closure (Task 7.2).

// validateIssueLinkDefinition validates the generation-bound issue link
// definition records of one Aggregate: every link must be a well-formed issue
// link with a valid closure policy for its relation, and the provider
// reconciliation evidence must correspond one-to-one with the links.
func validateIssueLinkDefinition(agg Aggregate) error {
	if len(agg.IssueLinks) == 0 {
		return nil
	}
	if len(agg.IssueLinkReconciliation) != len(agg.IssueLinks) {
		return validationError("issue link reconciliation has %d results for %d links", len(agg.IssueLinkReconciliation), len(agg.IssueLinks))
	}
	for i := range agg.IssueLinks {
		if err := validateIssueLink(agg.IssueLinks[i]); err != nil {
			return err
		}
		if err := validateReconciliationResult(agg.IssueLinkReconciliation[i], agg.IssueLinks[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateIssueLink checks one generation-bound issue link: shape validity
// plus the automatic-closure promotion rule. Only implementation links close
// automatically on merge; parent and related links never do.
func validateIssueLink(link domain.IssueLink) error {
	if err := domain.ValidateIssueLink(&link); err != nil {
		return validationError("invalid issue link: %v", err)
	}
	if link.ClosurePolicy == domain.ClosurePolicyAuto && link.Relation != domain.IssueLinkImplementation {
		return validationError("auto-close policy on %s issue link %s: only implementation issues may auto-close", link.Relation, link.URL)
	}
	return nil
}

// DeliveryPlan is the generation-bound delivery-mode definition record of one
// Task Generation: the requested mode, the effective mode resolved at spawn
// acceptance, and the reason they differ when a fallback applied. The plan is
// bounded (ADR-0004 §6): a generation accepts exactly one requested →
// effective transition, established when the capability attestation is
// accepted as authoritative evidence, and is never mutated again within the
// generation (Task 7.3).
type DeliveryPlan struct {
	RequestedMode  string `json:"requested_mode"`
	EffectiveMode  string `json:"effective_mode"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// CapabilityAttestation is the generation-bound reference to the accepted
// capability attestation: the project, the execution home, and the config
// snapshot digest the attestation was resolved under. Only the accepted
// reference becomes authoritative evidence in the Aggregate; the runtime
// capability observation data (probes, harness, gate agent, executable
// identity) stays outside the Aggregate (Task 7.3, ADR-0004 §6).
type CapabilityAttestation struct {
	Project      string `json:"project"`
	Home         string `json:"home"`
	ConfigDigest string `json:"config_digest,omitempty"`
}

// validateDeliveryDefinition validates the generation-bound delivery-plan and
// capability-attestation definition records of one Aggregate: the records are
// attached together (never one without the other), the delivery plan carries
// non-empty requested and effective modes, and the attestation reference
// binds a non-empty project and home.
func validateDeliveryDefinition(agg Aggregate) error {
	if agg.DeliveryPlan == nil && agg.CapabilityAttestation == nil {
		return nil
	}
	if agg.DeliveryPlan == nil || agg.CapabilityAttestation == nil {
		return validationError("task %s/%s has a delivery plan without an attestation reference (or vice versa)", agg.TaskID, agg.Generation)
	}
	if err := validateDeliveryPlan(*agg.DeliveryPlan); err != nil {
		return err
	}
	if err := validateAttestationReference(*agg.CapabilityAttestation); err != nil {
		return err
	}
	return nil
}

// validateDeliveryPlan checks one generation-bound delivery plan: requested
// and effective modes are required safe identities, and a mode fallback must
// carry its reason so a mode change is never silent.
func validateDeliveryPlan(plan DeliveryPlan) error {
	if err := validateDeliveryMode(plan.RequestedMode); err != nil {
		return err
	}
	if err := validateDeliveryMode(plan.EffectiveMode); err != nil {
		return err
	}
	if plan.RequestedMode != plan.EffectiveMode && strings.TrimSpace(plan.FallbackReason) == "" {
		return validationError("delivery plan fallback from %q to %q requires a fallback reason", plan.RequestedMode, plan.EffectiveMode)
	}
	return nil
}

// validateDeliveryMode accepts a safe non-empty delivery mode identity.
func validateDeliveryMode(mode string) error {
	if mode == "" || mode != strings.TrimSpace(mode) || strings.ContainsAny(mode, `/\\`) {
		return validationError("invalid delivery mode %q", mode)
	}
	return nil
}

// validateAttestationReference checks the generation-bound capability
// attestation reference: it binds a non-empty project and execution home. The
// config snapshot digest is optional (spawns without typed config carry none)
// but must be a safe value when present.
func validateAttestationReference(ref CapabilityAttestation) error {
	if strings.TrimSpace(ref.Project) == "" {
		return validationError("capability attestation reference missing project")
	}
	if strings.TrimSpace(ref.Home) == "" {
		return validationError("capability attestation reference missing home")
	}
	if ref.ConfigDigest != "" && strings.ContainsAny(ref.ConfigDigest, `/\\`) {
		return validationError("capability attestation reference has unsafe config digest")
	}
	return nil
}

// validateReconciliationResult checks one provider evidence entry against the
// link it describes: the status must be a known reconciliation outcome and the
// result must reference the exact link definition at the same index.
func validateReconciliationResult(result domain.IssueLinkReconciliationResult, link domain.IssueLink) error {
	switch result.Status {
	case domain.IssueLinkClosed, domain.IssueLinkPending, domain.IssueLinkOpen, domain.IssueLinkUnavailable, domain.IssueLinkManualPolicy:
	default:
		return validationError("issue link reconciliation result has unknown status %q", result.Status)
	}
	if result.Link != link {
		return validationError("issue link reconciliation result does not match link %s", link.URL)
	}
	return nil
}
