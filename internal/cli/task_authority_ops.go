package cli

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// resolveTaskOwner resolves the authoritative owner identity for a CLI task
// mutation from the exact home, matching the legacy home fallback: captain
// identity for captain homes, otherwise the home identity (basename for a
// general home).
func resolveTaskOwner(homeDir string) string {
	identity, rank, err := home.ReadHomeIdentity(homeDir)
	if err != nil {
		identity = filepath.Base(homeDir)
		rank = home.RankGeneral
	}
	if rank == home.RankCaptain {
		return "captain:" + identity
	}
	return identity
}

// newTaskAuthorityOperationID supplies a fresh invocation identity for one
// CLI mutation (ADR-0007 §6): the CLI never reuses an identity, so repeating
// the same command fails as a typed conflict instead of replaying a previous
// invocation.
func newTaskAuthorityOperationID(verb string) string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", verb, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%x", verb, buf[:])
}

// newCanonicalOperation builds one typed canonical mutation Operation from a
// fresh invocation identity and the typed request intent. The digest is
// derived from the intent, never from a generic payload seam.
func newCanonicalOperation(verb string, intent domain.Intent) (domain.Operation, error) {
	opID, err := domain.NewOperationID(newTaskAuthorityOperationID(verb))
	if err != nil {
		return domain.Operation{}, err
	}
	return domain.NewOperation(opID, intent)
}

// authoritativeMetaField reports whether a .meta projection key duplicates an
// authoritative Task Aggregate field. Canonical records win: `task show`
// prints these from the Aggregate and never lets the projection override
// them (Task 3.2 criterion 3).
func authoritativeMetaField(key string) bool {
	switch key {
	case "description", "kind", "project", "owner", "generation", "state":
		return true
	}
	return false
}
