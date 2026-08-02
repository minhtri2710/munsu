package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueryTaskReadinessIsPureAndReportsDistinctReasons(t *testing.T) {
	homeDir := t.TempDir()
	queued, err := CreateTaskAggregate(homeDir, "queued", "owner", "queued work", "ship", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTaskAggregate(homeDir, "blocked", "owner", "blocked work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateCurrentTaskAggregateState(homeDir, "blocked", "blocked", "dependency"); err != nil {
		t.Fatal(err)
	}

	beforeAggregate := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateRelPath(queued.TaskID, queued.Generation)))
	beforeCurrent := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateDir, queued.TaskID, taskCurrentFile))

	ready, err := QueryTaskReadiness(homeDir, "queued")
	if err != nil {
		t.Fatal(err)
	}
	if !ready.Ready || len(ready.BlockingReasons) != 0 {
		t.Fatalf("queued readiness = %+v", ready)
	}
	blocked, err := QueryTaskReadiness(homeDir, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Ready || len(blocked.BlockingReasons) != 1 || blocked.BlockingReasons[0] != ReadinessBlocked {
		t.Fatalf("blocked readiness = %+v", blocked)
	}

	if got := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateRelPath(queued.TaskID, queued.Generation))); got != beforeAggregate {
		t.Fatal("readiness query changed aggregate")
	}
	if got := readLifecycleFile(t, filepath.Join(homeDir, taskAggregateDir, queued.TaskID, taskCurrentFile)); got != beforeCurrent {
		t.Fatal("readiness query changed current pointer")
	}
}

func TestQueryTaskReadinessReportsEachBlockingReason(t *testing.T) {
	homeDir := t.TempDir()
	cases := []struct {
		id    string
		state string
		want  ReadinessReason
	}{
		{"blocked", "blocked", ReadinessBlocked},
		{"working", "working", ReadinessInFlight},
		{"done", "done", ReadinessTerminal},
	}
	for _, tc := range cases {
		if _, err := CreateTaskAggregate(homeDir, tc.id, "owner", tc.id, "ship", ""); err != nil {
			t.Fatal(err)
		}
		if tc.state == "working" {
			if err := BindTaskEndpoint(homeDir, tc.id, "1", TaskEndpointBinding{
				Backend: "tmux", Handle: "pane", LeaseID: "lease", FenceToken: "fence", BoundAtUnix: 1,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := UpdateCurrentTaskAggregateState(homeDir, tc.id, tc.state, "reason"); err != nil {
			t.Fatal(err)
		}
		readiness, err := QueryTaskReadiness(homeDir, tc.id)
		if err != nil || len(readiness.BlockingReasons) != 1 || readiness.BlockingReasons[0] != tc.want {
			t.Fatalf("%s readiness = %+v err=%v", tc.id, readiness, err)
		}
	}
	missing, err := QueryTaskReadiness(homeDir, "missing")
	if err != nil || len(missing.BlockingReasons) != 1 || missing.BlockingReasons[0] != ReadinessReason("not-found") {
		t.Fatalf("missing readiness = %+v err=%v", missing, err)
	}
}

func readLifecycleFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
