package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInlineSpansRecoversCitationAfterUnmatchedRunAcrossLines(t *testing.T) {
	openRun := 0
	openText := ""
	if spans := scanInlineSpans("An unclosed `` run", &openRun, &openText); len(spans) != 0 {
		t.Fatalf("first line returned spans: %#v", spans)
	}
	if openRun != 2 {
		t.Fatalf("openRun after first line = %d, want 2", openRun)
	}
	if spans := scanInlineSpans("followed by `home.MissingAfterUnmatched`", &openRun, &openText); len(spans) != 0 {
		t.Fatalf("second line returned spans before boundary recovery: %#v", spans)
	}

	spans := inlineSpans(openText)
	if len(spans) != 1 || spans[0].text != "home.MissingAfterUnmatched" {
		t.Fatalf("boundary recovery = %#v, want the citation span", spans)
	}
}

func TestInlineSpansPreservesShorterRunInsideMultilineSpan(t *testing.T) {
	openRun := 0
	openText := ""
	scanInlineSpans("``outer `", &openRun, &openText)
	spans := scanInlineSpans("inner`` and `home.MissingAfterSpan`", &openRun, &openText)

	if openRun != 0 || openText != "" {
		t.Fatalf("scanner state after closing spans = (%d, %q), want empty", openRun, openText)
	}
	if len(spans) != 2 {
		t.Fatalf("spans = %#v, want multiline span and citation", spans)
	}
	if spans[0].text != "outer `\ninner" {
		t.Errorf("multiline span text = %q, want %q", spans[0].text, "outer `\ninner")
	}
	if spans[1].text != "home.MissingAfterSpan" {
		t.Errorf("citation text = %q, want %q", spans[1].text, "home.MissingAfterSpan")
	}
}

func TestInlineSpansAllowsPunctuationBeforeCitation(t *testing.T) {
	openRun := 0
	openText := ""
	if spans := scanInlineSpans("An unmatched `` run", &openRun, &openText); len(spans) != 0 {
		t.Fatalf("opening line returned spans: %#v", spans)
	}
	spans := recoverInlineSpans(openText + "\nThen.`home.MissingAfterPunctuation`")
	if len(spans) != 1 || spans[0].text != "home.MissingAfterPunctuation" {
		t.Fatalf("recovered spans = %#v, want punctuation-prefixed citation", spans)
	}
}

func TestInlineBlockBoundaryRecognizesEmptyATXHeading(t *testing.T) {
	for _, line := range []string{"#", "###", "   ######"} {
		if !inlineBlockBoundary(line) {
			t.Errorf("inlineBlockBoundary(%q) = false, want true", line)
		}
	}
}

func TestInlineSpansRecoversPendingTextAtEOF(t *testing.T) {
	openRun := 0
	openText := ""
	scanInlineSpans("prefix `` and `home.MissingAtEOF`", &openRun, &openText)
	if openRun != 2 {
		t.Fatalf("openRun = %d, want 2 before EOF recovery", openRun)
	}

	spans := recoverInlineSpans(openText)
	if len(spans) != 1 || spans[0].text != "home.MissingAtEOF" {
		t.Fatalf("EOF recovery = %#v, want the citation span", spans)
	}
}

func TestInlineSpansRecoversSuccessivePendingRuns(t *testing.T) {
	openRun := 0
	openText := ""
	scanInlineSpans("prefix ``` then `` then `home.MissingAfterRuns`", &openRun, &openText)
	if openRun != 3 {
		t.Fatalf("openRun = %d, want 3 before boundary recovery", openRun)
	}

	spans := recoverInlineSpans(openText)
	if len(spans) != 1 || spans[0].text != "home.MissingAfterRuns" {
		t.Fatalf("boundary recovery = %#v, want the citation span", spans)
	}
}

func TestScanRawHTMLClosingTagAllowsHorizontalWhitespace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package home\n\nfunc MissingAfterSpace() {}\nfunc MissingAfterTab() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc := "<script>\n</script bogus\nAn unmatched `` run inside HTML.\n</script >\nThen `home.MissingAfterSpace` then ``.\n\n<SCRIPT>\nAn unmatched `` run inside HTML.\n</SCRIPT\t>\nThen `home.MissingAfterTab` then ``.\n"
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "doc.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := scan(root)
	if err != nil {
		t.Fatalf("scan(%q): %v", root, err)
	}
	want := map[string]bool{
		"resolved\tdocs/doc.md\tsymbol\thome.MissingAfterSpace": true,
		"resolved\tdocs/doc.md\tsymbol\thome.MissingAfterTab":   true,
	}
	for _, row := range rows {
		delete(want, row)
	}
	for row := range want {
		t.Errorf("scan output missing %q", row)
	}
}

func TestScanRecoversCitationAfterInlineTripleBackticks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package home\n\nfunc MissingAfterInlineTriple() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "An unmatched `` run\n```example``` and `home.MissingAfterInlineTriple`\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "doc.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := scan(root)
	if err != nil {
		t.Fatalf("scan(%q): %v", root, err)
	}
	for _, row := range rows {
		if row == "resolved\tdocs/doc.md\tsymbol\thome.MissingAfterInlineTriple" {
			return
		}
	}
	t.Fatal("scan output missing inline citation after rejected triple-backtick fence-like line")
}

func TestScanRecoversCitationAfterNonOneOrderedListMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package home\n\nfunc MissingAfterOrdered() {}\nfunc MissingAfterFollowing() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "- An unmatched ``` then ``outer\n2. inner`` and `home.MissingAfterOrdered`\nThen `home.MissingAfterFollowing`\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "doc.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := scan(root)
	if err != nil {
		t.Fatalf("scan(%q): %v", root, err)
	}
	want := map[string]bool{
		"resolved\tdocs/doc.md\tsymbol\thome.MissingAfterOrdered":   true,
		"resolved\tdocs/doc.md\tsymbol\thome.MissingAfterFollowing": true,
	}
	for _, row := range rows {
		delete(want, row)
	}
	for row := range want {
		t.Errorf("scan output missing %q", row)
	}
}

func TestScanRecoversUnmatchedRunsAcrossBoundaries(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "citations", "unmatched-backtick-continue")
	rows, err := scan(root)
	if err != nil {
		t.Fatalf("scan(%q): %v", root, err)
	}
	want := map[string]bool{
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterQuote":         true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterRuns":          true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterItem":          true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterLazy":          true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterLazyItem":      true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterHTML":          true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterRawHTML":       true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterInlineHTML":    true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterMultilineHTML": true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterListHTML":      true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterIndented":      true,
		"unresolved\tdocs/doc.md\tsymbol\thome.MissingAfterHTMLBlank":     true,
	}
	for _, row := range rows {
		if want[row] {
			delete(want, row)
		}
	}
	for row := range want {
		t.Errorf("scan output missing %q", row)
	}
}
