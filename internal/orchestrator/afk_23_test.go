package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// --- ResolveTarget tests ---

func TestResolveTarget_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	handle, session, err := ResolveTarget(tmp)
	if err != nil {
		t.Fatalf("ResolveTarget on clean home: %v", err)
	}
	if handle != "" {
		t.Errorf("ResolveTarget = %q, want empty", handle)
	}
	if session != "" {
		t.Errorf("session = %q, want empty", session)
	}
}

func TestResolveTarget_FromConfig(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	paneHandle := "my-session:my-pane-id"
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte(paneHandle+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	handle, session, err := ResolveTarget(tmp)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if handle != paneHandle {
		t.Errorf("handle = %q, want %q", handle, paneHandle)
	}
	if session != "my-session" {
		t.Errorf("session = %q, want my-session", session)
	}
}

func TestResolveTarget_ConfigMissingColon(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	// No colon in the pane handle — session is empty.
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("barePane\n"), 0644); err != nil {
		t.Fatal(err)
	}

	handle, session, err := ResolveTarget(tmp)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if handle != "barePane" {
		t.Errorf("handle = %q, want barePane", handle)
	}
	if session != "" {
		t.Errorf("session = %q, want empty", session)
	}
}

func TestResolveTarget_ConfigEmpty(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "general-pane"), []byte("  \n"), 0644); err != nil {
		t.Fatal(err)
	}

	handle, _, err := ResolveTarget(tmp)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if handle != "" {
		t.Errorf("handle = %q, want empty for whitespace-only config", handle)
	}
}

func TestResolveTarget_ConfigError(t *testing.T) {
	// Non-existent config directory should return empty, not error.
	tmp := t.TempDir()
	handle, _, err := ResolveTarget(tmp)
	if err != nil {
		t.Fatalf("ResolveTarget on empty home: %v", err)
	}
	if handle != "" {
		t.Errorf("handle = %q, want empty", handle)
	}
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

// --- Digester SetTargetSafety tests ---

func TestDigesterSetTargetSafety(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	// Set safety before flush — should appear in output.
	d.SetTargetSafety(true, "empty")

	now := time.Now().Add(defaultWindow + time.Second)
	if err := d.Flush(now); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Read and verify digest file.
	data, err := os.ReadFile(filepath.Join(tmp, digestFile))
	if err != nil {
		t.Fatalf("reading digest file: %v", err)
	}

	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		t.Fatalf("unmarshaling digest: %v", err)
	}

	if be.SafeTarget == nil {
		t.Fatal("SafeTarget is nil, want non-nil")
	}
	if *be.SafeTarget != true {
		t.Errorf("SafeTarget = %v, want true", *be.SafeTarget)
	}
	if be.TargetVerdict != "empty" {
		t.Errorf("TargetVerdict = %q, want empty", be.TargetVerdict)
	}
}

func TestDigesterSetTargetSafetyUnsafe(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	// Unsafe target (dead shell).
	d.SetTargetSafety(false, "unknown")

	now := time.Now().Add(defaultWindow + time.Second)
	if err := d.Flush(now); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmp, digestFile))
	var be BatchedEscalation
	json.Unmarshal(data, &be)

	if be.SafeTarget == nil {
		t.Fatal("SafeTarget is nil, want non-nil")
	}
	if *be.SafeTarget {
		t.Errorf("SafeTarget = true, want false")
	}
	if be.TargetVerdict != "unknown" {
		t.Errorf("TargetVerdict = %q, want unknown", be.TargetVerdict)
	}
}

func TestDigesterSetTargetSafetyResetsAfterFlush(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	d.SetTargetSafety(true, "empty")
	now := time.Now().Add(defaultWindow + time.Second)
	d.Flush(now)

	// After flush, safety data should be reset.
	d.SetTargetSafety(false, "pending")

	// A captain flush should only contain the latest safety data.
	d.Flush(now.Add(time.Second))

	data, _ := os.ReadFile(filepath.Join(tmp, digestFile))
	var be BatchedEscalation
	json.Unmarshal(data, &be)

	if be.SafeTarget == nil {
		t.Fatal("SafeTarget is nil in captain flush")
	}
	if *be.SafeTarget {
		t.Errorf("SafeTarget = true in captain flush, want false")
	}
	if be.TargetVerdict != "pending" {
		t.Errorf("TargetVerdict = %q in captain flush, want pending", be.TargetVerdict)
	}
}

func TestDigesterNoTargetSafetyInDigestWhenNotSet(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	// Feed and flush without setting target safety.
	d.Feed(&Digest{
		Routines: []WakeDigest{{Kind: "check", Key: "health", Payload: "ok"}},
	})

	now := time.Now().Add(defaultWindow + time.Second)
	if err := d.Flush(now); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmp, digestFile))
	var be BatchedEscalation
	json.Unmarshal(data, &be)

	if be.SafeTarget != nil {
		t.Errorf("SafeTarget = %v, want nil when not set", *be.SafeTarget)
	}
	if be.TargetVerdict != "" {
		t.Errorf("TargetVerdict = %q, want empty when not set", be.TargetVerdict)
	}
}

func TestDigesterSetTargetSafetyWithEscalation(t *testing.T) {
	tmp := t.TempDir()
	d := NewDigester(tmp)

	// Feed an escalated entry and set safety.
	d.Feed(&Digest{
		Escalated: []WakeDigest{{Kind: "afk", Key: "task-1", Payload: "PR merged"}},
	})
	d.SetTargetSafety(true, "empty")
	d.SetTargetSafety(false, "unknown")

	// Last SetTargetSafety wins.
	now := time.Now().Add(defaultWindow + time.Second)
	d.Flush(now)

	data, _ := os.ReadFile(filepath.Join(tmp, digestFile))
	var be BatchedEscalation
	json.Unmarshal(data, &be)

	if be.EscalatedCount != 1 {
		t.Errorf("EscalatedCount = %d, want 1", be.EscalatedCount)
	}
	if be.SafeTarget == nil {
		t.Fatal("SafeTarget is nil")
	}
	if *be.SafeTarget {
		t.Errorf("SafeTarget = true, want false (last set wins)")
	}
	if be.TargetVerdict != "unknown" {
		t.Errorf("TargetVerdict = %q, want unknown", be.TargetVerdict)
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
