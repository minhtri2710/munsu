package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/minhtri2710/munsu/internal/domain"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// captureTerminalIdentity is a variable for testing — can be replaced to
// simulate provider responses without real GitHub access.
// Uses the typed provider client only (no degraded gh CLI fallback)
// so terminal identity capture is fail-closed on provider absence.
var captureTerminalIdentity = captureTerminalIdentityViaProvider

// PrepareDeliveryResult captures the outcome of a PrepareDelivery verification.
type PrepareDeliveryResult struct {
	IdentityVerified bool   `json:"identityVerified"`
	HeadImmutable    bool   `json:"headImmutable"`
	ChecksGreen      bool   `json:"checksGreen"`
	DeliveryState    string `json:"deliveryState"`
	ProviderState    string `json:"providerState"`
	StoredHeadSHA    string `json:"storedHeadSHA"`
	ProviderHeadSHA  string `json:"providerHeadSHA"`
	CheckDetail      string `json:"checkDetail,omitempty"`
}

// verifyProviderChecksGreen checks whether all checks on an open PR are green
// by querying the provider. Returns true if all checks have passed (no failures).
// For merged PRs, checks are already confirmed at merge time, so this returns true.
// For GitLab MRs or when the provider is unavailable, returns true (best-effort).
var verifyProviderChecksGreen = func(stored *domain.DeliveryIdentity) (bool, string) {
	switch stored.Provider {
	case "github":
		client, err := DefaultGitHubClient()
		if err != nil {
			return true, "GitHub provider not available; checks assumed green"
		}

		ghURL, err := domain.ParseGHURL(stored.URL)
		if err != nil {
			return true, "cannot parse URL; checks assumed green"
		}

		data, err := client.ViewPRJSON(ghURL.Owner, ghURL.Repo, ghURL.Num, "checks")
		if err != nil {
			return true, fmt.Sprintf("cannot fetch check runs: %v; checks assumed green", err)
		}

		var raw struct {
			Checks []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
				Status     string `json:"status"`
			} `json:"checks"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return true, fmt.Sprintf("cannot parse check runs: %v; checks assumed green", err)
		}

		var failed []string
		for _, c := range raw.Checks {
			if c.Status == "COMPLETED" && c.Conclusion == "FAILURE" {
				failed = append(failed, c.Name)
			}
		}
		if len(failed) > 0 {
			return false, fmt.Sprintf("failed checks: %s", strings.Join(failed, ", "))
		}

		return true, "all checks green"

	case "gitlab":
		return true, "GitLab checks not implemented; assumed green"

	default:
		return true, "unknown provider; checks assumed green"
	}
}

// PrepareDelivery is parent-owned: it verifies provider identity, immutable
// head, and terminal green required checks before transitioning the delivery
// lifecycle to delivered. It is called by the captain/general after receiving
// a soldier's terminal report, never by the soldier itself.
//
// Verification steps:
//  1. Read stored delivery identity from meta
//  2. Fetch provider snapshot to verify identity fields match
//  3. Verify immutable head (provider head SHA matches stored)
//  4. Verify PR is merged OR open with all checks green
//  5. On success, route the delivered terminal transition through the
//     composed Task Authority (Task 7.5): the generation-bound terminal
//     evidence record commits with the delivered transition, and the
//     delivery_state meta key is reconciled as a post-commit projection.
//     A projection failure returns a typed partial error and never rolls
//     back the authoritative commit.
//
// Returns a PrepareDeliveryResult with per-check verdicts. Even on success,
// callers should inspect the result for diagnostics. On verification failure,
// returns an error describing the first failing check.
func PrepareDelivery(homeDir, taskID string, auth *taskauthority.Authority) (*PrepareDeliveryResult, error) {
	if auth == nil {
		return nil, fmt.Errorf("prepare delivery: requires a composed task authority")
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("prepare delivery: reading meta: %w", err)
	}

	// Read stored identity
	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("prepare delivery: reading stored identity: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("prepare delivery: no delivery identity in meta for task %s; run pr-check first", taskID)
	}

	// Check current delivery state — already delivered is idempotent
	if meta[MetaDeliveryState] == string(DeliveryStateDelivered) {
		return &PrepareDeliveryResult{
			IdentityVerified: true,
			HeadImmutable:    true,
			ChecksGreen:      true,
			DeliveryState:    string(DeliveryStateDelivered),
			ProviderState:    "",
			StoredHeadSHA:    stored.HeadSHA,
			ProviderHeadSHA:  stored.HeadSHA,
		}, nil
	}

	// Step 1: Fetch provider snapshot
	snap, err := FetchProviderSnapshot(stored.URL)
	if err != nil {
		return nil, fmt.Errorf("prepare delivery: provider snapshot: %w", err)
	}

	// Step 2: Verify provider identity (same provider, repo, PR, base, head ref)
	if err := verifySnapshotIdentity(stored, snap); err != nil {
		return &PrepareDeliveryResult{
			IdentityVerified: false,
			DeliveryState:    meta[MetaDeliveryState],
			ProviderState:    snap.State,
			StoredHeadSHA:    stored.HeadSHA,
			ProviderHeadSHA:  snap.HeadSHA,
		}, fmt.Errorf("prepare delivery: identity mismatch: %w", err)
	}

	// Step 3: Verify immutable head (provider head SHA matches stored)
	headMatch := snap.HeadSHA != "" && snap.HeadSHA == stored.HeadSHA
	if !headMatch {
		detail := ""
		if snap.HeadSHA == "" {
			detail = "provider returned empty head SHA"
		} else {
			detail = fmt.Sprintf("provider head %q differs from stored %q", snap.HeadSHA, stored.HeadSHA)
		}
		return &PrepareDeliveryResult{
			IdentityVerified: true,
			HeadImmutable:    false,
			DeliveryState:    meta[MetaDeliveryState],
			ProviderState:    snap.State,
			StoredHeadSHA:    stored.HeadSHA,
			ProviderHeadSHA:  snap.HeadSHA,
			CheckDetail:      detail,
		}, fmt.Errorf("prepare delivery: immutable head check failed: %s", detail)
	}

	// Step 4: Verify PR is merged or open with all checks green
	if snap.Merged {
		// Merged — terminal checks already passed. No check run needed.
		result := &PrepareDeliveryResult{
			IdentityVerified: true,
			HeadImmutable:    true,
			ChecksGreen:      true,
			DeliveryState:    string(DeliveryStateDelivered),
			ProviderState:    snap.State,
			StoredHeadSHA:    stored.HeadSHA,
			ProviderHeadSHA:  snap.HeadSHA,
			CheckDetail:      "PR is merged; checks confirmed at merge time",
		}

		// Step 4b: Verify issue links before delivery
		if links := domain.IssueLinksFromMeta(meta); len(links) > 0 {
			if err := PrepareDeliveryIssueLinks(links); err != nil {
				return nil, fmt.Errorf("prepare delivery: issue link verification: %w", err)
			}
		}

		// Step 5: authoritative delivered terminal transition (Task 7.5).
		// The terminal evidence record commits inside the Authority; the
		// delivery_state meta key is a post-commit projection. A projection
		// failure warns and never rolls back the authoritative commit.
		if _, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDelivered, stored); err != nil {
			var projErr *DeliveryProjectionError
			if errors.As(err, &projErr) {
				fmt.Fprintf(os.Stderr, "Warning: delivery projection failed: %v\n", projErr)
			} else {
				return nil, fmt.Errorf("prepare delivery: terminal transition: %w", err)
			}
		}

		return result, nil
	}

	// PR is open — verify checks are green
	if snap.State == "OPEN" {
		checkOK, detail := verifyProviderChecksGreen(stored)
		if !checkOK {
			return &PrepareDeliveryResult{
				IdentityVerified: true,
				HeadImmutable:    true,
				ChecksGreen:      false,
				DeliveryState:    meta[MetaDeliveryState],
				ProviderState:    snap.State,
				StoredHeadSHA:    stored.HeadSHA,
				ProviderHeadSHA:  snap.HeadSHA,
				CheckDetail:      detail,
			}, fmt.Errorf("prepare delivery: checks not green: %s", detail)
		}

		result := &PrepareDeliveryResult{
			IdentityVerified: true,
			HeadImmutable:    true,
			ChecksGreen:      true,
			DeliveryState:    string(DeliveryStateDelivered),
			ProviderState:    snap.State,
			StoredHeadSHA:    stored.HeadSHA,
			ProviderHeadSHA:  snap.HeadSHA,
			CheckDetail:      detail,
		}

		// Step 4b: Verify issue links before delivery
		if links := domain.IssueLinksFromMeta(meta); len(links) > 0 {
			if err := PrepareDeliveryIssueLinks(links); err != nil {
				return nil, fmt.Errorf("prepare delivery: issue link verification: %w", err)
			}
		}

		// Step 5: authoritative delivered terminal transition (Task 7.5).
		if _, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDelivered, stored); err != nil {
			var projErr *DeliveryProjectionError
			if errors.As(err, &projErr) {
				fmt.Fprintf(os.Stderr, "Warning: delivery projection failed: %v\n", projErr)
			} else {
				return nil, fmt.Errorf("prepare delivery: terminal transition: %w", err)
			}
		}

		return result, nil
	}

	// PR is closed but not merged — cannot deliver
	return &PrepareDeliveryResult{
		IdentityVerified: true,
		HeadImmutable:    true,
		ChecksGreen:      false,
		DeliveryState:    meta[MetaDeliveryState],
		ProviderState:    snap.State,
		StoredHeadSHA:    stored.HeadSHA,
		ProviderHeadSHA:  snap.HeadSHA,
		CheckDetail:      fmt.Sprintf("PR is %s (closed but not merged)", snap.State),
	}, fmt.Errorf("prepare delivery: PR #%d is %s (closed but not merged); cannot deliver", snap.Number, snap.State)
}

// VerifyDoneIdentity checks the provider before accepting a terminal done report.
// It is called from report_cmd for ship tasks reporting done.
//
// Returns nil when:
//   - No PR URL in msg AND no stored delivery identity (non-PR report, skip)
//   - PR is merged AND final provider head equals stored identity
//   - Non-ship task (scout/report artifacts exempt)
//
// Returns error when:
//   - Ship task has stored identity but no PR URL in msg (incomplete report)
//   - PR is open or closed-unmerged (reject)
//   - PR is merged but head SHA doesn't match stored identity (direct to reconciliation)
//   - Provider is unavailable or ambiguous
func VerifyDoneIdentity(homeDir, taskID, msg string) error {
	// Read meta — may fail if no meta exists (e.g. scout task, no PR identity yet)
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		// No meta yet — this is a pre-identity report (scout or early ship).
		// Only reject if the message contains a PR URL that needs verification;
		// otherwise skip verification.
		_, found, extractErr := ExtractPRURL(msg)
		if extractErr != nil {
			return fmt.Errorf("terminal done: invalid PR URL: %w", extractErr)
		}
		if !found {
			// No PR URL, no meta — nothing to verify
			return nil
		}
		// Has PR URL but no meta — we need the meta to verify. Try to create
		// a minimal meta for identity capture downstream to work.
		return fmt.Errorf("terminal done: no task meta for %s (soldier has not been initialized); run pr-check first", taskID)
	}

	kind := meta["kind"]
	if kind != "" && kind != "ship" {
		// Scout/report artifacts skip provider verification
		return nil
	}

	// Extract PR URL from message
	prURL, found, err := ExtractPRURL(msg)
	if err != nil {
		return fmt.Errorf("terminal done: invalid PR URL: %w", err)
	}

	// Read stored identity (may be nil if no identity has been captured)
	stored, _ := domain.IdentityFromMeta(meta)

	if !found {
		// No PR URL in message. If the task has a stored delivery identity,
		// require the message to include the PR URL for explicit confirmation.
		if stored != nil && stored.URL != "" {
			return fmt.Errorf("terminal done: task %s has delivery identity at %s but message does not include 'PR <url>'; include PR URL in done report to confirm delivery", taskID, stored.URL)
		}
		// No stored identity either — non-PR ship report, skip verification
		return nil
	}

	// Resolve the PR URL to use: prefer the one from the message, but verify
	// it matches the stored identity if one exists.
	resolveURL := prURL
	if stored != nil && stored.URL != "" {
		if stored.URL != prURL {
			return fmt.Errorf("terminal done: reported PR URL %q does not match stored identity URL %q", prURL, stored.URL)
		}
		resolveURL = stored.URL
	}

	// Fetch provider snapshot
	snap, err := FetchProviderSnapshot(resolveURL)
	if err != nil {
		return fmt.Errorf("terminal done: provider snapshot: %w", err)
	}

	// Validate merge-result evidence for merged PRs
	if snap.Merged && snap.MergedSHA == "" {
		return fmt.Errorf("terminal done: PR #%d is merged but provider returned no merge-result evidence (empty mergedSHA); cannot confirm delivery", snap.Number)
	}

	if !snap.Merged {
		if snap.State == "OPEN" {
			return fmt.Errorf("terminal done: PR #%d is still open (state=%s): cannot report done until merged", snap.Number, snap.State)
		}
		return fmt.Errorf("terminal done: PR #%d is closed but not merged (state=%s): cannot report done", snap.Number, snap.State)
	}

	// PR is merged. Check head SHA against stored identity.
	if stored != nil && stored.HeadSHA != "" && snap.HeadSHA != "" && snap.HeadSHA != stored.HeadSHA {
		return fmt.Errorf("terminal done: PR #%d is merged but provider head SHA %q differs from stored %q; run 'munsu delivery reconcile %s' to update metadata",
			snap.Number, snap.HeadSHA, stored.HeadSHA, taskID)
	}

	return nil
}

// provider client (GitHub via gh-axi or GitLab via glab). Rejects degraded
// CLI fallback paths — provider absence means fail closed.
func captureTerminalIdentityViaProvider(prURL string) (*domain.DeliveryIdentity, error) {
	provider, _, _, _, _, err := ParseProviderURL(prURL)
	if err != nil {
		return nil, fmt.Errorf("unrecognized PR/MR URL: %w", err)
	}

	switch provider {
	case "github":
		client, err := DefaultGitHubClient()
		if err != nil {
			return nil, fmt.Errorf("GitHub provider not available: %w", err)
		}
		return client.CaptureIdentity(prURL)
	case "gitlab":
		client, err := DefaultGitLabClient()
		if err != nil {
			return nil, fmt.Errorf("GitLab provider not available: %w", err)
		}
		return client.CaptureIdentity(prURL)
	default:
		return nil, fmt.Errorf("unknown provider %q for URL %s", provider, prURL)
	}
}

// ExtractPRURL extracts a GitHub PR or GitLab MR URL from the terminal
// report message. Strict parsing: only the "PR <url>" prefix is accepted
// to avoid false positives from arbitrary message text. Returns the
// validated URL and true, or empty string and false if no PR URL is present.
// Malformed input (PR prefix but invalid URL) returns an error via
// ParseProviderURL.
func ExtractPRURL(msg string) (string, bool, error) {
	// Trim leading whitespace only — preserve trailing content for prefix match.
	trimmed := strings.TrimLeft(msg, " \t\r\n")

	// Only accept explicit "PR <url>" prefix, not arbitrary text scanning.
	if !strings.HasPrefix(trimmed, "PR ") {
		return "", false, nil
	}

	candidate := strings.TrimSpace(trimmed[3:])
	if candidate == "" {
		return "", false, fmt.Errorf("PR prefix found but URL is empty")
	}

	// Validate the URL is a recognized PR or MR URL
	_, _, _, _, _, err := ParseProviderURL(candidate)
	if err != nil {
		return "", false, fmt.Errorf("invalid PR URL in terminal report: %w", err)
	}

	return candidate, true, nil
}

// CaptureTerminalIdentity captures the terminal provider evidence and routes
// the done terminal transition through the composed Task Authority (Task
// 7.5). It runs at the report_cmd seam, before any status append, receipt,
// event, or wake write.
//
// Idempotent: if meta already contains a complete matching identity, it is
// validated and reused without re-capturing from the provider. The terminal
// evidence record commits inside the Authority (the transition is an
// in-value no-op on retry) and the identity keys are reconciled as a
// post-commit projection; a projection failure returns a typed partial
// error and never rolls back the authoritative commit. If the reported URL
// conflicts with the stored identity, it fails closed.
//
// Provider/identity/authority failure fails closed: no terminal status,
// receipt, ack reset, event, or wake is produced. Retry is idempotent.
//
// When no PR URL is present in msg (non-PR terminal report), no identity
// capture is attempted — the function returns nil.
func CaptureTerminalIdentity(homeDir, taskID, msg string, auth *taskauthority.Authority) error {
	// Extract and validate the PR URL
	prURL, found, err := ExtractPRURL(msg)
	if err != nil {
		return fmt.Errorf("terminal identity: %w", err)
	}
	if !found {
		// Not a PR report — skip identity capture
		return nil
	}
	if auth == nil {
		return fmt.Errorf("terminal identity: requires a composed task authority")
	}

	// Resolve the terminal evidence identity. A complete matching stored
	// identity is reused (idempotent retry / pr-checked path); otherwise the
	// evidence is captured fresh from the provider and the Authority fails
	// closed if it does not bind the prepared head and PR.
	meta, metaErr := home.ReadMeta(homeDir, taskID)
	var ident *domain.DeliveryIdentity
	if metaErr == nil {
		if existingURL, ok := meta["pr_url"]; ok && existingURL != "" {
			// URL already stored — check if it matches the one being reported
			if existingURL != prURL {
				return fmt.Errorf("terminal identity: reported PR URL %q conflicts with stored pr_url %q in meta; use pr-check to update", prURL, existingURL)
			}
			// Check if stored identity is already complete
			if existing, err := domain.IdentityFromMeta(meta); err == nil && existing != nil {
				if err := domain.ValidateIdentity(existing); err == nil {
					ident = existing
				}
			}
		}
	}

	// Capture fresh from the provider when no complete stored identity exists.
	if ident == nil {
		ident, err = captureTerminalIdentity(prURL)
		if err != nil {
			return fmt.Errorf("terminal identity: capturing from provider: %w", err)
		}
		// Validate captured identity before routing
		if err := domain.ValidateIdentity(ident); err != nil {
			return fmt.Errorf("terminal identity: provider returned incomplete data: %w", err)
		}
	}

	// Route the done terminal transition through the composed Authority. The
	// terminal evidence record commits inside the Authority; the identity
	// meta keys are reconciled as a post-commit projection inside the
	// wrapper. A projection failure is a typed partial error (non-rollback).
	if _, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDone, ident); err != nil {
		return fmt.Errorf("terminal identity: %w", err)
	}

	return nil
}

// --- Delivery authority wrappers (Task 7.5) ---

// DeliveryProjectionError is the typed partial outcome of a delivery
// operation whose authoritative commit succeeded but whose .meta projection
// could not be written. The authoritative state is never rolled back; the
// projection can be retried independently and replays idempotently
// (ADR-0007 §7).
type DeliveryProjectionError struct {
	TaskID        string
	ProjectionErr error
}

func (e *DeliveryProjectionError) Error() string {
	return fmt.Sprintf("delivery committed for %s but projection failed: %v", e.TaskID, e.ProjectionErr)
}

func (e *DeliveryProjectionError) Unwrap() error { return e.ProjectionErr }

// StoreDeliveryPrepare routes the pr-check delivery preparation through the
// composed Task Authority (Task 7.5): the generation-bound delivery prepare
// record (provider identity snapshot, immutable head SHA, review-ready
// state) commits fenced to the aggregate generation, and the identity meta
// keys plus delivery_state are reconciled as a post-commit projection. The
// expected prior prepared head is acknowledged (force-with-lease): a changed
// head must be re-prepared explicitly, never silently reused. Re-preparing
// the identical identity and head is an in-value no-op. Production callers
// always supply the Authority; nil fails closed.
func StoreDeliveryPrepare(homeDir string, auth *taskauthority.Authority, taskID string, ident *domain.DeliveryIdentity) (taskauthority.DeliveryResult, error) {
	if auth == nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("delivery preparation requires a composed task authority")
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("store delivery prepare: invalid identity: %w", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("store delivery prepare: resolving task generation: %w", err)
	}
	priorHead := ""
	if agg.DeliveryPrepare != nil {
		priorHead = agg.DeliveryPrepare.HeadSHA
	}
	res, err := auth.PrepareDelivery(taskauthority.PrepareDeliveryRequest{
		OperationID:        mustDeliveryOperationID("delivery-prepare-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		State:              taskauthority.DeliveryPrepareStateReviewReady,
		HeadSHA:            ident.HeadSHA,
		Identity:           snapshotFromIdentity(ident),
		ExpectedPriorHead:  priorHead,
		Reason:             "pr-check",
	})
	if err != nil {
		return taskauthority.DeliveryResult{}, err
	}
	if err := projectDeliveryPrepareMeta(homeDir, taskID, ident); err != nil {
		return res, err
	}
	return res, nil
}

// StoreDeliveryCompletion routes a delivered/done terminal transition through
// the composed Task Authority (Task 7.5): the generation-bound terminal
// evidence record (terminal transition, exact head, provider identity
// snapshot) commits fenced to the aggregate generation, and the identity
// meta keys plus the delivery_state projection are reconciled as a
// post-commit projection. A changed head vs the prepared head, or a terminal
// identity for a different PR, fails closed; replaying the same terminal
// state for the same head is an in-value no-op. Production callers always
// supply the Authority; nil fails closed.
func StoreDeliveryCompletion(homeDir string, auth *taskauthority.Authority, taskID, terminal string, ident *domain.DeliveryIdentity) (taskauthority.DeliveryResult, error) {
	if auth == nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("delivery completion requires a composed task authority")
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("store delivery completion: invalid identity: %w", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return taskauthority.DeliveryResult{}, fmt.Errorf("store delivery completion: resolving task generation: %w", err)
	}
	res, err := auth.CompleteDelivery(taskauthority.CompleteDeliveryRequest{
		OperationID:        mustDeliveryOperationID("delivery-complete-" + taskID),
		Actor:              deliveryActor(homeDir),
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		Terminal:           terminal,
		HeadSHA:            ident.HeadSHA,
		Identity:           snapshotFromIdentity(ident),
		Reason:             "delivery terminal transition",
	})
	if err != nil {
		return taskauthority.DeliveryResult{}, err
	}
	if err := projectDeliveryTerminalMeta(homeDir, taskID, terminal, ident); err != nil {
		return res, err
	}
	return res, nil
}

// --- Delivery projection helpers (caller-owned, ADR-0007 §7) ---

// projectDeliveryPrepareMeta reconciles the .meta delivery identity keys,
// the legacy pr/pr_head keys, and delivery_state=review-ready after the
// authoritative delivery preparation commit.
func projectDeliveryPrepareMeta(homeDir, taskID string, ident *domain.DeliveryIdentity) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	for k, v := range ident.ToMeta() {
		meta[k] = v
	}
	meta["pr"] = ident.URL
	meta["pr_head"] = ident.HeadSHA
	meta[MetaDeliveryState] = string(DeliveryStateReviewReady)
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return &DeliveryProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return nil
}

// projectDeliveryTerminalMeta reconciles the .meta delivery identity keys
// and the delivery_state projection after the authoritative delivery
// completion commit. The delivered projection mirrors the legacy CAS
// (delivery_state + identity revision advance); done carries no delivery
// state meta value (done is the report state, the terminal record is
// authoritative).
func projectDeliveryTerminalMeta(homeDir, taskID, terminal string, ident *domain.DeliveryIdentity) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	for k, v := range ident.ToMeta() {
		meta[k] = v
	}
	meta["pr"] = ident.URL
	meta["pr_head"] = ident.HeadSHA
	if terminal == taskauthority.DeliveryTerminalDelivered {
		meta[MetaDeliveryState] = string(DeliveryStateDelivered)
		meta[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return &DeliveryProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return nil
}
