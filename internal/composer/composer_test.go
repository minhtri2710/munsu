package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- StripANSI tests ---

func TestStripANSI_NoANSI(t *testing.T) {
	in := "hello world"
	got := StripANSI(in)
	if got != in {
		t.Errorf("StripANSI(%q) = %q, want %q", in, got, in)
	}
}

func TestStripANSI_SimpleSGR(t *testing.T) {
	in := "\x1b[31mred\x1b[0m"
	want := "red"
	if got := StripANSI(in); got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
	}
}

func TestStripANSI_MultipleSGR(t *testing.T) {
	in := "\x1b[1;32mbold green\x1b[0m \x1b[4munderline\x1b[0m"
	want := "bold green underline"
	if got := StripANSI(in); got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
	}
}

func TestStripANSI_Complex(t *testing.T) {
	// Multiple params, colon form SGR
	in := "\x1b[38:2::255:100:50mcolourful\x1b[0m"
	want := "colourful"
	if got := StripANSI(in); got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
	}
}

func TestStripANSI_NonSGREscape(t *testing.T) {
	// ESC not followed by [ should be left alone
	in := "before\x1bXafter"
	want := "before\x1bXafter" // lone ESC without [ is not CSI
	if got := StripANSI(in); got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
	}
}

func TestStripANSI_CSIWithIntermediate(t *testing.T) {
	// CSI with parameter bytes and intermediate bytes before the final byte
	in := "\x1b[1!sdec-mode" // ! is intermediate (0x21)
	want := "dec-mode"
	if got := StripANSI(in); got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
	}
}

// --- StripGhost tests ---

func TestStripGhost_PlainText(t *testing.T) {
	in := "hello world"
	got := StripGhost(in)
	if got != in {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, in)
	}
}

func TestStripGhost_DimText(t *testing.T) {
	// SGR 2 = dim/faint → should be stripped
	in := "\x1b[2mdim text\x1b[0mnormal"
	want := "normal"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DimResetBy22(t *testing.T) {
	// SGR 22 = normal intensity ends the dim run
	in := "\x1b[2mdim\x1b[22mnormal"
	want := "normal"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DimResetBy0(t *testing.T) {
	// SGR 0 = reset ends everything
	in := "\x1b[2mdim\x1b[0mbright"
	want := "bright"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DarkTruecolorSemicolon(t *testing.T) {
	// SGR 38;2;50;60;70 = dark truecolor fg (luminance ≈ 57 < 128)
	in := "\x1b[38;2;50;60;70mdark text\x1b[0mnormal"
	want := "normal"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_BrightTruecolor(t *testing.T) {
	// SGR 38;2;200;200;200 = bright truecolor (luminance ≈ 200), should NOT be stripped
	in := "\x1b[38;2;200;200;200mbright text\x1b[0m"
	want := "bright text"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DarkTruecolorColonForm(t *testing.T) {
	// Colon form: 38:2::50:60:70
	in := "\x1b[38:2::50:60:70mdark\x1b[0mnormal"
	want := "normal"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_256ColourNotStripped(t *testing.T) {
	// 38;5;n (256-colour) is NOT luminance-tested, so it should be kept
	in := "\x1b[38;5;240mdim-256\x1b[0m"
	want := "dim-256"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DarkFgResetByDefault(t *testing.T) {
	// SGR 39 = default fg resets darkfg
	in := "\x1b[38;2;50;60;70mdark\x1b[39mnormal"
	want := "normal"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DarkFgResetByBaseColour(t *testing.T) {
	// A base fg colour (30-37) resets darkfg
	in := "\x1b[38;2;50;60;70mdark\x1b[31mred\x1b[0m"
	want := "red"
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_GrokPlaceholder(t *testing.T) {
	// Grok-style placeholder: dark truecolor like "Type a message..."
	in := "\x1b[38;2;80;80;80mType a message...\x1b[0m"
	want := "" // entirely dark → stripped to nothing
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_ClaudePromptGlyphNotDim(t *testing.T) {
	// Claude's ❯ glyph in bold colour, ghost suggestion in dim
	// The glyph (not dim) stays; the dim suggestion is stripped.
	in := "\x1b[1m\x1b[38;2;255;200;100m\u276F \x1b[0m\x1b[2mType a message...\x1b[0m"
	want := "\u276F " // bold glyph stays, dim suggestion stripped
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

func TestStripGhost_DimOnlyRunes(t *testing.T) {
	// Entire content is dim
	in := "\x1b[2mDim suggestion text\x1b[0m"
	want := ""
	if got := StripGhost(in); got != want {
		t.Errorf("StripGhost(%q) = %q, want %q", in, got, want)
	}
}

// --- ClassifyContent tests ---

func TestClassify_ClaudeBordered(t *testing.T) {
	// claude ❯ inside a bordered composer box → Empty
	got := ClassifyContent("\u276F", "\u276F", true)
	if got != Empty {
		t.Errorf("ClassifyContent(❯, bordered) = %v, want empty", got)
	}
}

func TestClassify_ClaudeBare(t *testing.T) {
	// claude ❯ on a bare row → Empty
	got := ClassifyContent("\u276F", "\u276F", false)
	if got != Empty {
		t.Errorf("ClassifyContent(❯, bare) = %v, want empty", got)
	}
}

func TestClassify_Codex(t *testing.T) {
	// codex › on a bare row → Empty
	got := ClassifyContent("\u203A", "\u203A", false)
	if got != Empty {
		t.Errorf("ClassifyContent(›, bare) = %v, want empty", got)
	}
}

func TestClassify_DeadShellBareGt(t *testing.T) {
	// dead shell > on a bare row → Unknown
	got := ClassifyContent(">", ">", false)
	if got != Unknown {
		t.Errorf("ClassifyContent(>, bare) = %v, want unknown", got)
	}
}

func TestClassify_DeadShellBareDollar(t *testing.T) {
	// dead shell $ on a bare row → Unknown
	got := ClassifyContent("$", "$", false)
	if got != Unknown {
		t.Errorf("ClassifyContent($, bare) = %v, want unknown", got)
	}
}

func TestClassify_DeadShellBarePercent(t *testing.T) {
	got := ClassifyContent("%", "%", false)
	if got != Unknown {
		t.Errorf("ClassifyContent(%%, bare) = %v, want unknown", got)
	}
}

func TestClassify_DeadShellBareHash(t *testing.T) {
	got := ClassifyContent("#", "#", false)
	if got != Unknown {
		t.Errorf("ClassifyContent(#, bare) = %v, want unknown", got)
	}
}

func TestClassify_ShellGlyphBordered(t *testing.T) {
	// Shell prompt glyph inside a bordered box → Empty (harness's own prompt)
	for _, g := range []string{">", "$", "%", "#"} {
		got := ClassifyContent(g, g, true)
		if got != Empty {
			t.Errorf("ClassifyContent(%q, bordered) = %v, want empty", g, got)
		}
	}
}

func TestClassify_PendingTypedText(t *testing.T) {
	// Pending typed text without prompt glyph
	got := ClassifyContent("ls -la", "ls -la", false)
	if got != Pending {
		t.Errorf("ClassifyContent(ls -la, bare) = %v, want pending", got)
	}
}

func TestClassify_PendingTypedTextWithGlyph(t *testing.T) {
	// Typed text after agent prompt glyph
	got := ClassifyContent("\u276F git push", "\u276F git push", true)
	if got != Pending {
		t.Errorf("ClassifyContent(❯ git push, true) = %v, want pending", got)
	}
}

func TestClassify_GrokPlaceholderGhostStripped(t *testing.T) {
	// Grok placeholder: ghost-stripped row is empty → Empty
	got := ClassifyContent("", "Type a message...", true)
	if got != Empty {
		t.Errorf("ClassifyContent(empty ghost-stripped, grok) = %v, want empty", got)
	}
}

func TestClassify_GrokPlaceholderBareGhostStripped(t *testing.T) {
	// Grok placeholder on a bare row, ghost-stripped to empty → depends on plainRow.
	// plainRow is "Type a message..." which is not an agent glyph → Unknown
	got := ClassifyContent("", "Type a message...", false)
	if got != Unknown {
		t.Errorf("ClassifyContent(empty bare, grok placeholder) = %v, want unknown", got)
	}
}

func TestClassify_EmptyRow(t *testing.T) {
	// Completely empty row → Empty
	got := ClassifyContent("", "", false)
	if got != Empty {
		t.Errorf("ClassifyContent(empty) = %v, want empty", got)
	}
}

func TestClassify_GhostStrippedAgentGlyphBare(t *testing.T) {
	// Ghost stripping removed everything, plain content is just "❯" → Empty
	got := ClassifyContent("", "\u276F", false)
	if got != Empty {
		t.Errorf("ClassifyContent(ghost-stripped to empty, plain=❯) = %v, want empty", got)
	}
}

func TestClassify_GhostStrippedShellGlyphBare(t *testing.T) {
	// Ghost stripping removed everything, plain content is ">" → Unknown
	got := ClassifyContent("", ">", false)
	if got != Unknown {
		t.Errorf("ClassifyContent(ghost-stripped to empty, plain=>) = %v, want unknown", got)
	}
}

func TestClassify_GhostStrippedAnyNonGlyphBare(t *testing.T) {
	// Ghost stripping removed everything, plain content is non-glyph → Unknown
	got := ClassifyContent("", "some text", false)
	if got != Unknown {
		t.Errorf("ClassifyContent(ghost-stripped to empty, plain=text) = %v, want unknown", got)
	}
}

func TestClassify_DimGhostSuggestionOnly(t *testing.T) {
	// Dim ghost suggestion (ghost-stripped = "") → Empty
	got := ClassifyContent("", "Suggest a command...", true)
	if got != Empty {
		t.Errorf("ClassifyContent(dim ghost, bordered) = %v, want empty", got)
	}
}

// --- OpenCode busy-queued tests ---

func TestClassify_OpenCodeEmpty(t *testing.T) {
	// OpenCode "o" glyph on bare row → Empty
	got := ClassifyContent("o", "o", false)
	if got != Empty {
		t.Errorf("ClassifyContent(o, bare) = %v, want empty", got)
	}
}

func TestClassify_OpenCodeEmptyBordered(t *testing.T) {
	// OpenCode "o" glyph inside bordered box → Empty
	got := ClassifyContent("o", "o", true)
	if got != Empty {
		t.Errorf("ClassifyContent(o, bordered) = %v, want empty", got)
	}
}

func TestClassify_OpenCodeBusyRunning(t *testing.T) {
	// OpenCode "o Running..." after glyph → Busy (agent executing)
	got := ClassifyContent("o Running...", "o Running...", true)
	if got != Busy {
		t.Errorf("ClassifyContent(o Running) = %v, want busy", got)
	}
}

func TestClassify_OpenCodeBusyThinking(t *testing.T) {
	// OpenCode "o Thinking..." after glyph → Busy (agent executing)
	got := ClassifyContent("o Thinking...", "o Thinking...", true)
	if got != Busy {
		t.Errorf("ClassifyContent(o Thinking) = %v, want busy", got)
	}
}

func TestClassify_OpenCodeBusyWorking(t *testing.T) {
	// OpenCode "o Working..." after glyph → Busy
	got := ClassifyContent("o Working...", "o Working...", true)
	if got != Busy {
		t.Errorf("ClassifyContent(o Working) = %v, want busy", got)
	}
}

func TestClassify_OpenCodeBusyGenerating(t *testing.T) {
	// OpenCode "o Generating..." after glyph → Busy
	got := ClassifyContent("o Generating...", "o Generating...", true)
	if got != Busy {
		t.Errorf("ClassifyContent(o Generating) = %v, want busy", got)
	}
}

func TestClassify_OpenCodePendingText(t *testing.T) {
	// OpenCode with typed text after glyph → Pending (not busy, typed input)
	// "o git push" has typed text after the glyph, but it's not a busy action.
	// Wait: "git push" is not in the busy list, so it should be Pending.
	got := ClassifyContent("o git push", "o git push", true)
	if got != Pending {
		t.Errorf("ClassifyContent(o git push) = %v, want pending", got)
	}
}

func TestVerdict_BusyString(t *testing.T) {
	if Busy.String() != "busy" {
		t.Errorf("Busy.String() = %q, want busy", Busy.String())
	}
}

// --- Golden fixture tests (end-to-end) ---

func TestGoldenFixtures(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata"))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			raw := string(data)

			// Parse fixture format: first line is metadata, rest is content.
			// Metadata format: expected|bordered|plainContent
			// Example: empty|true|❯
			lines := strings.SplitN(raw, "\n", 2)
			if len(lines) < 2 {
				t.Fatal("fixture must have at least 2 lines (metadata + content)")
			}
			meta := strings.TrimSpace(lines[0])
			content := strings.TrimRight(lines[1], "\n\r")

			parts := strings.SplitN(meta, "|", 3)
			if len(parts) < 2 {
				t.Fatalf("fixture metadata must be: expected|bordered[|plainRow], got %q", meta)
			}
			wantStr := strings.TrimSpace(parts[0])
			borderedStr := strings.TrimSpace(parts[1])
			bordered := borderedStr == "true"

			var want Verdict
			switch wantStr {
			case "empty":
				want = Empty
			case "pending":
				want = Pending
			case "unknown":
				want = Unknown
			default:
				t.Fatalf("unknown expected verdict %q", wantStr)
			}

			// Process: StripGhost → trim → ClassifyContent
			ghostStripped := StripGhost(content)
			trimmed := strings.TrimSpace(ghostStripped)
			plain := strings.TrimSpace(StripANSI(content))

			got := ClassifyContent(trimmed, plain, bordered)
			if got != want {
				t.Errorf("%s: ClassifyContent = %v, want %v\n  raw: %q\n  ghost: %q\n  plain: %q",
					entry.Name(), got, want, content, trimmed, plain)
			}
		})
	}
}

// --- Verdict String tests ---

func TestVerdict_String(t *testing.T) {
	tests := []struct {
		v    Verdict
		want string
	}{
		{Empty, "empty"},
		{Pending, "pending"},
		{Busy, "busy"},
	{Unknown, "unknown"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("Verdict(%d).String() = %q, want %q", tt.v, got, tt.want)
		}
	}
}
