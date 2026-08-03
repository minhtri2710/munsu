package cli

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// resolveTaskActor resolves the authoritative owner and actor identity for a
// CLI task mutation from the exact home, matching the legacy home fallback:
// captain identity for captain homes, otherwise the home identity (basename
// for a general home).
func resolveTaskActor(homeDir string) (string, taskauthority.Actor) {
	identity, rank, err := home.ReadHomeIdentity(homeDir)
	if err != nil {
		identity = filepath.Base(homeDir)
		rank = home.RankGeneral
	}
	owner := identity
	if rank == home.RankCaptain {
		owner = "captain:" + identity
	}
	return owner, taskauthority.Actor{ID: owner, Rank: string(rank)}
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
