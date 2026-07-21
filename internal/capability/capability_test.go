package capability

import (
	"encoding/json"
	"testing"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{Absent, "absent"},
		{Unsupported, "unsupported"},
		{Ready, "ready"},
		{Failed, "failed"},
	}
	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", int(tt.state), got, tt.want)
		}
	}
}

func TestStateString_Unknown(t *testing.T) {
	unknown := State(99)
	got := unknown.String()
	want := "State(99)"
	if got != want {
		t.Errorf("State(99).String() = %q, want %q", got, want)
	}
}

func TestStateMarshalText(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{Absent, "absent"},
		{Unsupported, "unsupported"},
		{Ready, "ready"},
		{Failed, "failed"},
	}
	for _, tt := range tests {
		b, err := tt.state.MarshalText()
		if err != nil {
			t.Errorf("MarshalText(%d): unexpected error: %v", int(tt.state), err)
			continue
		}
		if string(b) != tt.want {
			t.Errorf("MarshalText(%d) = %q, want %q", int(tt.state), string(b), tt.want)
		}
	}
}

func TestStateUnmarshalText(t *testing.T) {
	tests := []struct {
		text string
		want State
	}{
		{"absent", Absent},
		{"unsupported", Unsupported},
		{"ready", Ready},
		{"failed", Failed},
	}
	for _, tt := range tests {
		var s State
		if err := s.UnmarshalText([]byte(tt.text)); err != nil {
			t.Errorf("UnmarshalText(%q): unexpected error: %v", tt.text, err)
			continue
		}
		if s != tt.want {
			t.Errorf("UnmarshalText(%q) = %d, want %d", tt.text, int(s), int(tt.want))
		}
	}
}

func TestStateUnmarshalText_Unknown(t *testing.T) {
	var s State
	if err := s.UnmarshalText([]byte("bogus")); err == nil {
		t.Error("expected error for unknown state")
	}
}

func TestStateJSONRoundTrip(t *testing.T) {
	tests := []State{
		Absent,
		Unsupported,
		Ready,
		Failed,
	}
	for _, original := range tests {
		b, err := json.Marshal(original)
		if err != nil {
			t.Errorf("json.Marshal(%s): %v", original, err)
			continue
		}
		var decoded State
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Errorf("json.Unmarshal(%q): %v", string(b), err)
			continue
		}
		if decoded != original {
			t.Errorf("round trip %s -> %q -> %s", original, string(b), decoded)
		}
	}
}

func TestStateGoString(t *testing.T) {
	if got := Ready.GoString(); got != "ready" {
		t.Errorf("Ready.GoString() = %q, want %q", got, "ready")
	}
}

func TestStateValuesAreConsecutive(t *testing.T) {
	if Absent != 0 {
		t.Errorf("expected Absent to be 0, got %d", Absent)
	}
	if Unsupported != 1 {
		t.Errorf("expected Unsupported to be 1, got %d", Unsupported)
	}
	if Ready != 2 {
		t.Errorf("expected Ready to be 2, got %d", Ready)
	}
	if Failed != 3 {
		t.Errorf("expected Failed to be 3, got %d", Failed)
	}
}
