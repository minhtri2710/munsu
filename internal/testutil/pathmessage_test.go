package testutil

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathInMessageAcceptsEscapedRenderings proves the predicate answers the
// same question for the three ways munsu renders a path into a message:
// verbatim, through %q, and inside a JSON document.
func TestPathInMessageAcceptsEscapedRenderings(t *testing.T) {
	path := filepath.Join("base", "Users", "x", "AppData", "Temp", "home-&<001")

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

	payload, err := json.Marshal(map[string]string{"reason": "receipt in " + path})
	if err != nil {
		t.Fatal(err)
	}
	if !PathInMessage(string(payload), path) {
		t.Errorf("PathInMessage(%q, %q) = false, want true", payload, path)
	}

	if strings.Contains(string(payload), path) {
		t.Fatalf("JSON payload did not escape HTML-sensitive path %q: %s", path, payload)
	}
	if !strings.Contains(string(payload), `home-\u0026\u003c001`) {
		t.Fatalf("JSON payload omitted HTML escaping for %q: %s", path, payload)
	}

	// A path holding backslashes is escaped by %q, so a raw substring search
	// for it finds nothing in the quoted rendering.
	t.Run("backslashes are escaped", func(t *testing.T) {
		escaped := `base\x\home-001`
		quoted := fmt.Sprintf("in home %q", escaped)
		if strings.Contains(quoted, escaped) {
			t.Fatalf("%%q left %q unescaped; this test no longer proves anything", escaped)
		}
		if !PathInMessage(quoted, escaped) {
			t.Errorf("PathInMessage(%q, %q) = false, want true", quoted, escaped)
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
