package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillEntry records one selected skill in the launch envelope.
//
// A skill is a CLI tool the Soldier invokes natively (gh-axi, qmd,
// chrome-devtools-axi); munsu neither owns nor ships its text. The envelope is
// therefore a manifest of names, not a carrier of skill content.
type SkillEntry struct {
	Name       string `json:"name"`
	Role       string `json:"role"`       // "soldier", "captain", "general", or empty
	Applicable bool   `json:"applicable"` // true when soldier-applicable
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
	EnvelopeVersion        string            `json:"envelope_version"`
	TaskID                 string            `json:"task_id"`
	TaskKind               string            `json:"task_kind"`
	DeliveryMode           string            `json:"delivery_mode"`
	Repository             string            `json:"repository"`
	ParentCaptainID        string            `json:"parent_captain_id"`
	ParentHome             string            `json:"parent_home"`
	ScoutScope             string            `json:"scout_scope,omitempty"`
	ScoutRuntimeBudgetSecs int64             `json:"scout_runtime_budget_secs,omitempty"`
	RequiredSkills         []SkillEntry      `json:"required_skills,omitempty"`
	OptionalSkills         []SkillEntry      `json:"optional_skills,omitempty"`
	Metadata               map[string]string `json:"metadata,omitempty"`
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

// CollectSkills filters skills by role applicability (soldier/any) and builds
// the required/optional lists.
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
