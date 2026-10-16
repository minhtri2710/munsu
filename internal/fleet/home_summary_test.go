//go:build integration

package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// seedChild seeds a spawned child: its endpoint is bound (working), and done
// completes it with the endpoint still held.
func seedChild(t *testing.T, homeDir, taskID string, done bool) {
	t.Helper()
	auth := canonicalAtHome(t, homeDir)
	canonicalCreateTask(t, auth, taskID, "ship", "")
	bindSoldier(t, auth, taskID, taskauthority.EndpointBinding{Backend: "tmux", Handle: "@" + taskID})
	if !done {
		return
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCompleteRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), To: taskauthority.PhaseDone, Reason: "test",
	}
	if _, err := auth.Complete(mustFleetOperation(t, "op-done-"+taskID, req), req); err != nil {
		t.Fatal(err)
	}
}

func TestSummarizeCaptainHome_ActiveChild(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	seedChild(t, home, "t1", false)
	seedCanonicalPhase(t, home, "t2", taskauthority.PhaseQueued)
	if err := mhome.AppendStatus(home, "t1", "working: implementing"); err != nil {
		t.Fatal(err)
	}
	sum := SummarizeCaptainHome(home)
	if !sum.Valid {
		t.Fatalf("valid=false reason=%q", sum.Reason)
	}
	if sum.State != "active_child_work" {
		t.Fatalf("state=%q want active_child_work", sum.State)
	}
	if sum.Counts.ActiveChildren != 1 || sum.Counts.InFlight != 1 || sum.Counts.Queued != 1 {
		t.Fatalf("counts=%+v", sum.Counts)
	}
	if len(sum.ActiveChildren) != 1 || sum.ActiveChildren[0].ID != "t1" {
		t.Fatalf("active=%+v", sum.ActiveChildren)
	}
	if sum.ActiveChildren[0].Doing != "implementing" {
		t.Fatalf("doing=%q", sum.ActiveChildren[0].Doing)
	}
	if len(sum.Queued) != 1 || sum.Queued[0].ID != "t2" {
		t.Fatalf("queued=%+v", sum.Queued)
	}
}

func TestSummarizeCaptainHome_DecisionsHoldsLanded(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	seedChild(t, home, "t-decision", false)
	seedCanonicalPhase(t, home, "t-blocked", taskauthority.PhaseBlocked)
	seedChild(t, home, "t-done", true)
	seedCanonicalPhase(t, home, "t-queued", taskauthority.PhaseQueued)
	if err := mhome.AppendStatus(home, "t-decision", "needs-decision: pick A or B [key=approach]"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(home, "t-done", "done: PR https://github.com/example/repo/pull/9"); err != nil {
		t.Fatal(err)
	}

	sum := SummarizeCaptainHome(home)
	if !sum.Valid {
		t.Fatalf("valid=false reason=%q", sum.Reason)
	}
	if sum.State != "captain_decision" {
		t.Fatalf("state=%q want captain_decision", sum.State)
	}
	if sum.Counts.DecisionsOpen < 1 || len(sum.DecisionsOpen) < 1 {
		t.Fatalf("decisions=%+v counts=%+v", sum.DecisionsOpen, sum.Counts)
	}
	if sum.DecisionsOpen[0].Verb != "needs-decision" {
		t.Fatalf("decision verb=%q", sum.DecisionsOpen[0].Verb)
	}
	if sum.Counts.Holds < 1 {
		t.Fatalf("holds count=%d holds=%+v", sum.Counts.Holds, sum.Holds)
	}
	if sum.Counts.Landed != 1 || len(sum.Landed) != 1 {
		t.Fatalf("landed=%+v counts=%+v", sum.Landed, sum.Counts)
	}
	if !strings.Contains(sum.Landed[0].PRURL, "github.com/example/repo/pull/9") {
		t.Fatalf("pr_url=%q", sum.Landed[0].PRURL)
	}
	if sum.Counts.Queued != 1 {
		t.Fatalf("queued count=%d", sum.Counts.Queued)
	}
}

func TestSummarizeCaptainHome_EmptyHomeValid(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	sum := SummarizeCaptainHome(home)
	// Without any canonical tasks, the summary is still valid (empty view).
	if !sum.Valid {
		t.Fatalf("valid=false reason=%q for empty home", sum.Reason)
	}
	if sum.State != "no_active_work" {
		t.Fatalf("state=%q want no_active_work for empty home", sum.State)
	}
}

func TestSummarizeCaptainHome_OmittedCaps(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	auth := canonicalAtHome(t, home)
	for i := 0; i < 25; i++ {
		canonicalCreateTask(t, auth, fmt.Sprintf("q%02d", i), "ship", "")
	}

	sum := SummarizeCaptainHome(home)
	if sum.Counts.Queued != 25 {
		t.Fatalf("queued count=%d", sum.Counts.Queued)
	}
	if len(sum.Queued) != maxQueued {
		t.Fatalf("queued list len=%d want %d", len(sum.Queued), maxQueued)
	}
	found := false
	for _, o := range sum.Omitted {
		if o.Surface == "queued" && o.Count == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("omitted=%+v", sum.Omitted)
	}
}

func TestLastParentStatus(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	if err := mhome.AppendStatus(parent, "captain:api", "done: PR https://example/1 [key=x]"); err != nil {
		t.Fatal(err)
	}
	got := LastParentStatus(parent, "api")
	if !strings.Contains(got, "done") {
		t.Fatalf("got %q", got)
	}
}

// TestSummarizeCaptainHome_InFlightTaskWithoutEndpointIsInvalid pins that a
// working task holding no endpoint makes the summary invalid instead of
// dropping it from the children.
func TestSummarizeCaptainHome_InFlightTaskWithoutEndpointIsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name      string
		seed      func(t *testing.T, home string)
		wantValid bool
	}{
		{"working without an endpoint is invalid", func(t *testing.T, home string) { seedCanonicalPhase(t, home, "t1", taskauthority.PhaseWorking) }, false},
		{"working with a bound endpoint is valid", func(t *testing.T, home string) { seedChild(t, home, "t1", false) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if _, err := mhome.Init(home); err != nil {
				t.Fatal(err)
			}
			tc.seed(t, home)
			sum := SummarizeCaptainHome(home)
			if sum.Valid != tc.wantValid || (!tc.wantValid && sum.Reason != "in-flight task holds no endpoint") {
				t.Fatalf("summary = valid=%v reason=%q; want valid=%v", sum.Valid, sum.Reason, tc.wantValid)
			}
		})
	}
}
