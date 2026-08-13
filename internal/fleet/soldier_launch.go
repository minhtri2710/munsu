package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
)

// PromptName is the name of the prompt file persisted to the worktree.
const PromptName = ".soldier-prompt.md"

// LaunchPromptInput is the canonical input for building a Soldier launch prompt.
// All fields must be populated before BuildLaunchPrompt is called.
type LaunchPromptInput struct {
	TaskID                 string
	TaskKind               string // "ship" or "scout"
	DeliveryMode           string
	Repository             string // repo name for brief
	ParentCaptainID        string
	ParentHome             string
	WorktreePath           string // absolute path to the disposable worktree
	HomeDir                string // munsu home (parent)
	BriefContent           []byte // complete task brief content
	RequiredSkills         []SkillEntry
	OptionalSkills         []SkillEntry
	HarnessName            string
	ScoutScope             string
	ScoutRuntimeBudgetSecs int64
}

// BuildLaunchPrompt constructs the complete, role-specific Soldier launch prompt.
// It includes the canonical charter, task brief contents (not just a path),
// task identity, allowed/forbidden actions, terminal report requirements,
// and skill instructions.
//
// The prompt is designed to be passed directly as a launch argument to the
// harness (analogous to captain.buildLaunchArgs), so the Soldier starts
// with charter + task content already in context.
func BuildLaunchPrompt(input LaunchPromptInput) (string, *LaunchEnvelope, error) {
	if input.TaskID == "" {
		return "", nil, fmt.Errorf("soldier launch: task ID is required")
	}
	if input.WorktreePath == "" {
		return "", nil, fmt.Errorf("soldier launch: worktree path is required")
	}
	if input.HomeDir == "" {
		return "", nil, fmt.Errorf("soldier launch: home dir is required")
	}
	if len(input.BriefContent) == 0 {
		return "", nil, fmt.Errorf("soldier launch: brief content is required")
	}
	if input.TaskKind == "" {
		input.TaskKind = "ship"
	}
	if input.TaskKind == "scout" && (strings.TrimSpace(input.ScoutScope) == "" || input.ScoutRuntimeBudgetSecs <= 0) {
		return "", nil, fmt.Errorf("soldier launch: scout scope and positive runtime budget are required")
	}
	if input.TaskKind != "scout" && (strings.TrimSpace(input.ScoutScope) != "" || input.ScoutRuntimeBudgetSecs != 0) {
		return "", nil, fmt.Errorf("soldier launch: scout contract is only valid for scout tasks")
	}
	if input.DeliveryMode == "" {
		input.DeliveryMode = "direct-PR"
	}

	// 1. Build charter.
	charter := DefaultCharter(input.TaskID, input.TaskKind, input.DeliveryMode)
	charterSHA := sha256Content([]byte(charter))

	// 2. Build skill instructions section (strict: read/hash errors propagate).
	skillSection, err := buildSkillInstructions(input.RequiredSkills, input.OptionalSkills, input.WorktreePath)
	if err != nil {
		return "", nil, fmt.Errorf("soldier launch: skill instruction build failed: %w", err)
	}

	// 3. Build prompt: charter + brief content + skill instructions + terminal command.
	var b strings.Builder
	b.WriteString(charter)
	b.WriteString("\n\n")
	b.WriteString(string(input.BriefContent))
	b.WriteString("\n\n")
	if skillSection != "" {
		b.WriteString(skillSection)
		b.WriteString("\n\n")
	}
	b.WriteString(terminalReportReminder(input.TaskID, input.TaskKind, input.ParentCaptainID))

	prompt := b.String()
	promptSHA := sha256Content([]byte(prompt))

	// 4. Build envelope.
	briefSHA := sha256Content(input.BriefContent)
	env := &LaunchEnvelope{
		EnvelopeVersion:        EnvelopeVersion,
		TaskID:                 input.TaskID,
		TaskKind:               input.TaskKind,
		DeliveryMode:           input.DeliveryMode,
		Repository:             input.Repository,
		ParentCaptainID:        input.ParentCaptainID,
		ParentHome:             input.ParentHome,
		ScoutScope:             input.ScoutScope,
		ScoutRuntimeBudgetSecs: input.ScoutRuntimeBudgetSecs,
		CharterSHA256:          charterSHA,
		BriefSHA256:            briefSHA,
		PromptSHA256:           promptSHA,
		RequiredSkills:         input.RequiredSkills,
		OptionalSkills:         input.OptionalSkills,
		Metadata: map[string]string{
			"harness": input.HarnessName,
			"home":    input.HomeDir,
		},
	}

	return prompt, env, nil
}

// buildSkillInstructions returns a markdown section with skill content for
// applicable required skills (inline instructions) and a note about optional skills.
// An error is returned when a required skill's source file cannot be read or its
// SHA-256 does not match the declared hash. Optional read errors produce durable
// diagnostics without blocking prompt construction.
func buildSkillInstructions(required, optional []SkillEntry, baseDir string) (string, error) {
	var b strings.Builder

	// Required skills: inline canonical content. Read/hash verification is strict.
	var hasRequired bool
	for _, s := range required {
		if !s.Applicable {
			continue
		}
		if !hasRequired {
			b.WriteString("## Required Skills\n\n")
			hasRequired = true
		}
		b.WriteString(fmt.Sprintf("### %s\n\n", s.Name))
		if s.Version != "" {
			b.WriteString(fmt.Sprintf("Version: %s\n\n", s.Version))
		}
		if s.SourceSHA256 != "" {
			b.WriteString(fmt.Sprintf("Integrity: %s\n\n", s.SourceSHA256))
		}

		// Inline skill content from source. Read errors and hash mismatches are
		// hard failures for required skills.
		if s.SourcePath != "" {
			content, err := os.ReadFile(s.SourcePath)
			if err != nil {
				return "", fmt.Errorf("reading required skill %q from %s: %w", s.Name, s.SourcePath, err)
			}
			if s.SourceSHA256 != "" && sha256Content(content) != s.SourceSHA256 {
				return "", fmt.Errorf("required skill %q at %s: SHA-256 mismatch (declared %s)", s.Name, s.SourcePath, s.SourceSHA256)
			}
			if len(content) > 0 {
				b.WriteString(string(content))
				b.WriteString("\n\n")
			}
		}
	}

	// Optional skills: just list them.
	var hasOptional bool
	for _, s := range optional {
		if !s.Applicable {
			continue
		}
		if !hasOptional {
			if hasRequired {
				b.WriteString("---\n")
			}
			b.WriteString("## Optional Skills\n\n")
			hasOptional = true
		}
		b.WriteString(fmt.Sprintf("- %s", s.Name))
		if s.Version != "" {
			b.WriteString(fmt.Sprintf(" (version: %s)", s.Version))
		}
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String()), nil
}

// terminalReportReminder returns the exact terminal report command
// that the Soldier must use. The --key is required, not optional.
func terminalReportReminder(taskID, taskKind, parentCaptainID string) string {
	bt := "`"
	doneMessage := "PR {url}"
	doneDescription := "task complete, PR open (no merge)"
	if taskKind == "scout" {
		doneMessage = "summary of findings location"
		doneDescription = "scout report complete"
	}
	return fmt.Sprintf(`## Terminal Report Requirement

When the task is complete, you MUST execute exactly:

%[1]smunsu report done "%[5]s" --key %[2]s%[4]s

This is the authoritative terminal report signal to your parent Captain (%[3]s).
Do not use any other command or mechanism for terminal reporting.

The --key flag is REQUIRED and must match the task ID exactly.

Summary of report states:
- %[4]smunsu report working "..." --key <slug>%[4]s — material phase
- %[4]smunsu report blocked "{why}"%[4]s — after second obstacle encounter
- %[4]smunsu report needs-decision "{summary}"%[4]s — human decision needed
- %[4]smunsu report done "%[5]s" --key %[2]s%[4]s — %[6]s
- %[4]smunsu report failed "{reason}"%[4]s — task cannot be completed
`, bt, taskID, parentCaptainID, bt, doneMessage, doneDescription)
}

// BuildLaunchArgs builds the harness binary name and argument list for a
// Soldier launch — analogous to captain.buildLaunchArgs.
//
// The complete prompt is passed directly as a prompt argument so the Soldier
// starts with charter + task content already in context.
// model and effort may be empty strings; they are only appended when the
// adapter's template defines a corresponding flag.
// The harness must have PromptArg support (from CaptainLaunch contract);
// unsupported harnesses fail closed.
func BuildLaunchArgs(soldierHome, harnessName, model, effort, prompt string) (string, []string, error) {
	adapter, ok := harness.GetAdapter(harnessName)
	if !ok {
		return "", nil, fmt.Errorf("soldier launch: harness %q is not a verified harness", harnessName)
	}
	if !adapter.CaptainLaunch.Supported || !adapter.CaptainLaunch.PromptArg {
		return "", nil, fmt.Errorf("soldier launch: harness %q does not have a verified prompt-arg contract", harnessName)
	}
	tmpl := adapter.LaunchTemplate

	args := []string{}
	if model != "" && tmpl.ModelFlag != "" {
		args = append(args, tmpl.ModelFlag, model)
	} else if tmpl.DefaultModel != "" && tmpl.ModelFlag != "" {
		args = append(args, tmpl.ModelFlag, tmpl.DefaultModel)
	}
	if effort != "" && tmpl.EffortFlag != "" {
		args = append(args, tmpl.EffortFlag, effort)
	} else if tmpl.DefaultEffort != "" && tmpl.EffortFlag != "" {
		args = append(args, tmpl.EffortFlag, tmpl.DefaultEffort)
	}
	args = append(args, tmpl.ExtraArgs...)

	if adapter.CaptainLaunch.Separator != "" {
		args = append(args, adapter.CaptainLaunch.Separator)
	}

	// Pass the complete prompt as the final argument.
	args = append(args, prompt)

	return adapter.Name, args, nil
}

// LaunchArtifact is the deterministic launch artifact of one launch
// submission: the re-entrant script path and the exact command submitted to
// the endpoint, with its sha256 digest. The command digest binds the durable
// launch evidence to the exact submission. GuardName/GuardIdentity expose the
// persistent guard marker the script creates before execing the harness.
type LaunchArtifact struct {
	ScriptPath    string
	Command       string
	CommandDigest string
	GuardName     string
	GuardIdentity string
}

// LaunchArtifactInput carries the immutable launch identity for one artifact.
// Every value is deterministic per launch, so the artifact (and its command
// digest) is identical on every attempt of the same launch.
type LaunchArtifactInput struct {
	WorktreePath   string
	HomeDir        string
	TaskID         string
	SnapshotDigest string
	LaunchBin      string
	LaunchArgs     []string
	LaunchID       string
	Generation     string
	EndpointFence  string
}

// buildLaunchArtifact writes the deterministic .soldier-launch.sh script into
// the worktree and returns the exact submission command with its sha256
// digest. The script embeds the persistent re-entrant launch guard: BEFORE
// invoking/execing the harness it writes a durable guard marker (keyed by
// task+generation, carrying the exact launch identity) and exits/no-ops when
// the marker already carries the same identity, fails closed on a different
// identity/fence, and only then execs the harness. A duplicate submission of
// the same launch can therefore never start a second Soldier process; when
// the guard exists but the process cannot prove readiness, recovery fails
// closed instead of launching another process. An existing script whose
// content differs fails closed (identity mismatch, never overwritten).
func buildLaunchArtifact(in LaunchArtifactInput) (LaunchArtifact, error) {
	if in.LaunchBin == "" || len(in.LaunchArgs) == 0 {
		return LaunchArtifact{}, fmt.Errorf("soldier launch: no prompt-arg launch command; harness does not support prompt-arg delivery")
	}
	if in.LaunchID == "" || in.Generation == "" || in.EndpointFence == "" {
		return LaunchArtifact{}, fmt.Errorf("soldier launch: re-entrant launch guard requires the exact launch identity (launch id, generation, fence)")
	}
	guardName := fmt.Sprintf(".soldier-launch-guard-%s-%s", labelComponent(in.TaskID), in.Generation)
	guardIdentity := in.LaunchID + "|" + in.Generation + "|" + in.EndpointFence

	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd ")
	b.WriteString(spawnShQuote(in.WorktreePath))
	b.WriteString("\n")
	b.WriteString("export MUNSU_HOME=")
	b.WriteString(spawnShQuote(in.HomeDir))
	b.WriteString("\n")
	b.WriteString("export MUNSU_ROLE=soldier\n")
	b.WriteString("export MUNSU_TASK_ID=")
	b.WriteString(spawnShQuote(in.TaskID))
	b.WriteString("\n")
	b.WriteString("export MUNSU_PARENT_STATUS=")
	b.WriteString(spawnShQuote(in.HomeDir))
	b.WriteString("\n")
	if in.SnapshotDigest != "" {
		b.WriteString("export MUNSU_CONFIG_SNAPSHOT_DIGEST=")
		b.WriteString(spawnShQuote(in.SnapshotDigest))
		b.WriteString("\n")
	}
	// Persistent re-entrant launch guard: created by the launched script
	// BEFORE invoking the harness. The guard directory is created atomically
	// (mkdir succeeds for exactly one submission), so even concurrent
	// submissions cannot both exec the harness: the loser exits. Same launch
	// identity re-entry exits (the original harness remains the pane process);
	// a different identity/fence fails closed; a guard with no provable
	// readiness is never re-launched.
	b.WriteString("guard=")
	b.WriteString(spawnShQuote(guardName))
	b.WriteString("\n")
	b.WriteString("identity=")
	b.WriteString(spawnShQuote(guardIdentity))
	b.WriteString("\n")
	b.WriteString("if ! mkdir \"$guard\" 2>/dev/null; then\n")
	b.WriteString("  existing=\"$(cat \"$guard/identity\" 2>/dev/null || true)\"\n")
	b.WriteString("  if [ \"$existing\" != \"$identity\" ]; then\n")
	b.WriteString("    echo \"launch guard identity mismatch: existing '$existing' want '$identity'\" >&2\n")
	b.WriteString("    exit 1\n")
	b.WriteString("  fi\n")
	b.WriteString("  echo \"launch $identity already guarded; no second process\" >&2\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n")
	b.WriteString("printf '%s' \"$identity\" > \"$guard/identity\"\n")
	b.WriteString("exec ")
	b.WriteString(spawnShQuote(in.LaunchBin))
	for _, arg := range in.LaunchArgs {
		b.WriteString(" ")
		b.WriteString(spawnShQuote(arg))
	}
	b.WriteString("\n")
	content := b.String()

	scriptPath := filepath.Join(in.WorktreePath, LaunchScriptName)
	if existing, err := os.ReadFile(scriptPath); err == nil {
		if string(existing) != content {
			return LaunchArtifact{}, fmt.Errorf("launch artifact %s already exists with different content; identity mismatch, refuse to overwrite", scriptPath)
		}
	} else if !os.IsNotExist(err) {
		return LaunchArtifact{}, fmt.Errorf("reading existing launch artifact: %w", err)
	}
	if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
		return LaunchArtifact{}, fmt.Errorf("writing launch script: %w", err)
	}
	command := "bash " + spawnShQuote(scriptPath)
	return LaunchArtifact{ScriptPath: scriptPath, Command: command, CommandDigest: sha256Content([]byte(command)), GuardName: guardName, GuardIdentity: guardIdentity}, nil
}

// PersistLaunchFiles writes all durable launch files to the worktree:
// .soldier-charter.md, .soldier-brief.md, .soldier-envelope.json, and .soldier-prompt.md.
// Returns an error if any write fails.
func PersistLaunchFiles(worktreePath string, charter string, briefContent []byte, env *LaunchEnvelope, promptText string) error {
	// Write charter.
	if err := writeCharter(worktreePath, charter); err != nil {
		return err
	}

	// Write brief.
	briefPath := filepath.Join(worktreePath, BriefName)
	if err := os.WriteFile(briefPath, briefContent, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", BriefName, err)
	}

	// Write prompt.
	promptPath := filepath.Join(worktreePath, PromptName)
	if err := os.WriteFile(promptPath, []byte(promptText), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", PromptName, err)
	}

	// Write envelope.
	if err := WriteEnvelope(worktreePath, env); err != nil {
		return err
	}

	return nil
}

// FailClosedDuringLaunch returns true when a pre-session-allocation check
// fails — charter, brief, envelope, task meta, parent identity, delivery
// mode, or hashes are absent/mismatched/unreadable.
func FailClosedDuringLaunch(input LaunchPromptInput) error {
	var failures []string

	if input.TaskID == "" {
		failures = append(failures, "task ID is empty")
	}
	if input.ParentHome == "" {
		failures = append(failures, "parent home is empty")
	}
	if input.ParentCaptainID == "" {
		failures = append(failures, "parent captain ID is empty")
	}
	if input.WorktreePath == "" {
		failures = append(failures, "worktree path is empty")
	}
	if len(input.BriefContent) == 0 {
		failures = append(failures, "brief content is empty")
	}
	if input.DeliveryMode == "" {
		failures = append(failures, "delivery mode is empty")
	}

	// Verify worktree path exists.
	if input.WorktreePath != "" {
		if fi, err := os.Stat(input.WorktreePath); err != nil || !fi.IsDir() {
			failures = append(failures, fmt.Sprintf("worktree path %s: not a directory or inaccessible", input.WorktreePath))
		}
	}

	// Verify required skills are present (when source paths are set).
	for _, s := range input.RequiredSkills {
		if !s.Applicable || s.SourcePath == "" {
			continue
		}
		if _, err := os.Stat(s.SourcePath); err != nil {
			failures = append(failures, fmt.Sprintf("required skill %q source %s: %v", s.Name, s.SourcePath, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("soldier launch fail-closed: %s", strings.Join(failures, "; "))
	}
	return nil
}
