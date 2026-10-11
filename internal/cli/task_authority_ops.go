package cli

import (
	"crypto/rand"
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// resolveTaskOwner resolves the authoritative owner identity for a CLI task
// mutation from the exact home: captain identity for captain homes, otherwise
// the home identity (basename for a general home).
func resolveTaskOwner(homeDir string) (string, error) {
	identity, rank, err := home.ReadHomeIdentity(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolving task owner: %w", err)
	}
	if rank == home.RankCaptain {
		return "captain:" + identity, nil
	}
	return identity, nil
}

// newTaskAuthorityOperationID supplies a fresh invocation identity for one
// CLI mutation: the CLI never reuses an identity, so repeating
// the same command fails as a typed conflict instead of replaying a previous
// invocation.
func newTaskAuthorityOperationID(verb string) string {
	var buf [12]byte
	// crypto/rand.Read cannot return an error: since Go 1.24 it terminates the
	// process instead of reporting failure, so there is no branch to take.
	rand.Read(buf[:])
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
