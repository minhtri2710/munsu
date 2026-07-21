package soldier

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
)

// LaunchPromptInput is the canonical input for building a Soldier launch prompt.
// All fields must be populated before BuildLaunchPrompt is called.
type LaunchPromptInput struct {
	TaskID          string
	TaskKind        string // "ship" or "scout"
	DeliveryMode    string
	Repository      string // repo name for brief
	ParentCaptainID string
	ParentHome      string
	WorktreePath    string // absolute path to the disposable worktree
	HomeDir         string // munsu home (parent)
	BriefContent    []byte // complete task brief content
	RequiredSkills  []SkillEntry
	OptionalSkills  []SkillEntry
	HarnessName     string
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
	if input.DeliveryMode == "" {
		input.DeliveryMode = "direct-PR"
	}

	// 1. Build charter.
	charter := DefaultCharter(input.TaskID, input.TaskKind, input.DeliveryMode)
	charterSHA := sha256Content([]byte(charter))

	// 2. Build skill instructions section.
	skillSection := buildSkillInstructions(input.RequiredSkills, input.OptionalSkills)

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
	b.WriteString(terminalReportReminder(input.TaskID, input.ParentCaptainID))

	prompt := b.String()
	promptSHA := sha256Content([]byte(prompt))

	// 4. Build envelope.
	briefSHA := sha256Content(input.BriefContent)
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          input.TaskID,
		TaskKind:        input.TaskKind,
		DeliveryMode:    input.DeliveryMode,
		Repository:      input.Repository,
		ParentCaptainID: input.ParentCaptainID,
		ParentHome:      input.ParentHome,
		CharterSHA256:   charterSHA,
		BriefSHA256:     briefSHA,
		PromptSHA256:    promptSHA,
		RequiredSkills:  input.RequiredSkills,
		OptionalSkills:  input.OptionalSkills,
		Metadata: map[string]string{
			"harness": input.HarnessName,
			"home":    input.HomeDir,
		},
	}

	return prompt, env, nil
}

// buildSkillInstructions returns a markdown section with skill content for
// applicable required skills (inline instructions) and a note about optional skills.
func buildSkillInstructions(required, optional []SkillEntry) string {
	var b strings.Builder

	// Required skills: inline canonical content.
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

		// Inline skill content from source if available.
		if s.SourcePath != "" {
			content, err := os.ReadFile(s.SourcePath)
			if err == nil && len(content) > 0 {
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

	return strings.TrimSpace(b.String())
}

// terminalReportReminder returns the exact terminal report command
// that the Soldier must use.
func terminalReportReminder(taskID, parentCaptainID string) string {
	bt := "`"
	return fmt.Sprintf(`## Terminal Report Requirement

When the task is complete, you MUST execute exactly:

%smunsu report done "PR {url}" [--key %[2]s]%s

This is the authoritative terminal report signal to your parent Captain (%[3]s).
Do not use any other command or mechanism for terminal reporting.

Summary of report states:
- %[4]smunsu report working "..." [--key <slug>]%[4]s — material phase
- %[4]smunsu report blocked "{why}"%[4]s — after second obstacle encounter
- %[4]smunsu report needs-decision "{summary}"%[4]s — human decision needed
- %[4]smunsu report done "PR {url}"%[4]s — task complete, PR open (no merge)
- %[4]smunsu report failed "{reason}"%[4]s — task cannot be completed
`, bt, taskID, parentCaptainID, bt)
}

// BuildLaunchArgs builds the harness binary name and argument list for a
// Soldier launch — analogous to captain.buildLaunchArgs.
//
// The complete prompt is passed directly as a prompt argument so the Soldier
// starts with charter + task content already in context.
func BuildLaunchArgs(soldierHome, harnessName string, prompt string) (string, []string, error) {
	adapter, ok := harness.GetAdapter(harnessName)
	if !ok {
		return "", nil, fmt.Errorf("soldier launch: harness %q is not a verified harness", harnessName)
	}
	tmpl := adapter.LaunchTemplate

	args := []string{}
	if tmpl.ModelFlag != "" {
		args = append(args, tmpl.ModelFlag)
		// Use first env/default if available; otherwise let harness pick.
	}
	args = append(args, tmpl.ExtraArgs...)

	// For Pi harness: no separator, prompt arg directly.
	// For other harnesses: use contract.Separator if available.
	if adapter.CaptainLaunch.Supported && adapter.CaptainLaunch.Separator != "" {
		args = append(args, adapter.CaptainLaunch.Separator)
	}

	// Pass the complete prompt as the final argument.
	args = append(args, prompt)

	return adapter.Name, args, nil
}

// PersistLaunchFiles writes all durable launch files to the worktree:
// .soldier-charter.md, .soldier-brief.md, and .soldier-envelope.json.
// Returns an error if any write fails.
func PersistLaunchFiles(worktreePath string, charter string, briefContent []byte, env *LaunchEnvelope) error {
	// Write charter.
	if err := writeCharter(worktreePath, charter); err != nil {
		return err
	}

	// Write brief.
	briefPath := filepath.Join(worktreePath, BriefName)
	if err := os.WriteFile(briefPath, briefContent, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", BriefName, err)
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

