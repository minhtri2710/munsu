package home

import "testing"

// ErrSimulatedCrash is the panic value CrashCommitAfter raises in place of a
// process crash.
var ErrSimulatedCrash = simulatedCrash{}

type simulatedCrash struct{}

func (simulatedCrash) Error() string { return "home: simulated crash" }

// CrashCommitAfter makes the next Commit panic with ErrSimulatedCrash once its
// journal record is durable and k of its items are applied, so no roll-forward,
// revision advance or record removal runs, as after a process crash. The hook
// disarms itself when it fires and is restored at test cleanup.
func CrashCommitAfter(t testing.TB, k int) {
	orig := commitApply
	t.Cleanup(func() { commitApply = orig })
	applied := 0
	commitApply = func(h *Home, it ChangeItem) error {
		if applied == k {
			commitApply = orig
			panic(ErrSimulatedCrash)
		}
		applied++
		return orig(h, it)
	}
}

// ScopeRevision reads the committed revision of scope.
func (h *Home) ScopeRevision(scope string) (uint64, error) { return h.readRevision(scope) }
