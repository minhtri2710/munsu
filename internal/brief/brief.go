// Package brief scaffolds task brief templates for crewmate agents.
package brief

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
		if yolo {
			modeLine += " +yolo"
		}
		modeLine += "\n"
	}

	return fmt.Sprintf(`# Task brief: %s

## Setup
You are in a disposable git worktree of %s, at a detached HEAD on a clean default branch.

**Verify isolation before anything else.** Run `+"`"+`pwd -P`+"`"+` and `+"`"+`git rev-parse --show-toplevel`+"`"+`; both must resolve to the disposable task worktree you were launched in, such as a treehouse pool path or an Orca-managed worktree, not the primary checkout firstmate operates from.
The path check is authoritative: `+"`"+`git rev-parse --git-dir`+"`"+` and `+"`"+`git rev-parse --git-common-dir`+"`"+` can help inspect the repo, but they do not prove you are outside the primary checkout.
If the top-level path is the primary checkout or not the worktree you were launched in, STOP - do not branch or commit here - append `+"`"+`blocked: launched in primary checkout, not an isolated worktree`+"`"+` to the status file and stop.

1. First action: create your branch: `+"`"+`git checkout -b fm/%s`+"`"+`
2. Run `+"`"+`no-mistakes doctor`+"`"+`; if it reports the repo is not initialized here, run `+"`"+`no-mistakes init`+"`"+`.

%s## Rules
1. Never push to the default branch. Never merge a PR.
2. Stay inside this worktree; modify nothing outside it.
3. Use gh-axi for GitHub operations and chrome-devtools-axi for browser operations.
4. Report status by appending one line:
   `+"`"+`munsu task status %s {state} "message"
   Each append wakes firstmate, so report sparingly: only phase changes a supervisor
   would act on (setup done, bug reproduced, fix implemented, validation passed) and the
   needs-decision/blocked/paused/done/failed states. No step-by-step FYI progress lines;
   firstmate reads your pane for that.
   Use `+"`"+`paused: {why}`+"`"+` - distinct from `+"`"+`blocked:`+"`"+` - ONLY when you are deliberately idling on a
   known external wait you expect to clear on its own (an upstream release, a rate-limit reset,
   a scheduled window): firstmate then leaves your idle pane alone and rechecks it on a long
   cadence instead of treating it as a possible wedge. Use `+"`"+`blocked:`+"`"+` when you are stuck and need help.
5. If you hit the same obstacle twice, append `+"`"+`blocked: {why}`+"`"+` and stop; firstmate will help.
6. If a decision belongs to a human (product choices, destructive actions, ask-user findings),
   append `+"`"+`needs-decision: {summary of options}`+"`"+` and stop. Firstmate will reply with the decision.
   When firstmate replies or a blocker clears and you resume, append `+"`"+`resolved: {how it was decided or unblocked}`+"`"+` (add the same `+"`"+`[key=<slug>]`+"`"+` if you opened it with one) so the decision or blocker is durably closed and does not keep resurfacing.
7. Never stop, restart, or update the shared `+"`"+`no-mistakes`+"`"+` daemon - it is one instance serving
   every lane/home, so restarting it kills other lanes' in-flight pipeline runs. On ANY no-mistakes
   daemon error, append `+"`"+`blocked: {the daemon error}`+"`"+` and stop; only firstmate manages the daemon.

## Project memory
If `+"`"+`AGENTS.md`+"`"+` or `+"`"+`CLAUDE.md`+"`"+` already exists, or if this task produced durable project-intrinsic knowledge, run the ensure-agents-md script provided by firstmate.
Record only project knowledge useful to almost every future session.
For anything the codebase already shows, prefer a pointer to the authoritative file, command, or doc over copying the detail.
If you touch a project `+"`"+`AGENTS.md`+"`"+` that lacks `+"`"+`## Maintaining this file`+"`"+`, add that short self-governance section in the same pass.
Keep it proportionate: skip `+"`"+`AGENTS.md`+"`"+` edits for trivial tasks that produced no durable project knowledge.

## Definition of done
The task is complete only when committed on your branch.
When you believe it is complete, append `+"`"+`done: {summary}`+"`"+` to the status file and stop.
Firstmate will then instruct you to run /no-mistakes to validate and ship a PR.

You drive no-mistakes by responding to its gates, not by implementing fixes.
Follow the guidance no-mistakes itself provides for the mechanics: it loads when you invoke /no-mistakes, and `+"`"+`no-mistakes axi run --help`+"`"+` plus the `+"`"+`help`+"`"+` lines in each `+"`"+`axi`+"`"+` response are authoritative and version-matched to the installed binary.
Do not hand-edit, commit, or fix findings yourself while a run is active - the pipeline applies every fix.

Two firstmate-specific rules layer on top of that guidance:
- ask-user findings are not yours to answer: escalate to firstmate (rule 6) and stop.
  When the decision comes back, feed it to the gate with `+"`"+`no-mistakes axi respond`+"`"+` and let the pipeline apply it - do not route the question to "the user" or implement the fix yourself.
- Avoid `+"`"+`--yes`+"`"+`: the captain, not you, owns the ask-user decisions it would silently auto-resolve.

After /no-mistakes reports CI green (the CI-ready return point - do not wait for it to keep monitoring in the background until merge), append `+"`"+`done: PR {url} checks green`+"`"+` and stop. You are finished.
`, id, repo, id, modeLine, id)
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
3. Report status by appending one line:
   `+"`"+`munsu task status %s {state} "message"
4. When done, append `+"`"+`done: {summary of findings location}`+"`"+` and stop.
5. Do not modify project files - only the report.
`, id, repo, id, modeLine, id)
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
