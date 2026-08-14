package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tauth "github.com/minhtri2710/munsu/internal/taskauthority"
)

// Orphan scanning is REPORT ONLY. No path in this file signals, terminates or
// otherwise touches a scanned process: CompositeWriterFence.Controller is
// never reached from InspectOrphans. Automatic cleanup stays out until an
// explicit adoption registry exists, because no passive signal distinguishes
// a leftover from a daemon a member started on purpose (ADR-BEO-40-01 §5).

// Ownership marker keys read out of a scanned process environment. The list is
// a whitelist: no other key is ever kept, rendered or logged, because process
// environments carry credentials (MULTICA_TOKEN, EXA_API_KEY, ...).
// It holds exactly the keys a classifier reads — a key nothing resolves would
// widen the read for nothing.
const (
	MarkerMulticaTask = "MULTICA_TASK_ID"
	MarkerMunsuTask   = "MUNSU_TASK_ID"
	MarkerMunsuHome   = "MUNSU_HOME"
	MarkerTmpdir      = "TMPDIR"
)

var orphanMarkerKeys = map[string]bool{
	MarkerMulticaTask: true, MarkerMunsuTask: true, MarkerMunsuHome: true, MarkerTmpdir: true,
}

// runScopedTmpdirBasePrefix is the observed naming convention of the per-run
// temporary directory the Multica runtime creates and removes with the run
// (BEO-45 correlated it 6/6). It is a convention read off running processes,
// NOT a contract read out of runtime source: a runtime that skips the removal
// on its crash path leaves the directory in place and this oracle then reports
// the run as alive. That direction is the safe one — the scan misses garbage,
// it never invents it.
const runScopedTmpdirBasePrefix = "multica-task-"

// Ownership layers. L0 processes descend from a Multica agent run and are not
// spawned by munsu; L1 processes descend from a munsu soldier launch. Only L1
// has an authoritative liveness oracle (task authority).
const (
	LayerRuntimeRun = "L0"
	LayerMunsuTask  = "L1"
	LayerWriter     = "writer"
)

// OrphanVerdict is the classification of one scanned process. UNKNOWN is a
// real answer, not a soft GARBAGE: it means no oracle resolved the owning run,
// so only a human can decide.
type OrphanVerdict string

const (
	VerdictOwned   OrphanVerdict = "OWNED"
	VerdictGarbage OrphanVerdict = "GARBAGE"
	VerdictUnknown OrphanVerdict = "UNKNOWN"
)

// MarkedProcess is one process carrying at least one ownership marker.
// Markers holds whitelisted keys only.
type MarkedProcess struct {
	PID            int
	PPID           int
	PGID           int
	ExecutablePath string
	Markers        map[string]string
}

// MarkerScan is the result of one pass over the process table. Total counts
// every process the pass saw and Unreadable those whose environment it could
// not read, so a report can state how much of the machine it actually looked
// at and how much of it stayed opaque.
type MarkerScan struct {
	Total      int
	Unreadable int
	Marked     []MarkedProcess
}

// MarkerInventory enumerates processes carrying ownership markers. The scan
// walks the whole process table rather than a process group, so a process that
// called setsid() out of its run's group is still seen (3/3 of the leftovers
// BEO-45 inventoried had done exactly that).
//
// It sees a process only if it can read that process's environment. Another
// user's processes, and on macOS every Apple platform binary (/bin/bash,
// /usr/bin/python3 — the exact shape of the BEO-40 leftover), refuse that read:
// the kernel hands back the executable path and nothing else. Those processes
// are counted as unreadable rather than silently treated as unmarked.
type MarkerInventory interface {
	ListMarked() (MarkerScan, error)
}

type OSMarkerInventory struct{}

func (OSMarkerInventory) ListMarked() (MarkerScan, error) { return listMarkedProcesses() }

// RunLiveness is what an oracle concluded about the run that spawned a marked
// process. Unresolved is the default: an oracle that cannot answer says so.
type RunLiveness int

const (
	RunUnresolved RunLiveness = iota
	RunAlive
	RunEnded
)

// RunOracle resolves the liveness of the run owning a marked process, and
// returns the evidence it used.
type RunOracle interface {
	RunLiveness(scanHome string, process MarkedProcess) (RunLiveness, string)
}

// OSRunOracle dispatches by marker family: munsu tasks resolve against task
// authority (authoritative), runtime runs against the run-scoped TMPDIR
// (correlated observation).
type OSRunOracle struct{}

func (OSRunOracle) RunLiveness(scanHome string, process MarkedProcess) (RunLiveness, string) {
	if task := strings.TrimSpace(process.Markers[MarkerMunsuTask]); task != "" {
		return munsuTaskLiveness(scanHome, process, task)
	}
	if strings.TrimSpace(process.Markers[MarkerMulticaTask]) != "" {
		return runtimeTmpdirLiveness(process)
	}
	return RunUnresolved, "no ownership marker to resolve"
}

func munsuTaskLiveness(scanHome string, process MarkedProcess, task string) (RunLiveness, string) {
	declared := strings.TrimSpace(process.Markers[MarkerMunsuHome])
	canonical, err := canonicalHome(declared)
	if err != nil || canonical != scanHome {
		return RunUnresolved, fmt.Sprintf("MUNSU_HOME %q is not the scanned home", declared)
	}
	aggregates, err := canonicalAggregates(scanHome)
	if err != nil {
		return RunUnresolved, fmt.Sprintf("task authority unreadable: %v", err)
	}
	aggregate, ok := aggregates[task]
	if !ok {
		// Absence is not evidence the run ended. Both oracles conclude
		// GARBAGE only from positive evidence — L0 from a run-scoped TMPDIR
		// that is demonstrably gone, L1 from a record that says retired. A
		// live soldier whose record has not committed yet, or a home that was
		// rebuilt under it, would otherwise be labelled GARBAGE and hand a
		// member a reason to kill it: the same irreversible mistake automatic
		// cleanup would make, one step slower.
		return RunUnresolved, fmt.Sprintf("task %s has no current record in this home; absence does not prove the run ended", task)
	}
	if aggregate.Phase == tauth.PhaseRetired {
		return RunEnded, fmt.Sprintf("task %s is retired", task)
	}
	return RunAlive, fmt.Sprintf("task %s is %s", task, aggregate.Phase)
}

func runtimeTmpdirLiveness(process MarkedProcess) (RunLiveness, string) {
	tmpdir := strings.TrimSpace(process.Markers[MarkerTmpdir])
	if tmpdir == "" {
		return RunUnresolved, "no TMPDIR recorded for the owning run"
	}
	cleaned := filepath.Clean(tmpdir)
	if !strings.HasPrefix(filepath.Base(cleaned), runScopedTmpdirBasePrefix) {
		return RunUnresolved, fmt.Sprintf("TMPDIR %s is not a run-scoped directory", cleaned)
	}
	if _, err := os.Stat(cleaned); err != nil {
		if os.IsNotExist(err) {
			return RunEnded, fmt.Sprintf("run TMPDIR %s no longer exists", cleaned)
		}
		return RunUnresolved, fmt.Sprintf("run TMPDIR %s is unreadable: %v", cleaned, err)
	}
	return RunAlive, fmt.Sprintf("run TMPDIR %s still exists", cleaned)
}

// OrphanProcess is one classified process. Only the executable path is kept
// from the command line, and only whitelisted markers from the environment.
type OrphanProcess struct {
	PID            int
	PPID           int
	PGID           int
	Layer          string
	Kind           string
	TaskID         string
	ExecutablePath string
	Verdict        OrphanVerdict
	Reason         string
}

// OrphanReport is a read-only classification of the process table. Notes carry
// the limits of the pass that produced it.
type OrphanReport struct {
	Home           string
	Scanned        int
	Unreadable     int
	Marked         int
	Garbage        []OrphanProcess
	Unknown        []OrphanProcess
	Owned          []OrphanProcess
	StaleArtifacts []string
	Notes          []string
}

// InspectOrphans classifies every process carrying an ownership marker, plus
// this home's writer processes, and reports them. It never terminates
// anything.
func (f CompositeWriterFence) InspectOrphans(homeDir string) (OrphanReport, error) {
	if f.Marked == nil || f.Oracle == nil {
		return OrphanReport{}, fmt.Errorf("orphan scan requires a marker inventory and a run oracle")
	}
	scanHome, err := canonicalHome(homeDir)
	if err != nil {
		return OrphanReport{}, fmt.Errorf("canonicalizing home: %w", err)
	}
	scan, err := f.Marked.ListMarked()
	if err != nil {
		return OrphanReport{}, err
	}
	report := OrphanReport{Home: scanHome, Scanned: scan.Total, Unreadable: scan.Unreadable, Marked: len(scan.Marked)}
	if scan.Unreadable > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("%d processes kept their environment out of reach (another user, or a system-restricted binary such as /bin/bash on macOS); they carry no verdict here", scan.Unreadable))
	}
	entries := make(map[int]*OrphanProcess, len(scan.Marked))
	for _, process := range scan.Marked {
		liveness, reason := f.Oracle.RunLiveness(scanHome, process)
		entries[process.PID] = &OrphanProcess{
			PID: process.PID, PPID: process.PPID, PGID: process.PGID,
			Layer: markedLayer(process), TaskID: markedTask(process),
			ExecutablePath: process.ExecutablePath, Verdict: verdictFor(liveness), Reason: reason,
		}
	}
	if err := f.appendWriterEvidence(scanHome, entries, &report); err != nil {
		return report, err
	}
	for _, entry := range entries {
		switch entry.Verdict {
		case VerdictGarbage:
			report.Garbage = append(report.Garbage, *entry)
		case VerdictOwned:
			report.Owned = append(report.Owned, *entry)
		default:
			report.Unknown = append(report.Unknown, *entry)
		}
	}
	sortByPID(report.Garbage)
	sortByPID(report.Unknown)
	sortByPID(report.Owned)
	sort.Strings(report.StaleArtifacts)
	return report, nil
}

// appendWriterEvidence folds this home's durable writer identities and the
// writer processes they claim into the report: a claimed process is OWNED, an
// unclaimed one is UNKNOWN, and an identity whose process is verified gone is
// a stale artifact. A marker verdict already reached for the same PID stands —
// a writer that registered itself says nothing about the run that spawned it.
func (f CompositeWriterFence) appendWriterEvidence(scanHome string, entries map[int]*OrphanProcess, report *OrphanReport) error {
	if f.Processes == nil || f.Artifacts == nil || f.Verifier == nil {
		report.Notes = append(report.Notes, "writer identity evidence skipped: fence has no artifact scanner, process inventory or verifier")
		return nil
	}
	processes, err := f.Processes.List(scanHome)
	if err != nil {
		if errors.Is(err, ErrProcessInventoryUnsupported) {
			report.Notes = append(report.Notes, "writer identity evidence skipped: OS process inventory is unsupported on this platform")
			return nil
		}
		return err
	}
	artifacts, err := f.Artifacts.Scan(scanHome)
	if err != nil {
		return err
	}
	artifactByPID := make(map[int]WriterArtifact, len(artifacts))
	for _, artifact := range artifacts {
		artifactByPID[artifact.PID] = artifact
	}
	live := make(map[int]bool, len(processes))
	for _, process := range processes {
		live[process.PID] = true
		artifact, matched := artifactByPID[process.PID]
		claimed := matched && artifact.StartToken == process.StartToken && artifact.Kind == process.Kind
		entry, known := entries[process.PID]
		if !known {
			entry = &OrphanProcess{PID: process.PID, Layer: LayerWriter, ExecutablePath: process.ExecutablePath}
			entries[process.PID] = entry
			if claimed {
				entry.Verdict = VerdictOwned
				entry.Reason = fmt.Sprintf("writer identity %s claims PID %d", artifact.Path, process.PID)
			} else {
				entry.Verdict = VerdictUnknown
				entry.Reason = "no writer identity artifact in this home claims this process"
			}
		}
		entry.Kind = process.Kind
	}
	for _, artifact := range artifacts {
		if live[artifact.PID] {
			continue
		}
		dead, err := f.Verifier.VerifyDead(artifact)
		if err != nil {
			return fmt.Errorf("verifying writer artifact %s: %w", artifact.Path, err)
		}
		if dead {
			report.StaleArtifacts = append(report.StaleArtifacts, artifact.Path)
		}
	}
	return nil
}

func markedLayer(process MarkedProcess) string {
	if strings.TrimSpace(process.Markers[MarkerMunsuTask]) != "" {
		return LayerMunsuTask
	}
	return LayerRuntimeRun
}

func markedTask(process MarkedProcess) string {
	if task := strings.TrimSpace(process.Markers[MarkerMunsuTask]); task != "" {
		return task
	}
	return strings.TrimSpace(process.Markers[MarkerMulticaTask])
}

func verdictFor(liveness RunLiveness) OrphanVerdict {
	switch liveness {
	case RunAlive:
		return VerdictOwned
	case RunEnded:
		return VerdictGarbage
	default:
		return VerdictUnknown
	}
}

func sortByPID(processes []OrphanProcess) {
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
}

// keepMarkers copies the whitelisted keys out of a raw KEY=VALUE environment
// block and drops everything else, so no credential ever leaves the scan.
func keepMarkers(environment []string) map[string]string {
	markers := make(map[string]string)
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		key := entry[:separator]
		if !orphanMarkerKeys[key] {
			continue
		}
		markers[key] = entry[separator+1:]
	}
	return markers
}

// hasOwnershipMarker reports whether a process declares an owning run. A
// process without one is out of the scan's reach entirely — it is not reported
// as UNKNOWN, because that would make every process on the machine a finding.
func hasOwnershipMarker(markers map[string]string) bool {
	return strings.TrimSpace(markers[MarkerMulticaTask]) != "" || strings.TrimSpace(markers[MarkerMunsuTask]) != ""
}
