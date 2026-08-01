package taskauthority_test

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthority/storecontract"
)

// TestStoreContract runs the one authoritative Store contract suite against
// the in-memory adapter. It lives in the external test package because the
// internal test package cannot import the harness package without a cycle.
func TestStoreContract(t *testing.T) {
	storecontract.Run(t, func() taskauthority.Store {
		return taskauthority.NewMemStoreForTest()
	})
}
