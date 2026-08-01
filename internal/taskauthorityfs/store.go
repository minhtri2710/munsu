package taskauthorityfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Sentinel errors for the filesystem read path, reachable through errors.Is.
var (
	// ErrMigrationRequired reports legacy v1 task-authority state: canonical
	// reads refuse to serve until explicit migration runs. Migration is never
	// automatic.
	ErrMigrationRequired = errors.New("task-authority v1 state requires explicit migration")
	// ErrRecoveryRequired reports an interrupted transaction: a transaction
	// manifest exists, so canonical reads are unsafe until recovery runs.
	ErrRecoveryRequired = errors.New("task-authority transaction recovery required")
)

// MigrationRequiredError is a typed, inspectable error for homes that still
// carry legacy v1 task-authority records. V1Location names the first legacy
// record location (for example "state/.task-authority/aggregates").
type MigrationRequiredError struct {
	V1Location string
}

func (e *MigrationRequiredError) Error() string {
	return fmt.Sprintf("task-authority v1 state at %s requires explicit migration, never automatic", e.V1Location)
}

func (e *MigrationRequiredError) Unwrap() error { return ErrMigrationRequired }

// RecoveryRequiredError is a typed, inspectable error for interrupted
// transactions. ManifestPath names the first transaction manifest that blocks
// canonical reads until recovery runs.
type RecoveryRequiredError struct {
	ManifestPath string
}

func (e *RecoveryRequiredError) Error() string {
	return fmt.Sprintf("task-authority transaction at %s requires recovery before canonical reads", e.ManifestPath)
}

func (e *RecoveryRequiredError) Unwrap() error { return ErrRecoveryRequired }

// Store is the read-only filesystem adapter below taskauthority.Store. It
// loads one consistent canonical View from committed v2 documents and fails
// closed on legacy v1 state, interrupted transactions, and corrupt or
// contradictory records. It contains no lifecycle rules. Journaled Update,
// recovery, and idempotency land in the transaction slice; this file only
// implements the read path and the lock primitives Update will compose.
type Store struct {
	homeDir string
}

// NewStore constructs a read-only filesystem Store for the authority state
// under homeDir. Construction is side-effect free: it creates nothing,
// migrates nothing, and fails only when homeDir is empty or resolves to a
// non-directory. Canonical state is loaded by View.
func NewStore(homeDir string) (*Store, error) {
	if strings.TrimSpace(homeDir) == "" {
		return nil, fmt.Errorf("taskauthorityfs: empty home directory")
	}
	if info, err := os.Stat(homeDir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("taskauthorityfs: home %s is not a directory", homeDir)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return &Store{homeDir: homeDir}, nil
}

// View returns one canonical committed snapshot of the authority state. It
// fails closed in three cases: legacy v1 records exist anywhere under the
// home (migration is explicit and never automatic), any transaction manifest
// exists (an interrupted update requires recovery), or any committed document
// is corrupt, identity-mismatched, duplicated, or contradicts the current
// pointer. The entire read — v1 check, manifest check, and every record load
// — runs under state/.dispatch.lock, the same lock Update composes, so an
// update can never interleave and expose a torn snapshot.
func (s *Store) View() (taskauthority.View, error) {
	var view taskauthority.View
	err := withDispatchLock(s.homeDir, func() error {
		rel, hasV1, err := v1RecordLocation(s.homeDir)
		if err != nil {
			return err
		}
		if hasV1 {
			return &MigrationRequiredError{V1Location: rel}
		}
		if err := s.checkPendingManifests(); err != nil {
			return err
		}
		aggregates, err := s.loadAggregates()
		if err != nil {
			return err
		}
		holds, err := s.loadHolds()
		if err != nil {
			return err
		}
		interpretations, err := s.loadInterpretations()
		if err != nil {
			return err
		}
		decisions, err := s.loadDecisions()
		if err != nil {
			return err
		}
		receipts, err := s.loadReceipts()
		if err != nil {
			return err
		}
		audit, err := s.loadAudit()
		if err != nil {
			return err
		}
		view = taskauthority.View{
			Aggregates:      aggregates,
			Holds:           holds,
			Interpretations: interpretations,
			Decisions:       decisions,
			Receipts:        receipts,
			Audit:           audit,
		}
		return nil
	})
	return view, err
}

// checkPendingManifests fails closed when any transaction manifest exists:
// an interrupted update must recover before canonical reads. Corrupt or
// identity-mismatched manifests are corruption, not recoverable state.
func (s *Store) checkPendingManifests() error {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(transactionsDir))
	files, err := recordFiles("transaction_manifest", dir)
	if err != nil {
		return err
	}
	var first string
	for _, name := range files {
		rawID, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			return corruptDocument("transaction_manifest", "operation_id", "invalid manifest filename %q: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		manifest, err := DecodeTransactionManifest(data)
		if err != nil {
			return err
		}
		if manifest.OperationID != rawID {
			return corruptDocument("transaction_manifest", "operation_id", "manifest operation id %q does not match filename %q", manifest.OperationID, name)
		}
		if first == "" {
			first = filepath.ToSlash(filepath.Join(transactionsDir, name))
		}
	}
	if first != "" {
		return &RecoveryRequiredError{ManifestPath: first}
	}
	return nil
}

// rejectSymlinkEntry fails closed when entry is a symbolic link. The
// authority root never follows links — even one that would resolve back
// inside the root — so any link is corruption, never a document.
func rejectSymlinkEntry(document, field string, entry os.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return corruptDocument(document, field, "symlink entry %q is not allowed in the authority root", entry.Name())
	}
	return nil
}

// requireRegularFile fails closed when path is not a regular file, so a
// current pointer or document is never read through a symlink.
func requireRegularFile(document, field, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return corruptDocument(document, field, "entry %q is not a regular file", path)
	}
	return nil
}

// recordFiles returns the visible canonical *.json filenames in dir, sorted.
// Directories, symlinks, and unexpected visible entries fail closed; hidden
// entries and a missing dir are ignored.
func recordFiles(family, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if err := rejectSymlinkEntry(family, "", entry); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(name, documentExt) {
			return nil, corruptDocument(family, "", "unexpected entry %q", name)
		}
		files = append(files, name)
	}
	sort.Strings(files)
	return files, nil
}

// loadAggregates loads every task directory, failing closed on corrupt or
// contradictory records. Results are sorted by task id then generation.
func (s *Store) loadAggregates() ([]taskauthority.Aggregate, error) {
	root := filepath.Join(s.homeDir, filepath.FromSlash(aggregatesDir))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []taskauthority.Aggregate
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if err := rejectSymlinkEntry("aggregate", "", entry); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			return nil, corruptDocument("aggregate", "", "unexpected entry %q in aggregates root", name)
		}
		if err := validateTaskID(name); err != nil {
			return nil, corruptDocument("aggregate", "", "invalid task directory %q: %v", name, err)
		}
		aggs, err := s.loadTaskAggregates(name)
		if err != nil {
			return nil, err
		}
		out = append(out, aggs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TaskID != out[j].TaskID {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].Generation < out[j].Generation
	})
	return out, nil
}

// loadTaskAggregates loads one task's aggregate documents and cross-checks
// them against the current pointer. A visible task directory must carry a
// valid current pointer and exactly one matching current document; every
// other document must be historical. Filenames must be canonical generation
// identities, so leading-zero aliases and content/filename mismatches fail
// closed instead of selecting a record.
func (s *Store) loadTaskAggregates(taskID string) ([]taskauthority.Aggregate, error) {
	taskDir := filepath.Join(s.homeDir, filepath.FromSlash(aggregatesDir), taskID)
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return nil, err
	}
	var (
		pointerFound bool
		aggFiles     []string
	)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if err := rejectSymlinkEntry("aggregate", "", entry); err != nil {
			return nil, err
		}
		switch {
		case name == currentFileName:
			pointerFound = true
		case !entry.IsDir() && strings.HasSuffix(name, documentExt):
			aggFiles = append(aggFiles, name)
		default:
			return nil, corruptDocument("aggregate", "", "task %s: unexpected entry %q", taskID, name)
		}
	}
	if !pointerFound {
		return nil, corruptDocument("aggregate", "current", "task %s has no current pointer", taskID)
	}
	if err := requireRegularFile("aggregate", "current", filepath.Join(taskDir, currentFileName)); err != nil {
		return nil, err
	}
	pointerData, err := os.ReadFile(filepath.Join(taskDir, currentFileName))
	if err != nil {
		return nil, err
	}
	pointerGen, err := DecodeCurrentPointer(pointerData)
	if err != nil {
		return nil, err
	}
	var out []taskauthority.Aggregate
	currentSeen := false
	for _, name := range aggFiles {
		gen, err := taskauthority.ParseGeneration(strings.TrimSuffix(name, documentExt))
		if err != nil || name != gen.String()+documentExt {
			return nil, corruptDocument("aggregate", "generation", "task %s: non-canonical aggregate filename %q (duplicate or malformed generation alias)", taskID, name)
		}
		data, err := os.ReadFile(filepath.Join(taskDir, name))
		if err != nil {
			return nil, err
		}
		agg, err := DecodeAggregate(data)
		if err != nil {
			return nil, err
		}
		if agg.TaskID != taskID {
			return nil, corruptDocument("aggregate", "task_id", "document in task dir %s identifies task %q", taskID, agg.TaskID)
		}
		if agg.Generation != gen {
			return nil, corruptDocument("aggregate", "generation", "task %s: file %q contains generation %d", taskID, name, agg.Generation)
		}
		switch {
		case gen == pointerGen:
			if !agg.Current {
				return nil, corruptDocument("aggregate", "current", "task %s: current pointer names generation %d but its document is not current", taskID, gen)
			}
			currentSeen = true
		default:
			if agg.Current {
				return nil, corruptDocument("aggregate", "current", "task %s: generation %d marked current but pointer names generation %d (multiple currents)", taskID, gen, pointerGen)
			}
		}
		out = append(out, agg)
	}
	if !currentSeen {
		return nil, corruptDocument("aggregate", "current", "task %s: current pointer names generation %d but no matching current document exists (pointer divergence)", taskID, pointerGen)
	}
	return out, nil
}

// loadHolds loads every dispatch hold document, failing closed when a
// document identity disagrees with its filename. Results are sorted by id.
func (s *Store) loadHolds() ([]taskauthority.DispatchHold, error) {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(holdsDir))
	files, err := recordFiles("hold", dir)
	if err != nil {
		return nil, err
	}
	out := make([]taskauthority.DispatchHold, 0, len(files))
	for _, name := range files {
		rawID, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			return nil, corruptDocument("hold", "id", "invalid hold filename %q: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		hold, err := DecodeHold(data)
		if err != nil {
			return nil, err
		}
		if hold.ID != rawID {
			return nil, corruptDocument("hold", "id", "hold document id %q does not match filename %q", hold.ID, name)
		}
		out = append(out, hold)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// loadInterpretations loads every dispatch interpretation document, failing
// closed when a document identity disagrees with its filename. Results are
// sorted by id.
func (s *Store) loadInterpretations() ([]taskauthority.DispatchInterpretation, error) {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(interpretationsDir))
	files, err := recordFiles("interpretation", dir)
	if err != nil {
		return nil, err
	}
	out := make([]taskauthority.DispatchInterpretation, 0, len(files))
	for _, name := range files {
		rawID, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			return nil, corruptDocument("interpretation", "id", "invalid interpretation filename %q: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		rec, err := DecodeInterpretation(data)
		if err != nil {
			return nil, err
		}
		if rec.ID != rawID {
			return nil, corruptDocument("interpretation", "id", "interpretation document id %q does not match filename %q", rec.ID, name)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// loadDecisions loads every dispatch decision document, failing closed when
// a document key disagrees with its filename. Results are sorted by key.
func (s *Store) loadDecisions() ([]taskauthority.DispatchDecision, error) {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(decisionsDir))
	files, err := recordFiles("decision", dir)
	if err != nil {
		return nil, err
	}
	out := make([]taskauthority.DispatchDecision, 0, len(files))
	for _, name := range files {
		rawKey, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			return nil, corruptDocument("decision", "key", "invalid decision filename %q: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		dec, err := DecodeDecision(data)
		if err != nil {
			return nil, err
		}
		if dec.Key != rawKey {
			return nil, corruptDocument("decision", "key", "decision document key %q does not match filename %q", dec.Key, name)
		}
		out = append(out, dec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// loadReceipts loads every Task Operation receipt document, failing closed
// when a document operation id disagrees with its filename. Results are
// sorted by operation id.
func (s *Store) loadReceipts() ([]taskauthority.Receipt, error) {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(receiptsDir))
	files, err := recordFiles("receipt", dir)
	if err != nil {
		return nil, err
	}
	out := make([]taskauthority.Receipt, 0, len(files))
	for _, name := range files {
		rawID, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			return nil, corruptDocument("receipt", "operation_id", "invalid receipt filename %q: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		receipt, err := DecodeReceipt(data)
		if err != nil {
			return nil, err
		}
		if receipt.OperationID != rawID {
			return nil, corruptDocument("receipt", "operation_id", "receipt operation id %q does not match filename %q", receipt.OperationID, name)
		}
		out = append(out, receipt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperationID < out[j].OperationID })
	return out, nil
}

// loadAudit loads every typed audit event document, failing closed when a
// document operation id disagrees with its filename. Results are sorted by
// timestamp then operation id.
func (s *Store) loadAudit() ([]taskauthority.AuditEvent, error) {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(auditDir))
	files, err := recordFiles("audit", dir)
	if err != nil {
		return nil, err
	}
	out := make([]taskauthority.AuditEvent, 0, len(files))
	for _, name := range files {
		rawID, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
		if err != nil {
			return nil, corruptDocument("audit", "operation_id", "invalid audit filename %q: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		ev, err := DecodeAudit(data)
		if err != nil {
			return nil, err
		}
		if ev.OperationID != rawID {
			return nil, corruptDocument("audit", "operation_id", "audit operation id %q does not match filename %q", ev.OperationID, name)
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].OperationID < out[j].OperationID
	})
	return out, nil
}
