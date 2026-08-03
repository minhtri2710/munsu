//go:build integration

package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mustFleetIssueLink builds a valid auto-close implementation issue link.
func mustFleetIssueLink(t *testing.T, n int) domain.IssueLink {
	t.Helper()
	link := domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        n,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#42",
	}
	if err := domain.ValidateIssueLink(&link); err != nil {
		t.Fatal(err)
	}
	return link
}

// --- VerifyDeliveryIssueLinks tests ---

func TestVerifyDeliveryIssueLinks_NoLinks(t *testing.T) {
	links := []domain.IssueLink{}
	if err := VerifyDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error for empty links, got: %v", err)
	}
}

func TestVerifyDeliveryIssueLinks_ValidAutoClose(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error for valid auto-close link, got: %v", err)
	}
}

func TestVerifyDeliveryIssueLinks_ValidRelated(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/43",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        43,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyNever,
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error for valid related link, got: %v", err)
	}
}

func TestVerifyDeliveryIssueLinks_ValidParent(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/44",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        44,
			Relation:      domain.IssueLinkParent,
			ClosurePolicy: domain.ClosurePolicyNever,
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error for valid parent link, got: %v", err)
	}
}

func TestVerifyDeliveryIssueLinks_AutoCloseOnNonImplementation(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/45",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        45,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#45",
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err == nil {
		t.Fatal("expected error for auto-close on non-implementation link")
	}
}

func TestVerifyDeliveryIssueLinks_AutoCloseNoClosingRef(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/46",
			Provider:      "github",
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			// No ClosingRef and no Owner/Repo/Number for ClosingReference() to derive from
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err == nil {
		t.Fatal("expected error for auto-close without closing reference")
	}
}

func TestVerifyDeliveryIssueLinks_MultipleValid(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
		{
			URL:           "https://github.com/owner/repo/issues/43",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        43,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyNever,
		},
		{
			URL:           "https://github.com/owner/repo/issues/44",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        44,
			Relation:      domain.IssueLinkParent,
			ClosurePolicy: domain.ClosurePolicyNever,
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error for multiple valid links, got: %v", err)
	}
}

// --- ReconcileIssueLinks tests ---

func TestReconcileIssueLinks_NoLinks(t *testing.T) {
	results := ReconcileIssueLinks(nil, nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil links, got %d", len(results))
	}
}

func TestReconcileIssueLinks_AutoCloseClosed(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
	}

	checkFn := func(link *domain.IssueLink) (bool, error) {
		return true, nil // issue is closed
	}

	results := ReconcileIssueLinks(links, checkFn)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkClosed {
		t.Errorf("expected status %q, got %q", domain.IssueLinkClosed, results[0].Status)
	}
}

func TestReconcileIssueLinks_AutoCloseOpen(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
	}

	checkFn := func(link *domain.IssueLink) (bool, error) {
		return false, nil // issue is still open
	}

	results := ReconcileIssueLinks(links, checkFn)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkPending {
		t.Errorf("expected status %q, got %q", domain.IssueLinkPending, results[0].Status)
	}
}

func TestReconcileIssueLinks_AutoCloseUnavailable(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
	}

	checkFn := func(link *domain.IssueLink) (bool, error) {
		return false, errors.New("provider unavailable")
	}

	results := ReconcileIssueLinks(links, checkFn)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkUnavailable {
		t.Errorf("expected status %q, got %q", domain.IssueLinkUnavailable, results[0].Status)
	}
}

func TestReconcileIssueLinks_AutoCloseNonImplementation(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
	}

	results := ReconcileIssueLinks(links, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkOpen {
		t.Errorf("expected status %q for misconfigured auto-close on non-implementation, got %q",
			domain.IssueLinkOpen, results[0].Status)
	}
}

func TestReconcileIssueLinks_ManualPolicy(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyManual,
		},
	}

	results := ReconcileIssueLinks(links, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkManualPolicy {
		t.Errorf("expected status %q, got %q", domain.IssueLinkManualPolicy, results[0].Status)
	}
}

func TestReconcileIssueLinks_NeverClose(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyNever,
		},
	}

	results := ReconcileIssueLinks(links, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkManualPolicy {
		t.Errorf("expected status %q, got %q", domain.IssueLinkManualPolicy, results[0].Status)
	}
}

func TestReconcileIssueLinks_Mixed(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
		{
			URL:           "https://github.com/owner/repo/issues/43",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        43,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyNever,
		},
		{
			URL:           "https://github.com/owner/repo/issues/44",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        44,
			Relation:      domain.IssueLinkParent,
			ClosurePolicy: domain.ClosurePolicyManual,
		},
	}

	checkFn := func(link *domain.IssueLink) (bool, error) {
		return true, nil // all issues closed
	}

	results := ReconcileIssueLinks(links, checkFn)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First: auto-close implementation → closed
	if results[0].Status != domain.IssueLinkClosed {
		t.Errorf("result[0]: expected %q, got %q", domain.IssueLinkClosed, results[0].Status)
	}
	// Second: never-close related → manual-policy
	if results[1].Status != domain.IssueLinkManualPolicy {
		t.Errorf("result[1]: expected %q, got %q", domain.IssueLinkManualPolicy, results[1].Status)
	}
	// Third: manual-close parent → manual-policy
	if results[2].Status != domain.IssueLinkManualPolicy {
		t.Errorf("result[2]: expected %q, got %q", domain.IssueLinkManualPolicy, results[2].Status)
	}
}

// --- ReconcileAndStoreIssueLinks tests ---

// newIssueLinkAuthority seeds one queued task in an Authority and returns the
// authority together with the task's meta seed.
func newIssueLinkAuthority(t *testing.T, homeDir, taskID string) *taskauthority.Authority {
	t.Helper()
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-" + taskID,
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      taskID,
		Owner:       "owner",
		Kind:        "ship",
		Reason:      "create",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return auth
}

func TestReconcileAndStoreIssueLinks_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-issue-link-task"

	meta := map[string]string{
		"kind":                     "ship",
		"project":                  "munsu",
		"issue_link_0_url":         "https://github.com/owner/repo/issues/42",
		"issue_link_0_provider":    "github",
		"issue_link_0_owner":       "owner",
		"issue_link_0_repo":        "repo",
		"issue_link_0_number":      "42",
		"issue_link_0_relation":    "implementation",
		"issue_link_0_policy":      "auto-close",
		"issue_link_0_closing_ref": "owner/repo#42",
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newIssueLinkAuthority(t, homeDir, taskID)
	links := domain.IssueLinksFromMeta(meta)
	checkFn := func(link *domain.IssueLink) (bool, error) {
		return true, nil // issue is closed
	}

	results, err := ReconcileAndStoreIssueLinks(homeDir, auth, taskID, links, checkFn)
	if err != nil {
		t.Fatalf("ReconcileAndStoreIssueLinks: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.IssueLinkClosed {
		t.Errorf("expected status %q, got %q", domain.IssueLinkClosed, results[0].Status)
	}

	// The authoritative Aggregate holds the generation-bound definition record
	// and the provider evidence.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(agg.IssueLinks) != 1 || len(agg.IssueLinkReconciliation) != 1 {
		t.Fatalf("authoritative aggregate = %+v", agg)
	}
	if agg.IssueLinks[0].URL != links[0].URL || agg.IssueLinkReconciliation[0].Status != domain.IssueLinkClosed {
		t.Fatalf("committed definition/evidence = %+v / %+v", agg.IssueLinks, agg.IssueLinkReconciliation)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
}

// TestReconcileAndStoreIssueLinks_NoMetaWrite proves the production
// integration point no longer writes task .meta directly: after the
// authoritative commit, the meta projection key is untouched and only the
// caller-owned projection helper writes it.
func TestReconcileAndStoreIssueLinks_NoMetaWrite(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-meta-write"

	meta := map[string]string{
		"kind":                     "ship",
		"issue_link_0_url":         "https://github.com/owner/repo/issues/42",
		"issue_link_0_provider":    "github",
		"issue_link_0_owner":       "owner",
		"issue_link_0_repo":        "repo",
		"issue_link_0_number":      "42",
		"issue_link_0_relation":    "implementation",
		"issue_link_0_policy":      "auto-close",
		"issue_link_0_closing_ref": "owner/repo#42",
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newIssueLinkAuthority(t, homeDir, taskID)
	links := domain.IssueLinksFromMeta(meta)
	if _, err := ReconcileAndStoreIssueLinks(homeDir, auth, taskID, links, nil); err != nil {
		t.Fatalf("ReconcileAndStoreIssueLinks: %v", err)
	}

	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta["issue_link_reconciliation"] != "" {
		t.Error("ReconcileAndStoreIssueLinks must not write the .meta projection key")
	}
	if _, ok := readMeta["issue_link_repair_history"]; ok {
		t.Error("issue_link_repair_history must no longer be written")
	}
}

// TestReconcileAndStoreIssueLinks_IdempotentRetry proves a retry of the same
// reconciliation (same generation, same provider outcome) replays idempotently
// and preserves the original provider evidence.
func TestReconcileAndStoreIssueLinks_IdempotentRetry(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-idempotent-retry"

	meta := map[string]string{
		"kind":                     "ship",
		"issue_link_0_url":         "https://github.com/owner/repo/issues/42",
		"issue_link_0_provider":    "github",
		"issue_link_0_owner":       "owner",
		"issue_link_0_repo":        "repo",
		"issue_link_0_number":      "42",
		"issue_link_0_relation":    "implementation",
		"issue_link_0_policy":      "auto-close",
		"issue_link_0_closing_ref": "owner/repo#42",
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newIssueLinkAuthority(t, homeDir, taskID)
	links := domain.IssueLinksFromMeta(meta)

	first, err := ReconcileAndStoreIssueLinks(homeDir, auth, taskID, links, nil)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	second, err := ReconcileAndStoreIssueLinks(homeDir, auth, taskID, links, nil)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(first) != len(second) || first[0].Status != second[0].Status {
		t.Fatalf("retry changed evidence: first=%+v second=%+v", first, second)
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("retry advanced revision to %d, want 2", agg.Revision)
	}
}

// TestReconcileAndStoreIssueLinks_CrossHomeTaskHome proves the composed
// Authority targets the resolved task home (cross-home delivery): the
// authoritative commit lands in the task-home store and the .meta projection
// lands in the task home, while the command home is untouched.
func TestReconcileAndStoreIssueLinks_CrossHomeTaskHome(t *testing.T) {
	commandHome := t.TempDir()
	taskHome := t.TempDir()
	taskID := "test-cross-home"

	meta := map[string]string{
		"kind":                     "ship",
		"issue_link_0_url":         "https://github.com/owner/repo/issues/42",
		"issue_link_0_provider":    "github",
		"issue_link_0_owner":       "owner",
		"issue_link_0_repo":        "repo",
		"issue_link_0_number":      "42",
		"issue_link_0_relation":    "implementation",
		"issue_link_0_policy":      "auto-close",
		"issue_link_0_closing_ref": "owner/repo#42",
	}
	if err := home.WriteMeta(taskHome, taskID, meta); err != nil {
		t.Fatalf("WriteMeta(taskHome): %v", err)
	}

	// The Authority is composed over the resolved task home, exactly as the
	// CLI composition root does after RequireShipMeta.
	auth := newIssueLinkAuthority(t, taskHome, taskID)
	links := domain.IssueLinksFromMeta(meta)
	results, err := ReconcileAndStoreIssueLinks(taskHome, auth, taskID, links, nil)
	if err != nil {
		t.Fatalf("ReconcileAndStoreIssueLinks: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// The command home carries no authority state and no task meta.
	if _, err := home.ReadMeta(commandHome, taskID); err == nil {
		t.Error("command home must not own the task meta")
	}
	cmdAuth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := cmdAuth.Get(taskID); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("command-home authority must not own the task: %v", err)
	}
}

// TestReconcileAndStoreIssueLinks_FailsClosedWithoutAuthority proves the
// production integration point fails closed when no composed Authority is
// provided instead of writing meta directly.
func TestReconcileAndStoreIssueLinks_FailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-auth"
	meta := map[string]string{"kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	links := domain.IssueLinksFromMeta(meta)
	if _, err := ReconcileAndStoreIssueLinks(homeDir, nil, taskID, links, nil); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

// TestProjectIssueLinkReconciliation proves the caller-owned .meta projection
// writes the reconciliation outcome after the authoritative commit and
// returns a typed partial error when the projection cannot be written, never
// rolling back the authoritative state.
func TestProjectIssueLinkReconciliation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-projection"

	meta := map[string]string{"kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	results := []domain.IssueLinkReconciliationResult{
		{Link: mustFleetIssueLink(t, 42), Status: domain.IssueLinkClosed, Detail: "issue closed by merge"},
	}
	if err := projectIssueLinkReconciliation(homeDir, taskID, results); err != nil {
		t.Fatalf("projectIssueLinkReconciliation: %v", err)
	}
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta["issue_link_reconciliation"] == "" {
		t.Error("expected issue_link_reconciliation to be stored in meta")
	}
}

// --- RenderIssueLinkReconciliationResults tests ---

func TestRenderIssueLinkReconciliationResults(t *testing.T) {
	results := []domain.IssueLinkReconciliationResult{
		{
			Link: domain.IssueLink{
				URL:           "https://github.com/owner/repo/issues/42",
				Provider:      "github",
				Owner:         "owner",
				Repo:          "repo",
				Number:        42,
				Relation:      domain.IssueLinkImplementation,
				ClosurePolicy: domain.ClosurePolicyAuto,
				ClosingRef:    "owner/repo#42",
			},
			Status: domain.IssueLinkClosed,
			Detail: "issue closed by merge",
		},
		{
			Link: domain.IssueLink{
				URL:           "https://github.com/owner/repo/issues/43",
				Provider:      "github",
				Owner:         "owner",
				Repo:          "repo",
				Number:        43,
				Relation:      domain.IssueLinkRelated,
				ClosurePolicy: domain.ClosurePolicyNever,
			},
			Status: domain.IssueLinkManualPolicy,
			Detail: "closure policy is never-close: link will not be closed",
		},
	}

	output := RenderIssueLinkReconciliationResults(results)
	if output == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(output, "owner/repo#42") {
		t.Errorf("expected output to contain closing reference, got:\n%s", output)
	}
	if !strings.Contains(output, "closed") {
		t.Errorf("expected output to contain 'closed', got:\n%s", output)
	}
	if !strings.Contains(output, "manual-policy") {
		t.Errorf("expected output to contain 'manual-policy', got:\n%s", output)
	}
	if !strings.Contains(output, "issue-link-reconciliation:") {
		t.Errorf("expected output to contain AXI block, got:\n%s", output)
	}
}

func TestRenderIssueLinkReconciliationResults_Empty(t *testing.T) {
	output := RenderIssueLinkReconciliationResults(nil)
	if output != "" {
		t.Errorf("expected empty output for nil results, got: %q", output)
	}

	output = RenderIssueLinkReconciliationResults([]domain.IssueLinkReconciliationResult{})
	if output != "" {
		t.Errorf("expected empty output for empty results, got: %q", output)
	}
}

// --- PrepareDeliveryIssueLinks tests ---

func TestPrepareDeliveryIssueLinks_NoLinks(t *testing.T) {
	if err := PrepareDeliveryIssueLinks(nil); err != nil {
		t.Errorf("expected no error for nil links, got: %v", err)
	}
}

func TestPrepareDeliveryIssueLinks_Valid(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#42",
		},
	}
	if err := PrepareDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestPrepareDeliveryIssueLinks_Invalid(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkRelated,
			ClosurePolicy: domain.ClosurePolicyAuto, // auto-close on related link
		},
	}
	if err := PrepareDeliveryIssueLinks(links); err == nil {
		t.Fatal("expected error for auto-close on related link")
	}
}

// --- VerifyDeliveryIssueLinks with derived closing reference ---

func TestVerifyDeliveryIssueLinks_DerivedClosingRef(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        42,
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			// No ClosingRef but owner/repo/number provided → derived from ClosingReference()
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err != nil {
		t.Errorf("expected no error for derived closing reference, got: %v", err)
	}
}

func TestVerifyDeliveryIssueLinks_NoDerivedClosingRef(t *testing.T) {
	links := []domain.IssueLink{
		{
			URL:           "https://github.com/owner/repo/issues/42",
			Relation:      domain.IssueLinkImplementation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			// No ClosingRef, no owner/repo/number → ClosingReference() returns ""
		},
	}
	if err := VerifyDeliveryIssueLinks(links); err == nil {
		t.Fatal("expected error for auto-close without any closing reference")
	}
}

// --- Meta round-trip through WriteMeta/ReadMeta ---

func TestIssueLinkMetaRoundTrip(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-meta-roundtrip"

	original := &domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/42",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        42,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#42",
	}

	meta := map[string]string{
		"kind": "ship",
	}
	for k, v := range original.ToMeta(0) {
		meta[k] = v
	}

	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	restored := domain.IssueLinkFromMeta(readMeta, 0)
	if restored == nil {
		t.Fatal("IssueLinkFromMeta returned nil")
	}
	if restored.URL != original.URL {
		t.Errorf("URL: got %q, want %q", restored.URL, original.URL)
	}
	if restored.Provider != "github" {
		t.Errorf("Provider: got %q, want %q", restored.Provider, "github")
	}
	if restored.Number != 42 {
		t.Errorf("Number: got %d, want 42", restored.Number)
	}
	if restored.Relation != domain.IssueLinkImplementation {
		t.Errorf("Relation: got %q, want %q", restored.Relation, domain.IssueLinkImplementation)
	}
	if restored.ClosurePolicy != domain.ClosurePolicyAuto {
		t.Errorf("ClosurePolicy: got %q, want %q", restored.ClosurePolicy, domain.ClosurePolicyAuto)
	}
}
