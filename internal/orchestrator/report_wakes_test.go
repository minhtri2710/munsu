package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// directGeneralHome builds one General home, no Captain anywhere, one soldier
// task dispatched into that home, and a configured General pane.
func directGeneralHome(t *testing.T) (string, string) {
	t.Helper()
	generalHome := filepath.Join(t.TempDir(), "general-home")
	if err := os.MkdirAll(filepath.Join(generalHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(generalHome, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generalHome, "config", "general-pane"), []byte("fleet:p1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, rank, err := home.ReadHomeIdentity(generalHome)
	if err != nil {
		t.Fatalf("ReadHomeIdentity: %v", err)
	}
	if rank != home.RankGeneral {
		t.Fatalf("fixture home rank = %q, want %q", rank, home.RankGeneral)
	}
	return generalHome, identity
}

// reportDone files a soldier's material report the way `munsu report` does.
func reportDone(t *testing.T, h, id, state string) {
	t.Helper()
	if _, err := AppendReportStatus(h, id, domain.ReportStatusLine(state, "report", "default")); err != nil {
		t.Fatal(err)
	}
}

// settleAll claims every queued wake and acknowledges it, which settles every
// report wake among them.
func settleAll(t *testing.T, h string) []UnacceptedReport {
	t.Helper()
	claim, err := ClaimWakes(h, "general", 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, w := range claim.Wakes {
		ids = append(ids, w.Epoch+":"+w.Seq)
	}
	unaccepted, err := AckWakes(h, claim.LeaseID, ids)
	if err != nil {
		t.Fatal(err)
	}
	return unaccepted
}

// A task retires only after its parent settled every material report
// `munsu report` filed for it; a line the dispatcher wrote holds nothing, and a
// scout's done report never held its teardown.
func TestVerifyRetirementContinuity(t *testing.T) {
	const refusal = "report(s) its parent has not settled"
	for _, tc := range []struct {
		name    string
		kind    string
		setup   func(t *testing.T, h string)
		wantErr string
	}{
		{"report not derived yet", "ship", func(t *testing.T, h string) {
			reportDone(t, h, "task", "done")
		}, refusal},
		{"report derived, not settled", "ship", func(t *testing.T, h string) {
			reportDone(t, h, "task", "failed")
			if err := home.DeriveReportWakes(h); err != nil {
				t.Fatal(err)
			}
		}, refusal},
		{"report settled", "ship", func(t *testing.T, h string) {
			reportDone(t, h, "task", "done")
			settleAll(t, h)
		}, ""},
		{"dispatcher-written material line", "ship", func(t *testing.T, h string) {
			if err := home.AppendStatus(h, "task", "done: cli task done"); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"scout failed report not settled", "scout", func(t *testing.T, h string) {
			reportDone(t, h, "task", "failed")
		}, refusal},
		{"scout done report not settled", "scout", func(t *testing.T, h string) {
			reportDone(t, h, "task", "done")
			if err := home.DeriveReportWakes(h); err != nil {
				t.Fatal(err)
			}
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := t.TempDir()
			tc.setup(t, h)
			err := VerifyRetirementContinuity(h, "task", tc.kind)
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
