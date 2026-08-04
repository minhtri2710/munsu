package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	FleetBaseSchemaVersion = "munsu.config.base/v1"

	BaseDocumentPath = "config/base.json"
)

type DispatchCandidate struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

type DispatchProfile struct {
	Name           string              `json:"name,omitempty"`
	Match          []string            `json:"match,omitempty"`
	When           string              `json:"when,omitempty"`
	Harness        string              `json:"harness,omitempty"`
	Model          string              `json:"model,omitempty"`
	Effort         string              `json:"effort,omitempty"`
	MaxConcurrent  int                 `json:"maxConcurrent,omitempty"`
	SelectStrategy string              `json:"select,omitempty"`
	Why            string              `json:"why,omitempty"`
	Use            []DispatchCandidate `json:"use,omitempty"`
}

type CaptainProfile struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

type ProjectOverlay struct {
	SoldierHarness    string            `json:"soldierHarness,omitempty"`
	Model             string            `json:"model,omitempty"`
	DispatchAutonomy  string            `json:"dispatchAutonomy,omitempty"`
	DefaultMode       string            `json:"defaultMode,omitempty"`
	RequireNoMistakes *bool             `json:"requireNoMistakes,omitempty"`
	BacklogBackend    string            `json:"backlogBackend,omitempty"`
	DispatchProfiles  []DispatchProfile `json:"dispatchProfiles,omitempty"`
}

type FleetBaseDocument struct {
	SchemaVersion  string         `json:"schemaVersion"`
	Config         ProjectOverlay `json:"config"`
	CaptainProfile CaptainProfile `json:"captainProfile,omitempty"`
}

// ProjectFacts is the narrow, Fleet-owned scoped facts Config accepts to
// resolve one Project's overlay. It carries the Project's identity and the
// owning Captain's profile. Config never reads or stores a Project/Captain
// registry; Fleet supplies these facts at composition time. Overlay/profile
// values keyed by scoped identity are Config-owned but are supplied here so
// Config has no registry persistence authority.
type ProjectFacts struct {
	Name           string
	Path           string
	Mode           string
	Overlay        ProjectOverlay
	CaptainProfile CaptainProfile
}

type BoundaryOverrides struct {
	SoldierHarness    string
	Model             string
	DispatchAutonomy  string
	DefaultMode       string
	RequireNoMistakes *bool
	BacklogBackend    string
	DispatchProfiles  []DispatchProfile
}

type ResolvedProjectConfig struct {
	Project           string            `json:"project"`
	ProjectPath       string            `json:"projectPath"`
	SoldierHarness    string            `json:"soldierHarness,omitempty"`
	DispatchAutonomy  string            `json:"dispatchAutonomy,omitempty"`
	Model             string            `json:"model,omitempty"`
	DefaultMode       string            `json:"defaultMode,omitempty"`
	RequireNoMistakes bool              `json:"requireNoMistakes"`
	BacklogBackend    string            `json:"backlogBackend,omitempty"`
	DispatchProfiles  []DispatchProfile `json:"dispatchProfiles,omitempty"`
	CaptainProfile    CaptainProfile    `json:"captainProfile,omitempty"`
	Digest            string            `json:"digest"`
}

func (d FleetBaseDocument) Validate() error {
	return validateSchema("fleet base", d.SchemaVersion, FleetBaseSchemaVersion)
}

func validateSchema(name, got, want string) error {
	if got != want {
		return fmt.Errorf("%s schemaVersion %q is unsupported; expected %q", name, got, want)
	}
	return nil
}

// ResolveProject resolves one Project's overlay from the Fleet-owned scoped
// facts and the base overlay. Config owns the overlay resolution and the
// deterministic digest; it owns no registry and cannot mutate Project/Captain
// lifecycle.
func ResolveProject(base FleetBaseDocument, facts ProjectFacts, overrides BoundaryOverrides) (ResolvedProjectConfig, error) {
	if err := base.Validate(); err != nil {
		return ResolvedProjectConfig{}, err
	}
	if facts.Name == "" {
		return ResolvedProjectConfig{}, fmt.Errorf("project name is required")
	}
	if facts.Path == "" {
		return ResolvedProjectConfig{}, fmt.Errorf("project %q path is required", facts.Name)
	}

	effective := resolvedOverlay(base.Config, facts.Overlay, facts.Mode)
	digest, err := ProjectDigest(base, facts)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	applyBoundaryOverrides(&effective, overrides)

	captainProfile := base.CaptainProfile
	applyCaptainProfile(&captainProfile, facts.CaptainProfile)

	require := effective.RequireNoMistakes != nil && *effective.RequireNoMistakes
	return ResolvedProjectConfig{
		Project: facts.Name, ProjectPath: facts.Path,
		SoldierHarness: effective.SoldierHarness, Model: effective.Model,
		DispatchAutonomy: effective.DispatchAutonomy,
		DefaultMode:      effective.DefaultMode, RequireNoMistakes: require,
		BacklogBackend:   effective.BacklogBackend,
		DispatchProfiles: cloneProfiles(effective.DispatchProfiles),
		CaptainProfile:   captainProfile, Digest: digest,
	}, nil
}

func applyOverlay(dst *ProjectOverlay, src ProjectOverlay) {
	if src.SoldierHarness != "" {
		dst.SoldierHarness = src.SoldierHarness
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.DispatchAutonomy != "" {
		dst.DispatchAutonomy = src.DispatchAutonomy
	}
	if src.DefaultMode != "" {
		dst.DefaultMode = src.DefaultMode
	}
	if src.RequireNoMistakes != nil {
		value := *src.RequireNoMistakes
		dst.RequireNoMistakes = &value
	}
	if src.BacklogBackend != "" {
		dst.BacklogBackend = src.BacklogBackend
	}
	if len(src.DispatchProfiles) > 0 {
		dst.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	}
}

func applyBoundaryOverrides(dst *ProjectOverlay, src BoundaryOverrides) {
	applyOverlay(dst, ProjectOverlay{SoldierHarness: src.SoldierHarness, Model: src.Model, DispatchAutonomy: src.DispatchAutonomy, DefaultMode: src.DefaultMode, RequireNoMistakes: src.RequireNoMistakes, BacklogBackend: src.BacklogBackend, DispatchProfiles: src.DispatchProfiles})
}

func applyCaptainProfile(dst *CaptainProfile, src CaptainProfile) {
	if src.Harness != "" {
		dst.Harness = src.Harness
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.Effort != "" {
		dst.Effort = src.Effort
	}
}

func cloneOverlay(src ProjectOverlay) ProjectOverlay {
	result := src
	result.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	if src.RequireNoMistakes != nil {
		value := *src.RequireNoMistakes
		result.RequireNoMistakes = &value
	}
	return result
}

func cloneProfiles(src []DispatchProfile) []DispatchProfile {
	if src == nil {
		return nil
	}
	result := make([]DispatchProfile, len(src))
	for i := range src {
		result[i] = src[i]
		result[i].Match = append([]string(nil), src[i].Match...)
		result[i].Use = append([]DispatchCandidate(nil), src[i].Use...)
	}
	return result
}

// ProjectDigest returns the deterministic persisted digest for the base plus
// one Project's overlay facts. It is Config-owned and independent of any
// registry: the digest covers the resolved overlay only.
func ProjectDigest(base FleetBaseDocument, facts ProjectFacts) (string, error) {
	if err := base.Validate(); err != nil {
		return "", err
	}
	if facts.Name == "" {
		return "", fmt.Errorf("project name is required")
	}
	if facts.Path == "" {
		return "", fmt.Errorf("project %q path is required", facts.Name)
	}
	config := resolvedOverlay(base.Config, facts.Overlay, facts.Mode)
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal resolved project config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func resolvedOverlay(base ProjectOverlay, overlay ProjectOverlay, mode string) ProjectOverlay {
	effective := cloneOverlay(base)
	applyOverlay(&effective, overlay)
	if mode != "" && overlay.DefaultMode == "" {
		effective.DefaultMode = mode
	}
	return effective
}

func LoadFleetBase(home string) (FleetBaseDocument, error) {
	var document FleetBaseDocument
	if err := loadDocument(filepath.Join(home, BaseDocumentPath), &document); err != nil {
		return document, err
	}
	return document, document.Validate()
}

func StoreFleetBase(home string, document FleetBaseDocument) error {
	if err := document.Validate(); err != nil {
		return err
	}
	return storeDocument(filepath.Join(home, BaseDocumentPath), document)
}

func loadDocument(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading typed config document %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decoding typed config document %s: %w", path, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("decoding typed config document %s: trailing JSON data", path)
	}
	return nil
}

func storeDocument(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding typed config document %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating typed config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating typed config temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("installing typed config document %s: %w", path, err)
	}
	return nil
}
