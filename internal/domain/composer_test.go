package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type composerCase struct {
	name     string
	classify func(string) Verdict
	capture  string
	want     Verdict
}

func runComposerCases(t *testing.T, cases []composerCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.classify(tc.capture); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// The fixtures under testdata/composer are verbatim
// `herdr pane read <pane> --source recent --lines 12` captures of agy and pi
// panes taken 2026-10-16, with the agy banner's account email replaced by
// user@example.com. agy-trust is a zero-byte capture. agy-cleared still held
// the draft because C-u was rejected. The busy captures carry the harness's
// working row above the rules over an empty composer: the busy gate, not the
// composer gate, refuses them. The synthetic rows are the review's probes of
// #951 at 186efc2d, each of which an earlier parse read as Empty.
func TestComposerCaptures(t *testing.T) {
	want := map[string]Verdict{
		"agy-after.txt":   Empty,
		"agy-busy.txt":    Empty,
		"agy-cleared.txt": Pending,
		"agy-empty.txt":   Empty,
		"agy-pending.txt": Pending,
		"agy-trust.txt":   Unknown,
		"pi-after.txt":    Empty,
		"pi-busy.txt":     Empty,
		"pi-cleared.txt":  Empty,
		"pi-empty.txt":    Empty,
		"pi-pending.txt":  Pending,
		"pi-pending2.txt": Pending,
	}
	classify := map[string]func(string) Verdict{"agy": ClassifyAgyComposer, "pi": ClassifyPiComposer}
	fixture := func(name string) []string { return captureRows(t, filepath.Join("composer", name)) }
	entries, err := os.ReadDir(filepath.Join("testdata", "composer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Errorf("testdata/composer holds %d files, the table names %d", len(entries), len(want))
	}
	var cases []composerCase
	for _, e := range entries {
		v, ok := want[e.Name()]
		if !ok {
			t.Errorf("%s has no expected verdict", e.Name())
			continue
		}
		harness, _, _ := strings.Cut(e.Name(), "-")
		cases = append(cases, composerCase{e.Name(), classify[harness], strings.Join(fixture(e.Name()), "\n"), v})
	}

	rule := strings.Repeat("─", 50)
	const short = "──────────"
	lines := func(rows ...string) string { return strings.Join(rows, "\n") }
	setRow := func(name string, at int, row string) string {
		rows := fixture(name)
		rows[at] = row
		return strings.Join(rows, "\n")
	}
	cases = append(cases,
		composerCase{"pi rule-like row inside a draft", ClassifyPiComposer, lines(rule, "draft line 1", short, "", " "+rule, "footer"), Unknown},
		composerCase{"agy rule-like row inside a draft", ClassifyAgyComposer, lines(rule, "> draft line 1", short, ">", " "+rule, "footer"), Unknown},
		composerCase{"pi rule-like footer row", ClassifyPiComposer, lines(rule, "draft", rule, "", short, "cwd", "stats"), Unknown},
		composerCase{"agy rule-like footer row", ClassifyAgyComposer, lines(rule, "> draft", rule, ">", short, "footer"), Unknown},
		composerCase{"pi lower border cut off", ClassifyPiComposer, strings.Join(fixture("pi-pending.txt")[:8], "\n"), Unknown},
		composerCase{"agy lower border cut off", ClassifyAgyComposer, lines(rule, ">", rule, "> draft wake text not sent"), Unknown},
		composerCase{"pi autocomplete menu below the composer", ClassifyPiComposer, lines(rule, " ", rule, "→ README.md", "  go.mod", "/tmp/dir", "0.0%/256k"), Unknown},
		composerCase{"pi composer of spaces only", ClassifyPiComposer, setRow("pi-empty.txt", 8, "   "), Empty},
		composerCase{"agy glyph followed by spaces only", ClassifyAgyComposer, setRow("agy-empty.txt", 9, ">   "), Empty},
	)
	runComposerCases(t, cases)
}

// Rows of agy-empty and agy-pending: rule (8), glyph (9), rule (10), one footer
// row (11). Rows of pi-after and pi-pending: rule (6), composer (7), rule (8),
// cwd (9), stats (10), extension status (11). Each rule is a refused row beside
// the nearest allowed one.
func TestComposerRules(t *testing.T) {
	const (
		agyGlyph, agyLowerRule, agyFooterRow = 9, 10, 11
		piBlank, piLowerRule, piCwd, piStats = 7, 8, 9, 10
		piLastFooter                         = 11
		short                                = "──────────"
	)
	edit := func(name string, change func(rows []string) []string) string {
		rows := captureRows(t, filepath.Join("composer", name))
		return strings.Join(change(rows), "\n")
	}
	same := func(r []string) []string { return r }
	insertAfter := func(at int, extra ...string) func([]string) []string {
		return func(r []string) []string {
			return append(r[:at+1], append(extra, r[at+1:]...)...)
		}
	}
	setRow := func(at int, row string) func([]string) []string {
		return func(r []string) []string { r[at] = row; return r }
	}
	runComposerCases(t, []composerCase{
		{"agy glyph row and one footer row", ClassifyAgyComposer, edit("agy-empty.txt", same), Empty},
		{"agy blank row", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyGlyph, "")), Unknown},
		{"agy glyph with spaces and NBSP", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyGlyph, " > ")), Empty},
		{"agy glyph followed by text", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyGlyph, "> x")), Pending},
		{"agy two footer rows", ClassifyAgyComposer, edit("agy-empty.txt", insertAfter(agyLowerRule, "extra")), Unknown},
		{"agy footer with a blank prefix", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyFooterRow, "      Gemini 4 Argon · high")), Empty},
		{"agy footer without the model segment", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyFooterRow, "? for shortcuts")), Unknown},
		{"agy rules with no row between", ClassifyAgyComposer, edit("agy-empty.txt", func(r []string) []string { return append(r[:agyGlyph], r[agyLowerRule:]...) }), Unknown},
		{"agy upper rule narrower than the lower", ClassifyAgyComposer, edit("agy-pending.txt", insertAfter(agyGlyph, short, ">")), Unknown},
		{"pi blank row", ClassifyPiComposer, edit("pi-after.txt", same), Empty},
		{"pi row of spaces and NBSP", ClassifyPiComposer, edit("pi-after.txt", setRow(piBlank, "   ")), Empty},
		{"pi row holding the agy glyph", ClassifyPiComposer, edit("pi-after.txt", setRow(piBlank, ">")), Pending},
		{"pi tab row", ClassifyPiComposer, edit("pi-after.txt", setRow(piBlank, "\t")), Pending},
		{"pi four footer rows", ClassifyPiComposer, edit("pi-after.txt", insertAfter(piLastFooter, "extension")), Empty},
		{"pi five footer rows", ClassifyPiComposer, edit("pi-after.txt", insertAfter(piLastFooter, "extension", "extension")), Unknown},
		{"pi cwd and stats only", ClassifyPiComposer, edit("pi-after.txt", func(r []string) []string { return append(r[:piLastFooter], "") }), Empty},
		{"pi cwd only", ClassifyPiComposer, edit("pi-after.txt", func(r []string) []string { return append(r[:piStats], "") }), Unknown},
		{"pi cwd as a ~ path", ClassifyPiComposer, edit("pi-after.txt", setRow(piCwd, "~/Work/munsu")), Empty},
		{"pi first footer row is not a path", ClassifyPiComposer, edit("pi-after.txt", setRow(piCwd, "Work/munsu")), Unknown},
		{"pi second footer row has no stats", ClassifyPiComposer, edit("pi-after.txt", setRow(piStats, "(auto) • xp")), Unknown},
		{"pi upper rule missing", ClassifyPiComposer, edit("pi-after.txt", func(r []string) []string { return r[piBlank:] }), Unknown},
		{"pi lower rule narrower than the upper", ClassifyPiComposer, edit("pi-pending.txt", insertAfter(piLowerRule, "", short)), Unknown},
		{"claude blank row", ClassifyClaudeComposer, edit("pi-empty.txt", same), Pending},
	})
}
