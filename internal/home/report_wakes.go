package home

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
)

// ReportWakeKind is the kind of the wake a `munsu report` line derives.
const ReportWakeKind = "report"

// A task's status file is its reporter's report log: every material line
// `munsu report` wrote (domain.IsReportLine) becomes one report wake, derived
// by the unfenced writers of the wake queue under the wake lock. The task's
// report cursor records how far derivation has read and which derived reports
// are not settled yet; only those writers and settlement write it.
type reportCursor struct {
	Next    int      `json:"next"`
	LastSHA string   `json:"last_sha,omitempty"`
	Pending []string `json:"pending,omitempty"`
}

// ReportCursorPath is the report cursor of task id.
func ReportCursorPath(h, id string) (string, error) {
	return DurableFilePath(StateDir(h), id, ".report-cursor")
}

func isMaterialReport(line string) bool {
	return domain.IsReportLine(line) && domain.IsMaterialVerb(domain.LineVerb(line))
}

func lineSHA(line string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(line)))
}

func reportRef(i int, line string) string {
	return fmt.Sprintf("%d:%s", i, lineSHA(line))
}

func readReportCursor(h, id string) (reportCursor, error) {
	var cur reportCursor
	p, err := ReportCursorPath(h, id)
	if err != nil {
		return cur, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return cur, nil
	}
	if err != nil {
		return cur, err
	}
	if err := json.Unmarshal(data, &cur); err != nil {
		return cur, fmt.Errorf("report cursor %s: %w", id, err)
	}
	if cur.Next < 0 {
		return cur, fmt.Errorf("report cursor %s: negative next %d", id, cur.Next)
	}
	return cur, nil
}

func writeReportCursor(h, id string, cur reportCursor) error {
	p, err := ReportCursorPath(h, id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, secureFile)
}

// underivedReports returns the indexes of the material report lines past the
// cursor. A cursor whose line is gone means the reporter rewrote its history:
// derivation resumes at the end and derives only the last material report.
func underivedReports(cur reportCursor, lines []string) []int {
	start, reset := cur.Next, false
	if cur.Next > len(lines) || (cur.Next > 0 && lineSHA(lines[cur.Next-1]) != cur.LastSHA) {
		start, reset = len(lines), true
	}
	var out []int
	if reset {
		for j := len(lines) - 1; j >= 0; j-- {
			if isMaterialReport(lines[j]) {
				if !slices.Contains(cur.Pending, reportRef(j, lines[j])) {
					out = append(out, j)
				}
				break
			}
		}
		return out
	}
	for i := start; i < len(lines); i++ {
		if isMaterialReport(lines[i]) {
			out = append(out, i)
		}
	}
	return out
}

// statusTaskIDs lists the task IDs with a status file in the home.
func statusTaskIDs(h string) ([]string, error) {
	entries, err := os.ReadDir(StateDir(h))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), ".status")
		if !ok || e.IsDir() {
			continue
		}
		if id, err := ReverseDurableKey(stem); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// DeriveReportWakes queues one report wake for every material report line
// written since the last derivation, under the wake lock.
func DeriveReportWakes(h string) (err error) {
	lock, err := acquireWakeLock(h)
	if err != nil {
		return err
	}
	defer joinWakeLockError(&err, lock)
	if err := recoverWakeMutationLocked(h); err != nil {
		return err
	}
	return deriveReportWakesLocked(h, time.Now())
}

// skippedReportTasks holds, per home and task, the reason derivation last
// skipped that task, so a skip is logged once until its reason changes.
var skippedReportTasks sync.Map

// logReportSkip writes to stderr that task id's reports were not derived,
// once while the reason is unchanged.
func logReportSkip(h, id string, err error) {
	if prev, ok := skippedReportTasks.Swap(h+"\x00"+id, err.Error()); ok && prev == err.Error() {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: reports of task %s in %s are not derived: %v\n", id, h, err)
}

// deriveReportWakesLocked queues the wakes before it advances the cursors: a
// crash between the two derives those reports again (at least once, never
// lost). A torn last status line is left past the cursor until its newline
// lands, so a report still being written is never consumed. A task whose
// status or cursor cannot be read is skipped and logged, so one bad file never
// blocks every other report; its teardown still holds, because
// UnsettledReports fails on the same read.
func deriveReportWakesLocked(h string, now time.Time) error {
	ids, err := statusTaskIDs(h)
	if err != nil {
		return err
	}
	var records []WakeRecord
	advanced := map[string]reportCursor{}
	for _, id := range ids {
		lines, torn, err := readStatus(h, id)
		if err != nil {
			logReportSkip(h, id, err)
			continue
		}
		if torn {
			lines = lines[:len(lines)-1]
		}
		cur, err := readReportCursor(h, id)
		if err != nil {
			logReportSkip(h, id, err)
			continue
		}
		skippedReportTasks.Delete(h + "\x00" + id)
		next := reportCursor{Next: len(lines), Pending: cur.Pending}
		if len(lines) > 0 {
			next.LastSHA = lineSHA(lines[len(lines)-1])
		}
		if next.Next == cur.Next && next.LastSHA == cur.LastSHA {
			continue
		}
		for _, i := range underivedReports(cur, lines) {
			ref := reportRef(i, lines[i])
			records = append(records, newWakeRecord(ReportWakeKind, id, ref, id+": "+domain.ReportLineText(lines[i]), now))
			next.Pending = append(next.Pending, ref)
		}
		advanced[id] = next
	}
	if len(records) > 0 {
		queue, err := readWakeQueue(h)
		if err != nil {
			return err
		}
		if err := applyWakeMutationLocked(h, wakeMutation{queueSet: true, queueData: wakeQueueData(append(queue, records...))}); err != nil {
			return err
		}
	}
	for id, cur := range advanced {
		if err := writeReportCursor(h, id, cur); err != nil {
			return err
		}
	}
	return nil
}

// SettleReport records that the report wake ref of task id was settled, so it
// no longer holds the task's teardown. Settling an unknown ref is a no-op.
func SettleReport(h, id, ref string) (err error) {
	lock, err := acquireWakeLock(h)
	if err != nil {
		return err
	}
	defer joinWakeLockError(&err, lock)
	cur, err := readReportCursor(h, id)
	if err != nil {
		return err
	}
	i := slices.Index(cur.Pending, ref)
	if i < 0 {
		return nil
	}
	cur.Pending = slices.Delete(cur.Pending, i, i+1)
	return writeReportCursor(h, id, cur)
}

// UnsettledReports returns the lines of task id's reports nothing has settled:
// the derived ones still pending, and the material report lines not derived
// yet. A pending report whose line is gone from a rewritten status file is
// returned as its ref. It reads only, so a seat that may not write the queue
// can ask.
func UnsettledReports(h, id string) ([]string, error) {
	lines, err := ReadStatus(h, id)
	if err != nil {
		return nil, err
	}
	cur, err := readReportCursor(h, id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ref := range cur.Pending {
		out = append(out, pendingReportLine(ref, lines))
	}
	for _, i := range underivedReports(cur, lines) {
		out = append(out, lines[i])
	}
	return out, nil
}

// pendingReportLine is the status line a pending ref names, or the ref itself
// when that line is gone.
func pendingReportLine(ref string, lines []string) string {
	i, sha, ok := strings.Cut(ref, ":")
	n, err := strconv.Atoi(i)
	if !ok || err != nil || n < 0 || n >= len(lines) || lineSHA(lines[n]) != sha {
		return ref
	}
	return lines[n]
}

// OldestUnderivedReport returns the modification time of the oldest status
// file holding a material report line no wake carries yet. A report's line is
// at most that old, so an age measured from it is never overstated.
func OldestUnderivedReport(h string) (time.Time, bool) {
	ids, err := statusTaskIDs(h)
	if err != nil {
		return time.Time{}, false
	}
	var oldest time.Time
	found := false
	for _, id := range ids {
		lines, err := ReadStatus(h, id)
		if err != nil {
			continue
		}
		cur, err := readReportCursor(h, id)
		if err != nil || len(underivedReports(cur, lines)) == 0 {
			continue
		}
		p, err := StatusFilePath(h, id)
		if err != nil {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !found || fi.ModTime().Before(oldest) {
			oldest, found = fi.ModTime(), true
		}
	}
	return oldest, found
}
