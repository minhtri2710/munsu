package home

import (
	"os"
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
	if ok, err := AppendStatusOnce(h, "task-1", line); err != nil || ok {
		t.Fatalf("AppendStatusOnce(same line) = %v, %v; want no append", ok, err)
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
	if ok, err := StatusHasLine(h, "task-1", line); err != nil || !ok {
		t.Fatalf("StatusHasLine() = %v, %v; want true", ok, err)
	}
	if ok, err := AppendStatusOnce(h, "task-1", line); err != nil || ok {
		t.Fatalf("AppendStatusOnce(same line) = %v, %v; want no append", ok, err)
	}
}
