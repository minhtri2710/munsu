package home

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// The wire form is what every line-framed file already holds on disk (the wake
// queue), so it is pinned literally rather than by a round trip.
func TestEscapeLineWireForm(t *testing.T) {
	const raw = "a\tb\nc\rd\\e"
	const wire = `a\tb\nc\rd\\e`
	if got := EscapeLine(raw); got != wire {
		t.Errorf("EscapeLine(%q) = %q, want %q", raw, got, wire)
	}
	if got := UnescapeLine(wire); got != raw {
		t.Errorf("UnescapeLine(%q) = %q, want %q", wire, got, raw)
	}
	if got := UnescapeLine(`keep \x and trailing \`); got != `keep \x and trailing \` {
		t.Errorf("UnescapeLine kept unknown escapes as %q", got)
	}
}

// A status line holding newlines is one record on disk and reads back as given,
// so it cannot forge a later status line.
func TestAppendStatusKeepsMultiLineLineOneRecord(t *testing.T) {
	h := t.TempDir()
	const line = "working: one\nfailed: forged [key=x]\r\n\tthree\\n"
	if err := AppendStatus(h, "task-1", line); err != nil {
		t.Fatal(err)
	}
	path, err := StatusFilePath(h, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 1 {
		t.Fatalf(".status = %q, want one line", raw)
	}
	lines, err := ReadStatus(h, "task-1")
	if err != nil || len(lines) != 1 || lines[0] != line {
		t.Fatalf("ReadStatus() = %q, %v; want [%q]", lines, err, line)
	}
}

// An escaped report is one line however long its message is; every status read
// must take it whole instead of failing on bufio.Scanner's 64KB token limit.
func TestStatusReadsTakeALineOverTheScannerLimit(t *testing.T) {
	h := t.TempDir()
	line := "working: " + strings.Repeat("row\n", 30000)
	for i := 0; i < 2; i++ {
		if err := AppendStatus(h, "task-1", line); err != nil {
			t.Fatalf("AppendStatus #%d: %v", i+1, err)
		}
	}
	lines, err := ReadStatus(h, "task-1")
	if err != nil || len(lines) != 2 || lines[1] != line {
		t.Fatalf("ReadStatus() = %d lines, %v; want 2 lines, the last as written", len(lines), err)
	}
}

// A line appended after a torn last line, cut off before its newline, starts
// on its own line instead of being glued onto the torn one.
func TestAppendStatusEndsATornTailFirst(t *testing.T) {
	h := t.TempDir()
	if err := AppendStatus(h, "task-1", "working: one"); err != nil {
		t.Fatal(err)
	}
	path, err := StatusFilePath(h, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("worki"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := AppendStatus(h, "task-1", "failed: two"); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadStatus(h, "task-1")
	if want := []string{"working: one", "worki", "failed: two"}; err != nil || !slices.Equal(lines, want) {
		t.Fatalf("ReadStatus() = %q, %v; want %q", lines, err, want)
	}
}
