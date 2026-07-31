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
	FleetBaseSchemaVersion       = "munsu.config.base/v1"
	CaptainRegistrySchemaVersion = "munsu.config.captains/v1"
	ProjectRegistrySchemaVersion = "munsu.config.projects/v1"

	BaseDocumentPath    = "config/base.json"
	CaptainDocumentPath = "data/captains.json"
	ProjectDocumentPath = "data/projects.json"
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

type CaptainRecord struct {
	ID             string         `json:"id"`
	Home           string         `json:"home"`
	Project        string         `json:"project"`
	CaptainProfile CaptainProfile `json:"captainProfile,omitempty"`
}

type CaptainRegistryDocument struct {
	SchemaVersion string          `json:"schemaVersion"`
	Captains      []CaptainRecord `json:"captains"`
}

type ProjectRecord struct {
	Name   string         `json:"name"`
	Path   string         `json:"path"`
	Mode   string         `json:"mode,omitempty"`
	Config ProjectOverlay `json:"config,omitempty"`
}

type ProjectRegistryDocument struct {
	SchemaVersion string          `json:"schemaVersion"`
	Projects      []ProjectRecord `json:"projects"`
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

func (d CaptainRegistryDocument) Validate() error {
	if err := validateSchema("Captain registry", d.SchemaVersion, CaptainRegistrySchemaVersion); err != nil {
		return err
	}
	ids := make(map[string]struct{}, len(d.Captains))
	for _, captain := range d.Captains {
		if captain.ID == "" {
			return fmt.Errorf("Captain id is required")
		}
		if captain.Home == "" {
			return fmt.Errorf("Captain %q home is required", captain.ID)
		}
		if captain.Project == "" {
			return fmt.Errorf("Captain %q must own exactly one project", captain.ID)
		}
		if _, exists := ids[captain.ID]; exists {
			return fmt.Errorf("duplicate Captain %q", captain.ID)
		}
		ids[captain.ID] = struct{}{}
	}
	return nil
}

func (d ProjectRegistryDocument) Validate() error {
	if err := validateSchema("project registry", d.SchemaVersion, ProjectRegistrySchemaVersion); err != nil {
		return err
	}
	names := make(map[string]struct{}, len(d.Projects))
	for _, project := range d.Projects {
		if project.Name == "" {
			return fmt.Errorf("project name is required")
		}
		if project.Path == "" {
			return fmt.Errorf("project %q path is required", project.Name)
		}
		if _, exists := names[project.Name]; exists {
			return fmt.Errorf("duplicate project %q", project.Name)
		}
		names[project.Name] = struct{}{}
	}
	return nil
}

func validateSchema(name, got, want string) error {
	if got != want {
		return fmt.Errorf("%s schemaVersion %q is unsupported; expected %q", name, got, want)
	}
	return nil
}

func ValidateFleetBindings(captains CaptainRegistryDocument, projects ProjectRegistryDocument) error {
	if err := captains.Validate(); err != nil {
		return err
	}
	if err := projects.Validate(); err != nil {
		return err
	}
	known := make(map[string]struct{}, len(projects.Projects))
	for _, project := range projects.Projects {
		known[project.Name] = struct{}{}
	}
	owners := make(map[string]string)
	for _, captain := range captains.Captains {
		if _, exists := known[captain.Project]; !exists {
			return fmt.Errorf("Captain %q references unknown project %q", captain.ID, captain.Project)
		}
		if owner, exists := owners[captain.Project]; exists {
			return fmt.Errorf("project %q is already owned by Captain %q", captain.Project, owner)
		}
		owners[captain.Project] = captain.ID
	}
	return nil
}

func ResolveProject(base FleetBaseDocument, captains CaptainRegistryDocument, projects ProjectRegistryDocument, projectName string, overrides BoundaryOverrides) (ResolvedProjectConfig, error) {
	if err := base.Validate(); err != nil {
		return ResolvedProjectConfig{}, err
	}
	if err := ValidateFleetBindings(captains, projects); err != nil {
		return ResolvedProjectConfig{}, err
	}
	var project *ProjectRecord
	for i := range projects.Projects {
		if projects.Projects[i].Name == projectName {
			project = &projects.Projects[i]
			break
		}
	}
	if project == nil {
		return ResolvedProjectConfig{}, fmt.Errorf("unknown project %q", projectName)
	}

	effective := resolvedOverlay(base.Config, *project)
	digest, err := ProjectDigest(base, *project)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	applyBoundaryOverrides(&effective, overrides)

	captainProfile := base.CaptainProfile
	for _, captain := range captains.Captains {
		if captain.Project == projectName {
			applyCaptainProfile(&captainProfile, captain.CaptainProfile)
			break
		}
	}
	require := effective.RequireNoMistakes != nil && *effective.RequireNoMistakes
	return ResolvedProjectConfig{
		Project: project.Name, ProjectPath: project.Path,
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

// ProjectDigest returns the deterministic persisted digest for base plus one project overlay.
func ProjectDigest(base FleetBaseDocument, project ProjectRecord) (string, error) {
	if err := base.Validate(); err != nil {
		return "", err
	}
	if project.Name == "" {
		return "", fmt.Errorf("project name is required")
	}
	if project.Path == "" {
		return "", fmt.Errorf("project %q path is required", project.Name)
	}
	config := resolvedOverlay(base.Config, project)
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal resolved project config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func resolvedOverlay(base ProjectOverlay, project ProjectRecord) ProjectOverlay {
	effective := cloneOverlay(base)
	applyOverlay(&effective, project.Config)
	if project.Mode != "" && project.Config.DefaultMode == "" {
		effective.DefaultMode = project.Mode
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

func LoadCaptainRegistry(home string) (CaptainRegistryDocument, error) {
	var document CaptainRegistryDocument
	if err := loadDocument(filepath.Join(home, CaptainDocumentPath), &document); err != nil {
		return document, err
	}
	return document, document.Validate()
}

func LoadProjectRegistry(home string) (ProjectRegistryDocument, error) {
	var document ProjectRegistryDocument
	if err := loadDocument(filepath.Join(home, ProjectDocumentPath), &document); err != nil {
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

func StoreCaptainRegistry(home string, document CaptainRegistryDocument) error {
	if err := document.Validate(); err != nil {
		return err
	}
	return storeDocument(filepath.Join(home, CaptainDocumentPath), document)
}

func StoreProjectRegistry(home string, document ProjectRegistryDocument) error {
	if err := document.Validate(); err != nil {
		return err
	}
	return storeDocument(filepath.Join(home, ProjectDocumentPath), document)
}

func LoadDocuments(home string) (FleetBaseDocument, CaptainRegistryDocument, ProjectRegistryDocument, error) {
	base, err := LoadFleetBase(home)
	if err != nil {
		return FleetBaseDocument{}, CaptainRegistryDocument{}, ProjectRegistryDocument{}, err
	}
	captains, err := LoadCaptainRegistry(home)
	if err != nil {
		return base, CaptainRegistryDocument{}, ProjectRegistryDocument{}, err
	}
	projects, err := LoadProjectRegistry(home)
	if err != nil {
		return base, captains, ProjectRegistryDocument{}, err
	}
	if err := ValidateFleetBindings(captains, projects); err != nil {
		return base, captains, projects, err
	}
	return base, captains, projects, nil
}

func StoreDocuments(home string, base FleetBaseDocument, captains CaptainRegistryDocument, projects ProjectRegistryDocument) error {
	if err := base.Validate(); err != nil {
		return err
	}
	if err := ValidateFleetBindings(captains, projects); err != nil {
		return err
	}
	if err := StoreFleetBase(home, base); err != nil {
		return err
	}
	if err := StoreCaptainRegistry(home, captains); err != nil {
		return err
	}
	if err := StoreProjectRegistry(home, projects); err != nil {
		return err
	}
	return nil
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
