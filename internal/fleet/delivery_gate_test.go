//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// readGateRecord reads the gate record of one task and operation straight from
// the home's state root; ok is false when none was appended.
func readGateRecordFile(t *testing.T, homeDir, taskID, opID string) (taskauthority.GateRecord, bool) {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := h.Read(home.RootState, "task-authority/gates/"+taskID+"/"+opID+".json")
	if err != nil {
		return taskauthority.GateRecord{}, false
	}
	var g taskauthority.GateRecord
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return g, true
}

// activeJournal reads the one active delivery journal of the home.
func activeJournal(t *testing.T, homeDir string) *deliveryJournal {
	t.Helper()
	active := listActiveDeliveryJournals(t, homeDir)
	if len(active) != 1 {
		t.Fatalf("active journals = %v, want exactly one", active)
	}
	journal, err := readDeliveryJournalRecord(t, homeDir, active[0])
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func TestDeliverAppendsTheGateRecordBeforeTheMerge(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID, deliveryTestGitHubForge)
	provider := installScriptedProviderFor(t, "open-then-merged")
	var atMerge taskauthority.GateRecord
	var recordedBeforeMerge bool
	provider.onMerge = func() {
		j := activeJournal(t, homeDir)
		atMerge, recordedBeforeMerge = readGateRecordFile(t, homeDir, taskID, j.GateOpID)
	}

	if _, err := Deliver(homeDir, taskID, deliverRequest()); err != nil {
		t.Fatal(err)
	}
	if !recordedBeforeMerge {
		t.Fatal("no gate record existed when the provider merge ran")
	}
	files := listDeliveryJournalFiles(t, homeDir)
	journal, err := readDeliveryJournalRecord(t, homeDir, strings.TrimSuffix(files[0], ".json"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := readGateRecordFile(t, homeDir, taskID, journal.GateOpID)
	if !ok || got != atMerge {
		t.Fatalf("gate record after delivery = %+v (%v), want the one present at the merge %+v", got, ok, atMerge)
	}
	if got.TaskID != taskID || got.OperationID != journal.GateOpID || got.AuthorizationOperationID != journal.AuthorizeOpID ||
		got.Operation != taskauthority.DeliveryAuthorizationProviderMerge || got.HeadSHA != deliveryTestHead || got.Words != deliveryWords() {
		t.Fatalf("gate record = %+v, want the merge, its authorization, the head and the Human's words", got)
	}
}

func TestDeliverRefusesTheMergeWhenTheGateRecordCannotBeAppended(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID, deliveryTestGitHubForge)
	provider := installScriptedProviderFor(t, "open")
	var gateOp string
	provider.onValidate = func() { // a hold lands between the currency check and the gate append
		gateOp = activeJournal(t, homeDir).GateOpID
		hold := taskauthority.CanonicalAddHoldRequest{
			HomeID: c.HomeID(), HoldID: "hold-delivery", Scope: taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
			Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery}, Reason: "freeze",
		}
		if _, err := c.AddHold(mustFleetOperation(t, "op-hold-gate", hold), hold); err != nil {
			t.Error(err)
		}
	}

	_, err := Deliver(homeDir, taskID, deliverRequest())
	if err == nil || !strings.Contains(err.Error(), "appending the delivery gate record") {
		t.Fatalf("Deliver err = %v, want the gate refusal", err)
	}
	if provider.merges != 0 {
		t.Fatalf("merges = %d, want none without a gate record", provider.merges)
	}
	if _, ok := readGateRecordFile(t, homeDir, taskID, gateOp); ok || gateOp == "" {
		t.Fatalf("a gate record exists for refused operation %q", gateOp)
	}
}

func TestRecordDeliveryGate(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	taskID := "t1"
	mustWorkingDeliveryTask(t, c, taskID, deliveryTestGitHubForge)
	provider := installScriptedProviderFor(t, "open-then-merged")
	var live deliveryJournal
	results := map[string]error{}
	provider.onValidate = func() {
		live = *activeJournal(t, homeDir)
		results["first"] = recordDeliveryGate(c, &live)
		results["replay"] = recordDeliveryGate(c, &live)

		noOp := live
		noOp.GateOpID = ""
		results["no operation identity"] = recordDeliveryGate(c, &noOp)

		badTask := live
		badTask.TaskID = "Not A Task"
		results["invalid task"] = recordDeliveryGate(c, &badTask)

		otherAuth := live
		otherAuth.AuthorizeOpID = "op-not-current"
		results["authorization not current"] = recordDeliveryGate(c, &otherAuth)

		otherHead := live
		otherHead.Identity.HeadSHA = "9999888877776666555544443333222211110000"
		otherHead.GateOpID = deliveryGateOpID("other-head", taskID)
		results["other head"] = recordDeliveryGate(c, &otherHead)

		noWords := live
		noWords.Words = domain.Words{}
		noWords.GateOpID = deliveryGateOpID("no-words", taskID)
		results["no words"] = recordDeliveryGate(c, &noWords)
	}
	if _, err := Deliver(homeDir, taskID, deliverRequest()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"first", "replay"} {
		if results[name] != nil {
			t.Errorf("recordDeliveryGate(%s) = %v, want nil (an identical append replays)", name, results[name])
		}
	}
	for name, want := range map[string]string{
		"no operation identity":     "has no gate operation identity",
		"invalid task":              "",
		"authorization not current": "no longer current; the gate record was not appended",
		"other head":                "appending the delivery gate record",
		"no words":                  "appending the delivery gate record",
	} {
		if err := results[name]; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("recordDeliveryGate(%s) = %v, want %q", name, err, want)
		}
	}
	if _, ok := readGateRecordFile(t, homeDir, taskID, deliveryGateOpID("other-head", taskID)); ok {
		t.Error("a refused append left a gate record")
	}
	if _, ok := readGateRecordFile(t, homeDir, taskID, live.GateOpID); !ok {
		t.Error("the gate record of the journal is missing")
	}
}
