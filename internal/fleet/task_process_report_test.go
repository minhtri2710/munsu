package fleet

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func markedTaskProcess(pid int, task, homeDir string) MarkedProcess {
	return MarkedProcess{PID: pid, ExecutablePath: "/usr/bin/node", Markers: map[string]string{MarkerMunsuTask: task, MarkerMunsuHome: homeDir}}
}

func useTaskProcessFakes(t *testing.T, inventory MarkerInventory, details func([]int) map[int]processDetail) {
	t.Helper()
	oldInventory, oldDetails := taskMarkerInventory, taskProcessDetails
	taskMarkerInventory, taskProcessDetails = inventory, details
	t.Cleanup(func() { taskMarkerInventory, taskProcessDetails = oldInventory, oldDetails })
}

func TestSurvivingTaskProcessSteps(t *testing.T) {
	homeDir, otherHome := t.TempDir(), t.TempDir()
	known := func(pids []int) map[int]processDetail {
		details := map[int]processDetail{}
		for _, pid := range pids {
			details[pid] = processDetail{cwd: "/private/tmp/dev", ports: "5173"}
		}
		return details
	}
	for _, tc := range []struct {
		name      string
		inventory fakeMarkerInventory
		details   func([]int) map[int]processDetail
		reaped    map[int]bool
		want      []string
	}{
		{
			name:      "a surviving task process is reported with executable, cwd and port, ordered by pid",
			inventory: fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(900, "T-1", homeDir), markedTaskProcess(800, "T-1", homeDir)}}},
			details:   known,
			want: []string{
				"task process 800 still running: /usr/bin/node (cwd /private/tmp/dev, listening 5173); munsu does not stop it — inspect it by hand or run munsu doctor --orphans",
				"task process 900 still running: /usr/bin/node (cwd /private/tmp/dev, listening 5173); munsu does not stop it — inspect it by hand or run munsu doctor --orphans",
			},
		},
		{
			name:      "another task's or another home's process is not reported",
			inventory: fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(800, "T-2", homeDir), markedTaskProcess(801, "T-1", otherHome)}}},
			details:   known,
		},
		{
			name:      "a process the worktree reap just killed is not reported again",
			inventory: fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(800, "T-1", homeDir)}}},
			details:   known,
			reaped:    map[int]bool{800: true},
		},
		{
			name:      "the teardown process and its parent are not reported",
			inventory: fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(os.Getpid(), "T-1", homeDir), markedTaskProcess(os.Getppid(), "T-1", homeDir)}}},
			details:   known,
		},
		{
			name:      "a reader that answers nothing renders unknown and keeps the line",
			inventory: fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(800, "T-1", homeDir)}}},
			details:   func([]int) map[int]processDetail { return nil },
			want:      []string{"task process 800 still running: /usr/bin/node (cwd unknown, listening unknown); munsu does not stop it — inspect it by hand or run munsu doctor --orphans"},
		},
		{
			name:      "an inventory failure is one step",
			inventory: fakeMarkerInventory{err: errors.New("table unreadable")},
			details:   known,
			want:      []string{"could not list task processes: table unreadable"},
		},
		{
			name:      "a platform without an inventory adds no step",
			inventory: fakeMarkerInventory{err: ErrProcessInventoryUnsupported},
			details:   known,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTaskProcessFakes(t, tc.inventory, tc.details)
			got := survivingTaskProcessSteps(homeDir, "T-1", tc.reaped)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("steps = %q, want %q", got, tc.want)
			}
		})
	}
}
