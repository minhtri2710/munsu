package taskauthorityfs

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestReceiveTransferThroughFilesystemStore proves the destination receive
// commits the complete Task Generation payload — aggregate, dispatch record,
// transferred audit history, and the receive's own typed audit event — as one
// journaled Store transaction on the filesystem adapter, and that replay is
// idempotent from the durable receipt.
func TestReceiveTransferThroughFilesystemStore(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := taskauthority.New(store)

	req := taskauthority.TransferRequest{SourceHome: "/homes/source", DestinationHome: t.TempDir(), TaskID: "TASK-1", Generation: 7}
	digest, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	intent := taskauthority.TransferIntent{
		SourceHome:             req.SourceHome,
		DestinationHome:        req.DestinationHome,
		TaskID:                 req.TaskID,
		Generation:             req.Generation,
		RequestDigest:          digest,
		SourceOperationID:      "handoff-source-TASK-1-7",
		DestinationOperationID: "handoff-dest-TASK-1-7",
	}
	agg, err := taskauthority.NewAggregate("TASK-1", "captain:sm", "work", "ship", "munsu", "")
	if err != nil {
		t.Fatal(err)
	}
	agg.Generation = 7
	agg.DispatchInterpretationID = "interpretation-x"
	agg.DispatchInterpretationDigest = strings.Repeat("d", 64)
	payload := taskauthority.TransferPayload{
		Aggregate: agg,
		Interpretations: []taskauthority.DispatchInterpretation{{
			SchemaVersion:            taskauthority.TaskAuthoritySchema,
			ID:                       "interpretation-x",
			RequestedOrder:           []string{"TASK-1"},
			SelectedTasks:            []string{"TASK-1"},
			DependencySnapshotDigest: strings.Repeat("d", 64),
			Outcome:                  taskauthority.DispatchInterpretationAccepted,
			CreatedAt:                time.Now().UnixNano(),
		}},
		History: []taskauthority.AuditEvent{{
			OperationID: "task-create-TASK-1-7",
			Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
			Kind:        taskauthority.AuditLifecycle,
			TaskID:      "TASK-1",
			Generation:  7,
			Reason:      "created",
			After:       taskauthority.PhaseQueued,
			At:          time.Now().UnixNano(),
		}},
	}

	first, err := auth.ReceiveTransfer(taskauthority.ReceiveTransferRequest{Actor: taskauthority.Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatal("first receive reported replay")
	}

	v, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	cur, ok := v.Current("TASK-1")
	if !ok || cur.Generation != 7 || cur.Revision != taskauthority.FirstRevision {
		t.Fatalf("received aggregate = %+v ok=%v", cur, ok)
	}
	if len(v.Interpretations) != 1 || v.Interpretations[0].ID != "interpretation-x" {
		t.Fatalf("interpretations = %+v", v.Interpretations)
	}
	if len(v.Receipts) != 1 || v.Receipts[0].OperationID != intent.DestinationOperationID || v.Receipts[0].Digest != digest {
		t.Fatalf("receipts = %+v", v.Receipts)
	}

	// Replay from the durable receipt is an idempotent no-op.
	second, err := auth.ReceiveTransfer(taskauthority.ReceiveTransferRequest{Actor: taskauthority.Actor{ID: "general", Rank: "general"}, Intent: intent, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("replay did not return the original receipt")
	}
	v, err = store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Aggregates) != 1 || len(v.Receipts) != 1 {
		t.Fatalf("replay duplicated state: aggregates=%d receipts=%d", len(v.Aggregates), len(v.Receipts))
	}

	// The transferred history document is durable under its own operation ID.
	if _, err := DecodeAudit(mustReadReceiveAudit(t, store.homeDir, "task-create-TASK-1-7")); err != nil {
		t.Fatalf("transferred audit document: %v", err)
	}
}

func mustReadReceiveAudit(t *testing.T, homeDir, operationID string) []byte {
	t.Helper()
	rel, err := AuditRelPath(operationID)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := readDocument(filepath.Join(homeDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
