package orchestrator

import (
	"errors"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestSubmitReportRefusesBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  ReportRequest
		code string // a ReportRefusal code, or "" for a plain error
		want string
	}{
		{"invalid state", ReportRequest{TaskID: "t1", State: "bogus", Message: "m"}, "invalid_argument", `Invalid status state "bogus"`},
		{"no task id", ReportRequest{State: "working", Message: "m"}, "invalid_environment", "MUNSU_TASK_ID is not set"},
		{"review soldier", ReportRequest{Role: "soldier", TaskID: "t1", TaskKind: taskauthority.KindReview, State: "done", Message: "m"}, "review_task", "is a review task"},
		{"captain without parent", ReportRequest{Role: "captain", TaskID: "t1", State: "working", Message: "m"}, "", "MUNSU_PARENT_STATUS not set"},
		{"ship resolved", ReportRequest{Role: "soldier", TaskID: "t1", TaskKind: taskauthority.KindShip, State: "resolved", Message: "m"}, "", "cannot use 'resolved'"},
		{"empty message", ReportRequest{Role: "soldier", TaskID: "t1", State: "working"}, "", "message is required"},
		{"bad key", ReportRequest{TaskID: "t1", State: "working", Message: "m", Key: "a b"}, "", "invalid terminal key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.req.Home = dir
			_, err := SubmitReport(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SubmitReport = %v, want %q", err, tc.want)
			}
			var refusal *ReportRefusal
			if got := ""; errors.As(err, &refusal) {
				got = refusal.Code
				if got != tc.code {
					t.Fatalf("refusal code = %q, want %q", got, tc.code)
				}
			} else if tc.code != "" {
				t.Fatalf("err %v is not a ReportRefusal %q", err, tc.code)
			}
			if lines, _ := mhome.ReadStatus(dir, "t1"); len(lines) != 0 {
				t.Fatalf("a refused report wrote %q", lines)
			}
		})
	}
}

func TestSubmitReportAppendsOneLine(t *testing.T) {
	dir := t.TempDir()
	req := ReportRequest{Home: dir, Role: "general", TaskID: "t1", State: "working", Message: "on it"}
	if replay, err := SubmitReport(req); err != nil || replay {
		t.Fatalf("first report = %v, %v", replay, err)
	}
	if replay, err := SubmitReport(req); err != nil || !replay {
		t.Fatalf("identical repeat = %v, %v; want an idempotent replay", replay, err)
	}
	if lines, _ := mhome.ReadStatus(dir, "t1"); len(lines) != 1 {
		t.Fatalf("status = %q, want one line", lines)
	}
}
