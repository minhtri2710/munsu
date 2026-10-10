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

type ToolEntry struct {
	Adapter string   `json:"adapter"`
	Path    string   `json:"path,omitempty"`
	Args    []string `json:"args,omitempty"`
}

type ProjectOverlay struct {
	SoldierHarness   string            `json:"soldierHarness,omitempty"`
	Model            string            `json:"model,omitempty"`
	DispatchAutonomy string            `json:"dispatchAutonomy,omitempty"`
	Backend          string            `json:"backend,omitempty"`
	TamperCheck      string            `json:"tamperCheck,omitempty"`
	DispatchProfiles []DispatchProfile `json:"dispatchProfiles,omitempty"`
	Review           *ToolEntry        `json:"review,omitempty"`
	Forge            *ToolEntry        `json:"forge,omitempty"`
}

type ResolvedStep struct {
	Baseline   bool     `json:"baseline"`
	Adapter    string   `json:"adapter,omitempty"`
	Path       string   `json:"path,omitempty"`
	Args       []string `json:"args,omitempty"`
	ProbeState string   `json:"probeState"`
	Version    string   `json:"version,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// FleetBaseConfig is the fleet-wide overlay. Tool entries are project-only, so
// it has no Review or Forge field and a fleet base document cannot carry one.
// Keep its fields in step with ProjectOverlay.
type FleetBaseConfig struct {
	SoldierHarness   string            `json:"soldierHarness,omitempty"`
	Model            string            `json:"model,omitempty"`
	DispatchAutonomy string            `json:"dispatchAutonomy,omitempty"`
	Backend          string            `json:"backend,omitempty"`
	TamperCheck      string            `json:"tamperCheck,omitempty"`
	DispatchProfiles []DispatchProfile `json:"dispatchProfiles,omitempty"`
}

type FleetBaseDocument struct {
	SchemaVersion  string          `json:"schemaVersion"`
	Config         FleetBaseConfig `json:"config"`
	CaptainProfile CaptainProfile  `json:"captainProfile,omitempty"`
}

// ProjectFacts is the narrow, Fleet-owned scoped facts Config accepts to
// resolve one Project's overlay. It carries the Project's identity and its
// per-project overlay. Config never reads or stores a Project/Captain
// registry; Fleet supplies these facts at composition time. Overlay values
// keyed by scoped identity are Config-owned but are supplied here so Config
// has no registry persistence authority.
type ProjectFacts struct {
	Name    string
	Path    string
	Overlay ProjectOverlay
}

type ResolvedProjectConfig struct {
	Project          string            `json:"project"`
	ProjectPath      string            `json:"projectPath"`
	SoldierHarness   string            `json:"soldierHarness,omitempty"`
	DispatchAutonomy string            `json:"dispatchAutonomy,omitempty"`
	Model            string            `json:"model,omitempty"`
	Backend          string            `json:"backend,omitempty"`
	TamperCheck      string            `json:"tamperCheck,omitempty"`
	DispatchProfiles []DispatchProfile `json:"dispatchProfiles,omitempty"`
	CaptainProfile   CaptainProfile    `json:"captainProfile,omitempty"`
	ReviewStep       ResolvedStep      `json:"reviewStep"`
	ForgeStep        ResolvedStep      `json:"forgeStep"`
	Digest           string            `json:"digest"`
}

func (d FleetBaseDocument) Validate() error {
	if err := validateSchema("fleet base", d.SchemaVersion, FleetBaseSchemaVersion); err != nil {
		return err
	}
	return nil
}

func validateSchema(name, got, want string) error {
	if got != want {
		return fmt.Errorf("%s schemaVersion %q is unsupported; expected %q", name, got, want)
	}
	return nil
}

func ResolveProjectWithToolProbe(base FleetBaseDocument, facts ProjectFacts, probe func(string, ToolEntry) ResolvedStep) (ResolvedProjectConfig, error) {
	if err := base.Validate(); err != nil {
		return ResolvedProjectConfig{}, err
	}
	effective, err := finalResolvedOverlay(base, facts)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	review, forge, err := ResolveProjectTools(effective, probe)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	return ResolveProjectWithSteps(base, facts, review, forge)
}

func baselineStep() ResolvedStep {
	return ResolvedStep{Baseline: true, ProbeState: "baseline", Reason: "no tool configured"}
}

func ResolveProjectWithSteps(base FleetBaseDocument, facts ProjectFacts, reviewStep, forgeStep ResolvedStep) (ResolvedProjectConfig, error) {
	if err := base.Validate(); err != nil {
		return ResolvedProjectConfig{}, err
	}
	effective, err := finalResolvedOverlay(base, facts)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	if effective.Backend == "" {
		return ResolvedProjectConfig{}, fmt.Errorf("project %q resolved no session backend identity: set backend in the fleet base config or the project overlay", facts.Name)
	}
	if err := ValidateProjectTools(effective); err != nil {
		return ResolvedProjectConfig{}, err
	}
	if err := validateResolvedStep("review", reviewStep); err != nil {
		return ResolvedProjectConfig{}, err
	}
	if err := validateResolvedStep("forge", forgeStep); err != nil {
		return ResolvedProjectConfig{}, err
	}
	if !stepMatchesEntry(reviewStep, effective.Review) || !stepMatchesEntry(forgeStep, effective.Forge) {
		return ResolvedProjectConfig{}, fmt.Errorf("resolved tool steps do not match the project overlay")
	}
	digest, err := projectDigest(effective, reviewStep, forgeStep)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	return ResolvedProjectConfig{
		Project: facts.Name, ProjectPath: facts.Path,
		SoldierHarness: effective.SoldierHarness, Model: effective.Model,
		DispatchAutonomy: effective.DispatchAutonomy,
		Backend:          effective.Backend, TamperCheck: effective.TamperCheck,
		DispatchProfiles: cloneProfiles(effective.DispatchProfiles),
		CaptainProfile:   base.CaptainProfile,
		ReviewStep:       cloneResolvedStep(reviewStep), ForgeStep: cloneResolvedStep(forgeStep),
		Digest: digest,
	}, nil
}

func cloneResolvedStep(step ResolvedStep) ResolvedStep {
	step.Args = append([]string(nil), step.Args...)
	return step
}

func stepMatchesEntry(step ResolvedStep, entry *ToolEntry) bool {
	if entry == nil {
		return step.Baseline && step.ProbeState == "baseline"
	}
	if step.Baseline || step.Adapter != entry.Adapter || len(step.Args) != len(entry.Args) || (entry.Path != "" && step.Path != entry.Path) || step.Path == "" || !filepath.IsAbs(step.Path) {
		return false
	}
	for i := range step.Args {
		if step.Args[i] != entry.Args[i] {
			return false
		}
	}
	return true
}

func validateResolvedStep(name string, step ResolvedStep) error {
	if step.Baseline {
		if step.Adapter != "" || step.Path != "" || len(step.Args) != 0 || step.ProbeState != "baseline" {
			return fmt.Errorf("resolved %s baseline carries configured tool data", name)
		}
		return nil
	}
	var supported bool
	switch name {
	case "review":
		supported = step.Adapter == "no-mistakes"
	case "forge":
		supported = step.Adapter == "github" || step.Adapter == "gitlab"
	}
	if !supported || step.ProbeState != "ready" {
		return fmt.Errorf("resolved %s adapter %s probe %s: %s", name, step.Adapter, step.ProbeState, step.Reason)
	}
	if step.Path == "" || !filepath.IsAbs(step.Path) {
		return fmt.Errorf("resolved %s adapter %s requires an absolute executable path", name, step.Adapter)
	}
	return nil
}

func toolEntryFromStep(step ResolvedStep) *ToolEntry {
	if step.Baseline {
		return nil
	}
	// A github step's Path is the probed gh-axi location, not configuration;
	// the adapter takes no path or args.
	if step.Adapter == "github" {
		return &ToolEntry{Adapter: step.Adapter}
	}
	return &ToolEntry{Adapter: step.Adapter, Path: step.Path, Args: append([]string(nil), step.Args...)}
}

func ResolveProjectTools(overlay ProjectOverlay, probe func(string, ToolEntry) ResolvedStep) (ResolvedStep, ResolvedStep, error) {
	if err := ValidateProjectTools(overlay); err != nil {
		return ResolvedStep{}, ResolvedStep{}, err
	}
	resolve := func(step string, entry *ToolEntry) ResolvedStep {
		if entry == nil {
			return baselineStep()
		}
		if probe == nil {
			return ResolvedStep{Adapter: entry.Adapter, Path: entry.Path, Args: append([]string(nil), entry.Args...), ProbeState: "failed", Reason: "configured tool probe is unavailable"}
		}
		result := probe(step, *cloneToolEntry(entry))
		result.Adapter = entry.Adapter
		result.Args = append([]string(nil), entry.Args...)
		if entry.Path != "" {
			result.Path = entry.Path
		}
		return result
	}
	return resolve("review", overlay.Review), resolve("forge", overlay.Forge), nil
}

func projectDigest(config ProjectOverlay, review, forge ResolvedStep) (string, error) {
	payload := struct {
		Config     ProjectOverlay `json:"config"`
		ReviewStep ResolvedStep   `json:"reviewStep"`
		ForgeStep  ResolvedStep   `json:"forgeStep"`
	}{cloneOverlay(config), cloneResolvedStep(review), cloneResolvedStep(forge)}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal resolved project config: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
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
	if src.Backend != "" {
		dst.Backend = src.Backend
	}
	if src.TamperCheck != "" {
		dst.TamperCheck = src.TamperCheck
	}
	if len(src.DispatchProfiles) > 0 {
		dst.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	}
	if src.Review != nil {
		dst.Review = cloneToolEntry(src.Review)
	}
	if src.Forge != nil {
		dst.Forge = cloneToolEntry(src.Forge)
	}
}

func cloneOverlay(src ProjectOverlay) ProjectOverlay {
	result := src
	result.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	result.Review = cloneToolEntry(src.Review)
	result.Forge = cloneToolEntry(src.Forge)
	return result
}

func cloneToolEntry(src *ToolEntry) *ToolEntry {
	if src == nil {
		return nil
	}
	clone := *src
	clone.Args = append([]string(nil), src.Args...)
	return &clone
}

// finalResolvedOverlay applies the two typed layers — fleet base and project
// overlay/facts — producing the final resolved overlay document. It is the
// single canonical payload for the digest: it covers the resolved overlay
// (including Backend) and excludes the digest itself and non-overlay
// projections (CaptainProfile, project identity).
func finalResolvedOverlay(base FleetBaseDocument, facts ProjectFacts) (ProjectOverlay, error) {
	if facts.Name == "" {
		return ProjectOverlay{}, fmt.Errorf("project name is required")
	}
	if facts.Path == "" {
		return ProjectOverlay{}, fmt.Errorf("project %q path is required", facts.Name)
	}
	return resolvedOverlay(overlayOf(base.Config), facts.Overlay), nil
}

func overlayOf(base FleetBaseConfig) ProjectOverlay {
	return ProjectOverlay{
		SoldierHarness: base.SoldierHarness, Model: base.Model,
		DispatchAutonomy: base.DispatchAutonomy, Backend: base.Backend,
		TamperCheck: base.TamperCheck, DispatchProfiles: base.DispatchProfiles,
	}
}

func resolvedOverlay(base ProjectOverlay, overlay ProjectOverlay) ProjectOverlay {
	effective := cloneOverlay(base)
	applyOverlay(&effective, overlay)
	return effective
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
func ValidateProjectTools(overlay ProjectOverlay) error {
	if err := validateToolEntry("review", overlay.Review, map[string]bool{"no-mistakes": true}); err != nil {
		return err
	}
	if err := validateToolEntry("forge", overlay.Forge, map[string]bool{"github": true, "gitlab": true}); err != nil {
		return err
	}
	if overlay.Review != nil && overlay.Review.Adapter == "no-mistakes" && overlay.Forge == nil {
		return fmt.Errorf("review adapter no-mistakes requires a configured forge tool")
	}
	return nil
}

func validateToolEntry(step string, entry *ToolEntry, adapters map[string]bool) error {
	if entry == nil {
		return nil
	}
	if !adapters[entry.Adapter] {
		return fmt.Errorf("%s tool has unknown adapter %q", step, entry.Adapter)
	}
	if entry.Path != "" && !filepath.IsAbs(entry.Path) {
		return fmt.Errorf("%s adapter %s path must be absolute", step, entry.Adapter)
	}
	if entry.Adapter == "github" && (entry.Path != "" || len(entry.Args) != 0) {
		return fmt.Errorf("forge adapter github does not accept path or args because it uses gh-axi and gh from PATH")
	}
	return nil
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
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return fmt.Errorf("installing typed config document %s: %w", path, err)
	}
	return nil
}
