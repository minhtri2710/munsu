package soldier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillEntry records one selected skill in the launch envelope.
type SkillEntry struct {
	Name         string `json:"name"`
	Role         string `json:"role"`         // "soldier" or "general" or "captain"
	Applicable   bool   `json:"applicable"`   // true when soldier-applicable
	SourcePath   string `json:"source_path,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Version      string `json:"version,omitempty"`
}

// LaunchEnvelope is the structured, versioned launch context for a Soldier.
// Written to .soldier-envelope.json in the worktree root.
type LaunchEnvelope struct {
	EnvelopeVersion string            `json:"envelope_version"`
	TaskID          string            `json:"task_id"`
	TaskKind        string            `json:"task_kind"`
	DeliveryMode    string            `json:"delivery_mode"`
	Repository      string            `json:"repository"`
	ParentCaptainID string            `json:"parent_captain_id"`
	ParentHome      string            `json:"parent_home"`
	CharterSHA256   string            `json:"charter_sha256"`
	BriefSHA256     string            `json:"brief_sha256"`
	PromptSHA256    string            `json:"prompt_sha256"`
	RequiredSkills  []SkillEntry      `json:"required_skills,omitempty"`
	OptionalSkills  []SkillEntry      `json:"optional_skills,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// WriteEnvelope writes the launch envelope to .soldier-envelope.json.
func WriteEnvelope(worktreePath string, env *LaunchEnvelope) error {
	if env == nil {
		return fmt.Errorf("launch envelope is nil")
	}
	env.EnvelopeVersion = EnvelopeVersion
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling launch envelope: %w", err)
	}
	data = append(data, '\n')
	envPath := filepath.Join(worktreePath, EnvelopeName)
	if err := os.WriteFile(envPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", EnvelopeName, err)
	}
	return nil
}

// ReadEnvelope reads and parses the launch envelope from the worktree.
func ReadEnvelope(worktreePath string) (*LaunchEnvelope, error) {
	envPath := filepath.Join(worktreePath, EnvelopeName)
	data, err := os.ReadFile(envPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", EnvelopeName, err)
	}
	var env LaunchEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", EnvelopeName, err)
	}
	return &env, nil
}

// VerifyEnvelopeIntegrity checks that charter and brief SHA-256 hashes match
// the actual file contents. Returns an error on mismatch or missing files.
func VerifyEnvelopeIntegrity(worktreePath string) error {
	env, err := ReadEnvelope(worktreePath)
	if err != nil {
		return err
	}

	// Verify charter hash.
	charterData, err := os.ReadFile(filepath.Join(worktreePath, CharterName))
	if err != nil {
		return fmt.Errorf("%s: %w", CharterName, err)
	}
	if sha256Content(charterData) != env.CharterSHA256 {
		return fmt.Errorf("%s SHA-256 mismatch: envelope says %s, actual file differs", CharterName, env.CharterSHA256)
	}

	// Verify brief hash.
	briefPath := filepath.Join(worktreePath, BriefName)
	if _, err := os.Stat(briefPath); err == nil {
		briefData, err := os.ReadFile(briefPath)
		if err != nil {
			return fmt.Errorf("%s: %w", BriefName, err)
		}
		if env.BriefSHA256 != "" && sha256Content(briefData) != env.BriefSHA256 {
			return fmt.Errorf("%s SHA-256 mismatch: envelope says %s, actual file differs", BriefName, env.BriefSHA256)
		}
	}

	return nil
}

// VerifyRequiredSkills checks that all required skill entries have non-empty
// canonical source paths and matching SHA-256 hashes. Returns a structured
// error listing every missing or mismatched skill. Optional omissions produce
// durable diagnostics without hard failure.
func VerifyRequiredSkills(env *LaunchEnvelope, baseDir string) ([]string, error) {
	var failures []string
	for _, s := range env.RequiredSkills {
		if !s.Applicable {
			continue
		}
		if s.SourcePath == "" {
			failures = append(failures, fmt.Sprintf("required skill %q has no source path", s.Name))
			continue
		}
		absPath := s.SourcePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(baseDir, absPath)
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("required skill %q at %s: %v", s.Name, absPath, err))
			continue
		}
		if s.SourceSHA256 != "" && sha256Content(data) != s.SourceSHA256 {
			failures = append(failures, fmt.Sprintf("required skill %q at %s: SHA-256 mismatch", s.Name, absPath))
		}
	}
	if len(failures) > 0 {
		return failures, fmt.Errorf("required skill verification failed: %s", strings.Join(failures, "; "))
	}
	var diags []string
	for _, s := range env.OptionalSkills {
		if !s.Applicable || s.SourcePath == "" {
			continue
		}
		absPath := s.SourcePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(baseDir, absPath)
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			diags = append(diags, fmt.Sprintf("optional skill %q at %s not found (omitted)", s.Name, absPath))
		} else if s.SourceSHA256 != "" {
			data, err := os.ReadFile(absPath)
			if err == nil && sha256Content(data) != s.SourceSHA256 {
				diags = append(diags, fmt.Sprintf("optional skill %q at %s: SHA-256 mismatch (omitted)", s.Name, absPath))
			}
		}
	}
	if len(failures) > 0 {
		return diags, fmt.Errorf("required skill verification failed: %s", strings.Join(failures, "; "))
	}
	return diags, nil
}

// CollectSkills filters skills by role applicability (soldier/any) and builds
// the required/optional lists with integrity metadata.
// Skills with captain or general-only roles are excluded.
func CollectSkills(allSkills []SkillEntry, requiredNames, optionalNames []string) (required, optional []SkillEntry, diags []string) {
	// Build lookup from the skill catalog.
	catalog := make(map[string]SkillEntry)
	for _, s := range allSkills {
		catalog[s.Name] = s
	}

	seen := make(map[string]bool)

	for _, name := range requiredNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		entry, ok := catalog[name]
		if !ok {
			diags = append(diags, fmt.Sprintf("required skill %q not found in catalog", name))
			required = append(required, SkillEntry{Name: name, Applicable: false})
			continue
		}
		if !isSoldierApplicable(entry.Role) {
			diags = append(diags, fmt.Sprintf("required skill %q has role %q (not soldier-applicable)", name, entry.Role))
			required = append(required, SkillEntry{Name: name, Applicable: false})
			continue
		}
		entry.Applicable = true
		required = append(required, entry)
	}

	for _, name := range optionalNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		entry, ok := catalog[name]
		if !ok {
			diags = append(diags, fmt.Sprintf("optional skill %q not found in catalog (omitted)", name))
			continue
		}
		if !isSoldierApplicable(entry.Role) {
			diags = append(diags, fmt.Sprintf("optional skill %q has role %q (not soldier-applicable, omitted)", name, entry.Role))
			continue
		}
		entry.Applicable = true
		optional = append(optional, entry)
	}

	return required, optional, diags
}

// isSoldierApplicable returns true when the role permits soldier inclusion.
// Empty role means any rank. "soldier" is always applicable.
// "captain" and "general" are excluded.
func isSoldierApplicable(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "soldier", "any":
		return true
	case "captain", "general", "captain-only", "general-only":
		return false
	default:
		return false
	}
}
