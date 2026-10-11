package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestVerifyRetirementContinuity(t *testing.T) {
	const task = "task"
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T) (homeDir, taskID string)
		wantErr string
	}{
		{
			name: "pending uplink report",
			setup: func(t *testing.T) (string, string) {
				generalHome, identity := directGeneralHome(t)
				reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
				return generalHome, "direct-task"
			},
			wantErr: "uplink report not acknowledged",
		},
		{
			name: "uplink report acknowledged and retired",
			setup: func(t *testing.T) (string, string) {
				generalHome, identity := directGeneralHome(t)
				res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
				recv, err := home.NewReceiver(generalHome)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := recv.Ack(home.NotificationRef{MessageID: res.MessageID, SenderIdentity: "direct-task"}); err != nil {
					t.Fatal(err)
				}
				if err := ReconcileCaptainHook(generalHome, false, &countingTransport{}); err != nil {
					t.Fatal(err)
				}
				return generalHome, "direct-task"
			},
		},
		{
			name: "open relay with a material report",
			setup: func(t *testing.T) (string, string) {
				h := t.TempDir()
				if err := home.AppendStatus(h, task, "done [key=terminal]: complete"); err != nil {
					t.Fatal(err)
				}
				if err := InitTaskObligations(h, task, "terminal"); err != nil {
					t.Fatal(err)
				}
				return h, task
			},
			wantErr: "terminal report-relay not acknowledged",
		},
		{
			name: "open relay with no material report",
			setup: func(t *testing.T) (string, string) {
				h := t.TempDir()
				if err := InitTaskObligations(h, task, "terminal"); err != nil {
					t.Fatal(err)
				}
				return h, task
			},
		},
		{
			// DeliverWake opens a ReportRelay obligation in the parent home for
			// every material soldier report; when that parent is a captain home
			// nothing in the binary could close it, so teardown once refused
			// forever without --force.
			name: "captain-backed soldier",
			setup: func(t *testing.T) (string, string) {
				soldierHome, captainHome := t.TempDir(), t.TempDir()
				if err := os.WriteFile(filepath.Join(captainHome, ProvenanceMarkerName), []byte("captain=test-captain\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := DeliverWake(DeliverRequest{
					HomeDir: soldierHome, ParentHome: captainHome, TaskID: "scout-task",
					State: "done", Message: "scout complete", Key: "terminal", Role: "soldier",
				}); err != nil {
					t.Fatalf("DeliverWake: %v", err)
				}
				if err := home.AppendStatus(captainHome, "scout-task", "done: cli task done"); err != nil {
					t.Fatal(err)
				}
				return captainHome, "scout-task"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir, taskID := tc.setup(t)
			err := VerifyRetirementContinuity(homeDir, taskID)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("VerifyRetirementContinuity = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("VerifyRetirementContinuity = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPrepareForcedRetirementEvidenceCopiesStatus(t *testing.T) {
	h := t.TempDir()
	id := "task"
	if err := home.AppendStatus(h, id, "done: complete"); err != nil {
		t.Fatal(err)
	}
	steps, err := PrepareForcedRetirementEvidence(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || !strings.Contains(steps[0], "evidence preserved") {
		t.Fatalf("steps = %v", steps)
	}
	stem, err := home.DurableKey(id)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h, "state", ".backup", stem, stem+".status")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "done: complete\n" {
		t.Fatalf("backup = %q", data)
	}
}

func TestFinalizeRetirementJournalsClosesActivities(t *testing.T) {
	h := t.TempDir()
	id := "task"
	if err := home.AppendStatus(h, id, "working [key=build]: running"); err != nil {
		t.Fatal(err)
	}
	steps, err := FinalizeRetirementJournals(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 2 {
		t.Fatalf("steps = %v", steps)
	}
	lines, err := home.ReadStatus(h, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := lines[len(lines)-1]; !strings.Contains(got, "resolved [key=build]") {
		t.Fatalf("last status = %q", got)
	}
}
