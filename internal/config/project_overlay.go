package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const (
	ProjectOverlaySchemaVersion = "munsu.config.project-overlays/v1"
	// ProjectOverlayDocumentPath is the Config-owned document holding
	// per-Project overlay/profile values keyed by scoped Project name. Config
	// owns these overlay values but owns no registry: Project existence,
	// binding, and lifecycle live in the Fleet registry.
	ProjectOverlayDocumentPath = "config/project-overlays.json"
)

// ProjectOverlayDocument maps scoped Project names to their Config-owned
// overlay values.
type ProjectOverlayDocument struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Overlays      map[string]ProjectOverlay `json:"overlays,omitempty"`
}

func (d ProjectOverlayDocument) Validate() error {
	return validateSchema("project overlays", d.SchemaVersion, ProjectOverlaySchemaVersion)
}

// LoadProjectOverlays returns the Config-owned overlay values keyed by Project
// name. A missing document is an empty overlay map, not an error: Config owns
// no Project registry and a Project with no overlay is valid.
func LoadProjectOverlays(home string) (map[string]ProjectOverlay, error) {
	var document ProjectOverlayDocument
	if err := loadDocument(filepath.Join(home, ProjectOverlayDocumentPath), &document); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]ProjectOverlay{}, nil
		}
		return nil, err
	}
	if err := document.Validate(); err != nil {
		return nil, err
	}
	if document.Overlays == nil {
		return map[string]ProjectOverlay{}, nil
	}
	return document.Overlays, nil
}

// StoreProjectOverlay upserts the Config-owned overlay value for one scoped
// Project name. It never creates, retires, rebinds, or otherwise mutates
// Project lifecycle — that authority is the Fleet registry's.
func StoreProjectOverlay(home, project string, overlay ProjectOverlay) error {
	overlays, err := LoadProjectOverlays(home)
	if err != nil {
		return err
	}
	overlays[project] = cloneOverlay(overlay)
	document := ProjectOverlayDocument{
		SchemaVersion: ProjectOverlaySchemaVersion,
		Overlays:      overlays,
	}
	return storeDocument(filepath.Join(home, ProjectOverlayDocumentPath), document)
}

// RemoveProjectOverlay deletes the Config-owned overlay value for one scoped
// Project name. A missing key is a no-op.
func RemoveProjectOverlay(home, project string) error {
	overlays, err := LoadProjectOverlays(home)
	if err != nil {
		return err
	}
	if _, exists := overlays[project]; !exists {
		return nil
	}
	delete(overlays, project)
	document := ProjectOverlayDocument{
		SchemaVersion: ProjectOverlaySchemaVersion,
		Overlays:      overlays,
	}
	return storeDocument(filepath.Join(home, ProjectOverlayDocumentPath), document)
}

// MarshalJSON renders the overlay map with deterministic key order so digest
// and schema validation are stable across runs.
func (d ProjectOverlayDocument) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(d.Overlays))
	for k := range d.Overlays {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type alias ProjectOverlayDocument
	out := struct {
		SchemaVersion string                    `json:"schemaVersion"`
		Overlays      map[string]ProjectOverlay `json:"overlays,omitempty"`
	}{SchemaVersion: d.SchemaVersion, Overlays: make(map[string]ProjectOverlay, len(keys))}
	for _, k := range keys {
		out.Overlays[k] = d.Overlays[k]
	}
	return json.Marshal(out)
}

// ProjectOverlayAvailable reports whether a project overlay document exists.
func ProjectOverlayAvailable(home string) bool {
	_, err := os.Stat(filepath.Join(home, ProjectOverlayDocumentPath))
	return err == nil || !os.IsNotExist(err)
}

// LoadProjectOverlay returns the Config-owned overlay value for one Project,
// or the zero overlay when the Project has no overlay registered.
func LoadProjectOverlay(home, project string) (ProjectOverlay, error) {
	overlays, err := LoadProjectOverlays(home)
	if err != nil {
		return ProjectOverlay{}, err
	}
	return cloneOverlay(overlays[project]), nil
}
