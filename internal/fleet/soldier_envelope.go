package fleet

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
	Role         string `json:"role"`       // "soldier", "captain", "general", or empty
	Applicable   bool   `json:"applicable"` // true when soldier-applicable
	SourcePath   string `json:"source_path,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Version      string `json:"version,omitempty"`
}

// SoldierSkillDenied lists skill names that are explicitly forbidden in
// Soldier context regardless of their Role metadata. These are skills
// that require Captain or General authority to operate safely.
// This is an authoritative denylist — not a role-based filter.
var SoldierSkillDenied = map[string]bool{
	"captain-provisioning":   true,
	"munsu-ops":              true,
	"stuck-soldier-recovery": true,
	"no-mistakes":            true,
	"bootstrap-diagnostics":  true,
	"harness-adapters":       true, // spawn authority

	// Delivery and supervision skills.
	"merge-authority":    true,
	"teardown-authority": true,
	"supervision":        true,
	"watcher":            true,
	"converge":           true,
}

// SkillAuthorityClass returns the effective authority class for a skill:
// "soldier", "captain", or "general". It first checks the denylist, then
// falls back to the Role field. Skills matching denylist entries are always
// classified "captain" or "general" as appropriate.
func SkillAuthorityClass(name, role string) string {
	if SoldierSkillDenied[name] {
		return "captain"
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "soldier", "any":
		return "soldier"
	case "captain", "captain-only":
		return "captain"
	case "general", "general-only":
		return "general"
	default:
		return "captain"
	}
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

// VerifyEnvelopeIntegrity validates all durable files against the envelope:
//   - charter must exist and match CharterSHA256
//   - brief must exist and match BriefSHA256
//   - prompt must exist and match PromptSHA256
//   - task meta fields (TaskID, DeliveryMode, ParentCaptainID, ParentHome)
//     must be non-empty and self-consistent
//
// Returns an error on any mismatch or missing file.
func VerifyEnvelopeIntegrity(worktreePath string) error {
	env, err := ReadEnvelope(worktreePath)
	if err != nil {
		return err
	}

	// Verify identity/metadata fields are non-empty.
	if env.TaskID == "" {
		return fmt.Errorf("envelope: task ID is empty")
	}
	if env.DeliveryMode == "" {
		return fmt.Errorf("envelope: delivery mode is empty")
	}
	if env.ParentCaptainID == "" {
		return fmt.Errorf("envelope: parent captain ID is empty")
	}
	if env.ParentHome == "" {
		return fmt.Errorf("envelope: parent home is empty")
	}
	if env.EnvelopeVersion == "" {
		return fmt.Errorf("envelope: version is empty")
	}

	// Verify charter file exists and hash matches.
	charterPath := filepath.Join(worktreePath, CharterName)
	charterData, err := os.ReadFile(charterPath)
	if err != nil {
		return fmt.Errorf("%s: %w", CharterName, err)
	}
	if sha256Content(charterData) != env.CharterSHA256 {
		return fmt.Errorf("%s SHA-256 mismatch: envelope says %s, actual file differs", CharterName, env.CharterSHA256)
	}

	// Verify brief file exists and hash matches.
	briefPath := filepath.Join(worktreePath, BriefName)
	briefData, err := os.ReadFile(briefPath)
	if err != nil {
		return fmt.Errorf("%s: %w", BriefName, err)
	}
	if env.BriefSHA256 == "" {
		return fmt.Errorf("envelope: brief SHA-256 is empty")
	}
	if sha256Content(briefData) != env.BriefSHA256 {
		return fmt.Errorf("%s SHA-256 mismatch: envelope says %s, actual file differs", BriefName, env.BriefSHA256)
	}

	// Verify prompt file exists and hash matches.
	promptPath := filepath.Join(worktreePath, PromptName)
	promptData, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("%s: %w", PromptName, err)
	}
	if env.PromptSHA256 == "" {
		return fmt.Errorf("envelope: prompt SHA-256 is empty")
	}
	if sha256Content(promptData) != env.PromptSHA256 {
		return fmt.Errorf("%s SHA-256 mismatch: envelope says %s, actual file differs", PromptName, env.PromptSHA256)
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
// Uses SkillAuthorityClass for classification: skills in the denylist
// (SoldierSkillDenied) or with Captain/General-only roles are excluded.
// Non-applicable required skills are still returned (Applicable=false) with
// a diagnostic so callers can surface the rejection.
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
		authClass := SkillAuthorityClass(entry.Name, entry.Role)
		if authClass != "soldier" {
			diags = append(diags, fmt.Sprintf("required skill %q has authority class %q (denied for soldier)", name, authClass))
			required = append(required, SkillEntry{Name: name, Applicable: false, Role: entry.Role})
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
		authClass := SkillAuthorityClass(entry.Name, entry.Role)
		if authClass != "soldier" {
			diags = append(diags, fmt.Sprintf("optional skill %q has authority class %q (denied for soldier, omitted)", name, authClass))
			continue
		}
		entry.Applicable = true
		optional = append(optional, entry)
	}

	return required, optional, diags
}
