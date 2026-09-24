package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// This file owns the bounded active-index mechanics shared by the Fleet
// journals (delivery and Task Transfer): one index document plus one record
// per operation under a state-root key prefix, written through home.Commit.
// Recovery discovers journals only through the index (never a filesystem
// scan); a completed record leaves the index and is retained as terminal
// truth. What a record means, and when it may advance, stays with its owner.

// journalIndexVersion is the schema version of every Fleet journal index.
const journalIndexVersion = 1

// journalIndex is the bounded Fleet-owned index of ACTIVE journals of one
// home. Only IDs in Active are ever discovered during recovery, so completed
// journals cost nothing to skip.
type journalIndex struct {
	Version      int      `json:"version"`
	HomeRevision uint64   `json:"home_revision"`
	Active       []string `json:"active"`
}

// journalStore names one journal family: the state-root key prefix it lives
// under and the noun its errors, transaction IDs and panics carry.
type journalStore struct {
	dir  string
	noun string
}

// journalRecord is one journal record; every record carries its schema
// version and its own ID.
type journalRecord interface {
	journalHead() (version int, id string)
}

func (s journalStore) indexKey() string { return s.dir + "/index.json" }

// recordKey returns the contained logical key of one journal record under
// the home state root.
func (s journalStore) recordKey(id string) string { return s.dir + "/" + id + ".json" }

// txnID derives the deterministic Home transaction identity of one journal
// transition. Transitions are distinct, so a txnID is never reused for
// changed journal bytes and replay of the same transition is deterministic.
func (s journalStore) txnID(id, transition string) string {
	return s.noun + "-" + id + "-" + transition
}

// readIndex reads and validates the index through Home.Read. An absent index
// means no active journals; a malformed index (bad JSON, wrong version,
// duplicate or empty active IDs) fails closed.
func (s journalStore) readIndex(h *home.Home) (journalIndex, error) {
	data, err := h.Read(home.RootState, s.indexKey())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return journalIndex{}, nil
		}
		return journalIndex{}, fmt.Errorf("reading %s journal index: %w", s.noun, err)
	}
	var idx journalIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return journalIndex{}, fmt.Errorf("corrupt %s journal index: %w", s.noun, err)
	}
	if idx.Version != journalIndexVersion {
		return journalIndex{}, fmt.Errorf("unsupported %s journal index version %d", s.noun, idx.Version)
	}
	seen := make(map[string]bool, len(idx.Active))
	for _, id := range idx.Active {
		if id == "" || seen[id] {
			return journalIndex{}, fmt.Errorf("invalid %s journal index: duplicate or empty active id %q", s.noun, id)
		}
		seen[id] = true
	}
	return idx, nil
}

// items encodes the index document and one journal record as the change-set
// of one Home.Commit transition, so index membership, home revision and the
// journal record always persist atomically.
func (s journalStore) items(idx journalIndex, record journalRecord) ([]home.ChangeItem, error) {
	idxData, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	recordData, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	_, id := record.journalHead()
	return []home.ChangeItem{
		{Root: home.RootState, Key: s.indexKey(), Data: append(idxData, '\n')},
		{Root: home.RootState, Key: s.recordKey(id), Data: append(recordData, '\n')},
	}, nil
}

// commit persists record with an index holding active, one revision past
// base, in ONE atomic Home.Commit under the held fenced lock.
func (s journalStore) commit(h *home.Home, lk *home.Lock, base journalIndex, active []string, record journalRecord, transition string) error {
	next := journalIndex{Version: journalIndexVersion, HomeRevision: base.HomeRevision + 1, Active: active}
	items, err := s.items(next, record)
	if err != nil {
		return err
	}
	_, id := record.journalHead()
	_, err = h.Commit(lk, s.txnID(id, transition), base.HomeRevision, items)
	return err
}

// create durably records the intent of one new journal before its first side
// effect: the index gains the record's ID and the record is written in ONE
// atomic Home.Commit.
func (s journalStore) create(h *home.Home, lk *home.Lock, record journalRecord) error {
	idx, err := s.readIndex(h)
	if err != nil {
		return err
	}
	_, id := record.journalHead()
	if err := s.commit(h, lk, idx, append(idx.Active, id), record, "create"); err != nil {
		return fmt.Errorf("writing %s journal %s: %w", s.noun, id, err)
	}
	return nil
}

// complete commits the terminal truth of one active journal: the index drops
// its ID (bounding the active set) and the record, after finish marks it
// terminal, is rewritten in ONE atomic Home.Commit. The record is retained;
// no file is deleted and a completed record is never resumed.
func (s journalStore) complete(h *home.Home, lk *home.Lock, record journalRecord, finish func()) error {
	idx, err := s.readIndex(h)
	if err != nil {
		return err
	}
	_, id := record.journalHead()
	found := false
	active := make([]string, 0, len(idx.Active))
	for _, activeID := range idx.Active {
		if activeID == id {
			found = true
			continue
		}
		active = append(active, activeID)
	}
	if !found {
		return fmt.Errorf("completing %s journal %s: not active", s.noun, id)
	}
	finish()
	if err := s.commit(h, lk, idx, active, record, "complete"); err != nil {
		return fmt.Errorf("completing %s journal %s: %w", s.noun, id, err)
	}
	return nil
}

// readJournalRecord reads one journal record through Home.Read and refuses a
// record whose version or ID does not match.
func readJournalRecord[T any, P interface {
	*T
	journalRecord
}](h *home.Home, s journalStore, id string) (P, error) {
	data, err := h.Read(home.RootState, s.recordKey(id))
	if err != nil {
		return nil, err
	}
	record := P(new(T))
	if err := json.Unmarshal(data, record); err != nil {
		return nil, fmt.Errorf("corrupt %s journal %s: %w", s.noun, id, err)
	}
	if version, recordID := record.journalHead(); version != 1 || recordID != id {
		return nil, fmt.Errorf("invalid %s journal %s", s.noun, id)
	}
	return record, nil
}

// mustOperation builds a validated Operation from a typed Operation ID and
// intent, deriving the digest from the typed intent.
func (s journalStore) mustOperation(id string, intent domain.Intent) domain.Operation {
	opID, err := domain.NewOperationID(id)
	if err != nil {
		panic(fmt.Sprintf("%s: invalid operation id %q: %v", s.noun, id, err))
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		panic(fmt.Sprintf("%s: invalid operation for %q: %v", s.noun, id, err))
	}
	return op
}

// newJournalID mints a collision-safe random journal identity. crypto/rand
// Read never returns an error; it aborts the program instead.
func newJournalID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), buffer)
}
