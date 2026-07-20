package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShipBriefTemplateNoMistakes(t *testing.T) {
	tmpl := shipBriefTemplate("test-task-1", "munsu", "no-mistakes", false)

	checks := []string{
		"Task brief: test-task-1",
		"git checkout -b fm/",
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
	tmpl := shipBriefTemplate("test-task-1", "munsu", "direct-PR", false)

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
	tmpl := shipBriefTemplate("test-task-1", "munsu", "local-only", false)

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
	tmpl := scoutBriefTemplate("test-scout-1", "munsu", "security", false)

	checks := []string{
		"Scout brief: test-scout-1",
		"SCOUT task",
		"report.md",
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

func TestScaffoldShip(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "test-ship",
		Repo:    "munsu",
		Scout:   false,
		Mode:    "feat",
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

	if !strings.Contains(content, "Delivery mode: feat") {
		t.Errorf("brief should contain delivery mode")
	}
	if strings.Contains(content, "+yolo") {
		t.Errorf("brief should not contain +yolo when false")
	}
}

func TestScaffoldScout(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "test-scout",
		Repo:    "munsu",
		Scout:   true,
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
	if !strings.Contains(content, "report.md") {
		t.Errorf("scout brief should mention report.md")
	}
}

func TestScaffoldWithYolo(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "test-yolo",
		Repo:    "munsu",
		Scout:   false,
		Mode:    "fix",
		Yolo:    true,
	}

	if err := Scaffold(opts); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(Path(tmp, "test-yolo"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "Delivery mode: fix") {
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
	})

	if !Exists(tmp, "test-exists") {
		t.Error("Exists should be true after scaffold")
	}
}

func TestReportPath(t *testing.T) {
	p := ReportPath("/home/user/.munsu", "scout-1")
	want := filepath.Join("/home/user/.munsu", "data", "scout-1", "report.md")
	if p != want {
		t.Errorf("ReportPath = %q, want %q", p, want)
	}
}

func TestReportExists(t *testing.T) {
	tmp := t.TempDir()

	if ReportExists(tmp, "scout-nope") {
		t.Error("ReportExists should be false before creation")
	}

	dir := filepath.Join(tmp, "data", "scout-yes")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "report.md"), []byte("findings"), 0644)

	if !ReportExists(tmp, "scout-yes") {
		t.Error("ReportExists should be true after creation")
	}
}

func TestScaffoldCreatesDir(t *testing.T) {
	tmp := t.TempDir()

	opts := ScaffoldOptions{
		HomeDir: tmp,
		ID:      "dir-test",
		Repo:    "munsu",
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
	tmpl := shipBriefTemplate("t1", "repo", "", false)
	if !strings.Contains(tmpl, "munsu report") {
		t.Error("ship brief should reference munsu report command")
	}
	if strings.Contains(tmpl, "munsu task status") {
		t.Error("ship brief should not reference munsu task status command")
	}
}

func TestScoutBriefHasReportContract(t *testing.T) {
	tmpl := scoutBriefTemplate("scout-r", "repo", "", false)

	if !strings.Contains(tmpl, "Write your findings") {
		t.Error("scout brief should include report instructions")
	}
	if !strings.Contains(tmpl, "report.md") {
		t.Errorf("scout brief should mention report.md path")
	}
}

func TestShipBriefModeLine(t *testing.T) {
	tmpl := shipBriefTemplate("t1", "repo", "", false)
	if strings.Contains(tmpl, "Delivery mode:") {
		t.Error("should not emit delivery mode line when mode is empty")
	}
}

func TestShipBriefModeLinePresent(t *testing.T) {
	tmpl := shipBriefTemplate("t1", "repo", "refactor", false)
	if !strings.Contains(tmpl, "Delivery mode: refactor") {
		t.Error("should emit delivery mode line when mode is set")
	}
}

func TestBriefsDocumentIdempotentResolvedKey(t *testing.T) {
	for name, tmpl := range map[string]string{
		"ship":  shipBriefTemplate("t1", "repo", "", false),
		"scout": scoutBriefTemplate("s1", "repo", "", false),
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
