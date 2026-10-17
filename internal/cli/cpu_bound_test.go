//go:build !race

// These tests are single-goroutine and CPU-bound, so the race detector finds
// nothing in them and multiplies their time; the default lane runs them.

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// exitStatus12 is the error of a real subprocess that exited with code 12: the
// test binary run again with dashExitEnv set, which init answers.
func exitStatus12(t *testing.T) error {
	t.Helper()
	c := exec.Command(os.Args[0])
	c.Env = append(os.Environ(), dashExitEnv+"=1")
	err := c.Run()
	if (dashExecDone{err: err}).exitCode() != 12 {
		t.Fatalf("helper process error = %v, want exit status 12", err)
	}
	return err
}

const dashExitEnv = "MUNSU_DASHBOARD_TEST_EXIT12"

func init() {
	if os.Getenv(dashExitEnv) != "" {
		os.Exit(12)
	}
}

// The shown counts come from the components, and the frame stays inside the
// terminal, in fresh, stale, zero-task, zero-failure and no-event states, at
// every height from tall down to where header and footer alone fill the
// terminal, with the cursor on every item. A "Showing N of M" title is true
// (N rows drawn), a list with no such title drew all its rows, the selected
// row is drawn, and the footer is never what gets cut.
func TestDashboardFrameCountsAndBounds(t *testing.T) {
	type state struct {
		name       string
		nf, nt, ne int
		stale      bool
		feedFailed bool // the events were read, then a later feed read failed
		home       string
		result     bool // a finished command with a long argv and a two-digit exit code
		skipped    int  // malformed lines the good event read skipped
		firstFail  bool // the first and only read failed: no good read before it
		// then moves the model into another mode or adds free text the frame
		// draws; such a row skips the browse-only checks of the fixed-width loop.
		then func(*testing.T, dashboardModel) dashboardModel
		// parts are the exact stripped protected texts the state draws, top
		// to bottom, by terminal height. The drawn-count checks below hold them to
		// what each frame draws.
		parts map[int][]string
	}
	// Free texts, each longer than the widest terminal the sweep uses, so a
	// frame that drew one as protected would show the notice at widths it fits.
	longText := func(what string) string {
		return what + " " + strings.Repeat("with a realistic amount of detail ", 5)
	}
	states := []state{
		{"fresh", 30, 3, 3, false, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 12 of 33 (page 1 of 3)", "Events"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 3", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 0", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 30", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)"},
		}},
		{"fresh many tasks", 0, 30, 40, false, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu  refreshed 0s ago", "Tasks - Showing 19 of 30 (page 1 of 2)", "Events - Showing 5 of 40"},
			12: {" munsu  refreshed 0s ago", "Tasks - Showing 4 of 30 (page 1 of 8)", "Events - Showing 2 of 40"},
			6:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 30 (page 1 of 30)", "Events - Showing 1 of 40"},
			4:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 30 (page 1 of 30)"},
		}},
		{"stale", 30, 3, 3, true, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 11 of 33 (page 1 of 3)", "Events"},
			12: {" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 2", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"},
			6:  {" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"},
			4:  {" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 0"},
		}},
		{"stale many tasks", 2, 30, 40, true, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2", "Failed sources and tasks - Showing 14 of 32 (page 1 of 3)", "Events - Showing 5 of 40"},
			12: {" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2", "Failed sources and tasks - Showing 1 of 32 (page 1 of 32)", "Events - Showing 0 of 40"},
			6:  {" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "Failed sources and tasks - Showing 1 of 32 (page 1 of 32)", "Events - Showing 0 of 40"},
			4:  {" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2 - showing 0"},
		}},
		{"zero tasks", 5, 0, 3, false, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 5", "NEEDS YOU 5", "Failed sources and tasks", "Events"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 5", "NEEDS YOU 5 - showing 3", "Failed sources and tasks - Showing 1 of 5 (page 1 of 5)", "Events - Showing 0 of 3"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 5", "NEEDS YOU 5 - showing 0", "Failed sources and tasks - Showing 1 of 5 (page 1 of 5)", "Events - Showing 0 of 3"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 5", "Failed sources and tasks - Showing 1 of 5 (page 1 of 5)"},
		}},
		{"zero failures", 0, 6, 3, false, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu  refreshed 0s ago", "Tasks", "Events"},
			12: {" munsu  refreshed 0s ago", "Tasks - Showing 4 of 6 (page 1 of 2)", "Events - Showing 2 of 3"},
			6:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 6 (page 1 of 6)", "Events - Showing 1 of 3"},
			4:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 6 (page 1 of 6)"},
		}},
		{"zero events", 3, 6, 0, false, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 3", "NEEDS YOU 3", "Failed sources and tasks", "Events"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 3", "NEEDS YOU 3", "Failed sources and tasks - Showing 1 of 9 (page 1 of 9)", "Events"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 3", "NEEDS YOU 3 - showing 0", "Failed sources and tasks - Showing 1 of 9 (page 1 of 9)", "Events"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 3", "Failed sources and tasks - Showing 1 of 9 (page 1 of 9)"},
		}},
		{"feed unreadable after a read", 2, 6, 40, false, true, "", false, 0, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks", "Events - Showing 5 of 40 - unreadable:"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 1 of 40 - unreadable:"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40 - unreadable:"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)"},
		}},
		{"long home", 2, 6, 40, false, false, strings.Repeat("h", 70), false, 0, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks", "Events - Showing 5 of 40"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 1 of 40"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)"},
		}},
		{"command result", 2, 6, 40, false, false, "", true, 0, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks", "Events - Showing 5 of 40", "exit 12:"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40", "exit 12:"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40", "exit 12:"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0", "exit 12:"},
		}},
		{"feed unreadable after a read, lines skipped", 2, 6, 40, false, true, "", false, 3, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks", "Events - Showing 5 of 40 (3 malformed lines skipped) - unreadable:"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 1 of 40 (3 malformed lines skipped) - unreadable:"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40 (3 malformed lines skipped) - unreadable:"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)"},
		}},
		{"first read failed", 0, 0, 0, false, false, "", false, 0, true, nil, map[int][]string{
			30: {" munsu   FAILED ", "Fleet read failed:", "Events"},
			12: {" munsu   FAILED ", "Fleet read failed:", "Events"},
			6:  {" munsu   FAILED ", "Fleet read failed:", "Events"},
			4:  {" munsu   FAILED ", "Fleet read failed:", "Events"},
		}},
		{"empty", 0, 0, 0, false, false, "", false, 0, false, nil, map[int][]string{
			30: {" munsu  refreshed 0s ago", "No tasks and no failed sources.", "Events"},
			12: {" munsu  refreshed 0s ago", "No tasks and no failed sources.", "Events"},
			6:  {" munsu  refreshed 0s ago", "No tasks and no failed sources.", "Events"},
			4:  {" munsu  refreshed 0s ago", "No tasks and no failed sources.", "Events"},
		}},
		{"loading", 0, 0, 0, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			return testDashModel()
		}, map[int][]string{
			30: {" munsu   LOADING ", "Loading fleet...", "Events"},
			12: {" munsu   LOADING ", "Loading fleet...", "Events"},
			6:  {" munsu   LOADING ", "Loading fleet...", "Events"},
			4:  {" munsu   LOADING ", "Loading fleet...", "Events"},
		}},
		{"lines skipped, feed readable", 2, 6, 40, false, false, "", false, 3, false, nil, map[int][]string{
			30: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks", "Events - Showing 5 of 40 (3 malformed lines skipped)"},
			12: {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 1 of 40 (3 malformed lines skipped)"},
			6:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40 (3 malformed lines skipped)"},
			4:  {" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)"},
		}},
		{"read in progress", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m.reading = true
			return m
		}, map[int][]string{
			30: {" munsu  refreshed 0s ago (reading)", "Tasks", "Events"},
			12: {" munsu  refreshed 0s ago (reading)", "Tasks", "Events - Showing 2 of 3"},
			6:  {" munsu  refreshed 0s ago (reading)", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3"},
			4:  {" munsu  refreshed 0s ago (reading)", "Tasks - Showing 1 of 3 (page 1 of 3)"},
		}},
		{"form mode", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			return press(selectTask(t, m, "t-00"), "m")
		}, map[int][]string{
			30: {" munsu  refreshed 0s ago", "Tasks", "Events"},
			12: {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of 3"},
			6:  {" munsu  refreshed 0s ago"},
			4:  {" munsu  refreshed 0s ago"},
		}},
		{"confirm mode", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m = press(selectTask(t, m, "t-00"), "s")
			return press(send(m, tea.PasteMsg{Content: longText("send this line to the task")}), "enter")
		}, map[int][]string{
			30: {" munsu  refreshed 0s ago", "Tasks", "Events"},
			12: {" munsu  refreshed 0s ago", "Tasks - Showing 2 of 3 (page 1 of 2)", "Events - Showing 2 of 3"},
			6:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)"},
			4:  {" munsu  refreshed 0s ago"},
		}},
		{"running mode", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m.mode = modeRunning
			return m
		}, map[int][]string{
			30: {" munsu  refreshed 0s ago", "Tasks", "Events"},
			12: {" munsu  refreshed 0s ago", "Tasks", "Events - Showing 2 of 3"},
			6:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3"},
			4:  {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)"},
		}},
		{"browse result with output, an error and a notice", 0, 3, 3, false, false, "", false, 0, false, func(t *testing.T, m dashboardModel) dashboardModel {
			m.result = &dashExecDone{
				argv:   []string{"task", "done", longText("t-00")},
				err:    errors.New(longText("fork/exec /bin/munsu: no such file or directory")),
				output: longText("first line of output") + "\n" + longText("second line of output"),
			}
			m.notice = longText("selected task changed before the action ran")
			return m
		}, map[int][]string{
			30: {" munsu  refreshed 0s ago", "Tasks", "Events", "exit -1:"},
			12: {" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of 3", "exit -1:"},
			6:  {" munsu  refreshed 0s ago", "exit -1:"},
			4:  {" munsu  refreshed 0s ago", "exit -1:"},
		}},
	}
	// Longer than the widest terminal the sweep uses, so a clip cuts it.
	feedErr := "open /Users/someone/.munsu/projects/example/state/events.log: permission denied, " + strings.Repeat("and more ", 6)
	titleRe := regexp.MustCompile(`^(Tasks|Failed sources and tasks)( - Showing (\d+) of (\d+) \(page \d+ of \d+\))?$`)
	eventsRe := regexp.MustCompile(`^Events( - Showing (\d+) of (\d+))?`)
	itemRe := regexp.MustCompile(`^[▌ ] (✗ captain:c\d\d|[! ] [^ ]* *t-\d\d)`)
	eventRe := regexp.MustCompile(`^\d\d:\d\d:\d\d `)
	// A narrower terminal wraps the key footer onto more lines (and a confirm's
	// command onto more rows), so the list and feed titles change with the
	// width. The protected parts by state, height and width band; a width in no
	// band has the state's parts.
	type band struct {
		from, to int
		parts    []string
	}
	bands := map[string]map[int][]band{
		"fresh": {
			30: {
				{15, 60, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 13 of 33 (page 1 of 3)", "Events"}},
				{61, 120, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 8 of 33 (page 1 of 5)", "Events"}},
			},
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"}},
				{15, 61, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 1 of 3"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 30", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 30", "NEEDS YOU 30 - showing 0"}},
			},
		},
		"fresh many tasks": {
			30: {
				{15, 42, []string{" munsu  refreshed 0s ago", "Tasks - Showing 20 of 30 (page 1 of 2)", "Events - Showing 5 of 40"}},
				{43, 120, []string{" munsu  refreshed 0s ago", "Tasks - Showing 16 of 30 (page 1 of 2)", "Events - Showing 5 of 40"}},
			},
			12: {
				{15, 41, []string{" munsu  refreshed 0s ago", "Tasks - Showing 5 of 30 (page 1 of 6)", "Events - Showing 2 of 40"}},
			},
			6: {
				{1, 14, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 30 (page 1 of 30)", "Events - Showing 0 of 40"}},
			},
			4: {
				{1, 14, []string{" munsu  refreshed 0s ago", "Tasks - Showing 0 of 30 (page 1 of 30)"}},
			},
		},
		"stale": {
			30: {
				{15, 60, []string{" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 12 of 33 (page 1 of 3)", "Events"}},
				{61, 120, []string{" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 7 of 33 (page 1 of 5)", "Events"}},
			},
			12: {
				{1, 14, []string{" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 4", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"}},
				{15, 61, []string{" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "NEEDS YOU 30 - showing 5", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)", "Events - Showing 0 of 3"}},
			},
			6: {
				{1, 14, []string{" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:", "Failed sources and tasks - Showing 1 of 33 (page 1 of 33)"}},
			},
			4: {
				{1, 14, []string{" munsu   STALE last good read 60s ago  · needs you 30", "Last read failed:"}},
			},
		},
		"stale many tasks": {
			30: {
				{15, 61, []string{" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2", "Failed sources and tasks - Showing 15 of 32 (page 1 of 3)", "Events - Showing 5 of 40"}},
				{62, 120, []string{" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2", "Failed sources and tasks - Showing 10 of 32 (page 1 of 4)", "Events - Showing 5 of 40"}},
			},
			12: {
				{1, 14, []string{" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 32 (page 1 of 16)", "Events - Showing 1 of 40"}},
				{15, 61, []string{" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 32 (page 1 of 16)", "Events - Showing 2 of 40"}},
			},
			6: {
				{1, 14, []string{" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:", "Failed sources and tasks - Showing 1 of 32 (page 1 of 32)"}},
			},
			4: {
				{1, 14, []string{" munsu   STALE last good read 60s ago  · needs you 2", "Last read failed:"}},
			},
		},
		"zero tasks": {
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 5", "NEEDS YOU 5", "Failed sources and tasks - Showing 1 of 5 (page 1 of 5)", "Events - Showing 0 of 3"}},
				{15, 59, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 5", "NEEDS YOU 5", "Failed sources and tasks - Showing 1 of 5 (page 1 of 5)", "Events - Showing 1 of 3"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 5", "Failed sources and tasks - Showing 1 of 5 (page 1 of 5)", "Events - Showing 0 of 3"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 5", "NEEDS YOU 5 - showing 0"}},
			},
		},
		"zero failures": {
			12: {
				{15, 40, []string{" munsu  refreshed 0s ago", "Tasks - Showing 5 of 6 (page 1 of 2)", "Events - Showing 2 of 3"}},
			},
			6: {
				{1, 14, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 6 (page 1 of 6)", "Events - Showing 0 of 3"}},
			},
			4: {
				{1, 14, []string{" munsu  refreshed 0s ago", "Tasks - Showing 0 of 6 (page 1 of 6)"}},
			},
		},
		"zero events": {
			12: {
				{1, 59, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 3", "NEEDS YOU 3", "Failed sources and tasks - Showing 2 of 9 (page 1 of 5)", "Events"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 3", "Failed sources and tasks - Showing 1 of 9 (page 1 of 9)", "Events"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 3", "NEEDS YOU 3 - showing 0"}},
			},
		},
		"feed unreadable after a read": {
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 8 (page 1 of 4)", "Events - Showing 2 of 40 - unreadable:"}},
				{15, 59, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 3 of 8 (page 1 of 3)", "Events - Showing 2 of 40 - unreadable:"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40 - unreadable:"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0"}},
			},
		},
		"long home": {
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 8 (page 1 of 4)", "Events - Showing 2 of 40"}},
				{15, 59, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 3 of 8 (page 1 of 3)", "Events - Showing 2 of 40"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0"}},
			},
		},
		"command result": {
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 8 (page 1 of 4)", "Events - Showing 1 of 40", "exit 12:"}},
				{15, 59, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 8 (page 1 of 4)", "Events - Showing 2 of 40", "exit 12:"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "exit 12:"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "exit 12:"}},
			},
		},
		"feed unreadable after a read, lines skipped": {
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 8 (page 1 of 4)", "Events - Showing 2 of 40 (3 malformed lines skipped) - unreadable:"}},
				{15, 71, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 3 of 8 (page 1 of 3)", "Events - Showing 2 of 40 (3 malformed lines skipped) - unreadable:"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40 (3 malformed lines skipped) - unreadable:"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0"}},
			},
		},
		"first read failed": {
			4: {
				{1, 14, []string{" munsu   FAILED ", "Fleet read failed:"}},
			},
		},
		"empty": {
			4: {
				{1, 14, []string{" munsu  refreshed 0s ago", "No tasks and no failed sources."}},
			},
		},
		"loading": {
			4: {
				{1, 14, []string{" munsu   LOADING ", "Loading fleet..."}},
			},
		},
		"lines skipped, feed readable": {
			12: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 2 of 8 (page 1 of 4)", "Events - Showing 2 of 40 (3 malformed lines skipped)"}},
				{15, 59, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2", "Failed sources and tasks - Showing 3 of 8 (page 1 of 3)", "Events - Showing 2 of 40 (3 malformed lines skipped)"}},
			},
			6: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "Failed sources and tasks - Showing 1 of 8 (page 1 of 8)", "Events - Showing 0 of 40 (3 malformed lines skipped)"}},
			},
			4: {
				{1, 14, []string{" munsu   PARTIAL refreshed 0s ago  · needs you 2", "NEEDS YOU 2 - showing 0"}},
			},
		},
		"read in progress": {
			6: {
				{1, 14, []string{" munsu  refreshed 0s ago (reading)", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of 3"}},
			},
			4: {
				{1, 14, []string{" munsu  refreshed 0s ago (reading)", "Tasks - Showing 0 of 3 (page 1 of 3)"}},
			},
		},
		"form mode": {
			12: {
				{1, 29, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3"}},
				{30, 40, []string{" munsu  refreshed 0s ago", "Tasks - Showing 2 of 3 (page 1 of 2)", "Events - Showing 1 of 3"}},
			},
		},
		"confirm mode": {
			30: {
				{1, 9, []string{" munsu  refreshed 0s ago"}},
				{10, 10, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)"}},
				{11, 11, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3"}},
				{12, 13, []string{" munsu  refreshed 0s ago", "Tasks - Showing 2 of 3 (page 1 of 2)", "Events - Showing 2 of 3"}},
			},
			12: {
				{1, 29, []string{" munsu  refreshed 0s ago"}},
				{30, 33, []string{" munsu  refreshed 0s ago", "Tasks - Showing 0 of 3 (page 1 of 3)"}},
				{34, 39, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)"}},
				{40, 46, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of 3"}},
				{47, 58, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3"}},
				{59, 78, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 0 of 3"}},
				{79, 117, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3"}},
				{118, 120, []string{" munsu  refreshed 0s ago", "Tasks - Showing 2 of 3 (page 1 of 2)", "Events - Showing 1 of 3"}},
			},
			6: {
				{1, 117, []string{" munsu  refreshed 0s ago"}},
				{118, 120, []string{" munsu  refreshed 0s ago", "Tasks - Showing 0 of 3 (page 1 of 3)"}},
			},
		},
		"browse result with output, an error and a notice": {
			12: {
				{1, 14, []string{" munsu  refreshed 0s ago", "Tasks - Showing 1 of 3 (page 1 of 3)", "Events - Showing 1 of 3", "exit -1:"}},
				{15, 40, []string{" munsu  refreshed 0s ago", "Tasks - Showing 2 of 3 (page 1 of 2)", "Events - Showing 1 of 3", "exit -1:"}},
			},
		},
	}
	for _, st := range states {
		var tasks []fleet.TaskSnapshot
		for i := 0; i < st.nt; i++ {
			tasks = append(tasks, row(fmt.Sprintf("t-%02d", i), "working", longText(fmt.Sprintf("implementing step %d", i)), "primary", ""))
		}
		var failures []fleet.SourceFailure
		for i := 0; i < st.nf; i++ {
			failures = append(failures, fleet.SourceFailure{Source: fmt.Sprintf("captain:c%02d", i), Err: errors.New(longText(fmt.Sprintf("captain %d: open state/tasks.json: permission denied", i)))})
		}
		var events []orchestrator.Record
		for i := 1; i <= st.ne; i++ {
			events = append(events, ev(uint64(i), "watcher.cycle.failed", "munsu:watcher", "k", longText(fmt.Sprintf("step %d", i))))
		}
		base := testDashModel()
		if st.home != "" {
			base.home = st.home
		}
		if st.firstFail {
			base = send(base, dashRead{at: dashNow, snapErr: errors.New("scanning captain home /Users/someone/.munsu/projects/example/state/captains/c01: " + feedErr)})
		} else {
			good := goodRead(dashNow, tasks, failures, events)
			good.skipped = st.skipped
			base = send(base, good)
		}
		if st.result {
			base.result = &dashExecDone{argv: []string{"task", "done", strings.Repeat("a long argument ", 10)}, err: exitStatus12(t)}
		}
		if st.feedFailed {
			base = send(base, dashRead{at: dashNow, snap: &fleet.DisplaySnapshot{Tasks: tasks, Failures: failures}, eventErr: errors.New(feedErr)})
		}
		if st.stale {
			base = send(base, dashRead{at: dashNow.Add(time.Minute), snapErr: errors.New(longText("scanning captain home: read tasks: i/o error"))})
			base.now = func() time.Time { return dashNow.Add(time.Minute) }
		}
		total := st.nf + st.nt
		notes := 0
		if st.stale || st.result {
			notes = 1
		}
		if st.then != nil {
			base = st.then(t, base)
		}
		for _, w := range []int{100, 60} {
			if st.then != nil {
				continue
			}
			if st.skipped > 0 && w == 60 {
				// This row's feed title (error plus skipped note) is about 80
				// columns protected, so 60 is the notice; the sweep below
				// covers that width.
				continue
			}
			at := resized(base, w, 24)
			for cur := 0; cur < total; cur++ {
				if cur > 0 {
					at = press(at, "j")
				}
				for h := 30; h >= 1; h-- {
					m := resized(at, w, h)
					lines := strings.Split(ansi.Strip(m.frame()), "\n")
					where := fmt.Sprintf("%s %dx%d cursor %d", st.name, w, h, cur)
					if len(lines) > h {
						t.Fatalf("%s: %d lines, terminal has %d", where, len(lines), h)
					}
					for _, l := range lines {
						if ansi.StringWidth(l) > w {
							t.Fatalf("%s: line wider than the terminal: %q", where, l)
						}
					}
					// NEEDS YOU gives way to the footer, the list's title and
					// first row, and the feed's title.
					avail := h - 2 - notes - len(m.footer())
					if avail < 0 {
						continue
					}
					if !strings.HasSuffix(strings.TrimRight(lines[len(lines)-1], " "), "q quit") {
						t.Fatalf("%s: the footer is cut:\n%s", where, strings.Join(lines, "\n"))
					}
					var items, evs, titleN, titleM, feedN, feedM = 0, 0, -1, -1, -1, -1
					var listTitle, feedTitle, selected bool
					for _, l := range lines {
						l = dashUnbox(l)
						switch {
						case itemRe.MatchString(l):
							items++
							selected = selected || strings.HasPrefix(l, "▌ ")
						case eventRe.MatchString(l):
							evs++
						case titleRe.MatchString(l):
							listTitle = true
							if want := map[bool]string{true: "Failed sources and tasks", false: "Tasks"}[st.nf > 0]; titleRe.FindStringSubmatch(l)[1] != want {
								t.Fatalf("%s: list title %q, want %q:\n%s", where, l, want, strings.Join(lines, "\n"))
							}
							if g := titleRe.FindStringSubmatch(l); g[3] != "" {
								titleN, _ = strconv.Atoi(g[3])
								titleM, _ = strconv.Atoi(g[4])
							}
						case strings.HasPrefix(l, "Events"):
							feedTitle = true
							if st.feedFailed && !strings.Contains(l, " - unreadable: o") {
								t.Fatalf("%s: feed title %q does not say the feed is unreadable:\n%s", where, l, strings.Join(lines, "\n"))
							}
							if st.skipped > 0 && !strings.Contains(l, "(3 malformed lines skipped)") {
								t.Fatalf("%s: feed title %q lacks the malformed lines note:\n%s", where, l, strings.Join(lines, "\n"))
							}
							if g := eventsRe.FindStringSubmatch(l); g[2] != "" {
								feedN, _ = strconv.Atoi(g[2])
								feedM, _ = strconv.Atoi(g[3])
							}
						}
					}
					if total > 0 && avail >= 2 && (!listTitle || items == 0) || avail >= 3 && !feedTitle {
						t.Fatalf("%s: a section title is missing:\n%s", where, strings.Join(lines, "\n"))
					}
					if listTitle {
						if titleN >= 0 && (items != titleN || titleM != total) || titleN < 0 && items != total {
							t.Fatalf("%s: %d items drawn, title says %d of %d, total %d:\n%s", where, items, titleN, titleM, total, strings.Join(lines, "\n"))
						}
						if items > 0 && !selected {
							t.Fatalf("%s: the selected row is not drawn:\n%s", where, strings.Join(lines, "\n"))
						}
					}
					if feedTitle && st.ne > 0 {
						if feedN >= 0 && (evs != feedN || feedM != st.ne) || feedN < 0 && evs != st.ne {
							t.Fatalf("%s: %d events drawn, title says %d of %d, read %d:\n%s", where, evs, feedN, feedM, st.ne, strings.Join(lines, "\n"))
						}
					}
				}
			}
		}
		// The protected parts layout() returns for the state equal the authored
		// table exactly, so a free text made protected fails whatever its length.
		// Then at every width from 1 to 120: the frame is the "Terminal too
		// small" notice exactly when a table entry does not fit, else every entry
		// is drawn whole.
		notice := "Terminal too small: enlarge it."
		if base.mode == modeForm || base.mode == modeConfirm {
			notice = "Terminal too small: enlarge it, or esc to cancel."
		}
		// keepsAt is the protected parts layout() returns: the only read of
		// layout() here, checked against the authored table at every size.
		keepsAt := func(w, h int) []string {
			var got []string
			for _, l := range resized(base, w, h).layout() {
				if l.keep.text != "" {
					got = append(got, ansi.Strip(l.keep.text))
				}
			}
			return got
		}
		for _, h := range []int{30, 12, 6, 4} {
			partsAt := func(w int) []string {
				for _, b := range bands[st.name][h] {
					if w >= b.from && w <= b.to {
						return b.parts
					}
				}
				return st.parts[h]
			}
			if got := keepsAt(1000, h); !slices.Equal(got, st.parts[h]) {
				t.Fatalf("%s %d: protected parts\n got  %q\n want %q", st.name, h, got, st.parts[h])
			}
			// Free text follows a part when its line, without its border, at the
			// widest stacked width is longer. Another width can change a part's
			// counts, never its kind, so parts match with their digits masked.
			digits := regexp.MustCompile(`\d+`)
			follows := map[string]bool{}
			for _, l := range strings.Split(ansi.Strip(resized(base, dashSideMin-1, h).frame()), "\n") {
				l = digits.ReplaceAllString(dashUnbox(l), "#")
				for _, p := range st.parts[h] {
					if p = digits.ReplaceAllString(p, "#"); strings.HasPrefix(l, p) && len(l) > len(p) {
						follows[p] = true
					}
				}
			}
			for w := 1; w <= 120; w++ {
				f := ansi.Strip(resized(base, w, h).frame())
				where := fmt.Sprintf("%s %dx%d", st.name, w, h)
				lines := strings.Split(f, "\n")
				if len(lines) > h {
					t.Fatalf("%s: %d lines, terminal has %d", where, len(lines), h)
				}
				prot := partsAt(w)
				if got := keepsAt(w, h); !slices.Equal(got, prot) {
					t.Fatalf("%s: protected parts\n got  %q\n want %q", where, got, prot)
				}
				tooSmall := false
				for _, p := range prot {
					tooSmall = tooSmall || ansi.StringWidth(p) > w || ansi.StringWidth(p) == w && follows[digits.ReplaceAllString(p, "#")]
				}
				drawn := strings.TrimRight(f, " ") == ansi.Truncate(notice, w, "…")
				if drawn != tooSmall {
					t.Fatalf("%s: notice drawn = %v, a protected part is wider than the terminal = %v (%q):\n%s", where, drawn, tooSmall, prot, f)
				}
				for _, l := range lines {
					if ansi.StringWidth(l) > w {
						t.Fatalf("%s: line wider than the terminal: %q", where, l)
					}
				}
				for _, p := range prot {
					if !drawn && !strings.Contains(f, p) {
						t.Fatalf("%s: protected part %q is not drawn whole:\n%s", where, p, f)
					}
				}
				if drawn {
					continue
				}
				// Every drawn "Showing N of M", in every mode: N is the rows on
				// screen and M the real total; a title without one drew them all.
				items, evs, listN, listM, feedN, feedM := 0, 0, -1, -1, -1, -1
				var listTitle, feedTitle bool
				for _, l := range lines {
					l = dashUnbox(l)
					switch {
					case itemRe.MatchString(l):
						items++
					case eventRe.MatchString(l):
						evs++
					case titleRe.MatchString(l):
						listTitle = true
						if g := titleRe.FindStringSubmatch(l); g[3] != "" {
							listN, _ = strconv.Atoi(g[3])
							listM, _ = strconv.Atoi(g[4])
						}
					case strings.HasPrefix(l, "Events"):
						feedTitle = true
						if g := eventsRe.FindStringSubmatch(l); g[2] != "" {
							feedN, _ = strconv.Atoi(g[2])
							feedM, _ = strconv.Atoi(g[3])
						}
					}
				}
				if listTitle && (listN >= 0 && (items != listN || listM != total) || listN < 0 && items != total) {
					t.Fatalf("%s: %d items drawn, list title says %d of %d, total %d:\n%s", where, items, listN, listM, total, f)
				}
				if feedTitle && (feedN >= 0 && (evs != feedN || feedM != st.ne) || feedN < 0 && evs != st.ne) {
					t.Fatalf("%s: %d events drawn, feed title says %d of %d, read %d:\n%s", where, evs, feedN, feedM, st.ne, f)
				}
			}
		}
	}
}

// TestShellWriteReadsLongPrefixesInLinearTime pins R5 of review-80b12d4f0a2e:
// a run of words before the command word, and a directory stack pushd
// grows, cost linear time (fastestRuns).
func TestShellWriteReadsLongPrefixesInLinearTime(t *testing.T) {
	dir := t.TempDir()
	want := []string{filepath.Join(dir, "x")}
	for _, shape := range []struct {
		name, repeat, tail string
	}{
		{"time", "time ", "rm x"},
		{"time -p", "time -p ", "rm x"},
		{"pushd", "pushd " + dir + "; ", "rm x"},
	} {
		run := func(n int) func() {
			command := strings.Repeat(shape.repeat, n) + shape.tail
			return func() {
				if targets, ambiguous := shellWriteTargets(dir, command); ambiguous || !slices.Equal(targets, want) {
					t.Fatalf("shellWriteTargets of %s × %d = %q ambiguous=%v, want %q", shape.name, n, targets, ambiguous, want)
				}
			}
		}
		if small, large := fastestRuns(run(1250), run(20000)); large > 64*small {
			t.Errorf("%s × 20000 took %v, %.1f times 1250 (%v), want under 64", shape.name, large, float64(large)/float64(small), small)
		}
	}
}

// TestSafetyCheckReadsLongCommandInLinearTime pins that the hook's walk over
// a segment grows linearly: a git add of 16000 words took 32.9s and gigabytes
// when every position copied the words after it, and takes milliseconds and
// under 100 MiB without.
func TestSafetyCheckReadsLongCommandInLinearTime(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-fs", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-fs")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-fs")
	var words strings.Builder
	for i := range 16000 {
		fmt.Fprintf(&words, " f%d", i)
	}
	command := "git add" + words.String()
	allocated, elapsed := measureAllocation(func() {
		if block, reason := runPiSafetyForGit(t, worktree, command); block {
			t.Errorf("git add of 16000 words: block=true reason=%q, want allow", reason)
		}
	})
	if elapsed > 5*time.Second || allocated > 512<<20 {
		t.Errorf("git add of 16000 words took %v and %d MiB, want under 5s and 512 MiB", elapsed, allocated>>20)
	}
	// The write guard's enumeration of a write segment is linear too. Its
	// per-target classification is not what this pins.
	segment := tokenizeSegments(backslashEscapes, "rm"+words.String())[0]
	allocated, elapsed = measureAllocation(func() {
		if readings, ok := writeReadings(segment); !ok || len(readings) != 1 || len(readings[0]) != len(segment) {
			t.Errorf("writeReadings of rm with 16000 words = %d readings ok=%v, want one", len(readings), ok)
		}
	})
	if elapsed > 5*time.Second || allocated > 512<<20 {
		t.Errorf("writeReadings of rm with 16000 words took %v and %d MiB, want under 5s and 512 MiB", elapsed, allocated>>20)
	}
	// So is the dedupe of its targets: sixteen times the targets cost about
	// sixteen times as much, where a list scan per target cost about 256.
	// Comparing two sizes under the same load keeps -race and a busy host
	// from deciding the verdict.
	targets := func(n int) func() {
		command := "rm " + strings.Join(strings.Fields(words.String())[:n], " ")
		return func() {
			if targets, ambiguous := shellWriteTargets(worktree, command); len(targets) != n || ambiguous {
				t.Errorf("shellWriteTargets of rm with %d words = %d targets ambiguous=%v, want %d", n, len(targets), ambiguous, n)
			}
		}
	}
	if small, large := fastestRuns(targets(1000), targets(16000)); large > 64*small {
		t.Errorf("shellWriteTargets of rm with 16000 words took %v, %.0f times 1000 words (%v), want under 64", large, float64(large)/float64(small), small)
	}
	// The git guard reads each distinct word that reads as more than itself
	// once: sixteen times the quoted words cost about fourteen times as much,
	// where a list scan per word cost about 75.
	echo := func(n int) func() {
		var command strings.Builder
		command.WriteString("echo")
		for i := range n {
			fmt.Fprintf(&command, " 'a %d'", i)
		}
		return func() {
			if block, reason := runPiSafetyForGit(t, worktree, command.String()); block {
				t.Errorf("echo of %d quoted words: block=true reason=%q, want allow", n, reason)
			}
		}
	}
	if small, large := fastestRuns(echo(4000), echo(64000)); large > 32*small {
		t.Errorf("echo of 64000 quoted words took %v, %.0f times 4000 words (%v), want under 32", large, float64(large)/float64(small), small)
	}
}
