package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

// --- ExtractPRURL tests ---

func TestExtractPRURL_ValidURL(t *testing.T) {
	url := "https://github.com/minhtri2710/munsu/pull/42"
	msg := "PR " + url
	got, found, err := ExtractPRURL(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for valid PR URL")
	}
	if got != url {
		t.Errorf("got %q, want %q", got, url)
	}
}

func TestExtractPRURL_NoPRPrefix(t *testing.T) {
	msg := "some random message without PR prefix"
	_, found, err := ExtractPRURL(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false for message without PR prefix")
	}
}

func TestExtractPRURL_URLWithoutPrefix(t *testing.T) {
	msg := "https://github.com/minhtri2710/munsu/pull/42"
	_, found, err := ExtractPRURL(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false for URL without PR prefix")
	}
}

func TestExtractPRURL_EmptyAfterPrefix(t *testing.T) {
	msg := "PR "
	_, found, err := ExtractPRURL(msg)
	if err == nil {
		t.Fatal("expected error for empty URL after PR prefix")
	}
	if !strings.Contains(err.Error(), "URL is empty") {
		t.Errorf("expected 'URL is empty' error, got: %v", err)
	}
	if found {
		t.Fatal("expected found=false on error")
	}
}

func TestExtractPRURL_MalformedURL(t *testing.T) {
	msg := "PR not-a-url"
	_, found, err := ExtractPRURL(msg)
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if !strings.Contains(err.Error(), "invalid PR URL") {
		t.Errorf("expected 'invalid PR URL' error, got: %v", err)
	}
	if found {
		t.Fatal("expected found=false on error")
	}
}

func TestExtractPRURL_NonGithubURL(t *testing.T) {
	msg := "PR https://gitlab.com/owner/repo/pull/1"
	_, found, err := ExtractPRURL(msg)
	if err == nil {
		t.Fatal("expected error for non-github URL")
	}
	if !strings.Contains(err.Error(), "not a github.com URL") {
		t.Errorf("expected 'not a github.com URL' error, got: %v", err)
	}
	if found {
		t.Fatal("expected found=false on error")
	}
}

func TestExtractPRURL_Trimmed(t *testing.T) {
	url := "https://github.com/minhtri2710/munsu/pull/42"
	msg := "  PR  " + url + "  "
	got, found, err := ExtractPRURL(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if got != url {
		t.Errorf("got %q, want %q", got, url)
	}
}

// --- captureTerminalIdentityViaProvider tests (injected) ---

func TestCaptureTerminalIdentity_SkipsNonPRMessage(t *testing.T) {
	homeDir := t.TempDir()
	err := CaptureTerminalIdentity(homeDir, "test-task", "task complete, no PR")
	if err != nil {
		t.Fatalf("expected nil for non-PR message: %v", err)
	}
	// No meta should have been written
	_, err = task.ReadMeta(homeDir, "test-task")
	if err == nil {
		t.Fatal("expected error reading meta — no meta should have been written")
	}
}

func TestCaptureTerminalIdentity_IdempotentCompleteIdentity(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-idempotent"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write a complete identity to meta first (simulating a previous capture)
	existingIdent := validIdentity()
	if err := task.WriteMeta(homeDir, taskID, existingIdent.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Capture again — should be idempotent
	savedCapture := captureTerminalIdentity
	captureTerminalIdentity = func(prURL string) (*DeliveryIdentity, error) {
		t.Fatal("captureTerminalIdentity should not be called when identity already exists")
		return nil, nil
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err != nil {
		t.Fatalf("expected nil for idempotent capture: %v", err)
	}

	// Verify meta was not rewritten (timestamp should be unchanged)
	meta, err := task.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("reading meta: %v", err)
	}
	if meta["pr_timestamp"] != existingIdent.CapturedAt {
		t.Errorf("timestamp changed on idempotent capture: got %q, want %q", meta["pr_timestamp"], existingIdent.CapturedAt)
	}
}

func TestCaptureTerminalIdentity_IdempotentWithLegacyKeys(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-legacy-idempotent"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write identity using only legacy keys (pr_head, pr_base, no pr_head_sha/pr_base_ref)
	meta := map[string]string{
		"pr_url":       prURL,
		"pr_provider":  "github",
		"pr_owner":     "minhtri2710",
		"pr_repo":      "munsu",
		"pr_number":    "42",
		"pr_head":      "abc123def456abc123def456abc123def456abc1",
		"pr_base":      "main",
		"pr_head_ref":  "fm/feature-branch",
		"pr_timestamp": "2026-07-18T00:00:00Z",
	}
	if err := task.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Capture again — should be idempotent (IdentityFromMeta resolves legacy keys)
	savedCapture := captureTerminalIdentity
	captureTerminalIdentity = func(prURL string) (*DeliveryIdentity, error) {
		t.Fatal("captureTerminalIdentity should not be called when identity already exists via legacy keys")
		return nil, nil
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err != nil {
		t.Fatalf("expected nil for idempotent capture: %v", err)
	}
}

func TestCaptureTerminalIdentity_URLConflictFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-conflict"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write identity with a DIFFERENT URL
	meta := map[string]string{
		"pr_url":      "https://github.com/minhtri2710/munsu/pull/99",
		"pr_provider": "github",
	}
	if err := task.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for URL conflict")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("expected 'conflicts' error, got: %v", err)
	}
}

func TestCaptureTerminalIdentity_IncompleteIdentityReplaced(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-incomplete-replace"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write incomplete identity (has pr_url but missing other fields)
	meta := map[string]string{
		"pr_url":    prURL,
		"pr_number": "42",
		"pr_head":   "",
		// Missing pr_base, pr_head_ref, pr_timestamp, pr_provider, pr_owner, pr_repo
	}
	if err := task.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Capture with injected provider
	savedCapture := captureTerminalIdentity
	captureTerminalIdentity = func(url string) (*DeliveryIdentity, error) {
		return &DeliveryIdentity{
			Provider:   "github",
			Owner:      "minhtri2710",
			Repo:       "munsu",
			Number:     42,
			URL:        prURL,
			BaseRef:    "main",
			HeadRef:    "fm/feature/test",
			HeadSHA:    "abc123def456abc123def456abc123def456abc1",
			CapturedAt: "2026-07-19T00:00:00Z",
		}, nil
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err != nil {
		t.Fatalf("expected nil for replacement: %v", err)
	}

	// Verify meta now has complete identity
	readMeta, err := task.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("reading meta: %v", err)
	}
	if readMeta["pr_head_sha"] != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("pr_head_sha: got %q", readMeta["pr_head_sha"])
	}
	if readMeta["pr_base_ref"] != "main" {
		t.Errorf("pr_base_ref: got %q", readMeta["pr_base_ref"])
	}
	if readMeta["pr_provider"] != "github" {
		t.Errorf("pr_provider: got %q", readMeta["pr_provider"])
	}
}

func TestCaptureTerminalIdentity_ProviderFailureFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-provider-fail"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	savedCapture := captureTerminalIdentity
	captureTerminalIdentity = func(url string) (*DeliveryIdentity, error) {
		return nil, os.ErrPermission
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for provider failure")
	}
	if !strings.Contains(err.Error(), "capturing from provider") {
		t.Errorf("expected 'capturing from provider' error, got: %v", err)
	}

	// No meta should have been written on failure
	_, metaErr := task.ReadMeta(homeDir, taskID)
	if metaErr == nil {
		t.Fatal("expected meta NOT to exist after provider failure")
	}
}

func TestCaptureTerminalIdentity_ProviderEmptyHeadSHA(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-empty-sha"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	savedCapture := captureTerminalIdentity
	captureTerminalIdentity = func(url string) (*DeliveryIdentity, error) {
		// Return identity with empty HeadSHA — ValidateIdentity should reject it
		return &DeliveryIdentity{
			Provider:   "github",
			Owner:      "minhtri2710",
			Repo:       "munsu",
			Number:     42,
			URL:        prURL,
			BaseRef:    "main",
			HeadRef:    "fm/test",
			HeadSHA:    "",
			CapturedAt: "2026-07-19T00:00:00Z",
		}, nil
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for empty HeadSHA")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("expected 'incomplete' error, got: %v", err)
	}
}

func TestCaptureTerminalIdentity_MetaWriteFailureFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-meta-fail"

	// Make state dir a file so WriteMeta fails
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(filepath.Dir(stateDir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir, []byte("not-a-dir"), 0644); err != nil {
		t.Fatal(err)
	}

	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	savedCapture := captureTerminalIdentity
	captureTerminalIdentity = func(url string) (*DeliveryIdentity, error) {
		return validIdentity(), nil
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for meta write failure")
	}
	if !strings.Contains(err.Error(), "persisting") {
		t.Errorf("expected 'persisting' error, got: %v", err)
	}
}

func TestCaptureTerminalIdentity_SuccessfulCapture(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-successful"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	savedCapture := captureTerminalIdentity
	ident := &DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        prURL,
		BaseRef:    "main",
		HeadRef:    "fm/feature/test",
		HeadSHA:    "abc123def456abc123def456abc123def456abc1",
		CapturedAt: "2026-07-19T00:00:00Z",
	}
	captureTerminalIdentity = func(url string) (*DeliveryIdentity, error) {
		return ident, nil
	}
	defer func() { captureTerminalIdentity = savedCapture }()

	err := CaptureTerminalIdentity(homeDir, taskID, "PR "+prURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all fields were written
	meta, err := task.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("reading meta: %v", err)
	}

	checks := []struct {
		key  string
		want string
	}{
		{"pr_url", prURL},
		{"pr_provider", "github"},
		{"pr_owner", "minhtri2710"},
		{"pr_repo", "munsu"},
		{"pr_number", "42"},
		{"pr_base_ref", "main"},
		{"pr_base", "main"},
		{"pr_head_ref", "fm/feature/test"},
		{"pr_head_sha", "abc123def456abc123def456abc123def456abc1"},
		{"pr_head", "abc123def456abc123def456abc123def456abc1"},
		{"pr_timestamp", "2026-07-19T00:00:00Z"},
		// Legacy keys
		{"pr", prURL},
	}
	for _, c := range checks {
		if meta[c.key] != c.want {
			t.Errorf("%s: got %q, want %q", c.key, meta[c.key], c.want)
		}
	}
}
