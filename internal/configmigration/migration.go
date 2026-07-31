package configmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
)

// LegacyFileInfo describes a legacy config file found during planning.
type LegacyFileInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// ConfigMigrationPlan describes the legacy config files found and the typed
// documents that would be installed.
type ConfigMigrationPlan struct {
	HomeDir         string                            `json:"homeDir"`
	LegacyFiles     []LegacyFileInfo                   `json:"legacyFiles"`
	FleetBase       *config.FleetBaseDocument          `json:"fleetBase,omitempty"`
	CaptainRegistry *config.CaptainRegistryDocument    `json:"captainRegistry,omitempty"`
	ProjectRegistry *config.ProjectRegistryDocument    `json:"projectRegistry,omitempty"`
	MigrateCommand  string                            `json:"migrateCommand"`
	PlanDigest      string                            `json:"planDigest"`
}

// ConfigMigrationReceipt documents the migration that was applied.
type ConfigMigrationReceipt struct {
	HomeDir        string          `json:"homeDir"`
	LegacyFiles    []LegacyFileInfo `json:"legacyFiles"`
	ArchivePath    string          `json:"archivePath"`
	FleetDigest    string          `json:"fleetDigest,omitempty"`
	CaptainDigest  string          `json:"captainDigest,omitempty"`
	ProjectDigest string          `json:"projectDigest,omitempty"`
	AppliedAt      string          `json:"appliedAt"`
	ReceiptDigest  string          `json:"receiptDigest"`
}

// MigrationCommand returns the CLI command to run for config migration.
func MigrationCommand(homeDir string) string {
	return fmt.Sprintf("munsu migrate config plan --plan-out <plan.json> && munsu migrate config apply --plan <plan.json>")
}

// NeedsConfigMigration checks whether legacy config files exist and the typed
// documents are not yet installed. Returns true and the migration command if
// migration is needed.
func NeedsConfigMigration(homeDir string) (bool, string) {
	legacyPaths := []string{
		filepath.Join(homeDir, "data", "captains.md"),
		filepath.Join(homeDir, "data", "projects.md"),
		filepath.Join(homeDir, "config", "soldier-dispatch.json"),
	}
	hasLegacy := false
	for _, p := range legacyPaths {
		if _, err := os.Stat(p); err == nil {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return false, ""
	}
	// Check if typed documents are already installed.
	typedPaths := []string{
		filepath.Join(homeDir, config.BaseDocumentPath),
		filepath.Join(homeDir, config.CaptainDocumentPath),
		filepath.Join(homeDir, config.ProjectDocumentPath),
	}
	typedPresent := 0
	for _, p := range typedPaths {
		if _, err := os.Stat(p); err == nil {
			typedPresent++
		}
	}
	if typedPresent == 3 {
		// All typed documents present - migration already done.
		return false, ""
	}
	return true, MigrationCommand(homeDir)
}

// PlanConfigMigration examines the home directory for legacy config files and
// produces a migration plan. It validates all legacy data and converts it to
// typed documents, but does not write any files.
func PlanConfigMigration(homeDir string) (*ConfigMigrationPlan, error) {
	plan := &ConfigMigrationPlan{
		HomeDir:        homeDir,
		MigrateCommand: MigrationCommand(homeDir),
	}

	// Collect legacy file info.
	legacyPaths := map[string]string{
		"captains.md":           filepath.Join(homeDir, "data", "captains.md"),
		"projects.md":           filepath.Join(homeDir, "data", "projects.md"),
		"soldier-dispatch.json": filepath.Join(homeDir, "config", "soldier-dispatch.json"),
	}

	// Sort names for deterministic ordering.
	var names []string
	for name := range legacyPaths {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := legacyPaths[name]
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking legacy file %s: %w", path, err)
		}
		digest, err := fileDigest(path)
		if err != nil {
			return nil, fmt.Errorf("digesting legacy file %s: %w", path, err)
		}
		plan.LegacyFiles = append(plan.LegacyFiles, LegacyFileInfo{
			Path:   path,
			Name:   name,
			Digest: digest,
			Size:   info.Size(),
		})
	}

	if len(plan.LegacyFiles) == 0 {
		return nil, fmt.Errorf("no legacy config files found in %s", homeDir)
	}

	// Check that typed documents don't already exist.
	for _, lf := range plan.LegacyFiles {
		// Check if the corresponding typed document already exists.
		typedPath := legacyToTypedPath(lf.Name)
		if typedPath != "" {
			if _, err := os.Stat(filepath.Join(homeDir, typedPath)); err == nil {
				return nil, fmt.Errorf("typed document %s already exists; migration already applied or in progress", typedPath)
			}
		}
	}

	// Parse legacy files and convert to typed documents.
	// Start with a default fleet base document.
	fleetBase := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
	}

	// Parse soldier-dispatch.json if present.
	if hasLegacyFile(plan.LegacyFiles, "soldier-dispatch.json") {
		dp := dispatchPath(homeDir)
		legacy, err := loadDispatch(dp)
		if err != nil {
			return nil, fmt.Errorf("parsing legacy dispatch config: %w", err)
		}
		// Convert dispatch profiles to fleet base format.
		if legacy.DefaultHarness != "" {
			fleetBase.Config.SoldierHarness = legacy.DefaultHarness
		}
		if legacy.DefaultModel != "" {
			fleetBase.Config.Model = legacy.DefaultModel
		}
		for _, p := range legacy.Profiles {
			fleetBase.Config.DispatchProfiles = append(fleetBase.Config.DispatchProfiles, config.DispatchProfile{
				Name:           p.Name,
				Match:          p.Match,
				When:           p.When,
				Harness:        p.Harness,
				Model:          p.Model,
				Effort:         p.Effort,
				MaxConcurrent:  p.MaxConcurrent,
				SelectStrategy: p.SelectStrategy,
				Why:            p.Why,
				Use:            convertCandidates(p.Use),
			})
		}
	}

	// Parse captains.md if present.
	captainRegistry := config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
	}
	if hasLegacyFile(plan.LegacyFiles, "captains.md") {
		cp := captainRegistryPath(homeDir)
		legacy, err := parseRegistry(cp)
		if err != nil {
			return nil, fmt.Errorf("parsing legacy captain registry: %w", err)
		}
		for _, entry := range legacy {
			captainRegistry.Captains = append(captainRegistry.Captains, config.CaptainRecord{
				ID:      entry.ID,
				Home:    entry.Home,
				Project: entry.Project,
			})
		}
		captainRegistry.SchemaVersion = config.CaptainRegistrySchemaVersion
	}

	// Parse projects.md if present.
	projectRegistry := config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
	}
	if hasLegacyFile(plan.LegacyFiles, "projects.md") {
		rp := registryPath(homeDir)
		legacy, err := listFromFile(rp)
		if err != nil {
			return nil, fmt.Errorf("parsing legacy project registry: %w", err)
		}
		for _, entry := range legacy {
			projectRegistry.Projects = append(projectRegistry.Projects, config.ProjectRecord{
				Name: entry.Name,
				Path: entry.Description,
				Mode: entry.Mode,
			})
		}
	}
	if len(captainRegistry.Captains) > 0 || len(projectRegistry.Projects) > 0 {
		if err := config.ValidateFleetBindings(captainRegistry, projectRegistry); err != nil {
			return nil, fmt.Errorf("invalid Captain/project bindings: %w (source untouched)", err)
		}
	}

	plan.FleetBase = &fleetBase
	plan.CaptainRegistry = &captainRegistry
	plan.ProjectRegistry = &projectRegistry

	// Compute plan digest for source verification.
	digest, err := planDigest(plan)
	if err != nil {
		return nil, fmt.Errorf("computing plan digest: %w", err)
	}
	plan.PlanDigest = digest

	return plan, nil
}

// ApplyConfigMigration applies a config migration plan, archiving legacy files
// and writing typed documents. Returns a receipt. Idempotent: if the typed
// documents already exist and match the plan, returns a receipt without
// mutating anything.
func ApplyConfigMigration(plan *ConfigMigrationPlan) (*ConfigMigrationReceipt, error) {
	if plan == nil {
		return nil, fmt.Errorf("migration plan is required")
	}
	if len(plan.LegacyFiles) == 0 {
		return nil, fmt.Errorf("migration plan has no legacy files")
	}

	// Verify source hasn't changed since plan.
	for _, lf := range plan.LegacyFiles {
		current, err := fileDigest(lf.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("source file %s no longer exists; plan is stale", lf.Path)
			}
			return nil, fmt.Errorf("checking source file %s: %w", lf.Path, err)
		}
		if current != lf.Digest {
			return nil, fmt.Errorf("source file %s digest changed; plan is stale (expected %s, got %s)", lf.Path, lf.Digest, current)
		}
	}

	// Check that typed documents don't already exist (idempotency check).
	typedPaths := []string{
		filepath.Join(plan.HomeDir, config.BaseDocumentPath),
		filepath.Join(plan.HomeDir, config.CaptainDocumentPath),
		filepath.Join(plan.HomeDir, config.ProjectDocumentPath),
	}
	allTypedPresent := true
	for _, p := range typedPaths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			allTypedPresent = false
			break
		}
	}
	if allTypedPresent {
		// Already migrated - return a receipt.
		return existingReceipt(plan.HomeDir)
	}

	// Archive legacy files.
	archiveDir := filepath.Join(plan.HomeDir, ".config-migration-archive")
	ts := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	archivePath := filepath.Join(archiveDir, ts)
	if err := os.MkdirAll(archivePath, 0700); err != nil {
		return nil, fmt.Errorf("creating archive directory: %w", err)
	}

	for _, lf := range plan.LegacyFiles {
		dest := filepath.Join(archivePath, lf.Name)
		data, err := os.ReadFile(lf.Path)
		if err != nil {
			return nil, fmt.Errorf("reading legacy file for archive: %w", err)
		}
		if err := os.WriteFile(dest, data, 0600); err != nil {
			return nil, fmt.Errorf("archiving legacy file %s: %w", lf.Name, err)
		}
		// Remove the original legacy file.
		if err := os.Remove(lf.Path); err != nil {
			return nil, fmt.Errorf("removing legacy file %s: %w", lf.Path, err)
		}
	}

	// Write typed documents.
	if plan.FleetBase != nil {
		if err := config.StoreFleetBase(plan.HomeDir, *plan.FleetBase); err != nil {
			return nil, fmt.Errorf("writing fleet base document: %w", err)
		}
	}
	if plan.CaptainRegistry != nil && len(plan.CaptainRegistry.Captains) > 0 {
		if err := config.StoreCaptainRegistry(plan.HomeDir, *plan.CaptainRegistry); err != nil {
			return nil, fmt.Errorf("writing captain registry document: %w", err)
		}
	}
	if plan.ProjectRegistry != nil && len(plan.ProjectRegistry.Projects) > 0 {
		if err := config.StoreProjectRegistry(plan.HomeDir, *plan.ProjectRegistry); err != nil {
			return nil, fmt.Errorf("writing project registry document: %w", err)
		}
	}

	// Build receipt.
	receipt := &ConfigMigrationReceipt{
		HomeDir:     plan.HomeDir,
		LegacyFiles: append([]LegacyFileInfo(nil), plan.LegacyFiles...),
		ArchivePath: archivePath,
		AppliedAt:   ts,
	}
	if plan.FleetBase != nil {
		digest, err := documentDigest(*plan.FleetBase)
		if err == nil {
			receipt.FleetDigest = digest
		}
	}
	if plan.CaptainRegistry != nil {
		digest, err := documentDigest(*plan.CaptainRegistry)
		if err == nil {
			receipt.CaptainDigest = digest
		}
	}
	if plan.ProjectRegistry != nil {
		digest, err := documentDigest(*plan.ProjectRegistry)
		if err == nil {
			receipt.ProjectDigest = digest
		}
	}

	digest, err := receiptDigest(receipt)
	if err != nil {
		return nil, fmt.Errorf("computing receipt digest: %w", err)
	}
	receipt.ReceiptDigest = digest

	// Write receipt alongside archive.
	receiptData, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding receipt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(archivePath, "receipt.json"), append(receiptData, '\n'), 0600); err != nil {
		return nil, fmt.Errorf("writing receipt: %w", err)
	}

	return receipt, nil
}

// hasLegacyFile checks if a legacy file with the given name is in the plan.
func hasLegacyFile(files []LegacyFileInfo, name string) bool {
	for _, f := range files {
		if f.Name == name {
			return true
		}
	}
	return false
}

// legacyToTypedPath maps a legacy file name to its corresponding typed document
// path relative to the home directory. Returns empty string if no mapping exists.
func legacyToTypedPath(name string) string {
	switch name {
	case "soldier-dispatch.json":
		return config.BaseDocumentPath
	case "captains.md":
		return config.CaptainDocumentPath
	case "projects.md":
		return config.ProjectDocumentPath
	default:
		return ""
	}
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func documentDigest(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func planDigest(plan *ConfigMigrationPlan) (string, error) {
	// Create a copy without the digest field for deterministic hashing.
	digest := plan.PlanDigest
	plan.PlanDigest = ""
	defer func() { plan.PlanDigest = digest }()

	data, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func receiptDigest(r *ConfigMigrationReceipt) (string, error) {
	digest := r.ReceiptDigest
	r.ReceiptDigest = ""
	defer func() { r.ReceiptDigest = digest }()

	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func convertCandidates(src []DispatchCandidate) []config.DispatchCandidate {
	if src == nil {
		return nil
	}
	dst := make([]config.DispatchCandidate, len(src))
	for i, c := range src {
		dst[i] = config.DispatchCandidate{Harness: c.Harness, Model: c.Model, Effort: c.Effort}
	}
	return dst
}

// existingReceipt reads and returns the most recent receipt from the archive.
func existingReceipt(homeDir string) (*ConfigMigrationReceipt, error) {
	archiveDir := filepath.Join(homeDir, ".config-migration-archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("migration already applied but no receipt found: %w", err)
	}
	// Find the most recent archive.
	var latest string
	for _, e := range entries {
		if e.IsDir() && e.Name() > latest {
			latest = e.Name()
		}
	}
	if latest == "" {
		return nil, fmt.Errorf("migration already applied but no archive found")
	}
	receiptPath := filepath.Join(archiveDir, latest, "receipt.json")
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return nil, fmt.Errorf("migration already applied but receipt unreadable: %w", err)
	}
	var receipt ConfigMigrationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, fmt.Errorf("migration already applied but receipt corrupt: %w", err)
	}
	receipt.HomeDir = homeDir
	return &receipt, nil
}

// LegacyConfigError returns a formatted error for load paths that encounter
// legacy config files and need to fail with a migration command.
func LegacyConfigError(homeDir, legacyPath string) error {
	return fmt.Errorf("legacy configuration found at %s; run `munsu migrate config plan --plan-out <plan.json>` then `munsu migrate config apply --plan <plan.json>` to migrate %s", legacyPath, homeDir)
}

// LegacyConfigCheckError creates a "needs migration" error for places that
// directly call NeedsConfigMigration.
func LegacyConfigCheckError(homeDir string) error {
	return fmt.Errorf("legacy configuration detected; run `munsu migrate config plan --plan-out <plan.json>` then `munsu migrate config apply --plan <plan.json>` to migrate %s", homeDir)
}