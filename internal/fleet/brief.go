// Package brief scaffolds task brief templates for soldier agents.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// ScaffoldOptions controls brief generation.
type ScaffoldOptions struct {
	HomeDir                string // munsu home directory
	ID                     string // task ID
	Repo                   string // project/repo name
	Scout                  bool   // generate scout brief instead of ship brief
	Review                 bool   // generate the read-only reviewer brief; ReviewTask and ReviewHead are required
	Mode                   string // delivery mode (no-mistakes / direct-PR / local-only)
	Yolo                   bool   // yolo mode
	ScoutScope             string
	ScoutRuntimeBudgetSecs int64
	ReviewTask             string // the ship task a reviewer reads
	ReviewHead             string // the one head the reviewer judges
	TamperCheck            string // the project's tamper command, named in the review brief when set
	// Generation is the task generation the brief launches. A scout brief
	// binds the report contract to it: the soldier writes report-g<N>.md for
	// exactly the generation being launched. It must be positive for scouts.
	Generation taskauthority.Generation
}

// Scaffold writes a brief.md at $MUNSU_HOME/data/<id>/brief.md and refreshes
// the directory timestamp so the retention grace period starts at the latest
// brief write or cleanup-ownership release.
// Scaffold writes only the local brief artifact. Callers that need handoff
// recovery must complete it before entering the task-data fence.
func Scaffold(opts ScaffoldOptions) error {
	if opts.Scout {
		if err := opts.Generation.Validate(); err != nil {
			return fmt.Errorf("scout brief for %s requires the task generation: %w", opts.ID, err)
		}
	}
	// Ensure data/<id> directory exists
	dir := filepath.Join(opts.HomeDir, "data", opts.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating brief directory: %w", err)
	}

	content, err := buildBrief(opts)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	now := time.Now()
	if err := os.Chtimes(dir, now, now); err != nil {
		return fmt.Errorf("refreshing brief directory: %w", err)
	}
	return nil
}

// buildBrief assembles the brief markdown template.
func buildBrief(opts ScaffoldOptions) (string, error) {
	id := opts.ID
	repo := opts.Repo

	var b strings.Builder

	if opts.Review {
		tmpl, err := reviewBriefTemplate(id, repo, opts.ReviewTask, opts.ReviewHead, opts.TamperCheck)
		if err != nil {
			return "", err
		}
		b.WriteString(tmpl)
		return b.String(), nil
	}
	if opts.Scout {
		tmpl, err := scoutBriefTemplate(id, repo, opts.Mode, opts.Yolo, opts.ScoutScope, opts.ScoutRuntimeBudgetSecs, opts.Generation)
		if err != nil {
			return "", err
		}
		b.WriteString(tmpl)
		return b.String(), nil
	}
	tmpl, err := shipBriefTemplate(id, repo, opts.Mode, opts.Yolo)
	if err != nil {
		return "", err
	}
	b.WriteString(tmpl)

	return b.String(), nil
}

// shipBriefTemplate returns the ship-mode brief template. The delivery rules
// are selected by the exact delivery mode: an empty or unknown mode is a
// resolution failure, not a licence to render no-mistakes rules, so it fails
// loud instead of silently briefing the soldier for the wrong contract.
func shipBriefTemplate(id, repo, mode string, yolo bool) (string, error) {
	modeLine := fmt.Sprintf("Delivery mode: %s", mode)
	if yolo {
		modeLine += " +yolo"
	}

	setupStep := ""
	deliveryRules := ""
	switch mode {
	case "direct-PR":
		deliveryRules = `## Delivery
	Commit the completed change. Get its full SHA with ` + "`git rev-parse HEAD`" + `, run ` + "`munsu report needs-decision \"push <40-hex-SHA>\"`" + ` with that exact value, and stop. Push the task branch and open a PR only after the General confirms the exact-head Human grant is recorded. Do not use ` + "`-u`" + ` or ` + "`--set-upstream`" + `; they write Git config, which is not yours to change.
	Never run no-mistakes for this task. Never merge the PR.
`
	case "local-only":
		deliveryRules = `## Delivery
	Commit locally and stop for orchestrator merge.
	Do not push, open a PR, run no-mistakes, or merge the change yourself.
`
	case "no-mistakes":
		setupStep = "2. Run `no-mistakes doctor`.\n"
		deliveryRules = `## Delivery
	You drive no-mistakes by responding to its gates, not by implementing fixes.
	Follow ` + "`no-mistakes axi run --help`" + ` and the help lines in each AXI response.
	Do not hand-edit findings while a run is active; the pipeline applies fixes.
	Escalate ask-user findings through the task status protocol and answer gates with ` + "`no-mistakes axi respond`" + `; avoid ` + "`--yes`" + `.
	Commit the completed change. Get its full SHA with ` + "`git rev-parse HEAD`" + `, run ` + "`munsu report needs-decision \"push <40-hex-SHA>\"`" + ` with that exact value, and stop. After the General confirms the exact-head Human grant is recorded, start the no-mistakes run by pushing ` + "`git push no-mistakes HEAD:refs/heads/mu/" + id + "`" + `; after the pipeline reports CI green, append ` + "`done: PR {url} checks green`" + ` and stop.
`
	default:
		return "", fmt.Errorf("ship brief for %s: unknown delivery mode %q", id, mode)
	}

	return fmt.Sprintf(`# Task brief: %s

## Setup
You are in a disposable git worktree of %s, at a detached HEAD on a clean default branch.

**Verify isolation before anything else.** Run `+"`"+`pwd -P`+"`"+` and `+"`"+`git rev-parse --show-toplevel`+"`"+`; both must resolve to the disposable task worktree you were launched in, such as a treehouse pool path or an Orca-managed worktree, not the primary checkout munsu operates from.
The path check is authoritative: `+"`"+`git rev-parse --git-dir`+"`"+` and `+"`"+`git rev-parse --git-common-dir`+"`"+` can help inspect the repo, but they do not prove you are outside the primary checkout.
If the top-level path is the primary checkout or not the worktree you were launched in, STOP - do not branch or commit here - append `+"`"+`blocked: launched in primary checkout, not an isolated worktree`+"`"+` to the status file and stop.

1. First action: create your branch: `+"`"+`git checkout -b mu/%s`+"`"+`
%s
%s

## Rules
1. Never push to the default branch. Never merge a PR.
2. Stay inside this worktree; modify nothing outside it.
3. Use gh-axi for GitHub operations and chrome-devtools-axi for browser operations.
4. Report status by appending one line:
   `+"`"+`munsu report <state> "<msg>" [--key <slug>]`+"`"+`
   Each report signals munsu, so report sparingly: only phase changes a supervisor
   would act on and the needs-decision/blocked/paused/done/failed states.
5. If you hit the same obstacle twice, run `+"`"+`munsu report blocked "{why}"`+"`"+` and stop; munsu will help.
6. If a decision belongs to a human, run `+"`"+`munsu report needs-decision "{summary of options}"`+"`"+` and stop.
7. To close an open wake key, append `+"`"+`resolved [key=<slug>]: {summary}`+"`"+`. Repeating the same resolved key is safe.
8. Never stop, restart, or update the shared `+"`"+`no-mistakes`+"`"+` daemon - it is one instance serving every lane/home, so restarting it kills other lanes' in-flight pipeline runs. On ANY no-mistakes daemon error, append `+"`"+`blocked: {the daemon error}`+"`"+` and stop; only munsu manages the daemon.

## Project memory
If `+"`"+`AGENTS.md`+"`"+` or `+"`"+`CLAUDE.md`+"`"+` already exists, or if this task produced durable project-intrinsic knowledge, run `+"`"+`munsu ensure-agents-md .`+"`"+`.
Record only project knowledge useful to almost every future session.

## Test-impact map
The dispatcher fills this before spawn: for each path the task will change, the existing tests that exercise it, found by code search. Start from them.
{TEST_IMPACT}

## Definition of done
The task is complete only when committed on your branch.
Put the done evidence in the message body of a commit on your branch; the Reviewer reads it with `+"`"+`git log`+"`"+`:
- For each test you added or changed for a behaviour change, a red-proof row: the command you ran on the pre-change code with only that test applied (run it before you change production code), its non-zero exit code and the failure line; or `+"`"+`n/a: <reason>`+"`"+` when the change adds no behaviour, such as a pure refactor.
- For each new function, type or module, a reuse-search row: the code search you ran before writing it (`+"`"+`semble`+"`"+` or `+"`"+`zg`+"`"+`), the query and the hits, and why no hit served.
When delivery is complete, run `+"`"+`munsu report done "{summary}"`+"`"+` and stop.
Before that, close every open keyed decision with `+"`"+`resolved [key=<slug>]: {summary}`+"`"+`.
`, id, repo, id, setupStep, modeLine+"\n"+deliveryRules), nil
}

// scoutBriefTemplate returns the scout-mode brief template. Scaffold
// validates gen before calling this; the generation-bound report name in the
// contract is the soldier's write instruction.
//
// The delivery mode is validated on this path exactly as on the ship path: a
// scout never delivers, but its brief still names the contract the task is
// launched under, and an empty or unrecognized mode is a resolution failure
// upstream rather than a line to omit.
func scoutBriefTemplate(id, repo, mode string, yolo bool, scope string, budget int64, gen taskauthority.Generation) (string, error) {
	if !ValidDeliveryModes[mode] {
		return "", fmt.Errorf("scout brief for %s: unknown delivery mode %q", id, mode)
	}
	modeLine := fmt.Sprintf("Delivery mode: %s", mode)
	if yolo {
		modeLine += " +yolo"
	}
	modeLine += "\n"

	return fmt.Sprintf(`# Scout brief: %s

## Contract
Scope: %s
Maximum runtime (seconds): %d

## Setup
You are in a disposable git worktree of %s, at a detached HEAD on a clean default branch.

**This is a SCOUT task.** You do NOT branch, commit, push, or PR.
Your job is to explore, investigate, and report findings.

## Report contract
Write your findings to `+"`"+`$MUNSU_HOME/data/%s/%s`+"`"+`.
The report is a structured markdown document covering what was investigated,
what was found, and any recommendations.
The file name is generation-bound: this generation's report is the ONLY report
that answers for this generation; never read or reuse another generation's.

%s## Rules
1. Never create branches, commits, pushes, or PRs on scout tasks.
2. Stay inside this worktree; modify nothing outside it.
3. Report status via `+"`"+`munsu report`+"`"+`:
   `+"`"+`munsu report <state> "<msg>" [--key <slug>]`+"`"+`
4. To close an open wake key, append `+"`"+`resolved [key=<slug>]: {summary}`+"`"+`. Repeating the same resolved key is safe.
5. When done, run `+"`"+`munsu report done "{summary of findings location}"`+"`"+` and stop.
6. Do not modify project files - only the report.
`, id, scope, budget, repo, id, ReportName(gen), modeLine), nil
}

// reviewBriefTemplate returns the reviewer brief. The contract names the one
// task and head the reviewer judges; the review method carries lesson group 10
// (reviewer-output-verification): every verdict cites the runs behind it.
func reviewBriefTemplate(id, repo, reviewTask, reviewHead, tamperCheck string) (string, error) {
	if strings.TrimSpace(reviewTask) == "" || strings.TrimSpace(reviewHead) == "" {
		return "", fmt.Errorf("review brief for %s requires the reviewed task and head", id)
	}
	tamperSentence := ""
	if tamperCheck != "" {
		tamperSentence = " Run the project's tamper check `" + tamperCheck + "` from this checkout, with `<base>` replaced by the task's base. Exit 0 is clean; any other exit fails the head unless the task's brief names that rule and path and quotes the Human words that selected it."
	}
	return fmt.Sprintf(`# Review brief: %s

## Contract
Reviewed task: %s
Reviewed head: %s

## Setup
You are in the worktree of the reviewed task in %s. It is not yours: you read it and never write it.

Verify the head before anything else. Run `+"`"+`git rev-parse HEAD`+"`"+`; it must equal the reviewed head above.
If it differs, STOP and say so: the work moved and this review no longer speaks for it.

## Review method
1. Read the task's brief and the diff from its base to the reviewed head. Open every file you will cite.
2. Run every check the task's brief and the repository's contract require, yourself, in this checkout.
3. For each check, record the run: the exact command, its exit code, and the output you read. A summary such as "tests pass" is not a record.
4. For each brief, diff or document section you rely on, open it, read it, and name it.
5. A check that did not run is a failed check, never a pass: a missing tool, a command not found, 0 tests collected, every test skipped, or a skip the task's brief does not name each fails it, and every evidence row pastes how many tests or files the check ran and skipped.
6. Tamper: when the diff touches a verification or harness file (a test, fixture, golden, CI or lint config, or acceptance script), that changed file is itself reviewed. Read its diff for a hardcoded expected output, a weakened or removed assertion, a narrowed test selection, a new skip, or a silenced check (`+"`"+`|| true`+"`"+`, a lint disable). Each one fails the head unless the task's brief asked for it. Re-running a check the head itself changed reproduces the tamper and is not evidence on its own.%s
7. Red proof: the head's commit messages (`+"`"+`git log <base>..<head>`+"`"+`) carry a red-proof row for each test the head adds or changes for a behaviour change. Re-derive one row: make a temp copy with `+"`"+`mktemp -d`+"`"+` outside the checkout, extract the base into it (`+"`"+`git archive <base> | tar -x -C "$d"`+"`"+`), apply only the added or changed test files from the head (`+"`"+`git archive <head> -- <test files> | tar -x -C "$d"`+"`"+`), run that row's command once there, then delete the copy. Never run a mutant. A test with no row, or a row that does not reproduce, fails the head.
8. Kept tests: judge each added or changed test against these rules.
%s
A breach fails the head when it leaves the brief's stated behaviour unproven; otherwise do not raise it.
9. Production behaviour: when the head changes it, grep the production diff for each input and expected value the head's tests use, and check one input the tests do not use, its expected value argued from the brief or run once as a targeted check. A production branch keyed to a test literal fails the head. Run no input sweep, mutant run or whole-suite run for this.
10. Lean code: list each of these the task's brief did not call for: new files; abstractions with one caller; config or flags no caller varies; handling for states the types or callers make impossible; old paths kept beside new ones; a new helper that duplicates an existing one (search for it); comments that restate the code. Each one fails the head.
11. Reuse: the head's commit messages carry a reuse-search row for each new function, type or module. Rerun one row's search and confirm its hits.

## Rules
1. Read only. Edit, create, delete or move nothing (the temp copy of step 7 excepted); create no branch or commit; never push or merge.
2. Judge only the reviewed head. Do not review later work.
3. Do not run `+"`"+`munsu`+"`"+` commands.

## Verdict
Write the verdict file `+"`"+`$MUNSU_VERDICT_FILE`+"`"+` (one JSON object with the fields %s) for the reviewed head, with `+"`"+`outcome`+"`"+` set to `+"`"+`pass`+"`"+` or `+"`"+`fail`+"`"+` and the evidence from the review method in `+"`"+`evidence`+"`"+`. Write it to a sibling named `+"`"+`$MUNSU_VERDICT_FILE.tmp.<pid>.<hex>`+"`"+` (a number, then lowercase hex digits), rename that over the verdict file, then stop.
A PASS needs every required check run and cited.
`, id, reviewTask, reviewHead, repo, tamperSentence, keptTestRules, verdictFileShape("`")), nil
}

// requiredSections returns the "## " headings Scaffold writes for a brief of
// this kind, derived from the rendered template so no second list can drift.
// The delivery heading is mode-dependent and indented in the template, so it
// is not part of the set.
func requiredSections(kind string) ([]string, error) {
	content, err := buildBrief(ScaffoldOptions{ID: "x", Repo: "x", Scout: kind == taskauthority.KindScout, Review: kind == taskauthority.KindReview, Mode: "local-only", ScoutScope: "x", ReviewTask: "x", ReviewHead: "x", Generation: 1})
	if err != nil {
		return nil, err
	}
	var sections []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			sections = append(sections, strings.TrimRight(line, " \t\r"))
		}
	}
	return sections, nil
}

// LintBrief refuses a brief that lacks a section Scaffold writes. Sections the
// scaffold does not produce are never required.
func LintBrief(homeDir, id, kind string) error {
	want, err := requiredSections(kind)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(Path(homeDir, id))
	if err != nil {
		return fmt.Errorf("reading brief for task %s: %w", id, err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		have[strings.TrimRight(line, " \t\r")] = true
	}
	var missing []string
	for _, h := range want {
		if !have[h] {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("brief for task %s is missing scaffolded sections: %s; re-scaffold it with 'munsu brief' before spawning", id, strings.Join(missing, ", "))
	}
	return nil
}

// Path returns the expected brief.md path for the given task ID.
func Path(homeDir, id string) string {
	return filepath.Join(homeDir, "data", id, "brief.md")
}

// Exists checks whether a brief.md exists for the given task ID.
func Exists(homeDir, id string) bool {
	_, err := os.Stat(Path(homeDir, id))
	return err == nil
}

// reportNamePrefix and reportNameSuffix bracket the on-disk name of every
// scout report. The name is generation-bound AT CREATION: generation N writes
// report-g<N>.md and only that file answers for generation N, so no later
// generation can inherit earlier evidence and no archival step exists.
const (
	reportNamePrefix = "report-g"
	reportNameSuffix = ".md"
)

// ReportName is the on-disk name of generation gen's report.
func ReportName(gen taskauthority.Generation) string {
	return reportNamePrefix + gen.String() + reportNameSuffix
}

// ReportPath returns the expected report path for a scout task's generation.
func ReportPath(homeDir, id string, gen taskauthority.Generation) string {
	return filepath.Join(homeDir, "data", id, ReportName(gen))
}

// ReportExists checks whether the generation's report exists for the given
// task ID.
func ReportExists(homeDir, id string, gen taskauthority.Generation) bool {
	_, err := os.Stat(ReportPath(homeDir, id, gen))
	return err == nil
}

// isReportName reports whether name is a generation-scoped report name:
// report-g<canonical decimal>.md. Any other name is not report evidence.
func isReportName(name string) bool {
	if !strings.HasPrefix(name, reportNamePrefix) || !strings.HasSuffix(name, reportNameSuffix) {
		return false
	}
	num := strings.TrimSuffix(strings.TrimPrefix(name, reportNamePrefix), reportNameSuffix)
	parsed, err := strconv.ParseUint(num, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == num
}

// HasReportEvidence reports whether a task data directory holds a report
// worth keeping: any generation's generation-named report. It is the one
// owner of that question — the session-start sweep asks it before collecting
// a directory. A directory it cannot read is reported as holding evidence,
// so an unreadable directory is never reclaimed on the strength of a guess.
func HasReportEvidence(dataDir string) bool {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if isReportName(e.Name()) {
			return true
		}
	}
	return false
}
