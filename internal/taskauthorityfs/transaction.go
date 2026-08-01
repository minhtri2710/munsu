package taskauthorityfs

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// faultStage identifies a deterministic crash point inside Update. The hooks
// are package-private and nil in production; tests inject a fault at every
// stage and prove recovery converges to the fully committed view and receipt.
type faultStage int

const (
	faultStageNone faultStage = iota
	// faultStageBeforeManifest fires before the pending manifest is durable.
	faultStageBeforeManifest
	// faultStageAfterManifest fires after the pending manifest is durable and
	// before the first data write.
	faultStageAfterManifest
	// faultStageAfterDataWrite fires after each data entry write; afterWrite
	// selects the Nth write (1-based).
	faultStageAfterDataWrite
	// faultStageBeforeCommit fires after all data writes and before the
	// manifest is durably flipped to committed.
	faultStageBeforeCommit
	// faultStageAfterCommit fires after the committed marker and before
	// cleanup retires the manifest.
	faultStageAfterCommit
	// faultStageDuringCleanup fires after the commit marker and before the
	// manifest is removed.
	faultStageDuringCleanup
)

// faultInjector simulates a process crash by making Update return err at the
// configured stage. afterWrite selects the Nth data write (1-based) and is
// meaningful only for faultStageAfterDataWrite.
type faultInjector struct {
	stage      faultStage
	afterWrite int
	err        error
}

// faultAt returns the injected failure when a fault is configured for stage
// after writes data writes, and nil otherwise.
func (s *Store) faultAt(stage faultStage, writes int) error {
	f := s.fault
	if f == nil || f.stage != stage {
		return nil
	}
	if stage == faultStageAfterDataWrite && writes != f.afterWrite {
		return nil
	}
	return f.err
}

// Update serializes one authoritative mutation. Under the dispatch lock it
// completes any interrupted transaction, loads one committed view, runs fn
// against a transaction over that view, and commits the staged changes
// through a write-ahead manifest: pending manifest, idempotent data writes,
// durable commit marker, then safe manifest retirement. Repeating a
// committed Operation ID with the same digest returns the original receipt
// with Replayed=true without re-running fn; a changed digest conflicts.
// Callback and validation errors write nothing.
func (s *Store) Update(op taskauthority.Operation, fn func(tx *taskauthority.Tx) error) (taskauthority.Receipt, error) {
	var receipt taskauthority.Receipt
	err := withDispatchLock(s.homeDir, func() error {
		var err error
		receipt, err = s.updateLocked(op, fn)
		return err
	})
	return receipt, err
}

func (s *Store) updateLocked(op taskauthority.Operation, fn func(tx *taskauthority.Tx) error) (taskauthority.Receipt, error) {
	rel, hasV1, err := v1RecordLocation(s.homeDir)
	if err != nil {
		return taskauthority.Receipt{}, err
	}
	if hasV1 {
		return taskauthority.Receipt{}, &MigrationRequiredError{V1Location: rel}
	}
	if err := s.recoverLocked(); err != nil {
		return taskauthority.Receipt{}, err
	}
	view, err := s.loadViewLocked()
	if err != nil {
		return taskauthority.Receipt{}, err
	}

	if err := op.Validate(); err != nil {
		return taskauthority.Receipt{}, err
	}
	// Operation.Validate checks digest length only; the manifest pins digests
	// and must never accept a non-hex one, so the filesystem adapter enforces
	// full 64-hex shape before the callback can run.
	if !validDigest(op.Digest) {
		return taskauthority.Receipt{}, fmt.Errorf("%w: operation %s digest %q is not a 64-hex sha256 digest", taskauthority.ErrInvalidInput, op.ID, op.Digest)
	}
	if receipt, ok := committedReceipt(view.Receipts, op.ID); ok {
		if receipt.Digest != op.Digest {
			return taskauthority.Receipt{}, domain.NewError(domain.ErrorConflict,
				fmt.Sprintf("operation %s reused with different intent", op.ID),
				domain.RetryNever, taskauthority.ErrOperationConflict)
		}
		receipt.Replayed = true
		return receipt, nil
	}

	tx := taskauthority.NewTx(view)
	if err := fn(tx); err != nil {
		return taskauthority.Receipt{}, fmt.Errorf("taskauthorityfs: update %s callback: %w", op.ID, err)
	}
	builder := newManifestBuilder(op.ID, view)
	if err := tx.Apply(builder); err != nil {
		return taskauthority.Receipt{}, fmt.Errorf("taskauthorityfs: update %s validation: %w", op.ID, err)
	}

	// Preserve .dispatch.lock → <taskID>.meta.lock: a transaction that stages
	// no task aggregate is covered by the dispatch lock alone; one that stages
	// a single task takes that task's lock while dispatch remains held.
	// Multi-task staging is rejected rather than inventing lock semantics.
	switch tasks := builder.stagedTaskIDs(); len(tasks) {
	case 0:
	case 1:
		taskPath, err := taskLockPath(s.homeDir, tasks[0])
		if err != nil {
			return taskauthority.Receipt{}, err
		}
		taskFile, err := lockFile(taskPath)
		if err != nil {
			return taskauthority.Receipt{}, err
		}
		defer releaseLock(taskFile)
	default:
		return taskauthority.Receipt{}, fmt.Errorf("taskauthorityfs: update %s stages %d tasks (%s); multi-task transactions are not supported", op.ID, len(tasks), strings.Join(tasks, ", "))
	}

	manifest, receipt, err := builder.manifest(s.homeDir, op.Digest, time.Now().UnixNano())
	if err != nil {
		return taskauthority.Receipt{}, err
	}

	if err := s.faultAt(faultStageBeforeManifest, 0); err != nil {
		return taskauthority.Receipt{}, err
	}
	manifestRel, err := TransactionManifestRelPath(op.ID)
	if err != nil {
		return taskauthority.Receipt{}, err
	}
	manifestPath := filepath.Join(s.homeDir, filepath.FromSlash(manifestRel))
	manifestData, err := EncodeTransactionManifest(manifest)
	if err != nil {
		return taskauthority.Receipt{}, err
	}
	if err := writeFileAtomic(s.homeDir, manifestPath, manifestData); err != nil {
		return taskauthority.Receipt{}, err
	}
	if err := s.faultAt(faultStageAfterManifest, 0); err != nil {
		return taskauthority.Receipt{}, err
	}

	before := indexBeforeEntries(manifest.Before)
	for i, entry := range manifest.After {
		if err := applyManifestEntry(s.homeDir, manifestRel, entry, before); err != nil {
			return taskauthority.Receipt{}, fmt.Errorf("taskauthorityfs: update %s applying %s: %w", op.ID, entry.Path, err)
		}
		if err := s.faultAt(faultStageAfterDataWrite, i+1); err != nil {
			return taskauthority.Receipt{}, err
		}
	}

	if err := s.faultAt(faultStageBeforeCommit, 0); err != nil {
		return taskauthority.Receipt{}, err
	}
	committed := manifest
	committed.State = ManifestCommitted
	committedData, err := EncodeTransactionManifest(committed)
	if err != nil {
		return taskauthority.Receipt{}, err
	}
	if err := writeFileAtomic(s.homeDir, manifestPath, committedData); err != nil {
		return taskauthority.Receipt{}, err
	}
	if err := s.faultAt(faultStageAfterCommit, 0); err != nil {
		return taskauthority.Receipt{}, err
	}

	if err := s.faultAt(faultStageDuringCleanup, 0); err != nil {
		return taskauthority.Receipt{}, err
	}
	if err := removeManifest(manifestPath); err != nil {
		return taskauthority.Receipt{}, err
	}
	return receipt, nil
}

func committedReceipt(receipts []taskauthority.Receipt, id string) (taskauthority.Receipt, bool) {
	for _, receipt := range receipts {
		if receipt.OperationID == id {
			return receipt, true
		}
	}
	return taskauthority.Receipt{}, false
}

// stagedOutcome is the lifecycle result of the single current aggregate
// staged by a transaction, mirroring taskauthority.Tx.Outcome for the
// receipt persisted in the manifest.
type stagedOutcome struct {
	taskID     string
	generation taskauthority.Generation
	revision   taskauthority.Revision
	phase      taskauthority.Phase
}

// manifestBuilder records every staged change during tx.Apply and
// materializes the deterministic transaction manifest: all staged
// aggregate/hold/interpretation/decision/audit documents, derived current
// pointer replacements, and the durable operation receipt. Apply validation
// runs before the builder is asked for the manifest, so a manifest is never
// written for an invalid staged set.
type manifestBuilder struct {
	opID string
	view taskauthority.View

	entries       map[string]string // rel path -> document payload (later staged write wins)
	currentByTask map[string]taskauthority.Generation
	stagedTasks   map[string]bool
	audit         *taskauthority.AuditEvent
	outcome       *stagedOutcome
}

func newManifestBuilder(opID string, view taskauthority.View) *manifestBuilder {
	return &manifestBuilder{
		opID:          opID,
		view:          view,
		entries:       map[string]string{},
		currentByTask: map[string]taskauthority.Generation{},
		stagedTasks:   map[string]bool{},
	}
}

func (b *manifestBuilder) ApplyAggregate(agg taskauthority.Aggregate) error {
	rel, err := AggregateRelPath(agg.TaskID, agg.Generation)
	if err != nil {
		return err
	}
	data, err := EncodeAggregate(agg)
	if err != nil {
		return err
	}
	b.entries[rel] = string(data)
	b.stagedTasks[agg.TaskID] = true
	if agg.Current {
		b.currentByTask[agg.TaskID] = agg.Generation
		b.outcome = &stagedOutcome{taskID: agg.TaskID, generation: agg.Generation, revision: agg.Revision, phase: agg.Phase}
	}
	return nil
}

func (b *manifestBuilder) ApplyHold(hold taskauthority.DispatchHold) error {
	rel, err := HoldRelPath(hold.ID)
	if err != nil {
		return err
	}
	data, err := EncodeHold(hold)
	if err != nil {
		return err
	}
	b.entries[rel] = string(data)
	return nil
}

func (b *manifestBuilder) ApplyInterpretation(rec taskauthority.DispatchInterpretation) error {
	rel, err := InterpretationRelPath(rec.ID)
	if err != nil {
		return err
	}
	data, err := EncodeInterpretation(rec)
	if err != nil {
		return err
	}
	b.entries[rel] = string(data)
	return nil
}

func (b *manifestBuilder) ApplyDecision(dec taskauthority.DispatchDecision) error {
	rel, err := DecisionRelPath(dec.Key)
	if err != nil {
		return err
	}
	data, err := EncodeDecision(dec)
	if err != nil {
		return err
	}
	b.entries[rel] = string(data)
	return nil
}

func (b *manifestBuilder) ApplyAudit(ev taskauthority.AuditEvent) error {
	if ev.OperationID != b.opID {
		return fmt.Errorf("audit event operation id %q does not match operation %q", ev.OperationID, b.opID)
	}
	if b.audit != nil {
		return fmt.Errorf("operation %s stages multiple audit events; one typed audit event per operation is supported", b.opID)
	}
	rel, err := AuditRelPath(ev.OperationID)
	if err != nil {
		return err
	}
	data, err := EncodeAudit(ev)
	if err != nil {
		return err
	}
	b.entries[rel] = string(data)
	cp := ev
	b.audit = &cp
	return nil
}

// stagedTaskIDs returns the distinct staged task IDs in deterministic order.
func (b *manifestBuilder) stagedTaskIDs() []string {
	out := make([]string, 0, len(b.stagedTasks))
	for id := range b.stagedTasks {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// manifest materializes the deterministic pending manifest and its receipt.
// After entries are sorted by canonical relative path and cover every staged
// document, every derived current pointer replacement, and the receipt;
// before entries pin the current digest of every file the transaction will
// create or replace.
func (b *manifestBuilder) manifest(homeDir, digest string, now int64) (TransactionManifest, taskauthority.Receipt, error) {
	for taskID, gen := range b.currentByTask {
		committed, ok := b.view.Current(taskID)
		if ok && committed.Generation == gen {
			continue
		}
		rel, err := CurrentPointerRelPath(taskID)
		if err != nil {
			return TransactionManifest{}, taskauthority.Receipt{}, err
		}
		data, err := EncodeCurrentPointer(gen)
		if err != nil {
			return TransactionManifest{}, taskauthority.Receipt{}, err
		}
		b.entries[rel] = string(data)
	}

	receipt := b.receipt(digest, now)
	receiptRel, err := ReceiptRelPath(b.opID)
	if err != nil {
		return TransactionManifest{}, taskauthority.Receipt{}, err
	}
	receiptData, err := EncodeReceipt(receipt)
	if err != nil {
		return TransactionManifest{}, taskauthority.Receipt{}, err
	}
	b.entries[receiptRel] = string(receiptData)

	paths := make([]string, 0, len(b.entries))
	for rel := range b.entries {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	after := make([]ManifestEntry, 0, len(paths))
	for _, rel := range paths {
		payload := b.entries[rel]
		after = append(after, ManifestEntry{Path: rel, Digest: DigestHex([]byte(payload)), Payload: payload})
	}

	var before []ManifestEntry
	for _, entry := range after {
		abs := filepath.Join(homeDir, filepath.FromSlash(entry.Path))
		data, exists, err := readDocument(abs)
		if err != nil {
			return TransactionManifest{}, taskauthority.Receipt{}, err
		}
		if exists {
			before = append(before, ManifestEntry{Path: entry.Path, Digest: DigestHex(data)})
		}
	}

	var expected taskauthority.Generation
	if len(b.currentByTask) == 1 {
		for _, gen := range b.currentByTask {
			expected = gen
		}
	}

	manifest := TransactionManifest{
		OperationID:        b.opID,
		Digest:             digest,
		ExpectedGeneration: expected,
		Before:             before,
		After:              after,
		Audit:              b.audit,
		State:              ManifestPending,
		CreatedAt:          now,
	}
	return manifest, receipt, nil
}

// receipt builds the durable operation receipt: identity, intent digest,
// committed timestamp, and — when the transaction stages a current aggregate
// — the lifecycle outcome with the reopened flag read back from the view.
func (b *manifestBuilder) receipt(digest string, now int64) taskauthority.Receipt {
	receipt := taskauthority.Receipt{OperationID: b.opID, Digest: digest, CommittedAt: now}
	if b.outcome != nil {
		receipt.TaskID = b.outcome.taskID
		receipt.Generation = b.outcome.generation
		receipt.Revision = b.outcome.revision
		receipt.Phase = b.outcome.phase
		if prior, ok := b.view.Current(b.outcome.taskID); ok && prior.Generation != b.outcome.generation {
			receipt.Reopened = true
		}
	}
	return receipt
}
