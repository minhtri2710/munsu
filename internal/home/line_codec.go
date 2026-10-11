package home

import (
	"bufio"
	"io"
	"strings"
)

// EscapeLine makes s safe to store as one tab-separated field of a line-framed
// home file: backslash, tab, CR and LF become \\, \t, \r and \n, so a value can
// never end its line or its field. UnescapeLine reverses it. It is the single
// codec for the wake queue, the event log and the task status files.
func EscapeLine(s string) string {
	if !strings.ContainsAny(s, "\\\t\r\n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString("\\\\")
		case '\t':
			b.WriteString("\\t")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// UnescapeLine decodes a field written by EscapeLine. An unknown or trailing
// backslash is kept literally.
func UnescapeLine(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// EachLine calls fn with every line of r, without its line ending, however long
// the line is: a line is the whole message of an escaped report, and
// bufio.Scanner would fail on one past 64KB.
func EachLine(r io.Reader, fn func(line string)) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			fn(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
