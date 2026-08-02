package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// mustIssueLink builds a valid auto-close implementation issue link.
func mustIssueLink(t *testing.T, n int) domain.IssueLink {
	t.Helper()
	link := domain.IssueLink{
		URL:           "https://github.com/owner/repo/issues/1",
		Provider:      "github",
		Owner:         "owner",
		Repo:          "repo",
		Number:        n,
		Relation:      domain.IssueLinkImplementation,
		ClosurePolicy: domain.ClosurePolicyAuto,
		ClosingRef:    "owner/repo#1",
	}
	if err := domain.ValidateIssueLink(&link); err != nil {
		t.Fatal(err)
	}
	return link
}

// mustReconcileResult builds one provider evidence entry for the given link.
func mustReconcileResult(t *testing.T, link domain.IssueLink) domain.IssueLinkReconciliationResult {
	t.Helper()
	return domain.IssueLinkReconciliationResult{Link: link, Status: domain.IssueLinkClosed, Detail: "issue closed by merge"}
}

// issueLinkRequest builds a valid ReconcileIssueLinks request for one task.
func issueLinkRequest(taskID string, generation Generation, links []domain.IssueLink, results []domain.IssueLinkReconciliationResult) ReconcileIssueLinksRequest {
	return ReconcileIssueLinksRequest{
		OperationID:        "op-issue-links-" + taskID,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: generation,
		Links:              links,
		Results:            results,
		Reason:             "post-merge reconciliation",
	}
}

// TestReconcileIssueLinksCommitsDefinitionAndEvidence proves one
// ReconcileIssueLinks operation commits the generation-bound issue link
// definition record and the provider reconciliation evidence together with
// exactly one Revision advance, keeps the phase untouched, and persists a
// typed issue-link audit event and a durable idempotency receipt.
func TestReconcileIssueLinksCommitsDefinitionAndEvidence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	links := []domain.IssueLink{mustIssueLink(t, 42)}
	results := []domain.IssueLinkReconciliationResult{mustReconcileResult(t, links[0])}

	res, err := a.ReconcileIssueLinks(issueLinkRequest("t1", 1, links, results))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 2 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("reconcile result = %+v, want revision 2 queued", res)
	}
	if len(res.Results) != 1 || res.Results[0].Status != domain.IssueLinkClosed {
		t.Fatalf("reconcile results = %+v", res.Results)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("issue link commit must not change phase: %q", agg.Phase)
	}
	if len(agg.IssueLinks) != 1 || agg.IssueLinks[0].URL != links[0].URL || agg.IssueLinks[0].ClosurePolicy != domain.ClosurePolicyAuto {
		t.Fatalf("issue link definition = %+v", agg.IssueLinks)
	}
	if len(agg.IssueLinkReconciliation) != 1 || agg.IssueLinkReconciliation[0].Status != domain.IssueLinkClosed {
		t.Fatalf("issue link reconciliation = %+v", agg.IssueLinkReconciliation)
	}

	// A typed issue-link audit event committed with the mutation.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var issueEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditIssueLinks {
			issueEvents = append(issueEvents, ev)
		}
	}
	if len(issueEvents) != 1 {
		t.Fatalf("issue-link audit events = %d, want 1 (%+v)", len(issueEvents), v.Audit)
	}
	ev := issueEvents[0]
	if ev.OperationID != "op-issue-links-t1" || ev.Actor.ID != "test" || ev.TaskID != "t1" ||
		ev.Generation != 1 || ev.Before != "" || ev.After != "" || ev.Reason != "post-merge reconciliation" {
		t.Fatalf("issue-link audit event = %+v", ev)
	}

	// A durable receipt pins the operation.
	var pinned *Receipt
	for i := range v.Receipts {
		if v.Receipts[i].OperationID == "op-issue-links-t1" {
			pinned = &v.Receipts[i]
		}
	}
	if pinned == nil || pinned.Revision != 2 || pinned.Generation != 1 {
		t.Fatalf("receipts = %+v, want pinned op-issue-links-t1 revision 2", v.Receipts)
	}
}

// TestReconcileIssueLinksGenerationFence proves the Expected Generation fence
// rejects a stale generation and a missing task, mutating nothing.
func TestReconcileIssueLinksGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	links := []domain.IssueLink{mustIssueLink(t, 42)}
	results := []domain.IssueLinkReconciliationResult{mustReconcileResult(t, links[0])}

	if _, err := a.ReconcileIssueLinks(issueLinkRequest("t1", 7, links, results)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	if _, err := a.ReconcileIssueLinks(issueLinkRequest("missing", 1, links, results)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || len(agg.IssueLinks) != 0 || len(agg.IssueLinkReconciliation) != 0 {
		t.Fatalf("failed reconciles must not mutate: %+v", agg)
	}
}

// TestReconcileIssueLinksIdempotentReplay proves repeating the same Operation
// ID with the same intent replays the original receipt: the provider evidence
// is preserved, no second audit event commits, and the Revision does not
// advance twice.
func TestReconcileIssueLinksIdempotentReplay(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	req := issueLinkRequest("t1", 1, []domain.IssueLink{mustIssueLink(t, 42)}, []domain.IssueLinkReconciliationResult{mustReconcileResult(t, mustIssueLink(t, 42))})

	first, err := a.ReconcileIssueLinks(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.ReconcileIssueLinks(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("second reconcile must report Replayed=true")
	}
	if second.Revision != first.Revision || second.Generation != first.Generation {
		t.Fatalf("replayed result = %+v, want original %+v", second, first)
	}
	if len(second.Results) != 1 || second.Results[0].Status != first.Results[0].Status {
		t.Fatalf("replayed evidence = %+v, want %+v", second.Results, first.Results)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var issueEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditIssueLinks {
			issueEvents = append(issueEvents, ev)
		}
	}
	if len(issueEvents) != 1 {
		t.Fatalf("replay must not commit a second audit event: %d events", len(issueEvents))
	}
}

// TestReconcileIssueLinksChangedDigestConflicts proves reusing the Operation
// ID with changed provider evidence is a typed non-retryable conflict that
// preserves the original evidence.
func TestReconcileIssueLinksChangedDigestConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	links := []domain.IssueLink{mustIssueLink(t, 42)}
	first := issueLinkRequest("t1", 1, links, []domain.IssueLinkReconciliationResult{mustReconcileResult(t, links[0])})
	if _, err := a.ReconcileIssueLinks(first); err != nil {
		t.Fatal(err)
	}

	// Same Operation ID but the provider outcome changed (closed → pending).
	changed := issueLinkRequest("t1", 1, links, []domain.IssueLinkReconciliationResult{
		{Link: links[0], Status: domain.IssueLinkPending, Detail: "still open"},
	})
	if _, err := a.ReconcileIssueLinks(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest error = %v, want ErrOperationConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.IssueLinkReconciliation) != 1 || agg.IssueLinkReconciliation[0].Status != domain.IssueLinkClosed {
		t.Fatalf("original evidence must be preserved: %+v", agg.IssueLinkReconciliation)
	}
	if agg.Revision != 2 {
		t.Fatalf("conflicting retry must not advance revision: %d", agg.Revision)
	}
}

// TestReconcileIssueLinksParentRelatedNotAutoClose proves parent and related
// links can never be promoted to automatic closure policy: the operation
// fails closed before any mutation.
func TestReconcileIssueLinksParentRelatedNotAutoClose(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	for _, relation := range []domain.IssueLinkRelation{domain.IssueLinkParent, domain.IssueLinkRelated} {
		link := domain.IssueLink{
			URL:           "https://github.com/owner/repo/issues/44",
			Provider:      "github",
			Owner:         "owner",
			Repo:          "repo",
			Number:        44,
			Relation:      relation,
			ClosurePolicy: domain.ClosurePolicyAuto,
			ClosingRef:    "owner/repo#44",
		}
		results := []domain.IssueLinkReconciliationResult{{Link: link, Status: domain.IssueLinkOpen}}
		req := issueLinkRequest("t1", 1, []domain.IssueLink{link}, results)
		if _, err := a.ReconcileIssueLinks(req); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("auto-close %s link error = %v, want ErrInvalidInput", relation, err)
		}
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || len(agg.IssueLinks) != 0 {
		t.Fatalf("rejected promotion must not mutate: %+v", agg)
	}
}

// TestReconcileIssueLinksRejectsMismatchedEvidence proves provider evidence
// must correspond one-to-one with the definition records: a count mismatch, a
// per-index link mismatch, or an unknown status fails closed.
func TestReconcileIssueLinksRejectsMismatchedEvidence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	link := mustIssueLink(t, 42)

	// Count mismatch.
	req := issueLinkRequest("t1", 1, []domain.IssueLink{link}, nil)
	if _, err := a.ReconcileIssueLinks(req); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("count mismatch error = %v, want ErrInvalidInput", err)
	}

	// Per-index link mismatch.
	other := mustIssueLink(t, 43)
	req = issueLinkRequest("t1", 1, []domain.IssueLink{link}, []domain.IssueLinkReconciliationResult{
		{Link: other, Status: domain.IssueLinkClosed},
	})
	if _, err := a.ReconcileIssueLinks(req); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("link mismatch error = %v, want ErrInvalidInput", err)
	}

	// Unknown status.
	req = issueLinkRequest("t1", 1, []domain.IssueLink{link}, []domain.IssueLinkReconciliationResult{
		{Link: link, Status: "half-closed"},
	})
	if _, err := a.ReconcileIssueLinks(req); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown status error = %v, want ErrInvalidInput", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || len(agg.IssueLinks) != 0 {
		t.Fatalf("rejected evidence must not mutate: %+v", agg)
	}
}
