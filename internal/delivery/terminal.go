package delivery

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/ghurl"
	"github.com/minhtri2710/munsu/internal/task"
)

// captureTerminalIdentity is a variable for testing — can be replaced to
// simulate provider responses without real GitHub access.
// Uses the typed provider client only (no degraded gh CLI fallback)
// so terminal identity capture is fail-closed on provider absence.
var captureTerminalIdentity = captureTerminalIdentityViaProvider

// captureTerminalIdentityViaProvider captures identity using only the typed
// GitHub provider client. Rejects the degraded gh CLI fallback path for
// terminal identity capture — provider absence means fail closed.
func captureTerminalIdentityViaProvider(prURL string) (*DeliveryIdentity, error) {
	client, err := DefaultGitHubClient()
	if err != nil {
		return nil, fmt.Errorf("GitHub provider not available: %w", err)
	}
	return client.CaptureIdentity(prURL)
}

// ExtractPRURL extracts a GitHub PR URL from the terminal report message.
// Strict parsing: only the "PR <url>" prefix is accepted to avoid false
// positives from arbitrary message text. Returns the validated URL and true,
// or empty string and false if no PR URL is present.
// Malformed input (PR prefix but invalid URL) returns an error via ghurl.ParseGHURL.
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

	// Validate the URL is a real GitHub PR URL
	_, err := ghurl.ParseGHURL(candidate)
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
	meta, metaErr := task.ReadMeta(homeDir, taskID)
	if metaErr == nil {
		if existingURL, ok := meta["pr_url"]; ok && existingURL != "" {
			// URL already stored — check if it matches the one being reported
			if existingURL != prURL {
				return fmt.Errorf("terminal identity: reported PR URL %q conflicts with stored pr_url %q in meta; use pr-check to update", prURL, existingURL)
			}

			// Check if stored identity is already complete
			if existing, err := IdentityFromMeta(meta); err == nil && existing != nil {
				if err := ValidateIdentity(existing); err == nil {
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
	if err := ValidateIdentity(ident); err != nil {
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
	if err := task.WriteMeta(homeDir, taskID, meta); err != nil {
		return fmt.Errorf("terminal identity: persisting to task meta: %w", err)
	}

	return nil
}
