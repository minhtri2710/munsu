//go:build integration

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestStructuredGuardFailsClosedOnUnreadableTruth proves the structured
// `munsu guard` command returns a typed invalid_state failure when Task truth
// is unreadable (a legacy/meta-only soldier task with no canonical record),
// instead of evaluating the guard with an empty/down-counted fleet.
func TestStructuredGuardFailsClosedOnUnreadableTruth(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	// A task-facing meta without a canonical record is unreadable Task truth
	// under the clean-break contract.
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(homeDir, "state", "legacy-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runContract(t, []string{"guard"})
	if err == nil {
		t.Fatalf("munsu guard over unreadable truth = nil error, want fail-closed; output:\n%s", out)
	}
	if !strings.Contains(out, "invalid_state") {
		t.Errorf("guard failure must carry a structured invalid_state code, got:\n%s", out)
	}
	if strings.Contains(out, "kind: guard") {
		t.Errorf("guard failure must not report a normal guard result, got:\n%s", out)
	}
}

// TestPrerunGuardSurfacesUnreadableTruth proves the pre-run middleware does
// not silently treat an unreadable fleet as empty: it writes a warning to
// stderr naming the failure.
func TestPrerunGuardSurfacesUnreadableTruth(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(homeDir, "state", "legacy-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"capabilities"})
		_ = root.Execute()
	})
	if !strings.Contains(stderr, "cannot read authoritative fleet state") {
		t.Errorf("pre-run guard must surface unreadable Task truth on stderr (not return silently), got:\n%s", stderr)
	}
}

// TestWakeClaimResolveAckActualSyntax proves wake uses the real invocation
// strings documented in the contract: claim by --consumer, resolve by
// --claim-id/--event-id/--summary, and ack by positional lease-id + event-ids.
// The legacy positional-claim / --owner / --claim forms are rejected.
func TestWakeClaimResolveAckActualSyntax(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)

	// claim --consumer is the only accepted claim form.
	claim, err := runContract(t, []string{"wake", "claim", "--consumer", "me"})
	if err != nil {
		t.Fatalf("wake claim --consumer: %v\n%s", err, claim)
	}
	if !strings.Contains(claim, "kind: wake.claim") {
		t.Errorf("wake claim must emit kind wake.claim, got:\n%s", claim)
	}

	// ack by positional lease-id + event-id is accepted against a real lease.
	if err := mhome.EnqueueWake(homeDir, "signal", "task", "payload"); err != nil {
		t.Fatal(err)
	}
	leaseClaim, herr := mhome.ClaimWakes(homeDir, "test", 60, 1)
	if herr != nil || len(leaseClaim.Wakes) != 1 {
		t.Fatalf("claim: %+v err=%v", leaseClaim, herr)
	}
	eventID := leaseClaim.Wakes[0].Epoch + ":" + leaseClaim.Wakes[0].Seq
	ack, err := runContract(t, []string{"wake", "ack", leaseClaim.LeaseID, eventID})
	if err != nil {
		t.Fatalf("wake ack positional: %v\n%s", err, ack)
	}
	if !strings.Contains(ack, "kind: wake.ack") {
		t.Errorf("wake ack must emit kind wake.ack, got:\n%s", ack)
	}

	// Legacy positional claim form is rejected.
	if out, err := runContract(t, []string{"wake", "claim", "wake-1"}); err == nil {
		t.Errorf("wake claim with positional wake-id must be rejected, got:\n%s", out)
	}
	// Legacy --owner flag is rejected.
	if out, err := runContract(t, []string{"wake", "claim", "--owner", "x"}); err == nil {
		t.Errorf("wake claim --owner must be rejected (unknown flag), got:\n%s", out)
	}
	// Legacy ack --claim form is rejected.
	if out, err := runContract(t, []string{"wake", "ack", "--claim", "claim-1"}); err == nil {
		t.Errorf("wake ack --claim must be rejected (requires positional lease-id + event-ids), got:\n%s", out)
	}
}

// TestBackendCapabilitiesOnlyTmuxHerdr proves structured backend capability
// discovery exposes only tmux and herdr; all other runtime backends are not
// part of this contract surface.
func TestBackendCapabilitiesOnlyTmuxHerdr(t *testing.T) {
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "tmux"}); err != nil {
		t.Errorf("backend capabilities --backend tmux: %v\n%s", err, out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "herdr"}); err != nil {
		t.Errorf("backend capabilities --backend herdr: %v\n%s", err, out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "zellij"}); err == nil {
		t.Errorf("backend capabilities --backend zellij must be rejected (not exposed), got:\n%s", out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "cmux"}); err == nil {
		t.Errorf("backend capabilities --backend cmux must be rejected (not exposed), got:\n%s", out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "orca"}); err == nil {
		t.Errorf("backend capabilities --backend orca must be rejected (not exposed), got:\n%s", out)
	}
}
