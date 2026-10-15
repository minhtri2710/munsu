// Package config owns typed settings, defaults, validation, Project Overlays
// and immutable resolved snapshots. It reads no process environment: cli turns
// flags and ambient inputs into typed operations, and a snapshot is resolved
// once per operation and stays immutable for it.
//
// munsu has six ports: session (backend.Backend), worktree (backend.Provider),
// forge (fleet.DeliveryProvider), review (a step ending in a head-bound
// ReviewVerdict), guard (the git shim plus the OS write fence; a name only) and
// quota (harness candidate ordering). Adapters compile into the single binary;
// there is no Go plugin and no out-of-process adapter protocol.
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
	SelectStrategy string              `json:"select,omitempty"`
	Why            string              `json:"why,omitempty"`
	Use            []DispatchCandidate `json:"use,omitempty"`
	Scaffold       string              `json:"scaffold,omitempty"`
}

// A dispatch profile's Scaffold picks the Soldier charter for its runtime.
// Lean, also the empty value, is the charter a strong seat gets: CI and the
// independent reviewer cover what it would re-verify. Guided adds an explicit
// plan scaffold for a weaker or cheaper runtime.
const (
	ScaffoldLean   = "lean"
	ScaffoldGuided = "guided"
)

// ValidateScaffold is the one scaffold validator, run when a profile is written
// and again at spawn. Human words (domain.Words): grantor the Human, channel
// direct chat with the Lead, 2026-10-13, words=selected, 1A:
// "Xác nhận cả 7 refusal đã ghi theo tự quyết đi" (ratifies the "tự quyết
// đi" record: a dispatch profile scaffold other than lean or guided is
// refused).
func ValidateScaffold(scaffold string) error {
	switch scaffold {
	case "", ScaffoldLean, ScaffoldGuided:
		return nil
	}
	return fmt.Errorf("unknown dispatch profile scaffold %q (want %s or %s)", scaffold, ScaffoldLean, ScaffoldGuided)
}

type CaptainProfile struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// ToolEntry names a compiled-in adapter of one port, with an optional absolute
// path that replaces PATH lookup and optional args appended to the adapter's
// fixed argv. forge accepts github and gitlab; the github adapter takes neither
// path nor args (it runs gh-axi and gh from PATH). A tool on PATH
// with no entry is not used. A configured step whose probe is not Ready is refused
// and never falls back to its baseline; the operator
// removes the entry to run the baseline.
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
	Forge            *ToolEntry        `json:"forge,omitempty"`
	// FenceWritableRoots are extra absolute directories the write fence leaves
	// writable for this project's soldiers. FenceUnsupported "allow" lets this
	// project's seats launch unfenced on a host with no write fence. Both are
	// project-only, like the tool entry: the fleet base cannot carry them.
	FenceWritableRoots []string `json:"fenceWritableRoots,omitempty"`
	FenceUnsupported   string   `json:"fenceUnsupported,omitempty"`
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

// FleetBaseConfig is the fleet-wide overlay. The tool entry is project-only, so
// it has no Forge field and a fleet base document cannot carry one.
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
	Project            string            `json:"project"`
	ProjectPath        string            `json:"projectPath"`
	SoldierHarness     string            `json:"soldierHarness,omitempty"`
	DispatchAutonomy   string            `json:"dispatchAutonomy,omitempty"`
	Model              string            `json:"model,omitempty"`
	Backend            string            `json:"backend,omitempty"`
	TamperCheck        string            `json:"tamperCheck,omitempty"`
	DispatchProfiles   []DispatchProfile `json:"dispatchProfiles,omitempty"`
	CaptainProfile     CaptainProfile    `json:"captainProfile,omitempty"`
	ForgeStep          ResolvedStep      `json:"forgeStep"`
	FenceWritableRoots []string          `json:"fenceWritableRoots,omitempty"`
	FenceUnsupported   string            `json:"fenceUnsupported,omitempty"`
	Digest             string            `json:"digest"`
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

func ResolveProjectWithToolProbe(base FleetBaseDocument, facts ProjectFacts, probe func(ToolEntry) ResolvedStep) (ResolvedProjectConfig, error) {
	if err := base.Validate(); err != nil {
		return ResolvedProjectConfig{}, err
	}
	effective, err := finalResolvedOverlay(base, facts)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	forge, err := ResolveProjectTools(effective, probe)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	return ResolveProjectWithSteps(base, facts, forge)
}

func baselineStep() ResolvedStep {
	return ResolvedStep{Baseline: true, ProbeState: "baseline", Reason: "no tool configured"}
}

func ResolveProjectWithSteps(base FleetBaseDocument, facts ProjectFacts, forgeStep ResolvedStep) (ResolvedProjectConfig, error) {
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
	if err := validateResolvedStep(forgeStep); err != nil {
		return ResolvedProjectConfig{}, err
	}
	if !stepMatchesEntry(forgeStep, effective.Forge) {
		return ResolvedProjectConfig{}, fmt.Errorf("resolved forge step does not match the project overlay")
	}
	digest, err := projectDigest(effective, forgeStep)
	if err != nil {
		return ResolvedProjectConfig{}, err
	}
	return ResolvedProjectConfig{
		Project: facts.Name, ProjectPath: facts.Path,
		SoldierHarness: effective.SoldierHarness, Model: effective.Model,
		DispatchAutonomy: effective.DispatchAutonomy,
		Backend:          effective.Backend, TamperCheck: effective.TamperCheck,
		DispatchProfiles:   cloneProfiles(effective.DispatchProfiles),
		CaptainProfile:     base.CaptainProfile,
		ForgeStep:          cloneResolvedStep(forgeStep),
		FenceWritableRoots: append([]string(nil), effective.FenceWritableRoots...),
		FenceUnsupported:   effective.FenceUnsupported,
		Digest:             digest,
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

func validateResolvedStep(step ResolvedStep) error {
	if step.Baseline {
		if step.Adapter != "" || step.Path != "" || len(step.Args) != 0 || step.ProbeState != "baseline" {
			return fmt.Errorf("resolved forge baseline carries configured tool data")
		}
		return nil
	}
	if !forgeAdapters[step.Adapter] || step.ProbeState != "ready" {
		return fmt.Errorf("resolved forge adapter %s probe %s: %s", step.Adapter, step.ProbeState, step.Reason)
	}
	if step.Path == "" || !filepath.IsAbs(step.Path) {
		return fmt.Errorf("resolved forge adapter %s requires an absolute executable path", step.Adapter)
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

func ResolveProjectTools(overlay ProjectOverlay, probe func(ToolEntry) ResolvedStep) (ResolvedStep, error) {
	if err := ValidateProjectTools(overlay); err != nil {
		return ResolvedStep{}, err
	}
	entry := overlay.Forge
	if entry == nil {
		return baselineStep(), nil
	}
	if probe == nil {
		return ResolvedStep{Adapter: entry.Adapter, Path: entry.Path, Args: append([]string(nil), entry.Args...), ProbeState: "failed", Reason: "configured tool probe is unavailable"}, nil
	}
	result := probe(*cloneToolEntry(entry))
	result.Adapter = entry.Adapter
	result.Args = append([]string(nil), entry.Args...)
	if entry.Path != "" {
		result.Path = entry.Path
	}
	return result, nil
}

func projectDigest(config ProjectOverlay, forge ResolvedStep) (string, error) {
	payload := struct {
		Config    ProjectOverlay `json:"config"`
		ForgeStep ResolvedStep   `json:"forgeStep"`
	}{cloneOverlay(config), cloneResolvedStep(forge)}
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
	if src.Forge != nil {
		dst.Forge = cloneToolEntry(src.Forge)
	}
	if len(src.FenceWritableRoots) > 0 {
		dst.FenceWritableRoots = append([]string(nil), src.FenceWritableRoots...)
	}
	if src.FenceUnsupported != "" {
		dst.FenceUnsupported = src.FenceUnsupported
	}
}

func cloneOverlay(src ProjectOverlay) ProjectOverlay {
	result := src
	result.DispatchProfiles = cloneProfiles(src.DispatchProfiles)
	result.Forge = cloneToolEntry(src.Forge)
	result.FenceWritableRoots = append([]string(nil), src.FenceWritableRoots...)
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

var forgeAdapters = map[string]bool{"github": true, "gitlab": true}

func ValidateProjectTools(overlay ProjectOverlay) error {
	entry := overlay.Forge
	if entry == nil {
		return nil
	}
	if !forgeAdapters[entry.Adapter] {
		return fmt.Errorf("forge tool has unknown adapter %q", entry.Adapter)
	}
	if entry.Path != "" && !filepath.IsAbs(entry.Path) {
		return fmt.Errorf("forge adapter %s path must be absolute", entry.Adapter)
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
