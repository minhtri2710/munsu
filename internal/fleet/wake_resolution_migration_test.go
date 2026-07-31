package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetWakeResolutionMigrationReportsIndependentHomeOutcomes(t *testing.T) {
	parent := t.TempDir()
	good := filepath.Join(t.TempDir(), "good")
	bad := filepath.Join(t.TempDir(), "bad")
	writeFleetLegacyWakeResolutions(t, good, "100\tlease-1\t100:1\tchecked\n")
	writeFleetLegacyWakeResolutions(t, bad, "corrupt\n")
	if err := Register(parent, "good", good, "captain", "project-a"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "bad", bad, "captain", "project-b"); err != nil {
		t.Fatal(err)
	}

	good, err := filepath.EvalSymlinks(good)
	if err != nil {
		t.Fatal(err)
	}
	bad, err = filepath.EvalSymlinks(bad)
	if err != nil {
		t.Fatal(err)
	}

	plan := PlanFleetWakeResolutionMigration(parent)
	if len(plan.Outcomes) != 3 {
		t.Fatalf("fleet plan outcomes = %d, want 3: %+v", len(plan.Outcomes), plan.Outcomes)
	}
	assertFleetOutcome(t, plan.Outcomes, parent, "skipped")
	assertFleetOutcome(t, plan.Outcomes, good, "planned")
	assertFleetOutcome(t, plan.Outcomes, bad, "error")

	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed WakeResolutionFleetPlan
	if err := json.Unmarshal(planBytes, &reviewed); err != nil {
		t.Fatal(err)
	}

	apply := ApplyFleetWakeResolutionMigration(reviewed)
	assertFleetOutcome(t, apply.Outcomes, parent, "skipped")
	assertFleetOutcome(t, apply.Outcomes, good, "applied")
	assertFleetOutcome(t, apply.Outcomes, bad, "error")
	var got struct {
		State string `json:"state"`
	}
	data, err := os.ReadFile(filepath.Join(good, "state", ".wake-resolutions", "lease-1-100_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil || got.State != "completed" {
		t.Fatalf("good migrated record = %+v err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(bad, "state", ".wake-resolution-migration")); !os.IsNotExist(err) {
		t.Fatalf("bad home should not stage migration state: %v", err)
	}
}

func TestFleetWakeResolutionMigrationReportsStaleSourceIndependently(t *testing.T) {
	parent := t.TempDir()
	good := filepath.Join(t.TempDir(), "good")
	stale := filepath.Join(t.TempDir(), "stale")
	writeFleetLegacyWakeResolutions(t, good, "100\tlease-1\t100:1\tchecked\n")
	writeFleetLegacyWakeResolutions(t, stale, "100\tlease-1\t100:1\tchecked\n")
	if err := Register(parent, "good", good, "captain", "project-a"); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "stale", stale, "captain", "project-b"); err != nil {
		t.Fatal(err)
	}
	good, err := filepath.EvalSymlinks(good)
	if err != nil {
		t.Fatal(err)
	}
	stale, err = filepath.EvalSymlinks(stale)
	if err != nil {
		t.Fatal(err)
	}
	plan := PlanFleetWakeResolutionMigration(parent)
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed WakeResolutionFleetPlan
	if err := json.Unmarshal(planBytes, &reviewed); err != nil {
		t.Fatal(err)
	}
	writeFleetLegacyWakeResolutions(t, stale, "100\tlease-1\t100:1\tchanged\n")

	apply := ApplyFleetWakeResolutionMigration(reviewed)
	assertFleetOutcome(t, apply.Outcomes, good, "applied")
	staleOutcome := findFleetOutcome(t, apply.Outcomes, stale)
	if staleOutcome.Status != "error" || !strings.Contains(staleOutcome.Error, "source digest changed") {
		t.Fatalf("stale outcome = %+v, want source digest error", staleOutcome)
	}
}

func assertFleetOutcome(t *testing.T, outcomes []WakeResolutionFleetHomeOutcome, home, want string) {
	t.Helper()
	outcome := findFleetOutcome(t, outcomes, home)
	if outcome.Status != want {
		t.Fatalf("outcome for %s = %q, want %q: %+v", home, outcome.Status, want, outcome)
	}
}

func findFleetOutcome(t *testing.T, outcomes []WakeResolutionFleetHomeOutcome, home string) WakeResolutionFleetHomeOutcome {
	t.Helper()
	for _, outcome := range outcomes {
		if outcome.Home == home {
			return outcome
		}
	}
	t.Fatalf("missing outcome for %s in %+v", home, outcomes)
	return WakeResolutionFleetHomeOutcome{}
}

func writeFleetLegacyWakeResolutions(t *testing.T, home, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", ".wake-resolutions"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
