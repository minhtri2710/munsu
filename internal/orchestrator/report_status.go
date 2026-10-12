package orchestrator

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

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
