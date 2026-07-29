package fleet

import (
	"fmt"
	"github.com/minhtri2710/munsu/internal/domain"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
)

// captureTerminalIdentity is a variable for testing — can be replaced to
// simulate provider responses without real GitHub access.
// Uses the typed provider client only (no degraded gh CLI fallback)
// so terminal identity capture is fail-closed on provider absence.
var captureTerminalIdentity = captureTerminalIdentityViaProvider

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

// CaptureTerminalIdentity captures and persists delivery identity from a PR URL
// before a terminal report is considered successful. It runs at the report_cmd
// seam, before any status append, receipt, event, or wake write.
//
// Idempotent: if meta already contains a complete matching identity, it is
// validated and reused without rewriting or changing its timestamp. If the
// reported URL conflicts with stored identity, it fails closed.
//
// Provider/identity failure fails closed: no terminal status, receipt, ack reset,
// event, or wake is produced. Retry is idempotent.
//
// When no PR URL is present in msg (non-PR terminal report), no identity capture
// is attempted — the function returns nil.
func CaptureTerminalIdentity(homeDir, taskID, msg string) error {
	// Extract and validate the PR URL
	prURL, found, err := ExtractPRURL(msg)
	if err != nil {
		return fmt.Errorf("terminal identity: %w", err)
	}
	if !found {
		// Not a PR report — skip identity capture
		return nil
	}

	// Check existing meta for already-complete identity (idempotent retry)
	meta, metaErr := home.ReadMeta(homeDir, taskID)
	if metaErr == nil {
		if existingURL, ok := meta["pr_url"]; ok && existingURL != "" {
			// URL already stored — check if it matches the one being reported
			if existingURL != prURL {
				return fmt.Errorf("terminal identity: reported PR URL %q conflicts with stored pr_url %q in meta; use pr-check to update", prURL, existingURL)
			}

			// Check if stored identity is already complete
			if existing, err := domain.IdentityFromMeta(meta); err == nil && existing != nil {
				if err := domain.ValidateIdentity(existing); err == nil {
					// Complete matching identity already exists — idempotent skip
					return nil
				}
			}
		}
	}

	// Capture fresh from the provider
	ident, err := captureTerminalIdentity(prURL)
	if err != nil {
		return fmt.Errorf("terminal identity: capturing from provider: %w", err)
	}

	// Validate captured identity before persisting
	if err := domain.ValidateIdentity(ident); err != nil {
		return fmt.Errorf("terminal identity: provider returned incomplete data: %w", err)
	}

	// Read existing meta, preserving all fields
	if meta == nil || metaErr != nil {
		meta = make(map[string]string)
	}

	// Write identity fields into the meta map
	for k, v := range ident.ToMeta() {
		meta[k] = v
	}
	// Ensure legacy keys are set
	meta["pr"] = prURL
	meta["pr_head"] = ident.HeadSHA

	// Persist atomically
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return fmt.Errorf("terminal identity: persisting to task meta: %w", err)
	}

	return nil
}
