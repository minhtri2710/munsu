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
// apply is idempotent, so a record left in any state is recovered by redoing
// its items and advancing the scope revision.
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

// recover replays any incomplete write-ahead journal records by redoing their
// items (idempotent) and advancing each scope's revision. This is the
// mechanical crash recovery of an interrupted commit.
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
		path := filepath.Join(h.journalDir(), name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("home: read journal record: %w", err)
		}
		var rec journalRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("home: decode journal record %s: %w", name, err)
		}
		for _, it := range rec.Items {
			if err := h.applyItem(it); err != nil {
				return fmt.Errorf("home: recover item: %w", err)
			}
		}
		cur, err := h.readRevision(rec.Scope)
		if err != nil {
			return err
		}
		if rec.NewRevision > cur {
			if err := h.writeRevision(rec.Scope, rec.NewRevision); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("home: remove recovered journal record: %w", err)
		}
	}
	return nil
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
