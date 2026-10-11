package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeCapture(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

// The fixtures are verbatim 12-row `herdr pane read --lines 12` captures of two
// idle Claude Code panes: reply text, a blank row, the "done" line, a blank row,
// rule, glyph, rule, footer, then the trailing newline. Row indexes below name
// the composer rows.
const (
	rowUpperRule = 8
	rowGlyph     = 9
	rowLowerRule = 10
	rowFooter    = 11
)

func TestClassifyClaudeComposer(t *testing.T) {
	for _, name := range []string{"claude_composer_w34p74.capture", "claude_composer_w34p76.capture"} {
		if got := ClassifyClaudeComposer(strings.Join(claudeCapture(t, name), "\n")); got != Empty {
			t.Errorf("%s = %v, want empty", name, got)
		}
	}

	base := claudeCapture(t, "claude_composer_w34p74.capture")
	rule := base[rowUpperRule]
	with := func(edit func(rows []string) []string) string {
		return strings.Join(edit(append([]string(nil), base...)), "\n")
	}
	cases := []struct {
		name string
		rows func(rows []string) []string
		want Verdict
	}{
		{"glyph with spaces and NBSP around it", func(r []string) []string { r[rowGlyph] = "  ❯  "; return r }, Empty},
		{"glyph followed by text", func(r []string) []string { r[rowGlyph] = "❯ half typed"; return r }, Pending},
		{"two input rows", func(r []string) []string {
			return append(r[:rowGlyph+1], append([]string{"wrapped second row"}, r[rowGlyph+1:]...)...)
		}, Pending},
		{"upper rule missing", func(r []string) []string { return append(r[:rowUpperRule], r[rowGlyph:]...) }, Unknown},
		{"lower rule missing", func(r []string) []string { return append(r[:rowLowerRule], r[rowFooter:]...) }, Unknown},
		{"rule cut short", func(r []string) []string { r[rowLowerRule] = rule[:9*len("─")]; return r }, Unknown},
		{"rules with no row between", func(r []string) []string { return append(r[:rowGlyph], r[rowLowerRule:]...) }, Unknown},
		{"three rows below the lower rule", func(r []string) []string {
			return append(r[:rowFooter+1], "second", "third", "")
		}, Empty},
		{"four rows below the lower rule", func(r []string) []string {
			return append(r[:rowFooter+1], "second", "third", "fourth", "")
		}, Unknown},
		{"styled glyph", func(r []string) []string { r[rowGlyph] = "\x1b[2m❯\x1b[0m"; return r }, Pending},
		{"codex layout without rules", func(r []string) []string { return []string{"›", "  gpt-6 high"} }, Unknown},
		{"empty capture", func(r []string) []string { return nil }, Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyClaudeComposer(with(tc.rows)); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}
