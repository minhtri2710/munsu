package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures under testdata/composer are verbatim
// `herdr pane read <pane> --source recent --lines 12` captures of agy and pi
// panes taken 2026-10-16, with the agy banner's account email replaced by
// user@example.com. agy-trust is a zero-byte capture. agy-cleared still
// held the draft because C-u was rejected. The busy captures carry the
// harness's working row above the rules over an empty composer: the busy gate,
// not the composer gate, refuses them.
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
	entries, err := os.ReadDir(filepath.Join("testdata", "composer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Errorf("testdata/composer holds %d files, the table names %d", len(entries), len(want))
	}
	for _, e := range entries {
		v, ok := want[e.Name()]
		if !ok {
			t.Errorf("%s has no expected verdict", e.Name())
			continue
		}
		harness, _, _ := strings.Cut(e.Name(), "-")
		capture := strings.Join(captureRows(t, filepath.Join("composer", e.Name())), "\n")
		if got := classify[harness](capture); got != v {
			t.Errorf("%s = %v, want %v", e.Name(), got, v)
		}
	}
}

// Rows of agy-empty: rule, glyph, rule, one footer row. Rows of pi-after: rule,
// blank, rule, three footer rows. Each rule is a refused row beside the
// nearest allowed one.
func TestComposerRules(t *testing.T) {
	const (
		agyGlyph, agyLowerRule = 9, 10
		piBlank, piLowerRule   = 7, 8
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
	cases := []struct {
		name     string
		classify func(string) Verdict
		capture  string
		want     Verdict
	}{
		{"agy glyph row and one footer row", ClassifyAgyComposer, edit("agy-empty.txt", same), Empty},
		{"agy blank row", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyGlyph, "")), Unknown},
		{"agy glyph with spaces and NBSP", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyGlyph, " > ")), Empty},
		{"agy glyph followed by text", ClassifyAgyComposer, edit("agy-empty.txt", setRow(agyGlyph, "> x")), Pending},
		{"agy two footer rows", ClassifyAgyComposer, edit("agy-empty.txt", insertAfter(agyLowerRule, "extra")), Unknown},
		{"agy rules with no row between", ClassifyAgyComposer, edit("agy-empty.txt", func(r []string) []string { return append(r[:agyGlyph], r[agyLowerRule:]...) }), Unknown},
		{"pi blank row", ClassifyPiComposer, edit("pi-after.txt", same), Empty},
		{"pi row of spaces and NBSP", ClassifyPiComposer, edit("pi-after.txt", setRow(piBlank, "   ")), Empty},
		{"pi row holding the agy glyph", ClassifyPiComposer, edit("pi-after.txt", setRow(piBlank, ">")), Pending},
		{"pi tab row", ClassifyPiComposer, edit("pi-after.txt", setRow(piBlank, "\t")), Pending},
		{"pi four footer rows", ClassifyPiComposer, edit("pi-after.txt", insertAfter(piLowerRule, "extension")), Empty},
		{"pi five footer rows", ClassifyPiComposer, edit("pi-after.txt", insertAfter(piLowerRule, "extension", "extension")), Unknown},
		{"pi upper rule missing", ClassifyPiComposer, edit("pi-after.txt", func(r []string) []string { return r[piBlank:] }), Unknown},
		{"claude blank row", ClassifyClaudeComposer, edit("pi-empty.txt", same), Pending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.classify(tc.capture); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}
