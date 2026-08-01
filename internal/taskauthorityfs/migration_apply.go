package taskauthorityfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// migrationCrashAfter injects a deterministic crash point inside
// ApplyMigration. Package-private and empty in production; tests use it to
// prove every crash window resumes safely.
var migrationCrashAfter string

// Journal stages of one migration transaction. The journal is the durable
// commit marker: the target is marked committed before any legacy source is
// moved, so a retry resumes from the journal instead of guessing from
// missing sources.
const (
	journalStageCommitted = "committed"
	journalStageArchived  = "archived"
)

// migrationJournal is the durable write-ahead record of one migration
// transaction. A pending or absent journal means no target was committed;
// committed means the v2 targets are installed and verified; archived means
// the legacy sources were moved; the receipt is the terminal completion
// marker.
type migrationJournal struct {
	SchemaVersion        string `json:"schema_version"`
	HomeIdentity         string `json:"home_identity"`
	SourceDigest         string `json:"source_digest"`
	Stage                string `json:"stage"`
	TargetManifestDigest string `json:"target_manifest_digest"`
	ArchivePath          string `json:"archive_path,omitempty"`
	CompletedAt          int64  `json:"completed_at,omitempty"`
}

// migrationManifest pins every installed v2 file path to its SHA-256 digest.
// The receipt stores the manifest digest, so a completed migration verifies
// the installed targets without rewriting.
type migrationManifest struct {
	Files []SourceFile `json:"files"`
}

func migrationDirPath(homeDir string) string {
	return filepath.Join(homeDir, filepath.FromSlash(migrationDir))
}
func migrationReceiptPath(homeDir string) string {
	return filepath.Join(migrationDirPath(homeDir), "receipt.json")
}
func migrationJournalPath(homeDir string) string {
	return filepath.Join(migrationDirPath(homeDir), "journal.json")
}
func migrationManifestPath(homeDir string) string {
	return filepath.Join(migrationDirPath(homeDir), "manifest.json")
}
func migrationStagePath(homeDir, digest string) string {
	return filepath.Join(migrationDirPath(homeDir), "stage", digest)
}

// migrationCrash fails at the injected crash point.
func migrationCrash(after string) error {
	if migrationCrashAfter != "" && migrationCrashAfter == after {
		return fmt.Errorf("injected task-authority migration crash after %s", after)
	}
	return nil
}

// ApplyMigration applies one reviewed migration plan. It validates the plan,
// revalidates the home identity, completes or resumes an interrupted
// transaction from the journal, and converts the legacy v1 task-authority
// sources into committed v2 records with initial typed audit evidence and a
// durable receipt. Corrupt or conflicting source is never touched; a
// completed migration verifies and returns ErrAlreadyMigrated without
// rewriting.
func ApplyMigration(plan *MigrationPlan) (*MigrationReceipt, error) {
	if err := validateMigrationPlan(plan); err != nil {
		return nil, err
	}
	if len(plan.Quarantined) > 0 {
		return nil, fmt.Errorf("task-authority migration plan quarantines %d source record(s); resolve them and re-run `munsu migrate task-authority plan` before applying (corrupt or conflicting source remains untouched)", len(plan.Quarantined))
	}
	canon, err := canonicalMigrationHome(plan.HomeDir)
	if err != nil {
		return nil, err
	}
	if canon != plan.HomeDir {
		return nil, fmt.Errorf("home identity changed: plan %s current %s", plan.HomeDir, canon)
	}
	if identity, _, err := home.ReadHomeIdentity(canon); err != nil {
		return nil, err
	} else if identity != plan.HomeIdentity {
		return nil, fmt.Errorf("home identity changed: plan %s current %s", plan.HomeIdentity, identity)
	}

	// Completed migration: the receipt plus installed targets verifies and
	// returns already_migrated; a receipt that contradicts the plan fails
	// closed instead of rewriting.
	if receipt, done, err := verifyCompletedMigration(plan, canon); err != nil {
		return nil, err
	} else if done {
		return nil, &AlreadyMigratedError{HomeDir: plan.HomeDir, SourceDigest: plan.SourceDigest}
	} else if receipt != nil {
		return nil, fmt.Errorf("task-authority migration receipt for %s does not match plan %s", plan.HomeDir, plan.SourceDigest)
	}

	// Interrupted transaction: the journal pins the progress.
	if journal, ok, err := readMigrationJournal(canon); err != nil {
		return nil, err
	} else if ok {
		if journal.HomeIdentity != plan.HomeIdentity || journal.SourceDigest != plan.SourceDigest {
			return nil, fmt.Errorf("task-authority migration journal does not match plan (identity or digest changed)")
		}
		switch journal.Stage {
		case journalStageCommitted, journalStageArchived:
			return resumeMigration(plan, canon, journal)
		default:
			return nil, fmt.Errorf("task-authority migration journal has unknown stage %q", journal.Stage)
		}
	}

	return applyMigrationFresh(plan, canon)
}

// verifyCompletedMigration checks the durable receipt and installed targets.
// It returns (receipt, done=true) when the migration verifies, and an error
// when the receipt or installed state contradicts the plan. Nothing is
// rewritten on any path.
func verifyCompletedMigration(plan *MigrationPlan, canon string) (*MigrationReceipt, bool, error) {
	data, err := os.ReadFile(migrationReceiptPath(canon))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var receipt MigrationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, false, fmt.Errorf("corrupt task-authority migration receipt: %w", err)
	}
	if receipt.SchemaVersion != MigrationPlanSchema || receipt.HomeDir != plan.HomeDir || receipt.HomeIdentity != plan.HomeIdentity ||
		receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != plan.RecordCount {
		return &receipt, false, nil
	}
	if err := verifyInstalledTargets(plan, canon, receipt.TargetManifestDigest); err != nil {
		return &receipt, false, fmt.Errorf("installed task-authority v2 targets do not verify: %w", err)
	}
	if v1SourcesRemain(canon) {
		return &receipt, false, fmt.Errorf("legacy v1 task-authority sources remain under %s despite completion receipt", canon)
	}
	return &receipt, true, nil
}

// applyMigrationFresh runs the full transaction: under the dispatch lock and
// per-task locks it re-derives and compares the entire plan, checks target
// conflicts, stages and verifies all v2 documents, installs them, marks the
// journal committed, archives the legacy sources, then verifies the
// canonical store read and writes the durable receipt.
func applyMigrationFresh(plan *MigrationPlan, canon string) (*MigrationReceipt, error) {
	var manifestDigest string
	err := withDispatchLock(canon, func() error {
		digest, err := applyMigrationLocked(plan, canon)
		if err != nil {
			return err
		}
		manifestDigest = digest
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := verifyInstalledTargets(plan, canon, manifestDigest); err != nil {
		return nil, fmt.Errorf("installed task-authority v2 targets do not verify: %w", err)
	}
	return completeMigrationReceipt(plan, canon, manifestDigest)
}

// applyMigrationLocked runs the mutation phase under the dispatch lock and
// per-task locks (canonical order: dispatch first, then each task).
func applyMigrationLocked(plan *MigrationPlan, canon string) (string, error) {
	var taskFiles []*os.File
	defer func() {
		for i := len(taskFiles) - 1; i >= 0; i-- {
			releaseLock(taskFiles[i])
		}
	}()
	for _, taskID := range plannedTaskIDs(plan) {
		taskPath, err := taskLockPath(canon, taskID)
		if err != nil {
			return "", err
		}
		f, err := lockFile(taskPath)
		if err != nil {
			return "", err
		}
		taskFiles = append(taskFiles, f)
	}

	// Re-derive the plan under locks and compare the entire result, not just
	// the digest: any source change fails closed before anything is staged.
	current, err := PlanMigration(canon)
	if err != nil {
		return "", fmt.Errorf("re-planning source under lock: %w", err)
	}
	if !reflect.DeepEqual(current, plan) {
		return "", fmt.Errorf("task-authority source changed since plan: plan digest %s current digest %s", plan.SourceDigest, current.SourceDigest)
	}

	// Target conflicts: existing v2 files must all be planned targets or
	// planned audit events; anything else fails closed untouched.
	if err := checkTargetConflicts(plan, canon); err != nil {
		return "", err
	}

	// Stage every v2 document plus the durable manifest.
	now := time.Now().UnixNano()
	stageRoot := migrationStagePath(canon, plan.SourceDigest)
	if err := os.RemoveAll(stageRoot); err != nil {
		return "", err
	}
	files, manifest, err := buildStagedTargets(plan, canon, now)
	if err != nil {
		return "", err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestData = append(manifestData, '\n')
	if err := writeMigrationFile(migrationManifestPath(canon), manifestData); err != nil {
		return "", err
	}
	if err := writeStagedTargets(stageRoot, files); err != nil {
		return "", err
	}
	if err := verifyStagedTargets(plan, stageRoot, manifest); err != nil {
		return "", err
	}

	// Install the staged v2 namespace, verify installed digests, then mark
	// the journal committed before any legacy source moves.
	if err := installStagedV2(canon, stageRoot); err != nil {
		return "", err
	}
	if err := verifyInstalledFilesByManifest(canon, DigestHex(manifestData)); err != nil {
		return "", err
	}
	if err := migrationCrash("install"); err != nil {
		return "", err
	}
	manifestDigest := DigestHex(manifestData)
	journal := &migrationJournal{
		SchemaVersion:        MigrationPlanSchema,
		HomeIdentity:         plan.HomeIdentity,
		SourceDigest:         plan.SourceDigest,
		Stage:                journalStageCommitted,
		TargetManifestDigest: manifestDigest,
	}
	if err := writeMigrationJournal(canon, journal); err != nil {
		return "", err
	}
	if err := migrationCrash("committed"); err != nil {
		return "", err
	}

	// Archive the legacy v1 sources only after the target is committed.
	archivePath, err := verifyAndArchiveV1Sources(plan, canon, "")
	if err != nil {
		return "", err
	}
	journal.ArchivePath = archivePath
	journal.Stage = journalStageArchived
	if err := writeMigrationJournal(canon, journal); err != nil {
		return "", err
	}
	if err := migrationCrash("archive"); err != nil {
		return "", err
	}
	return manifestDigest, nil
}

// resumeMigration finishes an interrupted transaction from the journal:
// verify the installed targets, archive any remaining v1 sources (idempotent,
// with digest verification), and complete the receipt.
func resumeMigration(plan *MigrationPlan, canon string, journal *migrationJournal) (*MigrationReceipt, error) {
	if journal.TargetManifestDigest == "" {
		return nil, fmt.Errorf("task-authority migration journal committed without a target manifest digest")
	}
	if err := verifyInstalledFilesByManifest(canon, journal.TargetManifestDigest); err != nil {
		return nil, fmt.Errorf("installed task-authority v2 targets do not verify on resume: %w", err)
	}
	var archivePath string
	err := withDispatchLock(canon, func() error {
		path, err := verifyAndArchiveV1Sources(plan, canon, journal.ArchivePath)
		if err != nil {
			return err
		}
		archivePath = path
		journal.ArchivePath = path
		journal.Stage = journalStageArchived
		return writeMigrationJournal(canon, journal)
	})
	if err != nil {
		return nil, err
	}
	_ = archivePath
	if err := verifyInstalledTargets(plan, canon, journal.TargetManifestDigest); err != nil {
		return nil, fmt.Errorf("installed task-authority v2 targets do not verify on resume: %w", err)
	}
	return completeMigrationReceipt(plan, canon, journal.TargetManifestDigest)
}

// completeMigrationReceipt writes the durable receipt as the terminal marker
// and updates the journal to completed.
func completeMigrationReceipt(plan *MigrationPlan, canon, manifestDigest string) (*MigrationReceipt, error) {
	if v1SourcesRemain(canon) {
		return nil, fmt.Errorf("legacy v1 task-authority sources remain under %s before receipt", canon)
	}
	journal, ok, err := readMigrationJournal(canon)
	if err != nil {
		return nil, err
	}
	if !ok || journal.Stage != journalStageArchived || journal.SourceDigest != plan.SourceDigest {
		return nil, fmt.Errorf("task-authority migration journal is missing or inconsistent before receipt")
	}
	if len(plan.Sources) > 0 && journal.ArchivePath == "" {
		return nil, fmt.Errorf("task-authority migration journal missing archive path before receipt")
	}
	receipt := &MigrationReceipt{
		SchemaVersion:        MigrationPlanSchema,
		HomeDir:              plan.HomeDir,
		HomeIdentity:         plan.HomeIdentity,
		SourceSchema:         plan.SourceSchema,
		TargetSchema:         plan.TargetSchema,
		SourceDigest:         plan.SourceDigest,
		RecordCount:          plan.RecordCount,
		TargetManifestDigest: manifestDigest,
		ArchivePath:          journal.ArchivePath,
		CompletedAt:          time.Now().Unix(),
	}
	if err := writeMigrationJSON(migrationReceiptPath(canon), receipt); err != nil {
		return nil, err
	}
	if err := migrationCrash("receipt"); err != nil {
		return nil, err
	}
	journal.CompletedAt = receipt.CompletedAt
	journal.Stage = "completed"
	if err := writeMigrationJournal(canon, journal); err != nil {
		return nil, err
	}
	return receipt, nil
}

// plannedTaskIDs returns the distinct planned task IDs in sorted order.
func plannedTaskIDs(plan *MigrationPlan) []string {
	seen := map[string]bool{}
	for _, agg := range plan.Aggregates {
		seen[agg.TaskID] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// checkTargetConflicts fails closed when the v2 namespace already carries a
// visible file that is neither a planned target nor a planned audit event.
// Files that match the plan are allowed so an interrupted install retries
// idempotently; the install swaps the whole namespace anyway.
func checkTargetConflicts(plan *MigrationPlan, canon string) error {
	expected := map[string]bool{}
	for _, agg := range plan.Aggregates {
		rel, err := AggregateRelPath(agg.TaskID, agg.Generation)
		if err != nil {
			return err
		}
		expected[filepath.ToSlash(rel)] = true
		if agg.Current {
			rel, err := CurrentPointerRelPath(agg.TaskID)
			if err != nil {
				return err
			}
			expected[filepath.ToSlash(rel)] = true
		}
	}
	for _, hold := range plan.Holds {
		rel, err := HoldRelPath(hold.ID)
		if err != nil {
			return err
		}
		expected[filepath.ToSlash(rel)] = true
	}
	for _, rec := range plan.Interpretations {
		rel, err := InterpretationRelPath(rec.ID)
		if err != nil {
			return err
		}
		expected[filepath.ToSlash(rel)] = true
	}
	for _, dec := range plan.Decisions {
		rel, err := DecisionRelPath(dec.Key)
		if err != nil {
			return err
		}
		expected[filepath.ToSlash(rel)] = true
	}
	for _, audit := range plan.Audits {
		rel, err := AuditRelPath(audit.OperationID)
		if err != nil {
			return err
		}
		expected[filepath.ToSlash(rel)] = true
	}
	root := filepath.Join(canon, filepath.FromSlash(authorityRoot))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !expected[authorityRoot+"/"+rel] {
			return fmt.Errorf("target conflict: existing v2 file %s is not a planned migration target", authorityRoot+"/"+rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// stagedFile is one rendered v2 document with its canonical rel path.
type stagedFile struct {
	rel  string
	data []byte
}

// buildStagedTargets renders every planned v2 document: converted aggregates,
// derived current pointers, dispatch records, and the initial typed audit
// events stamped with one shared apply timestamp.
func buildStagedTargets(plan *MigrationPlan, canon string, now int64) ([]stagedFile, *migrationManifest, error) {
	var files []stagedFile
	add := func(rel string, data []byte) {
		files = append(files, stagedFile{rel: filepath.ToSlash(rel), data: data})
	}
	for _, agg := range plan.Aggregates {
		rel, err := AggregateRelPath(agg.TaskID, agg.Generation)
		if err != nil {
			return nil, nil, err
		}
		data, err := EncodeAggregate(agg)
		if err != nil {
			return nil, nil, err
		}
		add(rel, data)
		if agg.Current {
			rel, err := CurrentPointerRelPath(agg.TaskID)
			if err != nil {
				return nil, nil, err
			}
			data, err := EncodeCurrentPointer(agg.Generation)
			if err != nil {
				return nil, nil, err
			}
			add(rel, data)
		}
	}
	for _, hold := range plan.Holds {
		rel, err := HoldRelPath(hold.ID)
		if err != nil {
			return nil, nil, err
		}
		data, err := EncodeHold(hold)
		if err != nil {
			return nil, nil, err
		}
		add(rel, data)
	}
	for _, rec := range plan.Interpretations {
		rel, err := InterpretationRelPath(rec.ID)
		if err != nil {
			return nil, nil, err
		}
		data, err := EncodeInterpretation(rec)
		if err != nil {
			return nil, nil, err
		}
		add(rel, data)
	}
	for _, dec := range plan.Decisions {
		rel, err := DecisionRelPath(dec.Key)
		if err != nil {
			return nil, nil, err
		}
		data, err := EncodeDecision(dec)
		if err != nil {
			return nil, nil, err
		}
		add(rel, data)
	}
	for _, want := range plan.Audits {
		event := taskauthority.AuditEvent{
			OperationID: want.OperationID,
			Actor:       taskauthority.Actor{ID: "migration"},
			Kind:        want.Kind,
			TaskID:      want.TaskID,
			Generation:  want.Generation,
			Reason:      want.Reason,
			After:       want.After,
			At:          now,
		}
		rel, err := AuditRelPath(want.OperationID)
		if err != nil {
			return nil, nil, err
		}
		data, err := EncodeAudit(event)
		if err != nil {
			return nil, nil, err
		}
		add(rel, data)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	manifest := &migrationManifest{}
	for _, f := range files {
		manifest.Files = append(manifest.Files, SourceFile{Path: f.rel, Schema: SchemaVersion, SHA256: DigestHex(f.data)})
	}
	return files, manifest, nil
}

// writeStagedTargets writes every staged document under the stage root, which
// mirrors the home layout (state/.task-authority/v2/...). The v2 root is
// created even for an empty target set so a zero-record migration installs a
// valid empty namespace.
func writeStagedTargets(stageRoot string, files []stagedFile) error {
	if err := os.MkdirAll(filepath.Join(stageRoot, filepath.FromSlash(authorityRoot)), DirPerm); err != nil {
		return err
	}
	for _, f := range files {
		abs := filepath.Join(stageRoot, filepath.FromSlash(f.rel))
		if err := writeMigrationFile(abs, f.data); err != nil {
			return err
		}
	}
	return nil
}

// verifyStagedTargets validates every staged file before install: digest
// against the manifest, then a canonical Store read of the staged namespace,
// which fails closed on corrupt documents, identity mismatches, and current
// pointer contradictions.
func verifyStagedTargets(plan *MigrationPlan, stageRoot string, manifest *migrationManifest) error {
	for _, f := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(stageRoot, filepath.FromSlash(f.Path)))
		if err != nil {
			return err
		}
		if DigestHex(data) != f.SHA256 {
			return fmt.Errorf("staged file %s digest mismatch", f.Path)
		}
	}
	view, err := NewStore(stageRoot)
	if err != nil {
		return err
	}
	staged, err := view.View()
	if err != nil {
		return fmt.Errorf("staged v2 namespace does not load: %w", err)
	}
	if !recordSlicesEqual(staged.Aggregates, plan.Aggregates) || !recordSlicesEqual(staged.Holds, plan.Holds) ||
		!recordSlicesEqual(staged.Interpretations, plan.Interpretations) || !recordSlicesEqual(staged.Decisions, plan.Decisions) {
		return fmt.Errorf("staged v2 namespace differs from plan targets")
	}
	return nil
}

// installStagedV2 swaps the staged v2 namespace into the home with a
// same-filesystem rename dance, removing stale markers first.
func installStagedV2(canon, stageRoot string) error {
	staged := filepath.Join(stageRoot, filepath.FromSlash(authorityRoot))
	target := filepath.Join(canon, filepath.FromSlash(authorityRoot))
	oldDir := target + ".old"
	installing := target + ".installing"
	if err := os.RemoveAll(installing); err != nil {
		return err
	}
	if err := os.RemoveAll(oldDir); err != nil {
		return err
	}
	// The rename target's parent may not exist on a fresh home.
	if err := os.MkdirAll(filepath.Dir(target), DirPerm); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(target), DirPerm); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, oldDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if _, statErr := os.Stat(oldDir); statErr == nil {
			_ = os.Rename(oldDir, target)
		}
		return err
	}
	return os.RemoveAll(oldDir)
}

// verifyInstalledFilesByManifest verifies the manifest digest and every
// installed file content digest. It is safe under the dispatch lock and with
// legacy v1 sources still present.
func verifyInstalledFilesByManifest(canon, wantManifestDigest string) error {
	manifestData, err := os.ReadFile(migrationManifestPath(canon))
	if err != nil {
		return err
	}
	if DigestHex(manifestData) != wantManifestDigest {
		return fmt.Errorf("migration manifest digest mismatch: installed manifest %s does not match pinned %s", DigestHex(manifestData), wantManifestDigest)
	}
	var manifest migrationManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	for _, f := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(canon, filepath.FromSlash(f.Path)))
		if err != nil {
			return fmt.Errorf("installed target %s: %w", f.Path, err)
		}
		if DigestHex(data) != f.SHA256 {
			return fmt.Errorf("installed target %s digest mismatch", f.Path)
		}
	}
	return nil
}

// verifyInstalledTargets verifies the completed installation: manifest
// digest, per-file digests, decode/identity of every deterministic target
// and current pointer, presence of every planned audit event, and a
// canonical Store read that matches the plan targets.
func verifyInstalledTargets(plan *MigrationPlan, canon, wantManifestDigest string) error {
	if err := verifyInstalledFilesByManifest(canon, wantManifestDigest); err != nil {
		return err
	}
	for _, agg := range plan.Aggregates {
		rel, err := AggregateRelPath(agg.TaskID, agg.Generation)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(canon, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		installed, err := DecodeAggregate(data)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(installed, agg) {
			return fmt.Errorf("installed aggregate %s/%d differs from plan", agg.TaskID, agg.Generation)
		}
		if agg.Current {
			pointerRel, err := CurrentPointerRelPath(agg.TaskID)
			if err != nil {
				return err
			}
			pointerData, err := os.ReadFile(filepath.Join(canon, filepath.FromSlash(pointerRel)))
			if err != nil {
				return err
			}
			gen, err := DecodeCurrentPointer(pointerData)
			if err != nil {
				return err
			}
			if gen != agg.Generation {
				return fmt.Errorf("installed current pointer for %s names generation %d, want %d", agg.TaskID, gen, agg.Generation)
			}
		}
	}
	for _, hold := range plan.Holds {
		if err := verifyInstalledRecord(canon, func() (string, error) { return HoldRelPath(hold.ID) }, func(data []byte) error {
			installed, err := DecodeHold(data)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(installed, hold) {
				return fmt.Errorf("installed hold %s differs from plan", hold.ID)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, rec := range plan.Interpretations {
		if err := verifyInstalledRecord(canon, func() (string, error) { return InterpretationRelPath(rec.ID) }, func(data []byte) error {
			installed, err := DecodeInterpretation(data)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(installed, rec) {
				return fmt.Errorf("installed interpretation %s differs from plan", rec.ID)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, dec := range plan.Decisions {
		if err := verifyInstalledRecord(canon, func() (string, error) { return DecisionRelPath(dec.Key) }, func(data []byte) error {
			installed, err := DecodeDecision(data)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(installed, dec) {
				return fmt.Errorf("installed decision %s differs from plan", dec.Key)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, want := range plan.Audits {
		rel, err := AuditRelPath(want.OperationID)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(canon, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("planned audit %s: %w", want.OperationID, err)
		}
		ev, err := DecodeAudit(data)
		if err != nil {
			return err
		}
		if ev.OperationID != want.OperationID || ev.Kind != want.Kind {
			return fmt.Errorf("installed audit %s does not match plan", want.OperationID)
		}
		if want.Kind == taskauthority.AuditLifecycle && (ev.TaskID != want.TaskID || ev.Generation != want.Generation || ev.After != want.After) {
			return fmt.Errorf("installed audit %s does not match planned lifecycle outcome", want.OperationID)
		}
	}
	store, err := NewStore(canon)
	if err != nil {
		return err
	}
	view, err := store.View()
	if err != nil {
		return err
	}
	if !recordSlicesEqual(view.Aggregates, plan.Aggregates) || !recordSlicesEqual(view.Holds, plan.Holds) ||
		!recordSlicesEqual(view.Interpretations, plan.Interpretations) || !recordSlicesEqual(view.Decisions, plan.Decisions) {
		return fmt.Errorf("converted store view differs from plan targets")
	}
	return nil
}

// recordSlicesEqual compares two sorted record sets by length and element,
// treating nil and empty slices as equal so omitted JSON fields round-trip
// exactly.
func recordSlicesEqual[T any](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func verifyInstalledRecord(canon string, rel func() (string, error), check func([]byte) error) error {
	path, err := rel()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(canon, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	return check(data)
}

// verifyAndArchiveV1Sources verifies any remaining legacy v1 sources against
// the plan digest and moves them into the migration archive; when none
// remain it verifies the recorded archive covers every planned source. The
// archive path is reused across retries.
func verifyAndArchiveV1Sources(plan *MigrationPlan, canon, journalArchivePath string) (string, error) {
	// A plan with no v1 sources has nothing to archive.
	if len(plan.Sources) == 0 {
		return "", nil
	}
	entries, err := collectSourceFiles(canon)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		if migrationSourceDigest(entries) != plan.SourceDigest {
			return "", fmt.Errorf("task-authority v1 source digest changed during apply: plan %s current %s", plan.SourceDigest, migrationSourceDigest(entries))
		}
		return archiveV1Sources(plan, canon, journalArchivePath, entries)
	}
	if journalArchivePath == "" {
		return "", fmt.Errorf("legacy v1 task-authority sources absent but no migration archive recorded")
	}
	if err := verifyArchive(plan, canon, journalArchivePath); err != nil {
		return "", err
	}
	return journalArchivePath, nil
}

// archiveV1Sources moves each remaining legacy location into the migration
// archive (idempotent: a location already moved on a prior attempt is
// skipped) and verifies the archive covers every planned source.
func archiveV1Sources(plan *MigrationPlan, canon, journalArchivePath string, entries []SourceFile) (string, error) {
	archivePath := journalArchivePath
	if archivePath == "" {
		archivePath = filepath.ToSlash(filepath.Join(migrationDir, fmt.Sprintf("archive-%d-%s", time.Now().Unix(), plan.SourceDigest)))
	}
	archiveRoot := filepath.Join(canon, filepath.FromSlash(archivePath))
	if err := os.MkdirAll(archiveRoot, DirPerm); err != nil {
		return "", err
	}
	moves := []struct{ srcRel, archiveRel string }{
		{v1AggregatesRelPath, "aggregates"},
		{v1WorktreeLeasesRelPath, "worktree-leases"},
		{v1DispatchControlRelPath, "dispatch"},
	}
	for _, m := range moves {
		src := filepath.Join(canon, filepath.FromSlash(m.srcRel))
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		dst := filepath.Join(archiveRoot, m.archiveRel)
		if err := os.Rename(src, dst); err != nil {
			if _, statErr := os.Stat(dst); statErr == nil {
				continue
			}
			return "", err
		}
	}
	if err := verifyArchive(plan, canon, archivePath); err != nil {
		return "", err
	}
	if v1SourcesRemain(canon) {
		return "", fmt.Errorf("legacy v1 task-authority source remains visible after archive")
	}
	return archivePath, nil
}

// verifyArchive checks every planned source file is archived with an
// identical digest and that no unexpected visible file hides in the archive.
func verifyArchive(plan *MigrationPlan, canon, archivePath string) error {
	archiveRoot := filepath.Join(canon, filepath.FromSlash(archivePath))
	expected := map[string]string{}
	for _, f := range plan.Sources {
		archivedRel, ok := archiveRelFor(f.Path)
		if !ok {
			return fmt.Errorf("source file %s has no archive mapping", f.Path)
		}
		expected[archivedRel] = f.SHA256
		data, err := os.ReadFile(filepath.Join(archiveRoot, filepath.FromSlash(archivedRel)))
		if err != nil {
			return fmt.Errorf("archived source %s: %w", f.Path, err)
		}
		if DigestHex(data) != f.SHA256 {
			return fmt.Errorf("archived source %s digest mismatch", f.Path)
		}
	}
	return filepath.WalkDir(archiveRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(archiveRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(filepath.Base(rel), ".") {
			return nil
		}
		if _, ok := expected[rel]; !ok {
			return fmt.Errorf("unexpected archived file %s", rel)
		}
		return nil
	})
}

// archiveRelFor maps a home-relative v1 source path to its archive-relative
// path.
func archiveRelFor(homeRel string) (string, bool) {
	switch {
	case strings.HasPrefix(homeRel, v1AggregatesRelPath+"/"):
		return "aggregates/" + strings.TrimPrefix(homeRel, v1AggregatesRelPath+"/"), true
	case strings.HasPrefix(homeRel, v1WorktreeLeasesRelPath+"/"):
		return "worktree-leases/" + strings.TrimPrefix(homeRel, v1WorktreeLeasesRelPath+"/"), true
	case strings.HasPrefix(homeRel, v1DispatchControlRelPath+"/"):
		return "dispatch/" + strings.TrimPrefix(homeRel, v1DispatchControlRelPath+"/"), true
	}
	return "", false
}

// collectSourceFiles hashes every visible regular file under the three
// legacy v1 source locations, reproducing the plan's source inventory so a
// digest comparison is exact. Non-regular entries fail closed.
func collectSourceFiles(homeDir string) ([]SourceFile, error) {
	var out []SourceFile
	for _, rel := range []string{v1AggregatesRelPath, v1WorktreeLeasesRelPath, v1DispatchControlRelPath} {
		root := filepath.Join(homeDir, filepath.FromSlash(rel))
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				// The walk root may itself be hidden (state/.dispatch); only
				// skip hidden entries below the root.
				if entry.IsDir() && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
				return fmt.Errorf("legacy v1 source entry under %s is not a regular file", rel)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			r, err := filepath.Rel(homeDir, path)
			if err != nil {
				return err
			}
			out = append(out, SourceFile{Path: filepath.ToSlash(r), Schema: schemaForV1Path(filepath.ToSlash(r)), SHA256: DigestHex(data)})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// schemaForV1Path reproduces the plan's per-file schema labels so digest
// comparisons are exact.
func schemaForV1Path(rel string) string {
	switch {
	case strings.HasPrefix(rel, v1AggregatesRelPath+"/"):
		if strings.HasSuffix(rel, "/"+currentFileName) {
			return "aggregate_pointer"
		}
		return "aggregate"
	case strings.HasPrefix(rel, v1WorktreeLeasesRelPath+"/"):
		return "worktree_lease"
	case strings.HasPrefix(rel, v1DispatchControlRelPath+"/holds/"):
		return "dispatch_hold"
	case strings.HasPrefix(rel, v1DispatchControlRelPath+"/interpretations/"):
		return "dispatch_interpretation"
	case strings.HasPrefix(rel, v1DispatchControlRelPath+"/decisions/"):
		return "dispatch_decision"
	}
	return "unrecognized"
}

// v1SourcesRemain reports whether any recognized legacy v1 source location
// still has visible entries.
func v1SourcesRemain(homeDir string) bool {
	for _, rel := range []string{v1AggregatesRelPath, v1WorktreeLeasesRelPath, v1DispatchControlRelPath} {
		if has, err := hasVisibleEntries(filepath.Join(homeDir, filepath.FromSlash(rel))); err == nil && has {
			return true
		}
	}
	return false
}

// anyV1SourceVisible is v1SourcesRemain with an error.
func anyV1SourceVisible(homeDir string) (bool, error) {
	for _, rel := range []string{v1AggregatesRelPath, v1WorktreeLeasesRelPath, v1DispatchControlRelPath} {
		has, err := hasVisibleEntries(filepath.Join(homeDir, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

// writeMigrationJSON renders one migration-owned document deterministically.
func writeMigrationJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeMigrationFile(path, append(data, '\n'))
}

// writeMigrationFile writes migration-owned files (plans, journal, receipt,
// manifest, staged documents) atomically with private permissions. These
// live outside the authority root, so the store's ensureDirSafe cannot be
// used for them.
func writeMigrationFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("creating migration directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, DirPerm); err != nil {
		return fmt.Errorf("securing migration directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("creating migration temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(FilePerm); err != nil {
		tmp.Close()
		return fmt.Errorf("securing migration temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing migration temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing migration temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing migration temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming migration file into place: %w", err)
	}
	return nil
}

func writeMigrationJournal(homeDir string, journal *migrationJournal) error {
	return writeMigrationJSON(migrationJournalPath(homeDir), journal)
}

func readMigrationJournal(homeDir string) (*migrationJournal, bool, error) {
	data, err := os.ReadFile(migrationJournalPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var journal migrationJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, false, fmt.Errorf("corrupt task-authority migration journal: %w", err)
	}
	if journal.SchemaVersion != MigrationPlanSchema {
		return nil, false, fmt.Errorf("unsupported task-authority migration journal schema %q", journal.SchemaVersion)
	}
	return &journal, true, nil
}
