package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ChangeItem is one durable write within a change-set commit. root is a logical
// root name, key is a contained logical key within that root, and data is the
// encoded bytes supplied by the owning module. digest is an optional module
// supplied checksum recorded in the journal for audit trail.
type ChangeItem struct {
	Root   string `json:"root"`
	Key    string `json:"key"`
	Data   []byte `json:"data"`
	Digest string `json:"digest,omitempty"`
}

// journalRecord is the durable write-ahead intent for one change-set commit.
// Recovery redoes a record only when the scope revision shows the commit was
// interrupted before its revision advance; an already-applied or superseded
// record is discarded without re-applying, so recovery never overwrites newer
// committed data (see recoverRecord).
type journalRecord struct {
	TxnID            string       `json:"txn_id"`
	Scope            string       `json:"scope"`
	FenceToken       uint64       `json:"fence_token"`
	ExpectedRevision uint64       `json:"expected_revision"`
	NewRevision      uint64       `json:"new_revision"`
	Items            []ChangeItem `json:"items"`
	Committed        bool         `json:"committed"`
}

// Commit durably applies a change-set atomically under the held scoped lock.
// It verifies optimistic concurrency (expectedRevision must match the current
// scope revision) and fencing (lk must still be held). A write-ahead journal
// record is fsynced before the items are applied, so an interrupted commit is
// recovered mechanically on the next Open. It returns the new scope revision.
func (h *Home) Commit(lk *Lock, txnID string, expectedRevision uint64, items []ChangeItem) (uint64, error) {
	if lk == nil || lk.released {
		return 0, ErrFenced
	}
	if err := validateTxnID(txnID); err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, ErrEmptyChangeset
	}
	// Validate every item's containment before writing any journal intent.
	for _, it := range items {
		if _, err := h.Path(it.Root, it.Key); err != nil {
			return 0, err
		}
	}

	cur, err := h.readRevision(lk.scope)
	if err != nil {
		return 0, err
	}
	if cur != expectedRevision {
		return 0, ErrConflict
	}
	newRev := cur + 1

	rec := journalRecord{
		TxnID:            txnID,
		Scope:            lk.scope,
		FenceToken:       uint64(lk.token),
		ExpectedRevision: expectedRevision,
		NewRevision:      newRev,
		Items:            items,
	}
	if err := h.writeJournalRecord(rec); err != nil {
		return 0, err
	}
	for _, it := range items {
		if err := h.applyItem(it); err != nil {
			return 0, err
		}
	}
	if err := h.writeRevision(lk.scope, newRev); err != nil {
		return 0, err
	}
	rec.Committed = true
	if err := h.writeJournalRecord(rec); err != nil {
		return 0, err
	}
	if err := os.Remove(h.journalPath(lk.scope, txnID)); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("home: remove journal record: %w", err)
	}
	return newRev, nil
}

// recover replays interrupted write-ahead journal records. Each leftover record
// is recovered under its own scope lock so recovery never interleaves with a
// live commit; see recoverRecord for the redo-versus-discard decision.
func (h *Home) recover() error {
	entries, err := os.ReadDir(h.journalDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("home: read journal: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if err := h.recoverRecord(name); err != nil {
			return err
		}
	}
	return nil
}

// recoverRecord recovers one journal record under its scope lock. It redoes the
// commit only when the scope revision proves it was interrupted before the
// revision advance (cur == ExpectedRevision); a record whose items are already
// durable or have been superseded by a later commit (cur >= NewRevision) is
// discarded without re-applying, so recovery cannot overwrite newer committed
// data. A revision inconsistent with the record is corruption and fails Open.
func (h *Home) recoverRecord(name string) error {
	path := filepath.Join(h.journalDir(), name)
	// Peek the record only to learn its scope; the authoritative read happens
	// under the lock, since a concurrent recovery may remove it in between.
	scope, err := h.peekRecordScope(path)
	if err != nil {
		return err
	}
	if scope == "" {
		return nil
	}
	lk, err := h.Lock(scope)
	if err != nil {
		return err
	}
	defer lk.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("home: read journal record: %w", err)
	}
	var rec journalRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return fmt.Errorf("home: decode journal record %s: %w", name, err)
	}
	if rec.NewRevision != rec.ExpectedRevision+1 {
		return fmt.Errorf("home: corrupt journal record %s: new revision %d not one past expected %d", name, rec.NewRevision, rec.ExpectedRevision)
	}
	cur, err := h.readRevision(rec.Scope)
	if err != nil {
		return err
	}
	if cur < rec.ExpectedRevision {
		return fmt.Errorf("home: corrupt journal record %s: scope revision %d behind expected %d", name, cur, rec.ExpectedRevision)
	}
	if cur == rec.ExpectedRevision {
		for _, it := range rec.Items {
			if err := h.applyItem(it); err != nil {
				return fmt.Errorf("home: recover item: %w", err)
			}
		}
		if err := h.writeRevision(rec.Scope, rec.NewRevision); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("home: remove recovered journal record: %w", err)
	}
	return nil
}

// peekRecordScope reads a journal record's scope. A record removed by a
// concurrent recovery between the directory scan and this read yields "".
func (h *Home) peekRecordScope(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("home: read journal record: %w", err)
	}
	var rec journalRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", fmt.Errorf("home: decode journal record %s: %w", filepath.Base(path), err)
	}
	if rec.Scope == "" {
		return "", fmt.Errorf("home: corrupt journal record %s: empty scope", filepath.Base(path))
	}
	return rec.Scope, nil
}

func (h *Home) applyItem(it ChangeItem) error {
	rootPath, err := h.RootFor(it.Root)
	if err != nil {
		return err
	}
	path, err := joinContained(rootPath, it.Key)
	if err != nil {
		return err
	}
	if err := verifyNoFollow(rootPath, path); err != nil {
		return err
	}
	return canonicalAtomicWrite(path, it.Data)
}

func (h *Home) journalDir() string { return filepath.Join(h.root, JournalDirName) }

func (h *Home) journalPath(scope, txnID string) string {
	return filepath.Join(h.journalDir(), scope+"."+txnID+".json")
}

func (h *Home) writeJournalRecord(rec journalRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("home: encode journal record: %w", err)
	}
	return canonicalAtomicWrite(h.journalPath(rec.Scope, rec.TxnID), append(data, '\n'))
}

func (h *Home) revisionPath(scope string) string {
	return filepath.Join(h.journalDir(), scope+".rev")
}

func (h *Home) readRevision(scope string) (uint64, error) {
	data, err := os.ReadFile(h.revisionPath(scope))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("home: read revision: %w", err)
	}
	var v uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &v); err != nil {
		return 0, fmt.Errorf("home: parse revision: %w", err)
	}
	return v, nil
}

func (h *Home) writeRevision(scope string, v uint64) error {
	return canonicalAtomicWrite(h.revisionPath(scope), []byte(fmt.Sprintf("%d\n", v)))
}

func validateTxnID(txnID string) error {
	if txnID == "" || txnID == "." || txnID == ".." || filepath.Base(txnID) != txnID || strings.ContainsAny(txnID, `/\\`) {
		return ErrInvalidTxnID
	}
	return nil
}
