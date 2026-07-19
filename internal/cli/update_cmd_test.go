package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestUpdateCaptainsFlagRegistered verifies the --captains flag exists on the
// update command. This is the one-shot D5 command: self-update + FF captains +
// nudge. Without the flag, the update command is self-update only.
func TestUpdateCaptainsFlagRegistered(t *testing.T) {
	root := NewRootCommand()
	updateCmd, _, err := root.Find([]string{"update"})
	if err != nil {
		t.Fatalf("Find(update): %v", err)
	}
	if updateCmd.Flags().Lookup("captains") == nil {
		t.Fatal("update must register --captains (D5: one-shot self-update + captain FF + nudge)")
	}
}

// TestUpdateCaptainsHelpDocuments verifies --captains is documented in help.
func TestUpdateCaptainsHelpDocuments(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"update", "--help"})
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	if err := root.Execute(); err != nil {
		t.Fatalf("update --help failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "--captains") {
		t.Errorf("update --help should document --captains, got: %s", out)
	}
	if !strings.Contains(out, "captain") {
		t.Errorf("update --help should mention captain fast-forward behavior, got: %s", out)
	}
}
