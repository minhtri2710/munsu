// Package brief scaffolds task brief templates for soldier agents.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScaffoldOptions controls brief generation.
type ScaffoldOptions struct {
	HomeDir string // munsu home directory
	ID      string // task ID
	Repo    string // project/repo name
	Scout   bool   // generate scout brief instead of ship brief
	Mode    string // delivery mode (feat, fix, refactor, etc.)
	Yolo    bool   // yolo mode
}

// Scaffold writes a brief.md at $MUNSU_HOME/data/<id>/brief.md.
func Scaffold(opts ScaffoldOptions) error {
	// Ensure data/<id> directory exists
	dir := filepath.Join(opts.HomeDir, "data", opts.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating brief directory: %w", err)
	}

	content := buildBrief(opts)
	path := filepath.Join(dir, "brief.md")
	return os.WriteFile(path, []byte(content), 0644)
}

// buildBrief assembles the brief markdown template.
func buildBrief(opts ScaffoldOptions) string {
	id := opts.ID
	repo := opts.Repo

	var b strings.Builder

	if opts.Scout {
		b.WriteString(scoutBriefTemplate(id, repo, opts.Mode, opts.Yolo))
	} else {
		b.WriteString(shipBriefTemplate(id, repo, opts.Mode, opts.Yolo))
	}

	return b.String()
}

// shipBriefTemplate returns the ship-mode brief template.
func shipBriefTemplate(id, repo, mode string, yolo bool) string {
	modeLine := ""
	if mode != "" {
		modeLine = fmt.Sprintf("Delivery mode: %s", mode)
	}
	if yolo {
		modeLine += " +yolo"
	}

	setupStep := ""
	deliveryRules := ""
	switch mode {
	case "direct-PR":
		deliveryRules = `## Delivery
	Commit the completed change, push the feature branch, and open a PR directly against the default branch.
	Never run no-mistakes for this task. Never merge the PR.
`
	case "local-only":
		deliveryRules = `## Delivery
	Commit locally and stop for orchestrator merge.
	Do not push, open a PR, run no-mistakes, or merge the change yourself.
`
	default:
		setupStep = "2. Run `no-mistakes doctor`; if it reports the repo is not initialized here, run `no-mistakes init`.\n"
		deliveryRules = `## Delivery
	You drive no-mistakes by responding to its gates, not by implementing fixes.
	Follow ` + "`no-mistakes axi run --help`" + ` and the help lines in each AXI response.
	Do not hand-edit findings while a run is active; the pipeline applies fixes.
	Escalate ask-user findings through the task status protocol and answer gates with ` + "`no-mistakes axi respond`" + `; avoid ` + "`--yes`" + `.
	After no-mistakes reports CI green, append ` + "`done: PR {url} checks green`" + ` and stop.
`
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

## Definition of done
The task is complete only when committed on your branch.
When delivery is complete, run `+"`"+`munsu report done "{summary}"`+"`"+` and stop.
Before that, close every open keyed decision with `+"`"+`resolved [key=<slug>]: {summary}`+"`"+`.
`, id, repo, id, setupStep, modeLine+"\n"+deliveryRules)
}

// scoutBriefTemplate returns the scout-mode brief template.
func scoutBriefTemplate(id, repo, mode string, yolo bool) string {
	modeLine := ""
	if mode != "" {
		modeLine = fmt.Sprintf("Delivery mode: %s", mode)
		if yolo {
			modeLine += " +yolo"
		}
		modeLine += "\n"
	}

	return fmt.Sprintf(`# Scout brief: %s

## Setup
You are in a disposable git worktree of %s, at a detached HEAD on a clean default branch.

**This is a SCOUT task.** You do NOT branch, commit, push, or PR.
Your job is to explore, investigate, and report findings.

## Report contract
Write your findings to `+"`"+`$MUNSU_HOME/data/%s/report.md`+"`"+`.
The report is a structured markdown document covering what was investigated,
what was found, and any recommendations.

%s## Rules
1. Never create branches, commits, pushes, or PRs on scout tasks.
2. Stay inside this worktree; modify nothing outside it.
3. Report status via `+"`"+`munsu report`+"`"+`:
   `+"`"+`munsu report <state> "<msg>" [--key <slug>]`+"`"+`
4. To close an open wake key, append `+"`"+`resolved [key=<slug>]: {summary}`+"`"+`. Repeating the same resolved key is safe.
5. When done, run `+"`"+`munsu report done "{summary of findings location}"`+"`"+` and stop.
6. Do not modify project files - only the report.
`, id, repo, id, modeLine)
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

// ReportPath returns the expected report.md path for a scout task.
func ReportPath(homeDir, id string) string {
	return filepath.Join(homeDir, "data", id, "report.md")
}

// ReportExists checks whether a report.md exists for the given task ID.
func ReportExists(homeDir, id string) bool {
	_, err := os.Stat(ReportPath(homeDir, id))
	return err == nil
}
