package testutil

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathInMessageAcceptsEscapedRenderings proves the predicate answers the
// same question on both platforms for the three ways munsu renders a path into
// a message: verbatim, through %q, and inside a JSON document. The path is
// built with filepath.Join so it carries the host's separator, which is the
// whole of what differs between the platforms.
func TestPathInMessageAcceptsEscapedRenderings(t *testing.T) {
	path := filepath.Join("base", "Users", "x", "AppData", "Temp", "home-001")

	for _, tc := range []struct {
		name    string
		message string
	}{
		{"verbatim", "reading state in home " + path + ": task not found"},
		{"quoted", fmt.Sprintf("reading state in home %q: task not found", path)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !PathInMessage(tc.message, path) {
				t.Errorf("PathInMessage(%q, %q) = false, want true", tc.message, path)
			}
		})
	}

	t.Run("json", func(t *testing.T) {
		payload, err := json.Marshal(map[string]string{"reason": "receipt in " + path})
		if err != nil {
			t.Fatal(err)
		}
		if !PathInMessage(string(payload), path) {
			t.Errorf("PathInMessage(%q, %q) = false, want true", payload, path)
		}
	})

	// The host separator is what differs between platforms, so on Unix the
	// three cases above collapse to one and prove nothing about escaping. A
	// path spelled with backslashes is escaped by %q and by JSON on every
	// platform, which is the behaviour this predicate exists for.
	t.Run("separators are escaped on every platform", func(t *testing.T) {
		windows := `C:\Users\x\AppData\Temp\home-001`
		quoted := fmt.Sprintf("in home %q", windows)
		if strings.Contains(quoted, windows) {
			t.Fatalf("%%q left %q unescaped; this test no longer proves anything", windows)
		}
		if !PathInMessage(quoted, windows) {
			t.Errorf("PathInMessage(%q, %q) = false, want true", quoted, windows)
		}
	})

	t.Run("absent", func(t *testing.T) {
		if PathInMessage("reading state in home elsewhere: task not found", path) {
			t.Error("PathInMessage matched a message that does not name the path")
		}
	})

	t.Run("partial", func(t *testing.T) {
		if PathInMessage("reading state in home "+filepath.Dir(path)+": task not found", path) {
			t.Error("PathInMessage matched on an ancestor rather than the path itself")
		}
	})

	t.Run("empty", func(t *testing.T) {
		if PathInMessage("anything at all", "") {
			t.Error("PathInMessage matched the empty path")
		}
	})
}
