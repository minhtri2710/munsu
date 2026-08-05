package fleet

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// This file owns the single Fleet-owned journaled delivery execution path
// (ADR-0008 §3, §9): Deliver executes exactly one irreversible provider
// delivery operation over the canonical Delivery Authorization API. Durable
// journal intent precedes every mutation; the canonical authorization is
// issued under a pinned operation identity; DeliveryCurrency is verified
// immediately before each irreversible provider mutation and invalid
// currency fails closed before mutation; the provider mutation executes at
// most once per journal; a crash after mutation recovers by provider
// observation and classification (never a blind repeat); and the truthful
// closed-set outcome (completed, partial, remote-unknown, retryable)
// commits canonically and replays idempotently. No .meta/delivery_state
// projection authorizes delivery or merged truth.

// DeliverRequest is the typed intent of one journaled delivery execution.
// The request pins the irreversible operation kind, the exact typed delivery
// identity/head the delivery executes under, the provider merge method, and
// the closed-set preconditions Fleet asserts were verified before
// authorization.
type DeliverRequest struct {
	// Kind is the irreversible delivery operation kind. Only
	// DeliveryAuthorizationProviderMerge is supported by the Fleet delivery
	// execution path; repository-mutation kinds fail closed before any
	// journal intent (no typed repository capability exists).
	Kind taskauthority.DeliveryAuthorizationKind
	// Identity is the exact typed delivery identity (provider, owner, repo,
	// number, base/head ref, head SHA, URL) the delivery executes under. Its
	// head must equal the bound worktree head (the canonical authorization
	// gates this).
	Identity domain.DeliveryIdentity
	// Method is the provider merge method: squash (default), merge, or
	// rebase.
	Method string
	// Preconditions is the non-empty closed-set of typed delivery
	// preconditions Fleet asserts were verified before authorization
	// (pr-mergeable, pr-head-current, worktree-clean).
	Preconditions []taskauthority.DeliveryPrecondition
}

// DeliverResult is the committed truthful outcome of one journaled delivery
// execution. Status is a closed-set canonical status: completed, partial,
// remote-unknown, or retryable.
type DeliverResult struct {
	TaskID                   string
	Status                   taskauthority.DeliveryOutcomeStatus
	Detail                   string
	HeadSHA                  string
	MergedSHA                string
	AuthorizationOperationID string
	OutcomeOperationID       string
	Replayed                 bool
}

// IsError reports whether the committed outcome is not completed (partial,
// remote-unknown, retryable are non-zero outcomes).
func (r *DeliverResult) IsError() bool {
	if r == nil {
		return true
	}
	return r.Status != taskauthority.DeliveryOutcomeCompleted
}

// Render returns a human-readable summary of the delivery result with a
// machine-readable AXI block at the end for agent consumption.
func (r *DeliverResult) Render() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	switch r.Status {
	case taskauthority.DeliveryOutcomeCompleted:
		fmt.Fprintf(&b, "Delivery completed: provider confirms merged")
		if r.MergedSHA != "" {
			fmt.Fprintf(&b, " (merged=%s)", r.MergedSHA)
		}
		b.WriteString("\n")
	case taskauthority.DeliveryOutcomeRetryable:
		fmt.Fprintf(&b, "%s\n", r.Detail)
		b.WriteString("Next: re-capture the delivery identity after new changes, then retry delivery\n")
	case taskauthority.DeliveryOutcomePartial, taskauthority.DeliveryOutcomeRemoteUnknown:
		fmt.Fprintf(&b, "%s\n", r.Detail)
		if r.Status == taskauthority.DeliveryOutcomeRemoteUnknown {
			b.WriteString("Same irreversible mutation will not be repeated. Escalate to operator.\n")
		}
	default:
		if r.Detail != "" {
			fmt.Fprintf(&b, "%s\n", r.Detail)
		}
	}
	b.WriteString("\ndelivery:\n")
	fmt.Fprintf(&b, "  status: %s\n", r.Status)
	if r.MergedSHA != "" {
		fmt.Fprintf(&b, "  merged-sha: %s\n", r.MergedSHA)
	}
	if r.HeadSHA != "" {
		fmt.Fprintf(&b, "  head-sha: %s\n", r.HeadSHA)
	}
	fmt.Fprintf(&b, "  authorization-op: %s\n", r.AuthorizationOperationID)
	fmt.Fprintf(&b, "  outcome-op: %s\n", r.OutcomeOperationID)
	fmt.Fprintf(&b, "  replayed: %t\n", r.Replayed)
	return b.String()
}

// DeliveryProviderObservation is the typed read-only provider observation
// used to reconcile a delivery against remote truth after an irreversible
// mutation. State is one of OPEN, MERGED, CLOSED.
type DeliveryProviderObservation struct {
	State     string `json:"state"`
	HeadSHA   string `json:"head_sha,omitempty"`
	MergedSHA string `json:"merged_sha,omitempty"`
}

// DeliveryProvider is the one narrow typed Fleet capability consumed by
// Deliver, with separate observation and irreversible mutation methods.
// GitHub (gh-axi) and GitLab (glab) real adapters implement the supported
// provider merge path. Unsupported providers/kinds fail closed before any
// journal mutation authorization; there is no default provider, raw CLI
// fallback, shell script, or alternate execution route.
type DeliveryProvider interface {
	// Merge executes the irreversible provider merge under the exact
	// identity. It is called at most once per journal.
	Merge(ident domain.DeliveryIdentity, method string) error
	// Observe reads the current provider state under the exact identity.
	Observe(ident domain.DeliveryIdentity) (DeliveryProviderObservation, error)
}

// DeliveryFailClosedError is the typed fail-closed outcome of a delivery
// whose authorization or currency no longer permits the irreversible
// mutation: nothing was mutated and the journal was completed (or remains
// active when the authorization could not be released).
type DeliveryFailClosedError struct {
	TaskID     string
	Reason     string
	ReleaseErr error
}

func (e *DeliveryFailClosedError) Error() string {
	if e.ReleaseErr != nil {
		return fmt.Sprintf("delivery fail-closed for task %s: %s; releasing the delivery authorization failed: %v", e.TaskID, e.Reason, e.ReleaseErr)
	}
	return fmt.Sprintf("delivery fail-closed for task %s: %s", e.TaskID, e.Reason)
}

// deliveryProviderFor resolves the narrow typed delivery capability for the
// identity's provider. Absent, failed, or unsupported capabilities fail
// closed with no fallback or alternate execution route. It is a variable so
// focused tests can substitute a recorded fake; the production resolver
// probes the real typed capabilities.
var deliveryProviderFor = func(ident domain.DeliveryIdentity) (DeliveryProvider, error) {
	switch ident.Provider {
	case "github":
		if st := ProbeGitHubCapability(); st != backend.Ready {
			return nil, fmt.Errorf("GitHub delivery capability is %s (gh-axi must be Ready); no fallback execution route", st)
		}
		return &githubDeliveryProvider{client: &ghAxiClient{}}, nil
	case "gitlab":
		if st := ProbeGitLabCapability(); st != backend.Ready {
			return nil, fmt.Errorf("GitLab delivery capability is %s (glab must be Ready); no fallback execution route", st)
		}
		return &gitlabDeliveryProvider{client: &glabClient{runner: defaultGlabRunner}}, nil
	default:
		return nil, fmt.Errorf("unsupported delivery provider %q", ident.Provider)
	}
}

// Deliver executes one Fleet-owned journaled delivery operation over the
// canonical Delivery Authorization API: durable journal intent first, the
// canonical authorization issuance, a currency check immediately before the
// irreversible provider mutation, an at-most-once provider merge, provider
// observation and truthful closed-set outcome commit, and journal
// completion. Same operations replay idempotently; a crash before mutation
// retries safely and a crash after mutation recovers by observation and
// never repeats the mutation.
func Deliver(homeDir, taskID string, req DeliverRequest) (*DeliverResult, error) {
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = "squash"
	}
	if err := validateDeliverRequest(req, method); err != nil {
		return nil, err
	}
	// Capability probe fails closed BEFORE any journal intent: an absent,
	// failed, or unsupported provider capability never writes a journal and
	// never authorizes a mutation.
	if _, err := deliveryProviderFor(req.Identity); err != nil {
		return nil, err
	}

	h, err := home.Open(homeDir)
	if err != nil {
		return nil, fmt.Errorf("deliver %s: opening home: %w", taskID, err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		return nil, fmt.Errorf("deliver %s: composing task authority: %w", taskID, err)
	}
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		return nil, fmt.Errorf("deliver %s: locking delivery journal: %w", taskID, err)
	}
	defer lk.Release()

	// Converge any pending delivery journals before creating a new one, so an
	// interrupted delivery completes first and the new delivery observes
	// truthful state (the accepted Task Transfer journal mechanics).
	if err := recoverPendingDeliveryJournals(h, lk); err != nil {
		return nil, fmt.Errorf("deliver %s: recovering pending delivery journals: %w", taskID, err)
	}

	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return nil, err
	}
	agg, err := c.Get(tid)
	if err != nil {
		return nil, fmt.Errorf("deliver %s: resolving task: %w", taskID, err)
	}
	if err := prevalidateDeliveryTask(c, agg, req); err != nil {
		return nil, err
	}

	journal, err := buildDeliveryJournal(h.Root(), c, agg, req, method)
	if err != nil {
		return nil, err
	}
	// Durable intent BEFORE the first side effect (authorization issuance).
	if err := writeDeliveryJournal(h, lk, journal); err != nil {
		return nil, fmt.Errorf("deliver %s: writing delivery journal: %w", taskID, err)
	}
	deliveryCrashHook("journal")

	return resumeDeliveryJournal(h, lk, c, journal)
}

// validateDeliverRequest checks the typed delivery intent: a supported
// irreversible kind, a valid typed identity, a supported merge method, and a
// non-empty unique closed-set of typed preconditions.
func validateDeliverRequest(req DeliverRequest, method string) error {
	if req.Kind != taskauthority.DeliveryAuthorizationProviderMerge {
		return fmt.Errorf("delivery kind %q is unsupported: only %q is supported by the Fleet delivery execution path", req.Kind, taskauthority.DeliveryAuthorizationProviderMerge)
	}
	if err := domain.ValidateIdentity(&req.Identity); err != nil {
		return fmt.Errorf("delivery identity is invalid: %w", err)
	}
	switch method {
	case "squash", "merge", "rebase":
	default:
		return fmt.Errorf("unsupported provider merge method %q (squash, merge, rebase)", method)
	}
	if len(req.Preconditions) == 0 {
		return fmt.Errorf("delivery authorization requires at least one typed precondition")
	}
	seen := map[taskauthority.DeliveryPrecondition]bool{}
	for _, p := range req.Preconditions {
		if !p.Valid() {
			return fmt.Errorf("invalid delivery precondition %q", p)
		}
		if seen[p] {
			return fmt.Errorf("duplicate delivery precondition %q", p)
		}
		seen[p] = true
	}
	return nil
}

// prevalidateDeliveryTask mirrors the canonical authorization gates so a
// journal intent is never written for a task that cannot be authorized:
// current working task with owner and the exact bindings, the identity head
// matching the bound worktree head, no matching active delivery hold, no
// terminal committed outcome, and no already-active (unrevoked) delivery
// authorization. The canonical issuance re-fences every rule inside its
// transaction; this pass only avoids writing intent that must abort.
func prevalidateDeliveryTask(c *taskauthority.Canonical, agg taskauthority.Aggregate, req DeliverRequest) error {
	if !agg.Current {
		return fmt.Errorf("task %s is not the current generation", agg.TaskID)
	}
	if agg.Phase != taskauthority.PhaseWorking {
		return fmt.Errorf("delivery requires a working task; task %s is %s", agg.TaskID, agg.Phase)
	}
	if strings.TrimSpace(agg.Definition.Owner) == "" {
		return fmt.Errorf("delivery requires an owner for task %s", agg.TaskID)
	}
	if agg.Worktree == nil || agg.Endpoint == nil {
		return fmt.Errorf("delivery requires the bound worktree and endpoint of task %s", agg.TaskID)
	}
	if agg.Worktree.Head != req.Identity.HeadSHA {
		return fmt.Errorf("delivery identity head %q does not match the bound worktree head %q", req.Identity.HeadSHA, agg.Worktree.Head)
	}
	holds, err := c.ListHolds()
	if err != nil {
		return err
	}
	for _, hold := range holds {
		if hold.Matches(taskauthority.DispatchActionDelivery, agg.TaskID, agg.Definition.Project, agg.Generation.String(), agg.Definition.ParentTaskID) {
			return fmt.Errorf("delivery is held for task %s: %s (%s)", agg.TaskID, hold.ID, hold.Reason)
		}
	}
	tid, err := domain.NewTaskID(agg.TaskID)
	if err != nil {
		return err
	}
	out, err := c.DeliveryOutcome(tid)
	if err == nil {
		if deliveryOutcomeTerminal(out.Status) {
			return fmt.Errorf("task %s already committed terminal delivery outcome %q; a new delivery authorization conflicts", agg.TaskID, out.Status)
		}
	} else if !errors.Is(err, taskauthority.ErrNotFound) {
		return err
	}
	cur, err := c.DeliveryCurrency(tid)
	if err != nil {
		return err
	}
	if cur.Authorization != nil && !isRevokedCurrency(cur) {
		return fmt.Errorf("task %s already has an active delivery authorization %s; release it before a new delivery", agg.TaskID, cur.Authorization.OperationID)
	}
	return nil
}

// isRevokedCurrency reports whether the currency's only reason is that the
// current authorization is revoked (the canonical currency read short-circuits
// with exactly the revoked reason when the authorization pointer carries a
// committed revocation).
func isRevokedCurrency(cur taskauthority.DeliveryCurrency) bool {
	return len(cur.Reasons) == 1 && cur.Reasons[0] == taskauthority.DeliveryCurrencyRevoked
}

// buildDeliveryJournal pins the durable intent of one delivery: the task
// generation/revision precondition, the typed identity/head, the operation
// kind, the provider merge method, the asserted preconditions, and the
// deterministic authorization/revoke/outcome operation identities plus the
// exact request digests.
func buildDeliveryJournal(homeDir string, c *taskauthority.Canonical, agg taskauthority.Aggregate, req DeliverRequest, method string) (*deliveryJournal, error) {
	id, err := newDeliveryJournalID()
	if err != nil {
		return nil, err
	}
	tid, err := domain.NewTaskID(agg.TaskID)
	if err != nil {
		return nil, err
	}
	authorizeReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:        c.HomeID(),
		TaskID:        tid,
		Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:          req.Kind,
		Identity:      req.Identity,
		Preconditions: req.Preconditions,
	}
	authorizeDigest, err := domain.Digest(authorizeReq)
	if err != nil {
		return nil, err
	}
	// The fail-closed release and the retryable-cycle release are the two
	// deterministic revocation intents of one journal (pre-outcome at
	// revision+1, post-outcome at revision+2).
	failClosedRevoke := taskauthority.CanonicalRevokeDeliveryRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   tid,
		Precondition:             domain.Of(uint64(agg.Generation), uint64(agg.Revision)+1),
		AuthorizationOperationID: deliveryAuthorizeOpID(id, agg.TaskID),
		Reason:                   "delivery currency invalid",
	}
	failClosedRevokeDigest, err := domain.Digest(failClosedRevoke)
	if err != nil {
		return nil, err
	}
	retryRevoke := failClosedRevoke
	retryRevoke.Precondition = domain.Of(uint64(agg.Generation), uint64(agg.Revision)+2)
	retryRevoke.Reason = "retryable delivery outcome"
	retryRevokeDigest, err := domain.Digest(retryRevoke)
	if err != nil {
		return nil, err
	}
	preconditions := append([]taskauthority.DeliveryPrecondition(nil), req.Preconditions...)
	return &deliveryJournal{
		Version:                1,
		ID:                     id,
		Phase:                  deliveryPhasePrepared,
		Home:                   homeDir,
		Stage:                  deliveryStageAuthorize,
		TaskID:                 agg.TaskID,
		Generation:             uint64(agg.Generation),
		Revision:               uint64(agg.Revision),
		Kind:                   req.Kind,
		Identity:               req.Identity,
		Method:                 method,
		Preconditions:          preconditions,
		AuthorizeOpID:          deliveryAuthorizeOpID(id, agg.TaskID),
		RevokeOpID:             deliveryRevokeOpID(id, agg.TaskID),
		OutcomeOpID:            deliveryOutcomeOpID(id, agg.TaskID),
		AuthorizeDigest:        authorizeDigest,
		RevokeDigest:           retryRevokeDigest,
		RevokeFailClosedDigest: failClosedRevokeDigest,
	}, nil
}

// resumeDeliveryJournal runs one delivery's idempotent pipeline through the
// canonical primitives and the typed provider capability, then commits the
// journal's terminal truth. Every canonical operation reuses the recorded
// deterministic Operation IDs, so an interrupted delivery continues from any
// durable stage. The ordered invariant is: authorize -> currency check ->
// (at most once) irreversible provider mutation -> truthful outcome commit ->
// journal completion; no stage after "mutating" ever repeats the mutation.
func resumeDeliveryJournal(h *home.Home, lk *home.Lock, c *taskauthority.Canonical, journal *deliveryJournal) (*DeliverResult, error) {
	// A journal already durable at mutating when resume begins is a crash
	// recovery: the irreversible mutation was attempted and must NEVER be
	// repeated. Only a transition performed by this invocation (authorized
	// -> mutating) may execute the merge.
	alreadyMutating := journal.Stage == deliveryStageMutating

	// Stage: authorize — issue the canonical authorization (idempotent
	// replay). A failure means the authorization was never committed under
	// this journal: the intent is abandoned and nothing is unwound.
	if journal.Stage == deliveryStageAuthorize || journal.Stage == "" {
		if err := issueDeliveryAuthorization(c, journal); err != nil {
			_ = abortDeliveryJournal(h, lk, journal, err.Error())
			return nil, err
		}
		if err := transitionDeliveryJournal(h, lk, journal, "authorized", func(j *deliveryJournal) {
			j.Stage = deliveryStageAuthorized
		}); err != nil {
			return nil, err
		}
		deliveryCrashHook("authorized")
	}

	// Stage: authorized — the currency check runs immediately before any
	// irreversible mutation and invalid currency fails closed before it.
	if journal.Stage == deliveryStageAuthorized {
		if err := verifyDeliveryCurrency(c, journal); err != nil {
			return failClosedDelivery(h, lk, c, journal, err)
		}
		provider, err := deliveryProviderFor(journal.Identity)
		if err != nil {
			return failClosedDelivery(h, lk, c, journal, err)
		}
		obs, err := provider.Observe(journal.Identity)
		if err != nil {
			// The provider is unreachable before any mutation: fail closed
			// and release the authorization; no outcome is committed and no
			// irreversible mutation is attempted.
			return failClosedDelivery(h, lk, c, journal, fmt.Errorf("provider observation failed: %w", err))
		}
		if err := verifyProviderHead(journal, obs); err != nil {
			return failClosedDelivery(h, lk, c, journal, err)
		}
		switch obs.State {
		case "MERGED":
			// Committed remote truth; no irreversible mutation is needed.
			return pinAndCommitOutcome(h, lk, c, journal, deriveDeliveryOutcome(journal, obs, nil, nil))
		case "CLOSED":
			return pinAndCommitOutcome(h, lk, c, journal, deriveDeliveryOutcome(journal, obs, nil, nil))
		case "OPEN":
			// Persist the irreversible-mutation boundary, then execute the
			// provider merge exactly once.
			if err := transitionDeliveryJournal(h, lk, journal, "mutating", func(j *deliveryJournal) {
				j.Stage = deliveryStageMutating
			}); err != nil {
				return nil, err
			}
			deliveryCrashHook("mutating")
		default:
			return failClosedDelivery(h, lk, c, journal, fmt.Errorf("provider reported unknown state %q", obs.State))
		}
	}

	// Stage: mutating — the irreversible provider mutation was attempted at
	// most once (either just now, or by a crashed prior run). The merge is
	// NEVER repeated for a journal that was already durable at mutating;
	// recovery observes remote truth and classifies it.
	if journal.Stage == deliveryStageMutating {
		provider, err := deliveryProviderFor(journal.Identity)
		if err != nil {
			return nil, err
		}
		var mergeErr error
		if !alreadyMutating {
			mergeErr = provider.Merge(journal.Identity, journal.Method)
		}
		obs, obsErr := provider.Observe(journal.Identity)
		return pinAndCommitOutcome(h, lk, c, journal, deriveDeliveryOutcome(journal, obs, obsErr, mergeErr))
	}

	// Stage: outcome — the truthful outcome is pinned; commit it (replay
	// idempotently), release the authorization on retryable, and complete.
	if journal.Stage == deliveryStageOutcome {
		return commitPinnedOutcome(h, lk, c, journal)
	}

	return nil, fmt.Errorf("delivery journal %s is in an unresumable stage %q", journal.ID, journal.Stage)
}

// issueDeliveryAuthorization issues the canonical delivery authorization
// under the journal's deterministic operation identity, verifying the pinned
// request digest. Same operation + digest replays idempotently.
func issueDeliveryAuthorization(c *taskauthority.Canonical, journal *deliveryJournal) error {
	tid, err := domain.NewTaskID(journal.TaskID)
	if err != nil {
		return err
	}
	req := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:        c.HomeID(),
		TaskID:        tid,
		Precondition:  domain.Of(journal.Generation, journal.Revision),
		Kind:          journal.Kind,
		Identity:      journal.Identity,
		Preconditions: journal.Preconditions,
	}
	digest, err := domain.Digest(req)
	if err != nil {
		return err
	}
	if journal.AuthorizeDigest != "" && journal.AuthorizeDigest != digest {
		return fmt.Errorf("delivery journal %s authorization digest mismatch", journal.ID)
	}
	op := mustDeliveryOperation(journal.AuthorizeOpID, req)
	if _, err := c.AuthorizeDelivery(op, req); err != nil {
		return fmt.Errorf("delivery authorization issuance: %w", err)
	}
	return nil
}

// verifyDeliveryCurrency evaluates the canonical DeliveryCurrency of the task
// immediately before the irreversible mutation and verifies the EXACT
// authorization identity/kind/head/bindings/holds/preconditions the journal
// pinned. Any invalid or substituted state fails closed before mutation.
func verifyDeliveryCurrency(c *taskauthority.Canonical, journal *deliveryJournal) error {
	tid, err := domain.NewTaskID(journal.TaskID)
	if err != nil {
		return err
	}
	cur, err := c.DeliveryCurrency(tid)
	if err != nil {
		return fmt.Errorf("delivery currency evaluation: %w", err)
	}
	if cur.Authorization == nil {
		return fmt.Errorf("task %s has no delivery authorization; the journal's authorization is not current", journal.TaskID)
	}
	if cur.Authorization.OperationID != journal.AuthorizeOpID {
		return fmt.Errorf("delivery authorization identity mismatch: journal pinned %s but the current authorization is %s", journal.AuthorizeOpID, cur.Authorization.OperationID)
	}
	if cur.Authorization.Kind != journal.Kind {
		return fmt.Errorf("delivery authorization kind mismatch: journal pinned %q but the current authorization is %q", journal.Kind, cur.Authorization.Kind)
	}
	if cur.Authorization.Identity.HeadSHA != journal.Identity.HeadSHA {
		return fmt.Errorf("delivery authorization head mismatch: journal pinned %q but the current authorization binds %q", journal.Identity.HeadSHA, cur.Authorization.Identity.HeadSHA)
	}
	if !equalDeliveryPreconditions(cur.Authorization.Preconditions, journal.Preconditions) {
		return fmt.Errorf("delivery authorization precondition set differs from the journal's asserted preconditions")
	}
	if !cur.Valid {
		return fmt.Errorf("delivery authorization is not current: %v", cur.Reasons)
	}
	return nil
}

// verifyProviderHead fails closed before mutation when the provider reports a
// head different from the pinned delivery identity head (identity/head
// change). Merged observations carry consumed-head evidence and are not
// re-checked; empty provider heads are unverifiable and accepted only for
// merged truth (the classifier still fails closed on unknown states).
func verifyProviderHead(journal *deliveryJournal, obs DeliveryProviderObservation) error {
	if obs.State == "MERGED" || obs.HeadSHA == "" || obs.HeadSHA == journal.Identity.HeadSHA {
		return nil
	}
	return fmt.Errorf("provider head changed since capture: provider reports %q but the delivery identity pins %q", obs.HeadSHA, journal.Identity.HeadSHA)
}

// deliveryOutcomeDerivation is the derived truthful canonical outcome of one
// delivery observation.
type deliveryOutcomeDerivation struct {
	status    taskauthority.DeliveryOutcomeStatus
	detail    string
	headSHA   string
	mergedSHA string
}

// deriveDeliveryOutcome classifies a provider observation into the closed-set
// canonical outcome: merged -> completed, open -> retryable, closed ->
// partial, unknown/unreachable -> remote-unknown. The merge attempt error is
// preserved in the detail when present (the observation decides the status).
func deriveDeliveryOutcome(journal *deliveryJournal, obs DeliveryProviderObservation, obsErr, mergeErr error) deliveryOutcomeDerivation {
	head := obs.HeadSHA
	if head == "" {
		head = journal.Identity.HeadSHA
	}
	if obsErr != nil {
		detail := "provider observation failed: " + obsErr.Error()
		if mergeErr != nil {
			detail += "; merge attempt failed: " + mergeErr.Error()
		}
		return deliveryOutcomeDerivation{status: taskauthority.DeliveryOutcomeRemoteUnknown, detail: detail, headSHA: head}
	}
	switch obs.State {
	case "MERGED":
		return deliveryOutcomeDerivation{status: taskauthority.DeliveryOutcomeCompleted, detail: "provider confirms merged", headSHA: head, mergedSHA: obs.MergedSHA}
	case "OPEN":
		detail := "provider reports open; merge did not take effect"
		if mergeErr != nil {
			detail += "; merge attempt failed: " + mergeErr.Error()
		}
		return deliveryOutcomeDerivation{status: taskauthority.DeliveryOutcomeRetryable, detail: detail, headSHA: head}
	case "CLOSED":
		return deliveryOutcomeDerivation{status: taskauthority.DeliveryOutcomePartial, detail: "provider reports closed but not merged", headSHA: head}
	default:
		return deliveryOutcomeDerivation{status: taskauthority.DeliveryOutcomeRemoteUnknown, detail: "provider reported unknown state " + obs.State, headSHA: head}
	}
}

// pinAndCommitOutcome pins the derived outcome in the journal (stage
// outcome), then commits it canonically and completes the journal. The pin
// precedes the canonical commit so a crash between the two replays the exact
// pinned outcome instead of re-deriving a conflicting one.
func pinAndCommitOutcome(h *home.Home, lk *home.Lock, c *taskauthority.Canonical, journal *deliveryJournal, derivation deliveryOutcomeDerivation) (*DeliverResult, error) {
	if journal.Stage != deliveryStageOutcome {
		req := deliveryOutcomeRequest(c, journal, derivation)
		digest, err := domain.Digest(req)
		if err != nil {
			return nil, err
		}
		if err := transitionDeliveryJournal(h, lk, journal, "outcome", func(j *deliveryJournal) {
			j.Stage = deliveryStageOutcome
			j.OutcomeStatus = derivation.status
			j.OutcomeDetail = derivation.detail
			j.OutcomeHeadSHA = derivation.headSHA
			j.OutcomeMergedSHA = derivation.mergedSHA
			j.OutcomeDigest = digest
		}); err != nil {
			return nil, err
		}
		deliveryCrashHook("outcome")
	}
	return commitPinnedOutcome(h, lk, c, journal)
}

// deliveryOutcomeRequest builds the canonical outcome intent from the
// journal-pinned fields.
func deliveryOutcomeRequest(c *taskauthority.Canonical, journal *deliveryJournal, derivation deliveryOutcomeDerivation) taskauthority.CanonicalDeliveryOutcomeRequest {
	tid, _ := domain.NewTaskID(journal.TaskID)
	return taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   tid,
		Precondition:             domain.Of(journal.Generation, journal.Revision+1),
		AuthorizationOperationID: journal.AuthorizeOpID,
		Status:                   derivation.status,
		Detail:                   derivation.detail,
		HeadSHA:                  derivation.headSHA,
		MergedSHA:                derivation.mergedSHA,
	}
}

// commitPinnedOutcome commits the journal-pinned truthful outcome (replay
// idempotently), releases the authorization on a retryable outcome so a
// later distinct authorization may follow, and completes the journal. A
// canonical outcome commit conflict after mutation resolves to the already
// committed record (partial/remote-unknown stand; completed is never
// fabricated), because the committed terminal truth is never overridden.
func commitPinnedOutcome(h *home.Home, lk *home.Lock, c *taskauthority.Canonical, journal *deliveryJournal) (*DeliverResult, error) {
	if journal.OutcomeStatus == "" {
		return nil, fmt.Errorf("delivery journal %s has no pinned outcome", journal.ID)
	}
	tid, err := domain.NewTaskID(journal.TaskID)
	if err != nil {
		return nil, err
	}
	req := taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   tid,
		Precondition:             domain.Of(journal.Generation, journal.Revision+1),
		AuthorizationOperationID: journal.AuthorizeOpID,
		Status:                   journal.OutcomeStatus,
		Detail:                   journal.OutcomeDetail,
		HeadSHA:                  journal.OutcomeHeadSHA,
		MergedSHA:                journal.OutcomeMergedSHA,
	}
	digest, err := domain.Digest(req)
	if err != nil {
		return nil, err
	}
	if journal.OutcomeDigest != "" && journal.OutcomeDigest != digest {
		return nil, fmt.Errorf("delivery journal %s outcome digest mismatch", journal.ID)
	}
	op := mustDeliveryOperation(journal.OutcomeOpID, req)
	res, err := c.CommitDeliveryOutcome(op, req)
	if err != nil {
		if errors.Is(err, taskauthority.ErrConflict) || errors.Is(err, taskauthority.ErrOperationConflict) {
			// A distinct outcome is already committed under this journal's
			// outcome identity (the first recovery committed the truth and a
			// later observation derived different provider state). The
			// committed record is the truth; never override a terminal
			// outcome and never fabricate completed.
			committed, rerr := c.DeliveryOutcomeByOperation(tid, journal.OutcomeOpID)
			if rerr != nil {
				return nil, fmt.Errorf("resolving committed delivery outcome: %w", rerr)
			}
			res = taskauthority.DeliveryOutcomeResult{Outcome: committed, Replayed: true}
		} else {
			return nil, err
		}
	}
	out := res.Outcome
	deliveryCrashHook("committed")
	// A retryable outcome releases the authorization so the canonical retry
	// cycle (revoke -> re-authorize) may follow.
	if out.Status == taskauthority.DeliveryOutcomeRetryable {
		if err := releaseDeliveryAuthorization(h, lk, c, journal, "retryable delivery outcome", journal.Revision+2); err != nil {
			return nil, err
		}
	}
	if err := completeDeliveryJournal(h, lk, journal, deliveryStageOutcome); err != nil {
		return nil, err
	}
	deliveryCrashHook("completed")
	return &DeliverResult{
		TaskID:                   journal.TaskID,
		Status:                   out.Status,
		Detail:                   out.Detail,
		HeadSHA:                  out.HeadSHA,
		MergedSHA:                out.MergedSHA,
		AuthorizationOperationID: journal.AuthorizeOpID,
		OutcomeOperationID:       out.OperationID,
		Replayed:                 res.Replayed,
	}, nil
}

// failClosedDelivery is the fail-closed branch of a delivery whose currency
// or provider state no longer permits the irreversible mutation: nothing was
// mutated. When the authorization is already released (revoked or no longer
// the current authorization) the journal completes directly; otherwise the
// pinned authorization is released (idempotent) so a later distinct
// authorization may follow, and the journal completes as terminal truth.
// When the release itself fails (the task state moved or a transfer
// reservation is active), the journal stays active and recovery retries once
// state normalizes.
func failClosedDelivery(h *home.Home, lk *home.Lock, c *taskauthority.Canonical, journal *deliveryJournal, cause error) (*DeliverResult, error) {
	tid, err := domain.NewTaskID(journal.TaskID)
	if err != nil {
		return nil, err
	}
	cur, cerr := c.DeliveryCurrency(tid)
	if cerr == nil && (cur.Authorization == nil || isRevokedCurrency(cur)) {
		// The authorization is already released: nothing further to unwind.
		if err := completeDeliveryJournal(h, lk, journal, deliveryStageAuthorized); err != nil {
			return nil, err
		}
		return nil, &DeliveryFailClosedError{TaskID: journal.TaskID, Reason: cause.Error()}
	}
	if err := releaseDeliveryAuthorization(h, lk, c, journal, "delivery currency invalid", journal.Revision+1); err != nil {
		return nil, &DeliveryFailClosedError{TaskID: journal.TaskID, Reason: cause.Error(), ReleaseErr: err}
	}
	if err := completeDeliveryJournal(h, lk, journal, deliveryStageAuthorized); err != nil {
		return nil, err
	}
	return nil, &DeliveryFailClosedError{TaskID: journal.TaskID, Reason: cause.Error()}
}

// releaseDeliveryAuthorization revokes the journal's authorization under its
// pinned operation identity and digest, fenced to the given revision
// precondition. An already-released authorization (conflict) is treated as
// released; any other failure keeps the journal active.
func releaseDeliveryAuthorization(h *home.Home, lk *home.Lock, c *taskauthority.Canonical, journal *deliveryJournal, reason string, revision uint64) error {
	tid, err := domain.NewTaskID(journal.TaskID)
	if err != nil {
		return err
	}
	req := taskauthority.CanonicalRevokeDeliveryRequest{
		HomeID:                   c.HomeID(),
		TaskID:                   tid,
		Precondition:             domain.Of(journal.Generation, revision),
		AuthorizationOperationID: journal.AuthorizeOpID,
		Reason:                   reason,
	}
	digest, err := domain.Digest(req)
	if err != nil {
		return err
	}
	// The journal pins both deterministic release intents: the pre-outcome
	// fail-closed release and the post-outcome retryable-cycle release.
	pinned := journal.RevokeDigest
	if reason == "delivery currency invalid" {
		pinned = journal.RevokeFailClosedDigest
	}
	if pinned != "" && pinned != digest {
		return fmt.Errorf("delivery journal %s revoke digest mismatch", journal.ID)
	}
	op := mustDeliveryOperation(journal.RevokeOpID, req)
	if _, err := c.RevokeDeliveryAuthorization(op, req); err != nil {
		if errors.Is(err, taskauthority.ErrConflict) {
			// Already revoked (or no longer the active authorization): the
			// authorization is released; nothing further to unwind.
			return nil
		}
		return fmt.Errorf("releasing delivery authorization: %w", err)
	}
	return nil
}

// deliveryOutcomeTerminal reports whether the closed-set status is an end
// state of the delivery record.
func deliveryOutcomeTerminal(status taskauthority.DeliveryOutcomeStatus) bool {
	return status == taskauthority.DeliveryOutcomeCompleted || status == taskauthority.DeliveryOutcomePartial || status == taskauthority.DeliveryOutcomeRemoteUnknown
}

// equalDeliveryPreconditions reports whether two closed-set precondition
// collections are equal as sets.
func equalDeliveryPreconditions(a, b []taskauthority.DeliveryPrecondition) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[taskauthority.DeliveryPrecondition]int, len(a))
	for _, p := range a {
		count[p]++
	}
	for _, p := range b {
		if count[p] == 0 {
			return false
		}
		count[p]--
	}
	return true
}
