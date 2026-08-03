package taskauthorityfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestStoreInterpretDispatchCommitsAtomically proves the decision-required
// path commits its full staged set — interpretation, decision, hold, typed
// audit event, and the receipt pinning the interpretation ID — through the
// filesystem journal, and that the committed record replays after a fresh
// Store construction (durable, not in-memory).
func TestStoreInterpretDispatchCommitsAtomically(t *testing.T) {
	homeDir := t.TempDir()
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	a := taskauthority.New(store)
	actor := taskauthority.Actor{ID: "test", Rank: "general"}
	if _, err := a.Create(taskauthority.CreateRequest{
		OperationID: "op-create", Actor: actor,
		TaskID: "t1", Owner: "owner", Description: "work", Kind: "ship", Project: "proj",
	}); err != nil {
		t.Fatal(err)
	}

	req := taskauthority.InterpretDispatchRequest{
		OperationID: "op-interpret", Actor: actor,
		RequestedOrder: []string{"t1"},
		Dependencies:   []taskauthority.DispatchDependency{{TaskID: "t1", DependsOn: []string{"missing"}}},
		Autonomy:       taskauthority.DispatchAutonomyManual,
	}
	result, err := a.InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.Outcome != taskauthority.DispatchInterpretationDecisionRequired {
		t.Fatalf("outcome = %s, want decision-required", result.Record.Outcome)
	}

	// Every staged record committed through the journal: interpretation,
	// decision, hold, audit, and the receipt pinning the interpretation ID.
	root := filepath.Join(homeDir, filepath.FromSlash(authorityRoot))
	for _, rel := range []string{
		filepath.Join("interpretations", fileIDEncode(result.Record.ID)+documentExt),
		filepath.Join("decisions", fileIDEncode(result.Record.DecisionKey)+documentExt),
		filepath.Join("holds", fileIDEncode(result.Record.DecisionKey+"-hold")+documentExt),
		filepath.Join("audit", fileIDEncode("op-interpret")+documentExt),
		filepath.Join("receipts", fileIDEncode("op-interpret")+documentExt),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("committed document %s missing: %v", rel, err)
		}
	}

	// A fresh Store over the same home replays the operation and returns the
	// original committed record.
	reopened, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := taskauthority.New(reopened).InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Record.ID != result.Record.ID || replayed.Record.CreatedAt != result.Record.CreatedAt {
		t.Fatalf("replay after reopen = %+v, want original record %+v", replayed, result.Record)
	}
	v, err := reopened.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Interpretations) != 1 || len(v.Decisions) != 1 || len(v.Holds) != 1 {
		t.Fatalf("replay duplicated committed records: %+v", v)
	}
	var receipt taskauthority.Receipt
	for _, candidate := range v.Receipts {
		if candidate.OperationID == "op-interpret" {
			receipt = candidate
		}
	}
	if receipt.InterpretationID != result.Record.ID {
		t.Fatalf("receipt interpretation id = %q, want %q", receipt.InterpretationID, result.Record.ID)
	}
}

// TestStoreInterpretDispatchRejectsV1Homes pins the fail-closed boundary: a
// legacy v1 home refuses interpretation evaluation with the explicit
// migration-required error, never a lazy migration.
func TestStoreInterpretDispatchRejectsV1Homes(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state", ".task-authority", "aggregates", "t1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", ".task-authority", "aggregates", "t1", "1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = taskauthority.New(store).InterpretDispatch(taskauthority.InterpretDispatchRequest{
		OperationID: "op-interpret", Actor: taskauthority.Actor{ID: "test", Rank: "general"},
		RequestedOrder: []string{"t1"},
		Autonomy:       taskauthority.DispatchAutonomyManual,
	})
	if !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("err = %v, want ErrMigrationRequired on v1 homes", err)
	}
}
