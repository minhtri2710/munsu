package cli

import "testing"

// TestIntegrateSubcommandFlagsRegistered is a regression guard for a bug where
// `munsu integrate install --harness <X>` (and repair) failed with "unknown
// flag: --harness": the --harness/--scope/--dry-run flags were advertised in the
// parent command's help text but never registered on the install/repair
// subcommands (only safety-check registered --harness locally). Unit tests that
// called runIntegrateInstall directly bypassed cobra parsing, so the gap went
// undetected until an end-to-end `integrate install --harness agy` run.
func TestIntegrateSubcommandFlagsRegistered(t *testing.T) {
	root := NewRootCommand()

	for _, sub := range []string{"install", "repair"} {
		cmd, _, err := root.Find([]string{"integrate", sub})
		if err != nil {
			t.Fatalf("Find(integrate %s): %v", sub, err)
		}
		for _, flag := range []string{"harness", "scope", "dry-run"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("integrate %s must register --%s (regression: was missing, breaking all adapter installs)", sub, flag)
			}
		}
	}

	// safety-check must still carry --harness (it predated the fix and must not regress).
	sc, _, err := root.Find([]string{"integrate", "safety-check"})
	if err != nil {
		t.Fatalf("Find(integrate safety-check): %v", err)
	}
	if sc.Flags().Lookup("harness") == nil {
		t.Error("integrate safety-check must register --harness")
	}
	// status must register --scope so project-scope installs can be inspected
	// (without it, `integrate status` after `install --scope project` reports absent).
	st, _, err := root.Find([]string{"integrate", "status"})
	if err != nil {
		t.Fatalf("Find(integrate status): %v", err)
	}
	if st.Flags().Lookup("scope") == nil {
		t.Error("integrate status must register --scope")
	}
}
