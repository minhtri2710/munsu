package orchestrator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// ReportRequest is one `munsu report`: the reporter's role, task and homes as
// its launch environment names them, and the report itself.
type ReportRequest struct {
	Home       string // the reporter's own home (MUNSU_HOME)
	ParentHome string // a captain's General home (MUNSU_PARENT_STATUS)
	Role       string // MUNSU_ROLE
	TaskID     string // MUNSU_TASK_ID
	TaskKind   string // MUNSU_TASK_KIND, set by a soldier's launch
	State      string
	Message    string
	Key        string
}

// ReportRefusal is a report refused before anything is written. Usage marks a
// refusal of the arguments rather than of the environment or the task.
type ReportRefusal struct {
	Code, Help, Detail string
	Usage              bool
}

func (r *ReportRefusal) Error() string { return r.Detail }

// SubmitReport checks a report against the reporter's role and task and
// appends its one status line in the home the role reports to: a captain
// reports to its General's home, every other role to its own. It returns
// whether the line was an idempotent replay that was not appended.
func SubmitReport(req ReportRequest) (replay bool, err error) {
	if !domain.IsValidStatusState(req.State) {
		return false, &ReportRefusal{Code: "invalid_argument", Usage: true,
			Help:   fmt.Sprintf("Valid states: %s", strings.Join(domain.ValidStatusStates, ", ")),
			Detail: fmt.Sprintf("Invalid status state %q", req.State)}
	}
	if req.TaskID == "" {
		return false, &ReportRefusal{Code: "invalid_environment",
			Help:   "Run inside a munsu-managed task (MUNSU_TASK_ID must be set)",
			Detail: "MUNSU_TASK_ID is not set"}
	}
	// A review soldier's outcome is its verdict file, never a status report.
	if req.Role == "soldier" && req.TaskKind == taskauthority.KindReview {
		return false, &ReportRefusal{Code: "review_task",
			Help:   "A review task reports through its verdict file, not munsu report",
			Detail: fmt.Sprintf("report: task %s is a review task", req.TaskID)}
	}
	target := req.Home
	if req.Role == "captain" {
		if req.ParentHome == "" {
			return false, fmt.Errorf("report: MUNSU_PARENT_STATUS not set for captain role")
		}
		target = req.ParentHome
	}
	// Only "done" completes a ship task's delivery. A soldier's launch names
	// its kind; reading the canonical record would open the dispatcher's home.
	if req.State == "resolved" {
		kind := req.TaskKind
		if req.Role != "soldier" {
			if kind, err = canonicalTaskKind(target, req.TaskID); err != nil {
				return false, fmt.Errorf("report: cannot read task %s kind to check 'resolved': %w", req.TaskID, err)
			}
		}
		if kind == taskauthority.KindShip {
			return false, fmt.Errorf("report: ship task %s cannot use 'resolved' as delivery completion; use 'done' instead", req.TaskID)
		}
	}
	if req.Message == "" {
		return false, fmt.Errorf("report: message is required")
	}
	// A captain's material report is refused from a home whose captain
	// marker, or whose parent's identity, cannot be read.
	if req.Role == "captain" && domain.IsMaterialVerb(req.State) {
		if _, err := mhome.ValidateCaptainProvenance(req.Home); err != nil {
			return false, fmt.Errorf("report: deriving sender identity: %w", err)
		}
		if _, _, err := mhome.ReadHomeIdentity(req.ParentHome); err != nil {
			return false, fmt.Errorf("report: deriving receiver identity: %w", err)
		}
	}
	key := req.Key
	if key == "" {
		key = "default"
	}
	// The key is checked on every report but a soldier's or captain's material
	// one, a scout's done excepted. One rule for every report would refuse keys
	// this path accepts, or accept keys it refuses; that change waits for the
	// Human's words.
	material := domain.IsMaterialVerb(req.State) && (req.Role == "soldier" || req.Role == "captain")
	if !material || (req.Role == "soldier" && req.State == "done" && req.TaskKind == taskauthority.KindScout) {
		if !validReportKey(key) {
			return false, fmt.Errorf("report: invalid terminal key %q: must be a non-empty slug of [A-Za-z0-9_-]", key)
		}
	}
	// A soldier's or captain's line carries the report mark, so it derives a
	// report wake and holds the task's teardown until its parent settles it.
	line := domain.ReportStatusLine(req.State, req.Message, key)
	if req.Role != "soldier" && req.Role != "captain" {
		line = domain.ReportLineText(line)
	}
	replay, err = AppendReportStatus(target, req.TaskID, line)
	if err != nil {
		return false, fmt.Errorf("report: %w", err)
	}
	return replay, nil
}

func validReportKey(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_' || c == '-') {
			return false
		}
	}
	return s != ""
}

// canonicalTaskKind reads the task's kind from its canonical record. A task
// with no canonical record has no kind (""); an unreadable record is an error.
func canonicalTaskKind(homeDir, taskID string) (string, error) {
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return "", nil
	}
	h, err := mhome.Open(homeDir)
	if err != nil {
		return "", fmt.Errorf("opening task authority home %s: %w", homeDir, err)
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return "", err
	}
	agg, err := auth.Get(tid)
	if errors.Is(err, taskauthority.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return agg.Definition.Kind, nil
}

// AppendReportStatus appends one report's status line to the task's status
// file. A report-marked line is always appended: each one is a report of its
// own, so a repeat derives a wake of its own. An unmarked line identical to the
// tail (a dispatcher's consecutive identical report, such as a retry) is not
// appended again. It returns whether the line was such a replay.
func AppendReportStatus(homeDir, taskID, line string) (replay bool, err error) {
	lines, err := mhome.ReadStatus(homeDir, taskID)
	if err != nil {
		return false, fmt.Errorf("reading status: %w", err)
	}
	if !domain.IsReportLine(line) && len(lines) > 0 && lines[len(lines)-1] == line {
		return true, nil
	}
	if err := mhome.AppendStatus(homeDir, taskID, line); err != nil {
		return false, fmt.Errorf("appending status: %w", err)
	}
	return false, nil
}
