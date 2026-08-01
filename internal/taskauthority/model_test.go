package taskauthority

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGenerationPreservesPositiveMonotonicIdentity(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    Generation
		wantErr bool
	}{
		{"1", 1, false},
		{"42", 42, false},
		{"", 0, true},
		{"0", 0, true},
		{".", 0, true},
		{"..", 0, true},
		{"a/b", 0, true},
		{"-1", 0, true},
		{"1.5", 0, true},
	} {
		got, err := ParseGeneration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseGeneration(%q) succeeded with %d, want error", tc.in, got)
			}
			if !errors.Is(err, ErrInvalidGeneration) {
				t.Errorf("ParseGeneration(%q) error = %v, want ErrInvalidGeneration", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseGeneration(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseGeneration(%q) = %d, want %d", tc.in, got, tc.want)
		}
		if got.String() != tc.in {
			t.Errorf("Generation(%d).String() = %q, want %q", got, got.String(), tc.in)
		}
	}
	if g := Generation(7); g.Validate() != nil {
		t.Errorf("Generation(7).Validate() = %v", g.Validate())
	}
	if err := Generation(0).Validate(); !errors.Is(err, ErrInvalidGeneration) {
		t.Errorf("Generation(0).Validate() = %v, want ErrInvalidGeneration", err)
	}
}

func TestGenerationNext(t *testing.T) {
	next, err := Generation(1).Next()
	if err != nil || next != 2 {
		t.Fatalf("Next = %d, %v", next, err)
	}
	if _, err := Generation(0).Next(); !errors.Is(err, ErrInvalidGeneration) {
		t.Errorf("Generation(0).Next() = %v, want ErrInvalidGeneration", err)
	}
	if _, err := (^Generation(0)).Next(); !errors.Is(err, ErrInvalidGeneration) {
		t.Errorf("overflow Next() = %v, want ErrInvalidGeneration", err)
	}
}

func TestNewAggregateStartsAtFirstRevision(t *testing.T) {
	agg, err := NewAggregate("t1", "owner", "work", "ship", "proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 1 || agg.Revision != FirstRevision || agg.Phase != PhaseQueued || !agg.Current {
		t.Fatalf("new aggregate = %+v", agg)
	}
	if agg.SchemaVersion != taskAuthoritySchema {
		t.Fatalf("schema = %q, want %q", agg.SchemaVersion, taskAuthoritySchema)
	}
}

func TestAggregateValidationRejectsMismatchedGenerationBindings(t *testing.T) {
	agg, err := NewAggregate("t1", "owner", "work", "ship", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Bindings are structurally generation-bound: they live inside the
	// Aggregate record, so no separate generation field can drift.
	agg.Worktree = &WorktreeBinding{
		RepositoryIdentity: "repo",
		Path:               "/tmp/wt",
		GitDir:             "/tmp/wt/.git",
		CommonDir:          "/tmp/wt/common",
		Head:               "abc123",
		LeaseID:            "lease-1",
		FenceToken:         "fence-1",
		BoundAtUnix:        1,
	}
	if err := validateAggregate(agg); err != nil {
		t.Fatalf("valid worktree binding rejected: %v", err)
	}
	agg.Worktree.LeaseID = ""
	if err := validateAggregate(agg); err == nil {
		t.Fatal("invalid worktree binding accepted")
	}
}

func TestAggregateValidationRejectsBadIdentityAndPhase(t *testing.T) {
	agg, _ := NewAggregate("t1", "owner", "work", "ship", "", "")
	agg.TaskID = "../escape"
	if err := validateAggregate(agg); err == nil {
		t.Fatal("path-traversing task id accepted")
	}
	agg, _ = NewAggregate("t1", "owner", "work", "ship", "", "")
	agg.Phase = Phase("in-flight")
	if err := validateAggregate(agg); err == nil {
		t.Fatal("projection-only phase accepted as authoritative")
	}
	agg, _ = NewAggregate("t1", "owner", "work", "ship", "", "")
	agg.Definition.Owner = "  "
	if err := validateAggregate(agg); err == nil {
		t.Fatal("blank owner accepted")
	}
}

func TestDispatchHoldValidationRejectsMalformedScope(t *testing.T) {
	hold := mustHold(t, "h1", "start", "t1", "reason")
	if err := validateHold(hold); err != nil {
		t.Fatal(err)
	}
	hold.Scope.Generations = []string{"not-a-generation"}
	if err := validateHold(hold); err == nil {
		t.Fatal("malformed generation scope accepted")
	}
	hold = mustHold(t, "h2", "start", "t1", "reason")
	hold.Actions = []DispatchAction{"fly"}
	if err := validateHold(hold); err == nil {
		t.Fatal("unknown dispatch action accepted")
	}
}

func TestDispatchHoldScopeMatches(t *testing.T) {
	hold := DispatchHold{
		SchemaVersion: taskAuthoritySchema,
		ID:            "h1",
		Scope:         DispatchHoldScope{ProjectIDs: []string{"proj"}, TaskIDs: []string{"t1"}},
		Actions:       []DispatchAction{DispatchActionStart},
		Reason:        "r",
	}
	if !hold.Matches(DispatchActionStart, "t1", "proj", "1", "") {
		t.Fatal("matching task/project/generation did not match")
	}
	if hold.Matches(DispatchActionSpawn, "t1", "proj", "1", "") {
		t.Fatal("wrong action matched")
	}
	if hold.Matches(DispatchActionStart, "t2", "proj", "1", "") {
		t.Fatal("non-matching task matched")
	}
	if hold.Matches(DispatchActionStart, "t1", "other", "1", "") {
		t.Fatal("non-matching project matched")
	}
	released := hold
	released.ReleasedAt = 1
	if released.Matches(DispatchActionStart, "t1", "proj", "1", "") {
		t.Fatal("released hold matched")
	}
	// Empty scope matches everything.
	global := mustHold(t, "g1", "handoff", "", "global")
	if !global.Matches(DispatchActionHandoff, "any", "any", "9", "parent") {
		t.Fatal("global hold did not match")
	}
}

func TestAggregateJSONIsDeterministic(t *testing.T) {
	agg, _ := NewAggregate("t1", "owner", "work", "ship", "proj", "p0")
	first, err := json.Marshal(agg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(agg)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("nondeterministic JSON: %s vs %s", first, second)
	}
	if !strings.Contains(string(first), taskAuthoritySchema) {
		t.Fatalf("JSON does not carry schema identity: %s", first)
	}
}
