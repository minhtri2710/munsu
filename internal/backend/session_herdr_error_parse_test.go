package backend

import (
	"errors"
	"testing"
)

// TestParseHerdrErrorEnvelopeShapes pins the envelope extraction: any key
// order, braces inside a JSON string, surrounding text, and a leading object
// that is not the error envelope.
func TestParseHerdrErrorEnvelopeShapes(t *testing.T) {
	tests := []struct {
		name string
		text string
		code string
	}{
		{"id first", `{"id":"cli:1","error":{"code":"pane_not_found","message":"gone"}}`, HerdrErrPaneNotFound},
		{"error first", `{"error":{"code":"pane_not_found","message":"gone"},"id":"cli:1"}`, HerdrErrPaneNotFound},
		{"braces inside the message string", `{"error":{"code":"timeout","message":"saw } and { in output"}}`, HerdrErrTimeout},
		{"surrounding text", `herdr pane get s:p: exit status 1: {"error":{"code":"tab_not_found","message":"m"}} trailing`, HerdrErrTabNotFound},
		{"preceding object without an error", `{"result":{}} {"error":{"code":"protocol_mismatch","message":"m"}}`, HerdrErrProtocolMismatch},
		{"preceding object whose error has no code", `{"error":{"message":"m"}} {"error":{"code":"workspace_not_found","message":"m"}}`, HerdrErrWorkspaceNotFound},
		{"preceding malformed brace", `{ not json {"error":{"code":"pane_not_found","message":"m"}}`, HerdrErrPaneNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHerdrError(errors.New(tt.text))
			if got == nil || got.Code != tt.code {
				t.Fatalf("parseHerdrError(%q) = %+v, want code %q", tt.text, got, tt.code)
			}
		})
	}
}

func TestParseHerdrErrorNoEnvelope(t *testing.T) {
	for _, text := range []string{
		`{"result":{"agent":{}}}`,
		`{"error":{"message":"no code"}}`,
		`{ not json`,
		`pane not found`,
	} {
		if got := parseHerdrError(errors.New(text)); got != nil {
			t.Errorf("parseHerdrError(%q) = %+v, want nil", text, got)
		}
	}
}

// TestIsHerdrProtocolMismatchIsStructuredOnly: the code name in unstructured
// text is not a structured mismatch.
func TestIsHerdrProtocolMismatchIsStructuredOnly(t *testing.T) {
	if !isHerdrProtocolMismatch(errors.New(`{"id":"1","error":{"code":"protocol_mismatch","message":"m"}}`)) {
		t.Error("structured protocol_mismatch must be detected")
	}
	if isHerdrProtocolMismatch(errors.New("herdr failed: protocol_mismatch")) {
		t.Error("unstructured text naming protocol_mismatch must not be a mismatch")
	}
}
