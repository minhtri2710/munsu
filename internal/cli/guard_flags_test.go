package cli

import "testing"

// TestGuardHarnessFlagRegistered is a regression guard for a bug where
// `munsu guard --harness <X>` failed with "unknown flag: --harness": the guard
// command carrying --harness (and the runGuard* Stop-hook routing) was dead code
// (newGuardCmd, never registered at the root), while the registered top-level
// `guard` (newContractGuardCmd) had no --harness flag. The adapter hooks call
// `munsu guard --harness <X>`, so every turn-end guard was broken at runtime.
func TestGuardHarnessFlagRegistered(t *testing.T) {
	root := NewRootCommand()
	guardCmd, _, err := root.Find([]string{"guard"})
	if err != nil {
		t.Fatalf("Find(guard): %v", err)
	}
	if guardCmd.Flags().Lookup("harness") == nil {
		t.Fatal("guard must register --harness (regression: the registered top-level guard lacked it, breaking every adapter turn-end guard; the --harness logic lived only in dead code)")
	}
}
