package fleet

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestShipBriefTemplateNoMistakes(t *testing.T) {
	tmpl := mustShipBrief(t, "test-task-1", "munsu", "no-mistakes", false)

	checks := []string{
		"Task brief: test-task-1",
		"git checkout -b mu/",
		"no-mistakes doctor",
		"Delivery mode: no-mistakes",
		"no-mistakes axi respond",
		"CI green",
	}
	for _, c := range checks {
		if !strings.Contains(tmpl, c) {
			t.Errorf("no-mistakes brief missing %q", c)
		}
	}
	if strings.Contains(tmpl, "open a PR directly") {
		t.Error("no-mistakes brief inherited direct-PR instructions")
	}
	if strings.Contains(tmpl, "local-only") {
		t.Error("no-mistakes brief inherited local-only instructions")
	}
	for _, required := range []string{
		"shared `no-mistakes` daemon",
		"every lane/home",
		"kills other lanes",
	} {
		if !strings.Contains(tmpl, required) {
			t.Errorf("no-mistakes brief missing shared-daemon rule fragment %q", required)
		}
	}
}

func TestShipBriefTemplateDirectPR(t *testing.T) {
	tmpl := mustShipBrief(t, "test-task-1", "munsu", "direct-PR", false)

	checks := []string{
		"Delivery mode: direct-PR",
		"commit",
		"push",
		"open a PR directly",
		"Never merge",
	}
	for _, c := range checks {
		if !strings.Contains(tmpl, c) {
			t.Errorf("direct-PR brief missing %q", c)
		}
	}
	for _, forbidden := range []string{"no-mistakes doctor", "/no-mistakes", "no-mistakes axi", "orchestrator merge"} {
		if strings.Contains(tmpl, forbidden) {
			t.Errorf("direct-PR brief inherited forbidden instruction %q", forbidden)
		}
	}
}

func TestShipBriefTemplateLocalOnly(t *testing.T) {
	tmpl := mustShipBrief(t, "test-task-1", "munsu", "local-only", false)

	checks := []string{
		"Delivery mode: local-only",
		"Commit locally",
		"orchestrator merge",
		"Do not push",
	}
	for _, c := range checks {
		if !strings.Contains(tmpl, c) {
			t.Errorf("local-only brief missing %q", c)
		}
	}
	for _, forbidden := range []string{"no-mistakes doctor", "/no-mistakes", "no-mistakes axi", "open a PR directly"} {
		if strings.Contains(tmpl, forbidden) {
			t.Errorf("local-only brief inherited forbidden instruction %q", forbidden)
		}
	}
}

func TestScoutBriefTemplate(t *testing.T) {
	tmpl := mustScoutBrief(t, "test-scout-1", "munsu", "direct-PR", false, "security", 0, 1)

	checks := []string{
		"Scout brief: test-scout-1",
		"SCOUT task",
		ReportName(1),
		"Never create branches",
	}
	for _, c := range checks {
		if !strings.Contains(tmpl, c) {
			t.Errorf("scout brief missing %q", c)
		}
	}

	// Must NOT contain ship-mode features
	if strings.Contains(tmpl, "no-mistakes") {
		t.Error("scout brief should not contain no-mistakes references")
	}
	if strings.Contains(tmpl, "git checkout -b") {
		t.Error("scout brief should not contain branch creation")
	}
}

func TestScaffoldRefreshesDirectoryMtime(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "data", "aged")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := Scaffold(ScaffoldOptions{HomeDir: tmp, ID: "aged", Repo: "munsu", Mode: "no-mistakes"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().After(old) {
		t.Fatalf("directory mtime = %v, want refreshed", info.ModTime())
	}
}

func TestScaffoldShip(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "test-ship",
		Repo:    "munsu",
		Scout:   false,
		Mode:    "no-mistakes",
		Yolo:    false,
	}

	if err := Scaffold(opts); err != nil {
		t.Fatal(err)
	}

	// Verify file exists
	briefPath := Path(tmp, "test-ship")
	if _, err := os.Stat(briefPath); err != nil {
		t.Fatalf("brief.md not created: %v", err)
	}

	data, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "Delivery mode: no-mistakes") {
		t.Errorf("brief should contain delivery mode")
	}
	if strings.Contains(content, "+yolo") {
		t.Errorf("brief should not contain +yolo when false")
	}
}

func TestScaffoldScout(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir:    tmp,
		ID:         "test-scout",
		Repo:       "munsu",
		Scout:      true,
		Mode:       "direct-PR",
		Generation: 1,
	}

	if err := Scaffold(opts); err != nil {
		t.Fatal(err)
	}

	briefPath := Path(tmp, "test-scout")
	if _, err := os.Stat(briefPath); err != nil {
		t.Fatalf("brief.md not created: %v", err)
	}

	data, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "SCOUT task") {
		t.Errorf("scout brief should contain scout indicator")
	}
	if !strings.Contains(content, ReportName(1)) {
		t.Errorf("scout brief should mention the generation-scoped report path")
	}
}

func TestScaffoldWithYolo(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "test-yolo",
		Repo:    "munsu",
		Scout:   false,
		Mode:    "direct-PR",
		Yolo:    true,
	}

	if err := Scaffold(opts); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(Path(tmp, "test-yolo"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "Delivery mode: direct-PR") {
		t.Errorf("brief should contain delivery mode")
	}
	if !strings.Contains(string(content), "+yolo") {
		t.Errorf("brief should contain +yolo when true")
	}
}

func TestPath(t *testing.T) {
	p := Path("/home/user/.munsu", "task-1")
	want := filepath.Join("/home/user/.munsu", "data", "task-1", "brief.md")
	if p != want {
		t.Errorf("Path = %q, want %q", p, want)
	}
}

func TestExists(t *testing.T) {
	tmp := t.TempDir()

	// Should return false before creation
	if Exists(tmp, "test-exists") {
		t.Error("Exists should be false before scaffold")
	}

	Scaffold(ScaffoldOptions{
		HomeDir: tmp,
		ID:      "test-exists",
		Repo:    "munsu",
		Mode:    "no-mistakes",
	})

	if !Exists(tmp, "test-exists") {
		t.Error("Exists should be true after scaffold")
	}
}

func TestScaffoldScoutNamesReportByGeneration(t *testing.T) {
	tmp := t.TempDir()

	// The write path is generation-bound AT CREATION: the brief instructs the
	// soldier to write exactly the launching generation's report name, at the
	// same path the safety check resolves.
	const gen = taskauthority.Generation(3)
	if err := Scaffold(ScaffoldOptions{HomeDir: tmp, ID: "named-scout", Repo: "munsu", Scout: true, Mode: "direct-PR", Generation: gen}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(Path(tmp, "named-scout"))
	if err != nil {
		t.Fatal(err)
	}
	// The brief instructs the soldier in $MUNSU_HOME-relative terms; the
	// instructed markdown file must carry the standard forward-slash logical path,
	// while ReportPath resolves the OS filesystem path.
	wantLogical := path.Join("data", "named-scout", ReportName(gen))
	if !strings.Contains(string(content), wantLogical) {
		t.Fatalf("scout brief = %q, want it to instruct writing %q", content, wantLogical)
	}
	wantPath := filepath.Join("data", "named-scout", ReportName(gen))
	if !strings.HasSuffix(ReportPath(tmp, "named-scout", gen), wantPath) {
		t.Fatalf("ReportPath = %q, want it to resolve the instructed %q", ReportPath(tmp, "named-scout", gen), wantPath)
	}

	// A scout brief without the task generation fails closed: an unbound
	// report name would be an unversioned one.
	if err := Scaffold(ScaffoldOptions{HomeDir: tmp, ID: "unbound-scout", Repo: "munsu", Scout: true, Mode: "direct-PR"}); err == nil {
		t.Fatal("scaffolding a scout brief without a generation must fail")
	}
}

func TestReportPath(t *testing.T) {
	p := ReportPath("/home/user/.munsu", "scout-1", 1)
	want := filepath.Join("/home/user/.munsu", "data", "scout-1", ReportName(1))
	if p != want {
		t.Errorf("ReportPath = %q, want %q", p, want)
	}
}

func TestReportExists(t *testing.T) {
	tmp := t.TempDir()

	if ReportExists(tmp, "scout-nope", 1) {
		t.Error("ReportExists should be false before creation")
	}

	dir := filepath.Join(tmp, "data", "scout-yes")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, ReportName(1)), []byte("findings"), 0644)

	if !ReportExists(tmp, "scout-yes", 1) {
		t.Error("ReportExists should be true after creation")
	}
}

func TestScaffoldCreatesDir(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "dir-test",
		Repo:    "munsu",
		Mode:    "no-mistakes",
	}

	if err := Scaffold(opts); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "data", "dir-test")
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("data/<id> directory not created: %v", err)
	}
	if !fi.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
}

func TestShipBriefContainsStatusReporting(t *testing.T) {
	tmpl := mustShipBrief(t, "t1", "repo", "no-mistakes", false)
	if !strings.Contains(tmpl, "munsu report") {
		t.Error("ship brief should reference munsu report command")
	}
	if strings.Contains(tmpl, "munsu task status") {
		t.Error("ship brief should not reference munsu task status command")
	}
}

func TestScoutBriefHasReportContract(t *testing.T) {
	tmpl := mustScoutBrief(t, "scout-r", "repo", "direct-PR", false, "", 0, 1)

	if !strings.Contains(tmpl, "Write your findings") {
		t.Error("scout brief should include report instructions")
	}
	if !strings.Contains(tmpl, ReportName(1)) {
		t.Errorf("scout brief should mention the generation-scoped report path")
	}
}

// TestShipBriefUnknownModeFailsLoud pins the delivery-rules switch: an empty
// or unrecognized delivery mode is a resolution failure, never a silent
// fall-through to the no-mistakes rules.
func TestShipBriefUnknownModeFailsLoud(t *testing.T) {
	for _, mode := range []string{"", "direct-pr", "no-mistake", "yolo"} {
		t.Run(mode, func(t *testing.T) {
			tmpl, err := shipBriefTemplate("t1", "repo", mode, false)
			if err == nil {
				t.Fatalf("ship brief rendered for unknown delivery mode %q", mode)
			}
			if tmpl != "" {
				t.Fatalf("ship brief returned content alongside the refusal for mode %q", mode)
			}
			if !strings.Contains(err.Error(), "unknown delivery mode") {
				t.Fatalf("refusal does not name the unknown delivery mode: %v", err)
			}
		})
	}
}

// TestScaffoldRefusesUnknownMode pins the refusal at the Scaffold boundary:
// no brief.md is written for a mode the delivery rules cannot serve.
func TestScaffoldRefusesUnknownMode(t *testing.T) {
	home := t.TempDir()
	err := Scaffold(ScaffoldOptions{HomeDir: home, ID: "t1", Repo: "repo", Mode: ""})
	if err == nil {
		t.Fatal("Scaffold wrote a brief for an unknown delivery mode")
	}
	if _, statErr := os.Stat(Path(home, "t1")); statErr == nil {
		t.Fatal("Scaffold wrote brief.md despite the refusal")
	}
}

func TestShipBriefModeLinePresent(t *testing.T) {
	tmpl := mustShipBrief(t, "t1", "repo", "local-only", false)
	if !strings.Contains(tmpl, "Delivery mode: local-only") {
		t.Error("should emit delivery mode line when mode is set")
	}
}

func TestBriefsDocumentIdempotentResolvedKey(t *testing.T) {
	for name, tmpl := range map[string]string{
		"ship":  mustShipBrief(t, "t1", "repo", "no-mistakes", false),
		"scout": mustScoutBrief(t, "s1", "repo", "direct-PR", false, "", 0, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(tmpl, "resolved [key=<slug>]: {summary}") {
				t.Fatalf("brief missing keyed resolution syntax")
			}
			if !strings.Contains(tmpl, "Repeating the same resolved key is safe") {
				t.Fatalf("brief missing idempotency guidance")
			}
		})
	}
}

// mustShipBrief renders a ship brief for a real delivery mode, failing the
// test on the refusal path so every fixture below uses a mode the delivery
// rules actually serve.
// TestScoutBriefUnknownModeFailsLoud pins the scout path to the same
// invariant as ship: after the durable delivery contract, an empty or
// unrecognized delivery mode is a resolution failure upstream, never a line
// the brief quietly omits.
func TestScoutBriefUnknownModeFailsLoud(t *testing.T) {
	for _, mode := range []string{"", "direct-pr", "no-mistake", "yolo"} {
		t.Run(mode, func(t *testing.T) {
			tmpl, err := scoutBriefTemplate("s1", "repo", mode, false, "", 0, 1)
			if err == nil {
				t.Fatalf("scout brief rendered for unknown delivery mode %q", mode)
			}
			if tmpl != "" {
				t.Fatalf("scout brief returned content alongside the refusal for mode %q", mode)
			}
			if !strings.Contains(err.Error(), "unknown delivery mode") {
				t.Fatalf("refusal does not name the unknown delivery mode: %v", err)
			}
		})
	}
}

// TestScaffoldRefusesUnknownScoutMode pins the refusal at the Scaffold
// boundary for scouts: no brief.md is written for a mode the brief cannot
// name.
func TestScaffoldRefusesUnknownScoutMode(t *testing.T) {
	home := t.TempDir()
	err := Scaffold(ScaffoldOptions{HomeDir: home, ID: "s1", Repo: "repo", Scout: true, Generation: 1})
	if err == nil {
		t.Fatal("Scaffold wrote a scout brief for an unknown delivery mode")
	}
	if _, statErr := os.Stat(Path(home, "s1")); statErr == nil {
		t.Fatal("Scaffold wrote brief.md despite the refusal")
	}
}

func mustScoutBrief(t *testing.T, id, repo, mode string, yolo bool, scope string, budget int64, gen taskauthority.Generation) string {
	t.Helper()
	tmpl, err := scoutBriefTemplate(id, repo, mode, yolo, scope, budget, gen)
	if err != nil {
		t.Fatalf("scoutBriefTemplate(%q): %v", mode, err)
	}
	return tmpl
}

func mustShipBrief(t *testing.T, id, repo, mode string, yolo bool) string {
	t.Helper()
	tmpl, err := shipBriefTemplate(id, repo, mode, yolo)
	if err != nil {
		t.Fatalf("shipBriefTemplate(%q): %v", mode, err)
	}
	return tmpl
}
