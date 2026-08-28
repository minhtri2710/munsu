package fleet

import (
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

func setupTestHome(t *testing.T) (*home.Home, *taskauthority.Canonical) {
	t.Helper()
	tmp := t.TempDir()
	h, err := home.Init(tmp)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return h, auth
}

func seedQueuedTask(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      mustTaskID(t, taskID),
		Owner:       "test-owner",
		Description: "test task",
		Kind:        "ship",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-create-"+taskID), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err != nil {
		t.Fatal(err)
	}
}

// ----------------------------------------------------------------------------
// Group A: delivery_journal.go (9 guards)
// ----------------------------------------------------------------------------

func TestCompleteDeliveryJournal_NotActive(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	journal := &deliveryJournal{
		Version: 1,
		ID:      "not-in-index",
		Phase:   deliveryPhasePrepared,
		Home:    h.Root(),
	}
	err = completeDeliveryJournal(h, lk, journal, deliveryStageOutcome)
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("completeDeliveryJournal err = %v, want not active", err)
	}
}

func TestReadDeliveryIndex_InvalidActiveIDs(t *testing.T) {
	t.Run("empty active id", func(t *testing.T) {
		h, _ := setupTestHome(t)
		idxData := []byte(`{"version":1,"home_revision":1,"active":[""]}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryIndexKey), idxData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readDeliveryIndex(h)
		if err == nil || !strings.Contains(err.Error(), "duplicate or empty active id") {
			t.Fatalf("readDeliveryIndex err = %v, want duplicate or empty active id", err)
		}
	})
	t.Run("duplicate active id", func(t *testing.T) {
		h, _ := setupTestHome(t)
		idxData := []byte(`{"version":1,"home_revision":1,"active":["d1","d1"]}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryIndexKey), idxData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readDeliveryIndex(h)
		if err == nil || !strings.Contains(err.Error(), "duplicate or empty active id") {
			t.Fatalf("readDeliveryIndex err = %v, want duplicate or empty active id", err)
		}
	})
}

func TestReadDeliveryIndex_UnsupportedVersion(t *testing.T) {
	h, _ := setupTestHome(t)
	idxData := []byte(`{"version":99,"home_revision":1,"active":[]}`)
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryIndexKey), idxData, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := readDeliveryIndex(h)
	if err == nil || !strings.Contains(err.Error(), "unsupported delivery journal index version") {
		t.Fatalf("readDeliveryIndex err = %v, want unsupported delivery journal index version", err)
	}
}

func TestReadDeliveryJournal_UnknownPhase(t *testing.T) {
	h, _ := setupTestHome(t)
	jData := []byte(`{"version":1,"id":"j1","phase":"unknown-phase"}`)
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryJournalKey("j1")), jData, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := readDeliveryJournal(h, "j1")
	if err == nil || !strings.Contains(err.Error(), "has unknown phase") {
		t.Fatalf("readDeliveryJournal err = %v, want has unknown phase", err)
	}
}

func TestReadDeliveryJournal_InvalidVersionOrID(t *testing.T) {
	t.Run("version mismatch", func(t *testing.T) {
		h, _ := setupTestHome(t)
		jData := []byte(`{"version":99,"id":"j1","phase":"prepared"}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryJournalKey("j1")), jData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readDeliveryJournal(h, "j1")
		if err == nil || !strings.Contains(err.Error(), "invalid delivery journal") {
			t.Fatalf("readDeliveryJournal err = %v, want invalid delivery journal", err)
		}
	})
	t.Run("id mismatch", func(t *testing.T) {
		h, _ := setupTestHome(t)
		jData := []byte(`{"version":1,"id":"j-other","phase":"prepared"}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryJournalKey("j1")), jData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readDeliveryJournal(h, "j1")
		if err == nil || !strings.Contains(err.Error(), "invalid delivery journal") {
			t.Fatalf("readDeliveryJournal err = %v, want invalid delivery journal", err)
		}
	})
}

func TestRecoverPendingDeliveryJournal_ProvenanceOrHomeMismatch(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	jData := []byte(`{"version":1,"id":"j1","phase":"prepared","home":"/other/home"}`)
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryJournalKey("j1")), jData, 0644); err != nil {
		t.Fatal(err)
	}
	err = recoverPendingDeliveryJournal(h, lk, "j1")
	if err == nil || !strings.Contains(err.Error(), "invalid delivery journal entry") {
		t.Fatalf("recoverPendingDeliveryJournal err = %v, want invalid delivery journal entry", err)
	}
}

func TestRecoverPendingDeliveryJournal_TerminalPhase(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	jData := []byte(fmt.Sprintf(`{"version":1,"id":"j1","phase":"completed","home":"%s"}`, h.Root()))
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryJournalKey("j1")), jData, 0644); err != nil {
		t.Fatal(err)
	}
	err = recoverPendingDeliveryJournal(h, lk, "j1")
	if err == nil || !strings.Contains(err.Error(), `is terminal ("completed") but still active`) {
		t.Fatalf("recoverPendingDeliveryJournal err = %v, want is terminal but still active", err)
	}
}

func TestTransitionDeliveryJournal_NotActive(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	journal := &deliveryJournal{
		Version: 1,
		ID:      "not-in-index",
		Phase:   deliveryPhasePrepared,
		Home:    h.Root(),
	}
	err = transitionDeliveryJournal(h, lk, journal, "mutating", func(j *deliveryJournal) {})
	if err == nil || !strings.Contains(err.Error(), "is not active") {
		t.Fatalf("transitionDeliveryJournal err = %v, want is not active", err)
	}
}

func TestTransitionDeliveryJournal_TerminalPhase(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(deliveryLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	idxData := []byte(`{"version":1,"home_revision":1,"active":["j1"]}`)
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", deliveryJournalDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", deliveryIndexKey), idxData, 0644); err != nil {
		t.Fatal(err)
	}
	journal := &deliveryJournal{
		Version: 1,
		ID:      "j1",
		Phase:   deliveryPhaseCompleted,
		Home:    h.Root(),
	}
	err = transitionDeliveryJournal(h, lk, journal, "mutating", func(j *deliveryJournal) {})
	if err == nil || !strings.Contains(err.Error(), `is terminal ("completed"); cannot transition`) {
		t.Fatalf("transitionDeliveryJournal err = %v, want is terminal; cannot transition", err)
	}
}

// ----------------------------------------------------------------------------
// Group B: task_handoff_transaction.go (15 guards)
// ----------------------------------------------------------------------------

func TestCheckHandoffHolds_Held(t *testing.T) {
	_, auth := setupTestHome(t)
	req := taskauthority.CanonicalAddHoldRequest{
		HomeID:  auth.HomeID(),
		HoldID:  "hold-handoff",
		Scope:   taskauthority.DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionHandoff},
		Reason:  "freeze handoff",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-add-hold"), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AddHold(op, req); err != nil {
		t.Fatal(err)
	}
	err = checkHandoffHolds(auth, "t1", "", "", "")
	if err == nil || !errors.Is(err, taskauthority.ErrDispatchHeld) || !strings.Contains(err.Error(), "dispatch is held") {
		t.Fatalf("checkHandoffHolds err = %v, want dispatch is held", err)
	}
}

func TestCompleteHandoffJournal_NotActive(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	journal := &taskHandoffJournal{
		Version:         1,
		ID:              "not-in-index",
		Phase:           handoffPhasePrepared,
		SourceHome:      h.Root(),
		DestinationHome: h.Root(),
	}
	err = completeHandoffJournal(h, lk, journal)
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("completeHandoffJournal err = %v, want not active", err)
	}
}

func TestConfiguredParentHome_Malformed(t *testing.T) {
	h, _ := setupTestHome(t)
	cfgDir := filepath.Join(h.Root(), "config")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "parent-home"), []byte("   \n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := configuredParentHome(h.Root())
	if err == nil || !strings.Contains(err.Error(), "malformed parent-home configuration") {
		t.Fatalf("configuredParentHome err = %v, want malformed parent-home configuration", err)
	}
}

func TestDurableTaskHandoff_NoTasks(t *testing.T) {
	h1, _ := setupTestHome(t)
	h2, _ := setupTestHome(t)
	// Mark h2 with captain provenance
	if err := home.SeedCaptainProvenance(h2.Root(), "captain-1"); err != nil {
		t.Fatal(err)
	}
	err := durableTaskHandoff(h1.Root(), h2.Root(), nil)
	if err == nil || !strings.Contains(err.Error(), "handoff: no tasks requested") {
		t.Fatalf("durableTaskHandoff err = %v, want handoff: no tasks requested", err)
	}
}

func TestReadHandoffIndex_InvalidActiveIDs(t *testing.T) {
	t.Run("empty active id", func(t *testing.T) {
		h, _ := setupTestHome(t)
		idxData := []byte(`{"version":1,"home_revision":1,"active":[""]}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffIndexKey), idxData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readHandoffIndex(h)
		if err == nil || !strings.Contains(err.Error(), "duplicate or empty active id") {
			t.Fatalf("readHandoffIndex err = %v, want duplicate or empty active id", err)
		}
	})
	t.Run("duplicate active id", func(t *testing.T) {
		h, _ := setupTestHome(t)
		idxData := []byte(`{"version":1,"home_revision":1,"active":["h1","h1"]}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffIndexKey), idxData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readHandoffIndex(h)
		if err == nil || !strings.Contains(err.Error(), "duplicate or empty active id") {
			t.Fatalf("readHandoffIndex err = %v, want duplicate or empty active id", err)
		}
	})
}

func TestReadHandoffIndex_UnsupportedVersion(t *testing.T) {
	h, _ := setupTestHome(t)
	idxData := []byte(`{"version":99,"home_revision":1,"active":[]}`)
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffIndexKey), idxData, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := readHandoffIndex(h)
	if err == nil || !strings.Contains(err.Error(), "unsupported handoff journal index version") {
		t.Fatalf("readHandoffIndex err = %v, want unsupported handoff journal index version", err)
	}
}

func TestReadHandoffJournal_InvalidVersionOrID(t *testing.T) {
	t.Run("version mismatch", func(t *testing.T) {
		h, _ := setupTestHome(t)
		jData := []byte(`{"version":99,"id":"h1","phase":"prepared"}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffJournalKey("h1")), jData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readHandoffJournal(h, "h1")
		if err == nil || !strings.Contains(err.Error(), "invalid handoff journal") {
			t.Fatalf("readHandoffJournal err = %v, want invalid handoff journal", err)
		}
	})
	t.Run("id mismatch", func(t *testing.T) {
		h, _ := setupTestHome(t)
		jData := []byte(`{"version":1,"id":"h-other","phase":"prepared"}`)
		if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffJournalKey("h1")), jData, 0644); err != nil {
			t.Fatal(err)
		}
		_, err := readHandoffJournal(h, "h1")
		if err == nil || !strings.Contains(err.Error(), "invalid handoff journal") {
			t.Fatalf("readHandoffJournal err = %v, want invalid handoff journal", err)
		}
	})
}

func TestRecoverPendingJournal_ProvenanceMismatch(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	jData := []byte(`{"version":1,"id":"h1","phase":"prepared","source_home":"/other/home"}`)
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffJournalKey("h1")), jData, 0644); err != nil {
		t.Fatal(err)
	}
	err = recoverPendingJournal(h, lk, "h1")
	if err == nil || !strings.Contains(err.Error(), "invalid handoff journal entry") {
		t.Fatalf("recoverPendingJournal err = %v, want invalid handoff journal entry", err)
	}
}

func TestRecoverPendingJournal_TerminalPhase(t *testing.T) {
	h, _ := setupTestHome(t)
	lk, err := h.Lock(handoffLockScope)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	jData := []byte(fmt.Sprintf(`{"version":1,"id":"h1","phase":"completed","source_home":"%s"}`, h.Root()))
	if err := os.MkdirAll(filepath.Join(h.Root(), "state", taskHandoffDirName), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root(), "state", handoffJournalKey("h1")), jData, 0644); err != nil {
		t.Fatal(err)
	}
	err = recoverPendingJournal(h, lk, "h1")
	if err == nil || !strings.Contains(err.Error(), `handoff journal h1 is terminal ("completed") but still active`) {
		t.Fatalf("recoverPendingJournal err = %v, want is terminal but still active", err)
	}
}

func TestResolveHandoffKeys_DuplicateTask(t *testing.T) {
	h, auth := setupTestHome(t)
	seedQueuedTask(t, auth, "t1")
	_, err := resolveHandoffKeys(h.Root(), auth, []string{"t1", "t1"})
	if err == nil || !strings.Contains(err.Error(), "duplicate task") {
		t.Fatalf("resolveHandoffKeys err = %v, want duplicate task", err)
	}
}

func TestResolveHandoffTaskID_AmbiguousTask(t *testing.T) {
	parentHome, parentAuth := setupTestHome(t)
	seedQueuedTask(t, parentAuth, "t1")
	// Create captain home under parentHome/captains/c1
	capDir := filepath.Join(parentHome.Root(), "captains", "c1")
	capHome, err := home.Init(capDir)
	if err != nil {
		t.Fatal(err)
	}
	capAuth, err := taskauthority.NewCanonical(capHome)
	if err != nil {
		t.Fatal(err)
	}
	seedQueuedTask(t, capAuth, "t1")

	_, err = resolveHandoffTaskID(parentHome.Root(), "t1")
	if err == nil {
		t.Fatal("expected ambiguous task error, got nil")
	}
	var ambErr *home.AmbiguousTaskIDError
	if !errors.As(err, &ambErr) {
		t.Fatalf("resolveHandoffTaskID err = %v, want AmbiguousTaskIDError", err)
	}
}

func TestValidateHandoffTaskID_Invalid(t *testing.T) {
	cases := []string{
		"",
		".",
		"..",
		"a/b",
		`a\b`,
		"dir/task",
		"../task",
	}
	for _, id := range cases {
		err := validateHandoffTaskID(id)
		if err == nil || !strings.Contains(err.Error(), "invalid handoff task id") {
			t.Fatalf("validateHandoffTaskID(%q) err = %v, want invalid handoff task id", id, err)
		}
	}
}

func TestVerifyTransferReceived_ReservationMismatch(t *testing.T) {
	srcHome, srcAuth := setupTestHome(t)
	_, destAuth := setupTestHome(t)
	taskID := "t-mismatch"
	tid := mustTaskID(t, taskID)

	recReq := taskauthority.CanonicalReceiveTransferRequest{
		HomeID:           destAuth.HomeID(),
		TaskID:           tid,
		ReservationID:    "res-actual",
		SourceHome:       srcAuth.HomeID(),
		SourceGeneration: taskauthority.Generation(1),
		Definition:       taskauthority.TaskDefinition{Owner: "owner", Kind: "ship"},
		Reason:           "receive",
	}
	recOp, err := domain.NewOperation(mustOpID(t, "op-rec-1"), recReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destAuth.ReceiveTransfer(recOp, recReq); err != nil {
		t.Fatal(err)
	}

	task := &taskHandoffTask{
		TaskID:           taskID,
		SourceGeneration: 1,
		ReservationID:    "res-different",
	}
	actReq := taskauthority.CanonicalActivateTransferRequest{
		HomeID:        destAuth.HomeID(),
		TaskID:        tid,
		Precondition:  domain.Of(1, 1),
		ReservationID: "res-different",
		Reason:        "activate",
	}
	actOp, _ := domain.NewOperation(mustOpID(t, "op-act-1"), actReq)

	_, err = verifyTransferReceived(destAuth, srcAuth.HomeID(), actOp, &taskHandoffJournal{ID: "j1", SourceHome: srcHome.Root()}, task)
	if err == nil || !strings.Contains(err.Error(), "is not under reservation") {
		t.Fatalf("verifyTransferReceived err = %v, want is not under reservation", err)
	}
}

func TestVerifyTransferReceived_SourceProvenanceMismatch(t *testing.T) {
	srcHome, srcAuth := setupTestHome(t)
	_, destAuth := setupTestHome(t)
	taskID := "t-prov"
	tid := mustTaskID(t, taskID)

	recReq := taskauthority.CanonicalReceiveTransferRequest{
		HomeID:           destAuth.HomeID(),
		TaskID:           tid,
		ReservationID:    "res-1",
		SourceHome:       srcAuth.HomeID(),
		SourceGeneration: taskauthority.Generation(1),
		Definition:       taskauthority.TaskDefinition{Owner: "owner", Kind: "ship"},
		Reason:           "receive",
	}
	recOp, err := domain.NewOperation(mustOpID(t, "op-rec-2"), recReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destAuth.ReceiveTransfer(recOp, recReq); err != nil {
		t.Fatal(err)
	}

	task := &taskHandoffTask{
		TaskID:           taskID,
		SourceGeneration: 1,
		ReservationID:    "res-1",
	}
	actReq := taskauthority.CanonicalActivateTransferRequest{
		HomeID:        destAuth.HomeID(),
		TaskID:        tid,
		Precondition:  domain.Of(1, 1),
		ReservationID: "res-1",
		Reason:        "activate",
	}
	actOp, _ := domain.NewOperation(mustOpID(t, "op-act-2"), actReq)

	otherHomeID, _ := domain.NewHomeID("other-home-id")
	_, err = verifyTransferReceived(destAuth, otherHomeID, actOp, &taskHandoffJournal{ID: "j1", SourceHome: srcHome.Root()}, task)
	if err == nil || !strings.Contains(err.Error(), "source provenance mismatch") {
		t.Fatalf("verifyTransferReceived err = %v, want source provenance mismatch", err)
	}
}

func TestVerifyTransferReceived_SourceGenerationMismatch(t *testing.T) {
	srcHome, srcAuth := setupTestHome(t)
	_, destAuth := setupTestHome(t)
	taskID := "t-gen"
	tid := mustTaskID(t, taskID)

	recReq := taskauthority.CanonicalReceiveTransferRequest{
		HomeID:           destAuth.HomeID(),
		TaskID:           tid,
		ReservationID:    "res-1",
		SourceHome:       srcAuth.HomeID(),
		SourceGeneration: taskauthority.Generation(1),
		Definition:       taskauthority.TaskDefinition{Owner: "owner", Kind: "ship"},
		Reason:           "receive",
	}
	recOp, err := domain.NewOperation(mustOpID(t, "op-rec-3"), recReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destAuth.ReceiveTransfer(recOp, recReq); err != nil {
		t.Fatal(err)
	}

	task := &taskHandoffTask{
		TaskID:           taskID,
		SourceGeneration: 2, // generation mismatch: received is 1, task is 2
		ReservationID:    "res-1",
	}
	actReq := taskauthority.CanonicalActivateTransferRequest{
		HomeID:        destAuth.HomeID(),
		TaskID:        tid,
		Precondition:  domain.Of(1, 1),
		ReservationID: "res-1",
		Reason:        "activate",
	}
	actOp, _ := domain.NewOperation(mustOpID(t, "op-act-3"), actReq)

	_, err = verifyTransferReceived(destAuth, srcAuth.HomeID(), actOp, &taskHandoffJournal{ID: "j1", SourceHome: srcHome.Root()}, task)
	if err == nil || !strings.Contains(err.Error(), "source generation mismatch") {
		t.Fatalf("verifyTransferReceived err = %v, want source generation mismatch", err)
	}
}
