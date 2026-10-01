package fleet

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// verdictDocFields returns the json field names RecordReviewVerdict decodes.
func verdictDocFields() []string {
	var fields []string
	typ := reflect.TypeOf(reviewVerdictFileDoc{})
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	return fields
}

func TestVerdictFileShapeNamesExactlyTheFieldsTheRecordStepDecodes(t *testing.T) {
	got := strings.Split(verdictFileShape(""), ", ")
	if !reflect.DeepEqual(got, verdictDocFields()) {
		t.Fatalf("verdictFileShape fields = %q, want the decoded document's fields %q", got, verdictDocFields())
	}
	if shape := verdictFileShape("`"); !strings.HasPrefix(shape, "`schema_version`, `task`") || !strings.HasSuffix(shape, "`evidence`") {
		t.Fatalf("verdictFileShape(backtick) = %q, want every field quoted", shape)
	}
}

func TestReviewerCharterStatesTheReadOnlyContractAndTheVerdictFile(t *testing.T) {
	charter := DefaultCharter("rev-1", taskauthority.KindReview, "direct-PR")
	for _, want := range []string{
		"# Reviewer Charter",
		"**Task: rev-1**",
		"**Kind: review**",
		"read-only Reviewer",
		"MUST NOT",
		"Create a branch, commit",
		"`$MUNSU_VERDICT_FILE`",
		verdictFileShape("`"),
		"`" + string(domain.VerdictPass) + "` or `" + string(domain.VerdictFail) + "`",
		"`$MUNSU_VERDICT_FILE.tmp.<pid>.<hex>`",
		"## Review Evidence",
		"never counts toward a PASS",
	} {
		if !strings.Contains(charter, want) {
			t.Errorf("reviewer charter lacks %q", want)
		}
	}
	for _, unwanted := range []string{"PR {url}", "Do not merge the PR"} {
		if strings.Contains(charter, unwanted) {
			t.Errorf("reviewer charter carries ship delivery text %q", unwanted)
		}
	}
	if ship := DefaultCharter("ship-1", taskauthority.KindShip, "direct-PR"); strings.Contains(ship, "# Reviewer Charter") {
		t.Fatal("a ship charter is the reviewer charter")
	}
}

func TestReviewBriefNamesTheContractTheHeadCheckAndTheVerdictFile(t *testing.T) {
	home := t.TempDir()
	head := strings.Repeat("a", 40)
	if err := Scaffold(ScaffoldOptions{HomeDir: home, ID: "rev-1", Repo: "munsu", Review: true, ReviewTask: "ship-1", ReviewHead: head}); err != nil {
		t.Fatalf("Scaffold review: %v", err)
	}
	data, err := os.ReadFile(Path(home, "rev-1"))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(data)
	for _, want := range []string{
		"# Review brief: rev-1",
		"Reviewed task: ship-1",
		"Reviewed head: " + head,
		"`git rev-parse HEAD`",
		"## Review method",
		"`$MUNSU_VERDICT_FILE`",
		verdictFileShape("`"),
		"`" + string(domain.VerdictPass) + "` or `" + string(domain.VerdictFail) + "`",
		"`$MUNSU_VERDICT_FILE.tmp.<pid>.<hex>`",
		"Read only.",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("review brief lacks %q", want)
		}
	}
	if strings.Contains(brief, "## Delivery") {
		t.Error("review brief carries a delivery section")
	}
}

func TestReviewBriefRefusesAMissingTaskOrHead(t *testing.T) {
	for name, opts := range map[string]ScaffoldOptions{
		"no task": {ReviewHead: strings.Repeat("a", 40)},
		"no head": {ReviewTask: "ship-1"},
		"blank":   {ReviewTask: "  ", ReviewHead: "\t"},
	} {
		t.Run(name, func(t *testing.T) {
			opts.HomeDir, opts.ID, opts.Repo, opts.Review = t.TempDir(), "rev-1", "munsu", true
			err := Scaffold(opts)
			if err == nil || !strings.Contains(err.Error(), "requires the reviewed task and head") {
				t.Fatalf("Scaffold error = %v, want the review brief contract refusal", err)
			}
			if Exists(opts.HomeDir, "rev-1") {
				t.Fatal("a refused review brief was written")
			}
		})
	}
}

func TestLintBriefAcceptsEveryScaffoldedKindAndNamesTheMissingSections(t *testing.T) {
	for _, tc := range []struct {
		kind string
		opts ScaffoldOptions
	}{
		{taskauthority.KindShip, ScaffoldOptions{Mode: "direct-PR"}},
		{taskauthority.KindShip, ScaffoldOptions{Mode: "no-mistakes"}},
		{taskauthority.KindScout, ScaffoldOptions{Scout: true, Mode: "direct-PR", ScoutScope: "scope", Generation: 1}},
		{taskauthority.KindReview, ScaffoldOptions{Review: true, ReviewTask: "ship-1", ReviewHead: strings.Repeat("a", 40)}},
	} {
		t.Run(tc.kind+"/"+tc.opts.Mode, func(t *testing.T) {
			tc.opts.HomeDir, tc.opts.ID, tc.opts.Repo = t.TempDir(), "t1", "munsu"
			if err := Scaffold(tc.opts); err != nil {
				t.Fatal(err)
			}
			if err := LintBrief(tc.opts.HomeDir, "t1", tc.kind); err != nil {
				t.Fatalf("LintBrief of a scaffolded %s brief: %v", tc.kind, err)
			}
			data, err := os.ReadFile(Path(tc.opts.HomeDir, "t1"))
			if err != nil {
				t.Fatal(err)
			}
			stripped := strings.Replace(string(data), "## Rules", "## Rule", 1)
			if err := os.WriteFile(Path(tc.opts.HomeDir, "t1"), []byte(stripped), 0o644); err != nil {
				t.Fatal(err)
			}
			err = LintBrief(tc.opts.HomeDir, "t1", tc.kind)
			if err == nil || !strings.Contains(err.Error(), "missing scaffolded sections: ## Rules;") {
				t.Fatalf("LintBrief error = %v, want the renamed section named as missing", err)
			}
		})
	}
}

func TestLintBriefRefusesAnAbsentBrief(t *testing.T) {
	if err := LintBrief(t.TempDir(), "absent", taskauthority.KindShip); err == nil || !strings.Contains(err.Error(), "reading brief for task absent") {
		t.Fatalf("LintBrief absent brief error = %v", err)
	}
}

func TestLintBriefIgnoresTrailingWhitespaceOnHeadings(t *testing.T) {
	home := t.TempDir()
	if err := Scaffold(ScaffoldOptions{HomeDir: home, ID: "t1", Repo: "munsu", Mode: "direct-PR"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path(home, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(data), "\n", " \r\n")
	if err := os.WriteFile(Path(home, "t1"), []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LintBrief(home, "t1", taskauthority.KindShip); err != nil {
		t.Fatalf("LintBrief of a CRLF brief: %v", err)
	}
}
