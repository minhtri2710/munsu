package orchestrator

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// readEventLog parses the typed event log the way the binary's writers lay it
// out. The binary only ever appends; nothing in it reads the log back, so the
// reader belongs to the tests that assert on what was written.
func readEventLog(t *testing.T, homeDir string) []Record {
	t.Helper()
	data, err := os.ReadFile(LogPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading event log: %v", err)
	}
	var records []Record
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 6)
		if len(parts) < 6 {
			continue
		}
		id, _ := strconv.ParseUint(parts[0], 10, 64)
		ts, _ := strconv.ParseInt(parts[1], 10, 64)
		records = append(records, Record{
			ID: id, Timestamp: ts, Type: parts[2],
			Producer: parts[3], Key: parts[4], Payload: parts[5],
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records
}

func TestAppendAndRead(t *testing.T) {
	home := t.TempDir()

	id, err := Append(home, "build.complete", "watcher", "key-1", `{"status":"ok"}`)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if id != 1 {
		t.Errorf("first ID = %d, want 1", id)
	}

	id2, err := Append(home, "task.done", "soldier-1", "", `{"id":"task-abc"}`)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if id2 != 2 {
		t.Errorf("second ID = %d, want 2", id2)
	}

	records := readEventLog(t, home)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}

	if records[0].ID != 1 || records[0].Type != "build.complete" || records[0].Key != "key-1" {
		t.Errorf("record 0 = %+v", records[0])
	}
	if records[1].ID != 2 || records[1].Type != "task.done" || records[1].Producer != "soldier-1" {
		t.Errorf("record 1 = %+v", records[1])
	}
}

func TestAppendPersistence(t *testing.T) {
	home := t.TempDir()

	Append(home, "event1", "producer-a", "", "payload one")
	Append(home, "event2", "producer-b", "", "payload two")

	// Re-read with fresh instance
	records := readEventLog(t, home)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
}

func TestEventLogFileCreated(t *testing.T) {
	home := t.TempDir()

	Append(home, "test", "p", "", "check")
	logPath := LogPath(home)

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("event log file was not created")
	}
}

func TestEventLogFormat(t *testing.T) {
	home := t.TempDir()

	Append(home, "test.type", "producer-1", "my-key", "some payload")

	data, err := os.ReadFile(LogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	parts := strings.SplitN(line, "\t", 6)
	if len(parts) != 6 {
		t.Fatalf("expected 6 tab-separated parts, got %d: %q", len(parts), line)
	}
	if parts[2] != "test.type" {
		t.Errorf("type = %q, want test.type", parts[2])
	}
	if parts[3] != "producer-1" {
		t.Errorf("producer = %q, want producer-1", parts[3])
	}
	if parts[4] != "my-key" {
		t.Errorf("key = %q, want my-key", parts[4])
	}
}

// TestAppendConcurrentIDsAreUnique: concurrent writers (goroutines here, and
// separate munsu processes in production) must never share an event ID.
func TestAppendConcurrentIDsAreUnique(t *testing.T) {
	home := t.TempDir()
	const writers = 20
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Append(home, "concurrent", "test", "", "data"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	records := readEventLog(t, home)
	if len(records) != writers {
		t.Fatalf("got %d records, want %d", len(records), writers)
	}
	for i, r := range records {
		if r.ID != uint64(i+1) {
			t.Fatalf("record IDs = %v, want 1..%d each once", recordIDs(records), writers)
		}
	}
}

func recordIDs(records []Record) []uint64 {
	ids := make([]uint64, len(records))
	for i, r := range records {
		ids[i] = r.ID
	}
	return ids
}

const deliverWakeHelperEnv = "MUNSU_TEST_DELIVER_WAKE_HOME"

// TestHelperDeliverWakeProcess is the child for
// TestDeliverWakeEventIDsUniqueAcrossProcesses: one munsu report per process.
func TestHelperDeliverWakeProcess(t *testing.T) {
	homeDir := os.Getenv(deliverWakeHelperEnv)
	if homeDir == "" {
		return
	}
	if _, err := DeliverWake(DeliverRequest{
		HomeDir: homeDir, TaskID: os.Getenv(deliverWakeHelperEnv + "_TASK"),
		State: "working", Message: "progress", Role: "soldier",
	}); err != nil {
		t.Fatal(err)
	}
}

// TestDeliverWakeEventIDsUniqueAcrossProcesses: each munsu report is its own
// process, so an in-process counter cannot make the IDs unique.
func TestDeliverWakeEventIDsUniqueAcrossProcesses(t *testing.T) {
	homeDir := t.TempDir()
	for _, task := range []string{"t1", "t2"} {
		child := exec.Command(os.Args[0], "-test.run=^TestHelperDeliverWakeProcess$")
		child.Env = append(os.Environ(), deliverWakeHelperEnv+"="+homeDir, deliverWakeHelperEnv+"_TASK="+task)
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("report process for %s: %v\n%s", task, err, out)
		}
	}

	records := readEventLog(t, homeDir)
	if len(records) != 2 || records[0].ID == records[1].ID {
		t.Fatalf("event records = %+v, want two task.status events with distinct IDs", records)
	}
}

func writeEventLog(t *testing.T, homeDir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(LogPath(homeDir)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LogPath(homeDir), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func appendN(t *testing.T, homeDir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := Append(homeDir, "t", "p", "", "x"); err != nil {
			t.Fatal(err)
		}
	}
}

// outOfOrderLog holds IDs 5 3 4 3 9 1 in file order; the two ID-3 records are
// told apart by payload.
const outOfOrderLog = "5\t1\tt\tp\t\ta\n3\t2\tt\tp\t\tb\n4\t3\tt\tp\t\tc\n3\t4\tt\tp\t\td\n9\t5\tt\tp\t\te\n1\t6\tt\tp\t\tf\n"

func idPayloads(records []Record) []string {
	var out []string
	for _, r := range records {
		out = append(out, strconv.FormatUint(r.ID, 10)+":"+r.Payload)
	}
	return out
}

func TestLatestEventsReturnsNewestInOrder(t *testing.T) {
	home := t.TempDir()
	appendN(t, home, 5)

	got, skipped, err := LatestEvents(home, 3)
	if err != nil || skipped != 0 {
		t.Fatalf("LatestEvents() skipped=%d err=%v", skipped, err)
	}
	if ids := recordIDs(got); len(ids) != 3 || ids[0] != 3 || ids[1] != 4 || ids[2] != 5 {
		t.Errorf("ids = %v, want [3 4 5]", ids)
	}
	if got, _, _ := LatestEvents(home, 99); len(got) != 5 {
		t.Errorf("n above log size returned %d records, want 5", len(got))
	}
	for _, n := range []int{0, -1} {
		if got, _, err := LatestEvents(home, n); err != nil || len(got) != 0 {
			t.Errorf("LatestEvents(%d) returned %d records, err %v; want none", n, len(got), err)
		}
	}

	// out of file order: sorted by ID, equal IDs in file order
	writeEventLog(t, home, outOfOrderLog)
	got, _, _ = LatestEvents(home, 4)
	if want := []string{"3:d", "4:c", "5:a", "9:e"}; !reflect.DeepEqual(idPayloads(got), want) {
		t.Errorf("out-of-order records = %v, want %v", idPayloads(got), want)
	}
}

func TestEventsAfterIsOrderedAndBounded(t *testing.T) {
	home := t.TempDir()
	appendN(t, home, 6)

	got, _, err := EventsAfter(home, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ids := recordIDs(got); len(ids) != 3 || ids[0] != 3 || ids[1] != 4 || ids[2] != 5 {
		t.Errorf("ids = %v, want [3 4 5]", ids)
	}
	if got, _, _ := EventsAfter(home, 6, 3); len(got) != 0 {
		t.Errorf("cursor at newest returned %d records, want 0", len(got))
	}

	// out of file order: sorted by ID, equal IDs in file order
	writeEventLog(t, home, outOfOrderLog)
	got, _, _ = EventsAfter(home, 2, 3)
	if want := []string{"3:b", "3:d", "4:c"}; !reflect.DeepEqual(idPayloads(got), want) {
		t.Errorf("out-of-order records = %v, want %v", idPayloads(got), want)
	}
	if got, _, err := EventsAfter(home, 0, -1); err != nil || len(got) != 0 {
		t.Errorf("negative limit returned %d records, err %v; want none", len(got), err)
	}
}

func TestEventReadersMissingLogIsEmpty(t *testing.T) {
	home := t.TempDir()
	if got, skipped, err := LatestEvents(home, 5); err != nil || len(got) != 0 || skipped != 0 {
		t.Errorf("LatestEvents() = %v, %d, %v; want empty", got, skipped, err)
	}
	if got, skipped, err := EventsAfter(home, 0, 5); err != nil || len(got) != 0 || skipped != 0 {
		t.Errorf("EventsAfter() = %v, %d, %v; want empty", got, skipped, err)
	}
}

func TestEventReadersUnreadableLogIsError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(LogPath(home), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LatestEvents(home, 5); err == nil {
		t.Error("LatestEvents() on unreadable log: want error")
	}
	if _, _, err := EventsAfter(home, 0, 5); err == nil {
		t.Error("EventsAfter() on unreadable log: want error")
	}
}

func TestEventReadersExcludeTornTrailingLine(t *testing.T) {
	home := t.TempDir()
	writeEventLog(t, home, "1\t10\tt\tp\t\tx\n2\t11\tt\tp\t\ty")

	got, skipped, err := LatestEvents(home, 5)
	if err != nil || skipped != 0 {
		t.Fatalf("LatestEvents() skipped=%d err=%v", skipped, err)
	}
	if ids := recordIDs(got); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("ids = %v, want [1]", ids)
	}
	if got, _, _ := EventsAfter(home, 0, 5); len(got) != 1 {
		t.Errorf("EventsAfter() returned %d records, want 1", len(got))
	}
}

func TestEventReadersSkipAndCountMalformedLines(t *testing.T) {
	home := t.TempDir()
	writeEventLog(t, home, "1\t10\tt\tp\t\tx\nnot a record\nabc\t11\tt\tp\t\ty\n9\t13\tt\tp\tx\n\n2\t12\tt\tp\t\tz\n")

	for name, read := range map[string]func() ([]Record, int, error){
		"LatestEvents": func() ([]Record, int, error) { return LatestEvents(home, 5) },
		"EventsAfter":  func() ([]Record, int, error) { return EventsAfter(home, 0, 5) },
	} {
		got, skipped, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ids := recordIDs(got); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
			t.Errorf("%s: ids = %v, want [1 2]", name, ids)
		}
		if skipped != 3 {
			t.Errorf("%s: skipped = %d, want 3 (blank line is not counted)", name, skipped)
		}
	}
	// the five-field line (ID 9) must not feed nextID either
	if id, _, err := nextID(home); err != nil || id != 3 {
		t.Errorf("nextID() = %d, %v; want 3", id, err)
	}
}

// A line whose ID is unparsable is skipped by lastTaskStatusEvent as nextID
// skips it; it no longer voids an earlier matching event.
func TestLastTaskStatusEventSkipsMalformedLine(t *testing.T) {
	home := t.TempDir()
	writeEventLog(t, home, "7\t10\ttask.status\tt1\tk\tdone\nzz\t11\ttask.status\tt1\tk\tother\n")
	if id, found := lastTaskStatusEvent(home, "t1", "k", "done"); !found || id != 7 {
		t.Errorf("lastTaskStatusEvent() = %d, %v; want 7, true", id, found)
	}
}

func TestAppendEmptyPayloadsGetDistinctIDs(t *testing.T) {
	home := t.TempDir()
	var ids []uint64
	for _, payload := range []string{"", "", " ", "x"} {
		id, err := Append(home, "t", "p", "", payload)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for i, id := range ids {
		if id != uint64(i+1) {
			t.Fatalf("ids = %v, want [1 2 3 4]", ids)
		}
	}
}

// A tab or newline in any text field must stay inside that field: the log is
// one record per line and six fields per record.
func TestAppendKeepsHazardousFieldsInOneRecord(t *testing.T) {
	home := t.TempDir()
	const (
		eventType = "ty\tpe"
		producer  = "pro\tducer"
		key       = "k\tey"
		payload   = "first\nforged\t9\t1\ttask.status\tp\tk\tfake\r\nlast\\n"
	)
	id, err := Append(home, eventType, producer, key, payload)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(LogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); len(lines) != 1 || strings.Count(lines[0], "\t") != 5 {
		t.Fatalf("log = %q, want one line with five tabs", data)
	}
	got, skipped, err := LatestEvents(home, 5)
	if err != nil || skipped != 0 {
		t.Fatalf("LatestEvents() skipped=%d err=%v", skipped, err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].Type != eventType || got[0].Producer != producer || got[0].Key != key || got[0].Payload != payload {
		t.Fatalf("records = %+v, want the one appended record read back as written", got)
	}
}

// A multi-line report message is one .status line and one event, and both read
// back as the message was given.
func TestDeliverWakeKeepsMultiLineMessageOneStatusLineAndEvent(t *testing.T) {
	homeDir := t.TempDir()
	const message = "step one\nfailed: forged [key=other]\r\nstep two"
	if _, err := DeliverWake(DeliverRequest{HomeDir: homeDir, TaskID: "t1", State: "working", Message: message, Role: "soldier"}); err != nil {
		t.Fatal(err)
	}
	want := "working: " + message + " [key=default]"
	lines, err := mhome.ReadStatus(homeDir, "t1")
	if err != nil || len(lines) != 1 || lines[0] != want {
		t.Fatalf("ReadStatus() = %q, %v; want the one line %q", lines, err, want)
	}
	path, err := mhome.StatusFilePath(homeDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n != 1 {
		t.Fatalf(".status holds %d lines, want 1: %q", n, raw)
	}
	events, skipped, err := LatestEvents(homeDir, 5)
	if err != nil || skipped != 0 || len(events) != 1 || events[0].Payload != want {
		t.Fatalf("events = %+v skipped=%d err=%v; want one event with payload %q", events, skipped, err, want)
	}
}

// nextID reads only the log past the recorded high-water offset, takes the
// maximum over the whole log, and rescans a log that is shorter than the mark.
func TestNextIDHighWater(t *testing.T) {
	const (
		big   = "900\t1\tt\tp\t\ta\n"
		seven = "7\t2\tt\tp\t\tb\n"
	)
	cases := []struct {
		name string
		log  string
		mark string // written as the sidecar; empty writes none
		want uint64
	}{
		{name: "out of ID order", log: outOfOrderLog, want: 10},
		{name: "skips the scanned prefix", log: big + seven, mark: fmt.Sprintf("%d 6\n", len(big)), want: 8},
		{name: "reads a tail appended after the mark", log: seven + big, mark: fmt.Sprintf("%d 7\n", len(seven)), want: 901},
		{name: "rescans a log shorter than the mark", log: seven, mark: "9999 500\n", want: 8},
		{name: "rescans over an unparsable mark", log: seven, mark: "garbage", want: 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeEventLog(t, home, tc.log)
			if tc.mark != "" {
				if err := os.WriteFile(highWaterPath(home), []byte(tc.mark), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for i := uint64(0); i < 2; i++ {
				if id, err := Append(home, "t", "p", "", "x"); err != nil || id != tc.want+i {
					t.Fatalf("Append #%d = %d, %v; want %d", i+1, id, err, tc.want+i)
				}
			}
		})
	}
}

// The largest ID is never wrapped to 0: Append refuses and writes nothing, and
// one below the maximum still takes the maximum.
func TestAppendRefusesWhenLogIDIsMaximum(t *testing.T) {
	const maxLine = "18446744073709551615\t1\tt\tp\t\ta\n"
	home := t.TempDir()
	writeEventLog(t, home, "3\t1\tt\tp\t\tb\n"+maxLine)
	before, err := os.ReadFile(LogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the second call runs against whatever the first left behind
		_, err = Append(home, "t", "p", "", "x")
		if err == nil || !strings.Contains(err.Error(), "18446744073709551615") || !strings.Contains(err.Error(), LogPath(home)) {
			t.Fatalf("Append() = %v, want an error naming the maximum ID and the log path", err)
		}
	}
	after, _ := os.ReadFile(LogPath(home))
	if string(after) != string(before) {
		t.Fatalf("log changed on refusal: %q -> %q", before, after)
	}
	if _, err := os.Stat(highWaterPath(home)); !os.IsNotExist(err) {
		t.Fatalf("refusal wrote the high-water sidecar: %v", err)
	}

	below := t.TempDir()
	writeEventLog(t, below, "18446744073709551614\t1\tt\tp\t\ta\n")
	if id, err := Append(below, "t", "p", "", "x"); err != nil || id != math.MaxUint64 {
		t.Fatalf("Append() below the maximum = %d, %v; want %d", id, err, uint64(math.MaxUint64))
	}
}
