package orchestrator

import (
	"os"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// --- fakePaneCapture for safety tests ---

// fakePaneCapture returns predetermined content for pane capture tests.
type fakePaneCapture struct {
	content string
	err     error
}

func (f *fakePaneCapture) Capture(_ string, _ int) (string, error) {
	return f.content, f.err
}

func TestSplitTargetHandle(t *testing.T) {
	tests := []struct {
		handle      string
		wantSession string
		wantPaneID  string
	}{
		{"my-session:my-pane", "my-session", "my-pane"},
		{"session:123", "session", "123"},
		{"bare", "", "bare"},
		{"a:b:c", "a", "b:c"},
		{"", "", ""},
	}
	for _, tt := range tests {
		session, paneID := splitTargetHandle(tt.handle)
		if session != tt.wantSession {
			t.Errorf("splitTargetHandle(%q) session = %q, want %q", tt.handle, session, tt.wantSession)
		}
		if paneID != tt.wantPaneID {
			t.Errorf("splitTargetHandle(%q) paneID = %q, want %q", tt.handle, paneID, tt.wantPaneID)
		}
	}
}

// --- IsSafeInjectTarget tests ---

func TestIsSafeInjectTarget_ClaudePrompt(t *testing.T) {
	// Claude ❯ prompt → Empty → safe
	fake := &fakePaneCapture{content: "\u276F \n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for claude prompt, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}

func TestIsSafeInjectTarget_ClaudePromptBare(t *testing.T) {
	// Claude ❯ prompt (no trailing space) → Empty → safe
	fake := &fakePaneCapture{content: "\u276F\n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for claude prompt, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}

func TestIsSafeInjectTarget_CodexPrompt(t *testing.T) {
	// Codex › prompt → Empty → safe
	fake := &fakePaneCapture{content: "\u203A \n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for codex prompt, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}

func TestIsSafeInjectTarget_DeadShellGt(t *testing.T) {
	// Dead shell > (bare) → Unknown → NOT safe
	fake := &fakePaneCapture{content: "> \n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if safe {
		t.Errorf("safe = true for dead shell >, want false")
	}
	if verdict != domain.Unknown {
		t.Errorf("verdict = %v, want unknown", verdict)
	}
}

func TestIsSafeInjectTarget_DeadShellDollar(t *testing.T) {
	// Dead shell $ (bare) → Unknown → NOT safe
	fake := &fakePaneCapture{content: "$ \n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if safe {
		t.Errorf("safe = true for dead shell $, want false")
	}
	if verdict != domain.Unknown {
		t.Errorf("verdict = %v, want unknown", verdict)
	}
}

func TestIsSafeInjectTarget_PendingTypedText(t *testing.T) {
	// Typed text (pending) → Pending → NOT safe
	fake := &fakePaneCapture{content: "ls -la\n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if safe {
		t.Errorf("safe = true for pending text, want false")
	}
	if verdict != domain.Pending {
		t.Errorf("verdict = %v, want pending", verdict)
	}
}

func TestIsSafeInjectTarget_PendingWithGlyph(t *testing.T) {
	// Typed text with agent prompt glyph → Pending → NOT safe
	fake := &fakePaneCapture{content: "\u276F git push\n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if safe {
		t.Errorf("safe = true for pending text with glyph, want false")
	}
	if verdict != domain.Pending {
		t.Errorf("verdict = %v, want pending", verdict)
	}
}

func TestIsSafeInjectTarget_GhostOnly(t *testing.T) {
	// Ghost-only (dim suggestion text that strips to empty) → Empty → safe
	// SGR 2 = dim
	fake := &fakePaneCapture{content: "\x1b[2mType a message...\x1b[0m\n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for ghost-only, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}

func TestIsSafeInjectTarget_GrokPlaceholder(t *testing.T) {
	// Grok placeholder (dark truecolor that strips to empty) → Empty → safe
	fake := &fakePaneCapture{content: "\x1b[38;2;80;80;80mType a message...\x1b[0m\n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for grok placeholder, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}

func TestIsSafeInjectTarget_CaptureError(t *testing.T) {
	// Capture error → Unknown → NOT safe
	fake := &fakePaneCapture{err: os.ErrInvalid}
	_, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err == nil {
		t.Error("expected error for failed capture, got nil")
	}
	if verdict != domain.Unknown {
		t.Errorf("verdict = %v, want unknown on capture error", verdict)
	}
}

func TestIsSafeInjectTarget_CaptureErrImplementation(t *testing.T) {
	// Use proper error type
	fake := &fakePaneCapture{err: os.ErrInvalid}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err == nil {
		t.Error("expected error for failed capture, got nil")
	}
	if safe {
		t.Errorf("safe = true on capture error, want false")
	}
	if verdict != domain.Unknown {
		t.Errorf("verdict = %v, want unknown on capture error", verdict)
	}
}

// --- Edge cases ---

func TestIsSafeInjectTarget_EmptyRow(t *testing.T) {
	// Completely empty row → Empty → safe
	fake := &fakePaneCapture{content: "\n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for empty row, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}

func TestIsSafeInjectTarget_MultiLine(t *testing.T) {
	// Multi-line capture — last line is the composer row
	fake := &fakePaneCapture{content: "some output\n\u276F \n"}
	safe, verdict, err := IsSafeInjectTarget(fake, "session:pane")
	if err != nil {
		t.Fatalf("IsSafeInjectTarget: %v", err)
	}
	if !safe {
		t.Errorf("safe = false for claude prompt on last line, want true")
	}
	if verdict != domain.Empty {
		t.Errorf("verdict = %v, want empty", verdict)
	}
}
