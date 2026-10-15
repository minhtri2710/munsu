package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

func TestWakeClaimOutputsWakeContent(t *testing.T) {
	const (
		kind    = "signal"
		key     = "task-17"
		payload = "failed: CI is red"
	)

	for _, output := range []string{OutputTOON, OutputJSON} {
		t.Run(output, func(t *testing.T) {
			homeDir := t.TempDir()
			if _, err := home.Init(homeDir); err != nil {
				t.Fatal(err)
			}
			if err := home.EnqueueWake(homeDir, kind, key, payload); err != nil {
				t.Fatal(err)
			}

			out, err := runRoot(t, "wake", "claim", "--consumer", "general-pilot", "--home", homeDir, "--output", output)
			if err != nil {
				t.Fatalf("wake claim: %v\n%s", err, out)
			}

			if output == OutputJSON {
				var response struct {
					Data struct {
						Wakes []struct {
							WakeID  string `json:"wake_id"`
							Kind    string `json:"kind"`
							Key     string `json:"key"`
							Payload string `json:"payload"`
						} `json:"wakes"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(out), &response); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, out)
				}
				if len(response.Data.Wakes) != 1 || response.Data.Wakes[0].WakeID == "" || response.Data.Wakes[0].Kind != kind || response.Data.Wakes[0].Key != key || response.Data.Wakes[0].Payload != payload {
					t.Fatalf("claimed wake content = %+v, want one wake ID and kind %q, key %q, payload %q\n%s", response.Data.Wakes, kind, key, payload, out)
				}
				return
			}

			for _, content := range []string{kind, key, payload} {
				if !strings.Contains(out, content) {
					t.Errorf("wake claim output does not include %q:\n%s", content, out)
				}
			}
		})
	}
}

// A report whose settlement cannot finish comes back in the contract output
// with its ref and a hint that says what happened, for resolve and ack alike:
// a scout that cannot be completed leaves the report settled, and a report
// cursor that cannot be written leaves it holding teardown.
func TestWakeSettleOutputListsAnUnacceptedReport(t *testing.T) {
	settle := map[string]func(lease, event string) []string{
		"resolve": func(lease, event string) []string {
			return []string{"wake", "resolve", "--claim-id", lease, "--event-id", event, "--summary", "seen"}
		},
		"ack": func(lease, event string) []string {
			return []string{"wake", "ack", lease, event}
		},
	}
	for _, tc := range []struct {
		name        string
		corrupt     string // file broken after the claim, under the home
		wantReason  string
		wantHint    string
		wantSettled bool
	}{
		{"scout not completed", "state/task-authority/tasks/sc-1/current.json", "completing scout task", "munsu task done", true},
		{"report not settled", "state/sc-1.report-cursor", "settling report", "munsu teardown <id> --force", false},
	} {
		for name, args := range settle {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				homeDir := t.TempDir()
				initCLITestHome(t, homeDir)
				registerCLITestProject(t, homeDir, "munsu")
				if out, err := runTaskCommand(t, []string{"task", "add", "sc-1", "explore", "--kind", "scout", "--scope", "investigate report", "--repo", "munsu", "--home", homeDir}); err != nil {
					t.Fatalf("task add: %v\n%s", err, out)
				}
				line := domain.ReportStatusLine("done", "findings", "default")
				if err := home.AppendStatus(homeDir, "sc-1", line); err != nil {
					t.Fatal(err)
				}
				claim, err := home.ClaimWakes(homeDir, "test", 60, 1)
				if err != nil || len(claim.Wakes) != 1 {
					t.Fatalf("claim: %+v err=%v", claim, err)
				}
				if err := os.WriteFile(filepath.Join(homeDir, tc.corrupt), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
				ref := claim.Wakes[0].Ref
				args := append(args(claim.LeaseID, claim.Wakes[0].Epoch+":"+claim.Wakes[0].Seq), "--home", homeDir, "--output", OutputJSON)
				out, err := runRoot(t, args...)
				if err != nil {
					t.Fatalf("settle must succeed despite the unaccepted report: %v\n%s", err, out)
				}
				var resp Response[WakeAck]
				if err := json.Unmarshal([]byte(out), &resp); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, out)
				}
				got := resp.Data.UnacceptedReports
				if ref == "" || len(got) != 1 || got[0].Ref != ref || got[0].Key != "sc-1" || !strings.Contains(got[0].Reason, tc.wantReason) {
					t.Fatalf("unaccepted_reports = %+v, want the report with ref %s and reason %q", got, ref, tc.wantReason)
				}
				if !strings.Contains(got[0].Hint, tc.wantHint) {
					t.Fatalf("hint = %q, want %q", got[0].Hint, tc.wantHint)
				}
				if held, _ := home.LeaseWakes(homeDir, claim.LeaseID); len(held) != 0 {
					t.Fatalf("lease still holds %d wakes, want the wake settled", len(held))
				}
				left, err := home.UnsettledReports(homeDir, "sc-1")
				if settled := err == nil && len(left) == 0; settled != tc.wantSettled {
					t.Fatalf("unsettled = %q err=%v, want settled=%v", left, err, tc.wantSettled)
				}
			})
		}
	}
}
