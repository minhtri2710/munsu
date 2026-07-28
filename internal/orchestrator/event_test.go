package orchestrator

import (
	"os"
	"strings"
	"testing"
)

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
		t.Errorf("captain ID = %d, want 2", id2)
	}

	records, err := ReadAll(home)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
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

func TestAppendWithID(t *testing.T) {
	home := t.TempDir()

	if err := AppendWithID(home, 42, "legacy.event", "migration", "", "migrated"); err != nil {
		t.Fatalf("AppendWithID() error = %v", err)
	}

	records, err := ReadAll(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != 42 {
		t.Errorf("got ID %d, want 42", records[0].ID)
	}
}

func TestReadFrom(t *testing.T) {
	home := t.TempDir()

	Append(home, "a", "p", "", "first")
	Append(home, "b", "p", "", "general")
	Append(home, "c", "p", "", "third")

	records, err := ReadFrom(home, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].ID != 2 || records[1].ID != 3 {
		t.Errorf("IDs = %d, %d; want 2, 3", records[0].ID, records[1].ID)
	}
}

func TestReadAllEmpty(t *testing.T) {
	home := t.TempDir()
	records, err := ReadAll(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records, want 0", len(records))
	}
}

func TestSyntheticEventID(t *testing.T) {
	id1 := SyntheticEventID()
	id2 := SyntheticEventID()
	id3 := SyntheticEventID()

	if id1 >= id2 || id2 >= id3 {
		t.Error("synthetic IDs should be monotonic")
	}
	if id1 < (1<<48) || id2 < (1<<48) || id3 < (1<<48) {
		t.Error("synthetic IDs should be above 1<<48")
	}
}

func TestFromTaskStatus(t *testing.T) {
	home := t.TempDir()

	rec, err := FromTaskStatus(home, "task-abc", "done: completed successfully [key=ship-it]")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Type != "task.status" {
		t.Errorf("type = %q, want task.status", rec.Type)
	}
	if rec.Producer != "task-abc" {
		t.Errorf("producer = %q, want task-abc", rec.Producer)
	}
	if rec.Key != "ship-it" {
		t.Errorf("key = %q, want ship-it", rec.Key)
	}
	if rec.Payload != "done: completed successfully" {
		t.Errorf("payload = %q, want 'done: completed successfully'", rec.Payload)
	}
	if rec.ID < (1 << 48) {
		t.Error("synthetic ID should be above 1<<48")
	}
}

func TestFromTaskStatusNoKey(t *testing.T) {
	home := t.TempDir()
	rec, err := FromTaskStatus(home, "task-xyz", "working: building")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Key != "" {
		t.Errorf("key = %q, want empty", rec.Key)
	}
	if rec.Payload != "working: building" {
		t.Errorf("payload = %q, want 'working: building'", rec.Payload)
	}
}

func TestAppendPersistence(t *testing.T) {
	home := t.TempDir()

	Append(home, "event1", "producer-a", "", "payload one")
	Append(home, "event2", "producer-b", "", "payload two")

	// Re-read with fresh instance
	records, err := ReadAll(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
}

func TestAppendConcurrentSafe(t *testing.T) {
	home := t.TempDir()

	// Sequential id generation is safe; just verify no crashes
	for i := 0; i < 10; i++ {
		_, err := Append(home, "concurrent", "test", "", "data")
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	records, err := ReadAll(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 10 {
		t.Errorf("got %d records, want 10", len(records))
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

func TestReadFromNonExistent(t *testing.T) {
	home := t.TempDir()
	records, err := ReadFrom(home, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records from non-existent log", len(records))
	}
}

func TestAppendWithIDThenNextIsSequential(t *testing.T) {
	home := t.TempDir()

	// Write two events with explicit IDs
	AppendWithID(home, 100, "legacy", "", "", "old1")
	AppendWithID(home, 200, "legacy", "", "", "old2")

	// Next Append should pick up from 201
	id, err := Append(home, "normal", "p", "", "new")
	if err != nil {
		t.Fatal(err)
	}
	if id != 201 {
		t.Errorf("next ID = %d, want 201", id)
	}
}

func TestFromTaskStatusOnlyKey(t *testing.T) {
	home := t.TempDir()
	// Edge case: line that is just [key=foo]
	rec, err := FromTaskStatus(home, "t1", "[key=direct]")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Key != "direct" {
		t.Errorf("key = %q, want 'direct'", rec.Key)
	}
	if rec.Payload != "" {
		t.Errorf("payload = %q, want empty", rec.Payload)
	}
}
