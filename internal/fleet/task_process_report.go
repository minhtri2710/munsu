package fleet

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Teardown REPORTS the processes that still carry the task's launch markers
// after it has run; it never signals them and never refuses because of them.
// The only killer on the teardown path is reapWorktreeHolders, which acts on
// holders of the worktree path alone.

// taskMarkerInventory and taskProcessDetails are package variables so tests can
// replace the process table and the cwd/port reader.
var (
	taskMarkerInventory MarkerInventory = OSMarkerInventory{}
	taskProcessDetails                  = lsofProcessDetails
)

const unknownProcessField = "unknown"

// processDetail holds display strings: a field the reader could not read is
// unknownProcessField, never empty.
type processDetail struct {
	cwd   string
	ports string
}

// taskMarkedProcesses returns the scanned processes whose launch environment
// names this task and this home. The environment carries no generation; the
// cleanup claim blocks a reopen for the whole teardown.
func taskMarkedProcesses(inventory MarkerInventory, homeDir, taskID string) ([]MarkedProcess, error) {
	canonical, err := canonicalHome(homeDir)
	if err != nil {
		return nil, err
	}
	scan, err := inventory.ListMarked()
	if err != nil {
		return nil, err
	}
	var owned []MarkedProcess
	for _, process := range scan.Marked {
		if strings.TrimSpace(process.Markers[MarkerMunsuTask]) != taskID {
			continue
		}
		declared, err := canonicalHome(strings.TrimSpace(process.Markers[MarkerMunsuHome]))
		if err != nil || declared != canonical {
			continue
		}
		owned = append(owned, process)
	}
	return owned, nil
}

// survivingTaskProcessSteps returns one teardown step per process that still
// carries the task's markers, ordered by pid, leaving out the processes just
// reaped and the teardown process with its ancestors. An inventory failure is a
// step of its own; a platform without an inventory yields no step.
func survivingTaskProcessSteps(homeDir, taskID string, reaped map[int]bool) []string {
	processes, err := taskMarkedProcesses(taskMarkerInventory, homeDir, taskID)
	if errors.Is(err, ErrProcessInventoryUnsupported) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("could not list task processes: %v", err)}
	}
	lineage := teardownLineage()
	var survivors []MarkedProcess
	var pids []int
	for _, process := range processes {
		if reaped[process.PID] || lineage[process.PID] {
			continue
		}
		survivors = append(survivors, process)
		pids = append(pids, process.PID)
	}
	if len(survivors) == 0 {
		return nil
	}
	sort.Slice(survivors, func(i, j int) bool { return survivors[i].PID < survivors[j].PID })
	details := taskProcessDetails(pids)
	steps := make([]string, 0, len(survivors))
	for _, process := range survivors {
		detail := details[process.PID]
		executable, cwd, ports := process.ExecutablePath, detail.cwd, detail.ports
		for _, field := range []*string{&executable, &cwd, &ports} {
			if *field == "" {
				*field = unknownProcessField
			}
		}
		steps = append(steps, fmt.Sprintf("task process %d still running: %s (cwd %s, listening %s); munsu does not stop it — inspect it by hand or run munsu doctor --orphans", process.PID, executable, cwd, ports))
	}
	return steps
}

// teardownLineage is the teardown process and its ancestors, read from one ps
// table. A ps failure leaves the process and its parent, the part of the chain
// every host can name.
func teardownLineage() map[int]bool {
	lineage := map[int]bool{os.Getpid(): true}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		lineage[os.Getppid()] = true
		return lineage
	}
	parent := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr == nil && ppidErr == nil {
			parent[pid] = ppid
		}
	}
	for pid := parent[os.Getpid()]; pid > 0 && !lineage[pid]; pid = parent[pid] {
		lineage[pid] = true
	}
	return lineage
}

// lsofProcessDetails reads the cwd and listening TCP ports of pids with lsof(8)
// (a macOS system tool; present on the ubuntu runners' image only if installed
// there). Whatever lsof cannot answer stays unknown: this never fails.
func lsofProcessDetails(pids []int) map[int]processDetail {
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	pidArg := strings.Join(list, ",")
	cwds, cwdErr := lsofNames("-p", pidArg, "-d", "cwd")
	listening, portErr := lsofNames("-p", pidArg, "-iTCP", "-sTCP:LISTEN")
	details := make(map[int]processDetail, len(pids))
	for _, pid := range pids {
		detail := processDetail{cwd: unknownProcessField, ports: unknownProcessField}
		if cwdErr == nil && len(cwds[pid]) > 0 {
			detail.cwd = cwds[pid][0]
		}
		if portErr == nil {
			detail.ports = "none"
			var ports []string
			seen := map[string]bool{}
			for _, name := range listening[pid] {
				port := name[strings.LastIndexByte(name, ':')+1:]
				if !seen[port] {
					seen[port] = true
					ports = append(ports, port)
				}
			}
			sort.Slice(ports, func(i, j int) bool {
				a, _ := strconv.Atoi(ports[i])
				b, _ := strconv.Atoi(ports[j])
				return a < b
			})
			if len(ports) > 0 {
				detail.ports = strings.Join(ports, ",")
			}
		}
		details[pid] = detail
	}
	return details
}

// lsofNames runs lsof in field mode and returns the n-field values per pid.
// lsof exits 1 when it finds nothing for the selection, which is an answer.
func lsofNames(selection ...string) (map[int][]string, error) {
	args := append([]string{"-nP", "-w", "-a", "-Fpn"}, selection...)
	var stdout bytes.Buffer
	cmd := exec.Command("lsof", args...)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, err
		}
	}
	names := map[int][]string{}
	pid := 0
	for _, line := range strings.Split(stdout.String(), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid > 0 {
				names[pid] = append(names[pid], line[1:])
			}
		}
	}
	return names, nil
}
