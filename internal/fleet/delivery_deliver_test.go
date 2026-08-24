//go:build integration

package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestValidateDeliverRequestRefusesUnsupportedMethod(t *testing.T) {
	if err := validateDeliverRequest(deliverRequest(), "unsupported"); err == nil || !strings.Contains(err.Error(), "unsupported provider merge method") {
		t.Fatalf("validateDeliverRequest error = %v, want unsupported method refusal", err)
	}
}

func TestDeliveryProviderFor_UnknownProviderRefuses(t *testing.T) {
	_, err := deliveryProviderFor(domain.DeliveryIdentity{Provider: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported delivery provider") {
		t.Fatalf("deliveryProviderFor error = %v, want unknown-provider refusal", err)
	}
}

func TestDeliveryProviderFor_GitHubCapabilityAbsentRefuses(t *testing.T) {
	old := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = old })
	ghAxiLookPath = func() (string, error) {
		return "", errors.New("gh-axi not found")
	}

	_, err := deliveryProviderFor(domain.DeliveryIdentity{Provider: "github"})
	if err == nil || !strings.Contains(err.Error(), "gh-axi must be Ready") {
		t.Fatalf("deliveryProviderFor error = %v, want absent GitHub capability refusal", err)
	}
}

// TestDeliverJournalIntentPrecedesAuthorizationAndMutation proves the durable
// journal intent is written before the canonical authorization and the
// irreversible provider mutation, and the completed journal record is
// retained as terminal truth while the active index stays bounded.
func TestDeliverJournalIntentPrecedesAuthorizationAndMutation(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	provider := installScriptedProviderFor(t, "open-then-merged")

	result, err := Deliver(homeDir, taskID, deliverRequest())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result == nil || result.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("result = %+v, want completed", result)
	}
	if provider.merges != 1 {
		t.Fatalf("merges = %d, want exactly 1 (at most once)", provider.merges)
	}

	// The canonical authorization evidence was issued under the exact
	// identity/kind/preconditions.
	auth, err := c.DeliveryAuthorization(mustFleetTaskID(t, taskID))
	if err != nil {
		t.Fatalf("DeliveryAuthorization: %v", err)
	}
	if auth.Kind != taskauthority.DeliveryAuthorizationProviderMerge {
		t.Fatalf("authorization kind = %q, want provider-merge", auth.Kind)
	}
	if auth.Identity.HeadSHA != deliveryTestHead || auth.Identity.Number != 42 {
		t.Fatalf("authorization identity = %+v", auth.Identity)
	}
	if len(auth.Preconditions) != 2 {
		t.Fatalf("authorization preconditions = %v", auth.Preconditions)
	}

	// The active index is empty (journal completed) and exactly one terminal
	// journal record is retained, still discoverable only by its exact
	// identity (never by scan).
	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 0 {
		t.Fatalf("active delivery journals = %v, want none after completion", active)
	}
	files := listDeliveryJournalFiles(t, homeDir)
	if len(files) != 1 {
		t.Fatalf("retained journal records = %v, want exactly 1", files)
	}
	id := strings.TrimSuffix(files[0], ".json")
	journal, err := readDeliveryJournalRecord(t, homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != deliveryPhaseCompleted {
		t.Fatalf("journal phase = %q, want completed (retained terminal truth)", journal.Phase)
	}
	if journal.AuthorizeOpID == "" || journal.OutcomeOpID == "" || journal.RevokeOpID == "" {
		t.Fatalf("journal missing pinned operation identities: %+v", journal)
	}
}

// TestDeliverAuthorizationPinsExactRequestDigests proves the journal pins
// the exact authorization request digest and replay of the same operation is
// idempotent.
func TestDeliverAuthorizationPinsExactRequestDigests(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open-then-merged")

	if _, err := Deliver(homeDir, taskID, deliverRequest()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
	files := listDeliveryJournalFiles(t, homeDir)
	id := strings.TrimSuffix(files[0], ".json")
	journal, err := readDeliveryJournalRecord(t, homeDir, id)
	if err != nil {
		t.Fatal(err)
	}

	// The pinned authorization digest matches the deterministic digest of the
	// exact typed authorization intent.
	tid := mustFleetTaskID(t, taskID)
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
		t.Fatal(err)
	}
	if journal.AuthorizeDigest != digest {
		t.Fatalf("pinned authorize digest %q != derived %q", journal.AuthorizeDigest, digest)
	}

	// Same operation replays idempotently (the canonical receipt).
	if _, err := c.AuthorizeDelivery(mustFleetOperation(t, journal.AuthorizeOpID, req), req); err != nil {
		t.Fatalf("authorize replay: %v", err)
	}
}

// TestDeliverCurrencyCheckImmediatelyBeforeMutation proves the canonical
// DeliveryCurrency evaluation is valid at the exact moment the irreversible
// provider mutation executes.
func TestDeliverCurrencyCheckImmediatelyBeforeMutation(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	provider := installScriptedProviderFor(t, "open-then-merged")

	currencyAtMerge := make([]bool, 0, 1)
	provider.onMerge = func() {
		cur, err := c.DeliveryCurrency(mustFleetTaskID(t, taskID))
		if err != nil {
			t.Errorf("currency at merge: %v", err)
			return
		}
		currencyAtMerge = append(currencyAtMerge, cur.Valid)
	}

	if _, err := Deliver(homeDir, taskID, deliverRequest()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(currencyAtMerge) != 1 || !currencyAtMerge[0] {
		t.Fatalf("currency at merge = %v, want [true]", currencyAtMerge)
	}
}

// TestDeliverClassification proves the provider observations classify into
// the truthful closed-set canonical outcomes.
func TestDeliverClassification(t *testing.T) {
	journal := &deliveryJournal{Identity: deliveryTestIdentity()}
	cases := []struct {
		name   string
		obs    DeliveryProviderObservation
		obsErr error
		want   taskauthority.DeliveryOutcomeStatus
	}{
		{"merged", DeliveryProviderObservation{State: "MERGED", HeadSHA: deliveryTestHead, MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}, nil, taskauthority.DeliveryOutcomeCompleted},
		{"open", DeliveryProviderObservation{State: "OPEN", HeadSHA: deliveryTestHead}, nil, taskauthority.DeliveryOutcomeRetryable},
		{"closed", DeliveryProviderObservation{State: "CLOSED", HeadSHA: deliveryTestHead}, nil, taskauthority.DeliveryOutcomePartial},
		{"ambiguous", DeliveryProviderObservation{State: "UNKNOWN"}, nil, taskauthority.DeliveryOutcomeRemoteUnknown},
		{"unreachable", DeliveryProviderObservation{}, errors.New("provider unreachable"), taskauthority.DeliveryOutcomeRemoteUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			derived := deriveDeliveryOutcome(journal, tc.obs, tc.obsErr, nil)
			if derived.status != tc.want {
				t.Fatalf("status = %q, want %q (detail %q)", derived.status, tc.want, derived.detail)
			}
		})
	}
}

// TestDeliverMergedAndClosedRequireNoMutation proves a provider that already
// reports merged or closed commits the truthful outcome without executing any
// irreversible mutation.
func TestDeliverMergedAndClosedRequireNoMutation(t *testing.T) {
	for _, script := range []string{"merged", "closed"} {
		t.Run(script, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID)
			provider := installScriptedProviderFor(t, script)
			result, err := Deliver(homeDir, taskID, deliverRequest())
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0 (no irreversible mutation needed)", provider.merges)
			}
			want := taskauthority.DeliveryOutcomeCompleted
			if script == "closed" {
				want = taskauthority.DeliveryOutcomePartial
			}
			if result.Status != want {
				t.Fatalf("status = %q, want %q", result.Status, want)
			}
		})
	}
}

// TestDeliverRetryableReleasesAuthorizationForRetryCycle proves a retryable
// outcome revokes the authorization so a later distinct authorization may
// follow (the canonical retry cycle).
func TestDeliverRetryableReleasesAuthorizationForRetryCycle(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	installScriptedProviderFor(t, "open") // open after merge attempt -> retryable

	result, err := Deliver(homeDir, taskID, deliverRequest())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.Status != taskauthority.DeliveryOutcomeRetryable {
		t.Fatalf("status = %q, want retryable", result.Status)
	}
	cur, err := c.DeliveryCurrency(mustFleetTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if len(cur.Reasons) != 1 || cur.Reasons[0] != taskauthority.DeliveryCurrencyRevoked {
		t.Fatalf("currency after retryable = %+v, want revoked (release for the retry cycle)", cur)
	}
	// The active journal index is empty: the journal completed.
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
}

// TestDeliverUnsupportedCapabilityFailsBeforeMutation proves an absent or
// unsupported capability fails closed before any journal intent or mutation,
// with no fallback execution route.
func TestDeliverUnsupportedCapabilityFailsBeforeMutation(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)

	old := deliveryProviderFor
	t.Cleanup(func() { deliveryProviderFor = old })
	deliveryProviderFor = func(domain.DeliveryIdentity) (DeliveryProvider, error) {
		return nil, fmt.Errorf("GitHub delivery capability is absent (gh-axi must be Ready); no fallback execution route")
	}

	if _, err := Deliver(homeDir, taskID, deliverRequest()); err == nil || !strings.Contains(err.Error(), "no fallback execution route") {
		t.Fatalf("Deliver err = %v, want capability absence fail-closed", err)
	}
	// No journal intent was written and no authorization was issued.
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none (no journal for unsupported capability)", active)
	}
	if _, err := c.DeliveryAuthorization(mustFleetTaskID(t, taskID)); err == nil {
		t.Fatal("authorization issued without a capability")
	}
}

// TestDeliverUnsupportedKindFailsBeforeJournal proves an unsupported
// operation kind fails closed before any journal intent.
func TestDeliverUnsupportedKindFailsBeforeJournal(t *testing.T) {
	_, homeDir := newFleetCanonical(t)
	req := deliverRequest()
	req.Kind = taskauthority.DeliveryAuthorizationRepositoryMutation
	if _, err := Deliver(homeDir, "t1", req); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Deliver err = %v, want unsupported kind fail-closed", err)
	}
	if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
		t.Fatalf("active journals = %v, want none", active)
	}
}

// rewriteTaskDocBumpRevision rewrites the current task document on disk with
// an incremented aggregate revision and envelope home revision, so a
// subsequent canonical mutation fails closed (the accepted taskauthority
// white-box pattern).
func rewriteTaskDocBumpRevision(t *testing.T, homeDir, taskID string) {
	t.Helper()
	path := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		HomeRevision uint64                     `json:"home_revision"`
		Aggregate    map[string]json.RawMessage `json:"aggregate"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var rev uint64
	if err := json.Unmarshal(doc.Aggregate["revision"], &rev); err != nil {
		t.Fatal(err)
	}
	doc.Aggregate["revision"] = []byte(fmt.Sprintf("%d", rev+1))
	doc.HomeRevision++
	next, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, next, 0600); err != nil {
		t.Fatal(err)
	}
}

// TestDeliverFailClosedBeforeMutation proves stale revision, hold add/release,
// binding change, transfer reservation, revocation, and identity/head change
// all fail closed before any irreversible provider mutation.
func TestDeliverFailClosedBeforeMutation(t *testing.T) {
	cases := []struct {
		name                string
		mutate              func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string)
		wantTerminalJournal bool // true: the release succeeded and the journal completed
	}{
		{"stale-revision", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			rewriteTaskDocBumpRevision(t, homeDir, taskID)
		}, false},
		{"hold-add", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			hold := taskauthority.CanonicalAddHoldRequest{
				HomeID: c.HomeID(), HoldID: "hold-delivery", Scope: taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
				Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery}, Reason: "freeze",
			}
			if _, err := c.AddHold(mustFleetOperation(t, "op-hold-add-"+taskID, hold), hold); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"hold-add-release", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			hold := taskauthority.CanonicalAddHoldRequest{
				HomeID: c.HomeID(), HoldID: "hold-delivery", Scope: taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
				Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery}, Reason: "freeze",
			}
			if _, err := c.AddHold(mustFleetOperation(t, "op-hold-add-rel-"+taskID, hold), hold); err != nil {
				t.Fatal(err)
			}
			release := taskauthority.CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-delivery", Reason: "resume"}
			if _, err := c.ReleaseHold(mustFleetOperation(t, "op-hold-rel-"+taskID, release), release); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"binding-change", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			path := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				HomeRevision uint64                     `json:"home_revision"`
				Aggregate    map[string]json.RawMessage `json:"aggregate"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			doc.Aggregate["worktree"] = []byte(`{"path":"/changed/path"}`)
			doc.HomeRevision++
			next, _ := json.Marshal(doc)
			if err := os.WriteFile(path, next, 0600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"transfer-reservation", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			agg, err := c.Get(mustFleetTaskID(t, taskID))
			if err != nil {
				t.Fatal(err)
			}
			req := taskauthority.CanonicalReserveTransferRequest{
				HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID),
				Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
				ReservationID: "res-" + taskID, Destination: mustFleetHomeID(t, "dest-home"), FenceToken: "fence-res", Reason: "transfer",
			}
			if _, err := c.ReserveTransfer(mustFleetOperation(t, "op-res-"+taskID, req), req); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"revocation", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			auth, err := c.DeliveryAuthorization(mustFleetTaskID(t, taskID))
			if err != nil {
				t.Fatal(err)
			}
			agg, err := c.Get(mustFleetTaskID(t, taskID))
			if err != nil {
				t.Fatal(err)
			}
			req := taskauthority.CanonicalRevokeDeliveryRequest{
				HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID),
				Precondition:             domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
				AuthorizationOperationID: auth.OperationID, Reason: "abandoned",
			}
			if _, err := c.RevokeDeliveryAuthorization(mustFleetOperation(t, "op-revoke-"+taskID, req), req); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"identity-head-change", func(t *testing.T, c *taskauthority.Canonical, homeDir, taskID string) {
			path := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				HomeRevision uint64                     `json:"home_revision"`
				Aggregate    map[string]json.RawMessage `json:"aggregate"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			doc.Aggregate["worktree"] = []byte(`{"head":"9999888877776666555544443333222211110000"}`)
			doc.HomeRevision++
			next, _ := json.Marshal(doc)
			if err := os.WriteFile(path, next, 0600); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			mustWorkingDeliveryTask(t, c, taskID)
			provider := installScriptedProviderFor(t, "open-then-merged")

			// Mutate the canonical state at the authorized boundary, i.e.
			// between authorization issuance and the currency check, so the
			// check runs immediately before the would-be mutation.
			restore := SetDeliveryCrashHookForTest(func(boundary string) {
				if boundary == "authorized" {
					tc.mutate(t, c, homeDir, taskID)
				}
			})
			defer restore()

			_, err := Deliver(homeDir, taskID, deliverRequest())
			var failClosed *DeliveryFailClosedError
			if !errors.As(err, &failClosed) {
				t.Fatalf("Deliver err = %T %v, want *DeliveryFailClosedError", err, err)
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0 (fail closed before mutation)", provider.merges)
			}
			// A terminal canonical outcome must never be committed by the
			// fail-closed path.
			if out, oerr := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); oerr == nil {
				t.Fatalf("fail-closed path committed an outcome: %+v", out)
			}
			active := listActiveDeliveryJournals(t, homeDir)
			if tc.wantTerminalJournal {
				if len(active) != 0 {
					t.Fatalf("active journals = %v, want none (journal completed after release)", active)
				}
			} else if len(active) != 1 {
				t.Fatalf("active journals = %v, want 1 (journal retained when the release cannot complete)", active)
			}
		})
	}
}

// TestDeliverNoMetaSubstitutionAuthorizesDelivery proves a .meta
// delivery_state=merged claim never authorizes a delivery: Deliver requires
// the canonical journaled flow and commits the canonical outcome.
func TestDeliverNoMetaSubstitutionAuthorizesDelivery(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID)
	// The .meta projection claims merged truth that was never committed
	// canonically.
	if err := home.WriteMeta(homeDir, taskID, map[string]string{
		"kind": "ship", "delivery_state": string(domain.DeliveryStateMerged),
		"pr_provider": "github", "pr_owner": "minhtri2710", "pr_repo": "munsu",
		"pr_number": "42", "pr_url": "https://github.com/minhtri2710/munsu/pull/42",
		"pr_base_ref": "main", "pr_head_ref": "feature/delivery", "pr_head_sha": deliveryTestHead,
		"pr_timestamp": "2026-08-05T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeliveryOutcome(mustFleetTaskID(t, taskID)); err == nil {
		t.Fatal("canonical outcome should not exist from .meta alone")
	}
	// The canonical flow still runs and commits the truthful outcome.
	provider := installScriptedProviderFor(t, "open-then-merged")
	result, err := Deliver(homeDir, taskID, deliverRequest())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	if provider.merges != 1 {
		t.Fatalf("merges = %d, want 1", provider.merges)
	}
}

// mustFleetHomeID builds a validated home identity.
func mustFleetHomeID(t *testing.T, value string) domain.HomeID {
	t.Helper()
	id, err := domain.NewHomeID(value)
	if err != nil {
		t.Fatalf("NewHomeID(%s): %v", value, err)
	}
	return id
}
