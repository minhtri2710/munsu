package home

import "testing"

// ErrSimulatedCrash is the panic value CrashCommitAfter raises in place of a
// process crash.
var ErrSimulatedCrash = simulatedCrash{}

type simulatedCrash struct{}

func (simulatedCrash) Error() string { return "home: simulated crash" }

// CrashCommitAfter makes the next Commit panic with ErrSimulatedCrash once its
// journal record is durable and k of its n+2 apply steps have run (n item
// applies, the revision write, the record removal), as after a process crash.
// k = n+2 never fires. The hook disarms itself when it fires and is restored at
// test cleanup.
func CrashCommitAfter(t testing.TB, k int) {
	orig := commitStep
	t.Cleanup(func() { commitStep = orig })
	steps := 0
	commitStep = func() error {
		if steps == k {
			commitStep = orig
			panic(ErrSimulatedCrash)
		}
		steps++
		return nil
	}
}

// ScopeRevision reads the committed revision of scope.
func (h *Home) ScopeRevision(scope string) (uint64, error) { return h.readRevision(scope) }
