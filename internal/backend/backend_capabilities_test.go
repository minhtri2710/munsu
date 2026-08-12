package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCapabilityMatrixIsDeterministicAndAccurate asserts the exact capability
// descriptor for each of the five declared backends (BEO-16/P1a). It pins the
// tiers so a future change cannot silently claim more than is implemented.
func TestCapabilityMatrixIsDeterministicAndAccurate(t *testing.T) {
	matrix := CapabilityMatrix()
	if len(matrix) != 5 {
		t.Fatalf("capability matrix has %d backends, want 5", len(matrix))
	}
	wantOrder := []string{"tmux", "herdr", "zellij", "cmux", "orca"}
	for i, c := range matrix {
		if c.Name != wantOrder[i] {
			t.Fatalf("matrix[%d].Name = %q, want %q", i, c.Name, wantOrder[i])
		}
	}

	// Verified session adapters: tmux and herdr.
	for _, name := range []string{"tmux", "herdr"} {
		c, err := Capabilities(name)
		if err != nil {
			t.Fatalf("Capabilities(%q): %v", name, err)
		}
		if c.Tier != TierVerified {
			t.Errorf("%s tier = %v, want verified", name, c.Tier)
		}
		if c.Create != CapCurrent || c.ReservationAwareCreate != CapCurrent || c.Submit != CapCurrent || c.Probe != CapCurrent || c.Dispose != CapCurrent {
			t.Errorf("%s lifecycle capabilities incorrect: %+v", name, c)
		}
		if c.WorktreeOwnership != CapUnsupported {
			t.Errorf("%s worktree ownership = %v, want unsupported (worktree is a separate provider)", name, c.WorktreeOwnership)
		}
		if c.Secondmate != CapUnsupported {
			t.Errorf("%s secondmate = %v, want unsupported", name, c.Secondmate)
		}
	}

	// Experimental session adapters: zellij, cmux, orca.
	for _, name := range []string{"zellij", "cmux", "orca"} {
		c, err := Capabilities(name)
		if err != nil {
			t.Fatalf("Capabilities(%q): %v", name, err)
		}
		if c.Tier != TierExperimental {
			t.Errorf("%s tier = %v, want experimental", name, c.Tier)
		}
		// Reservation-aware create is verified for tmux/herdr only.
		if c.ReservationAwareCreate != CapUnsupported {
			t.Errorf("%s reservation-aware create = %v, want unsupported", name, c.ReservationAwareCreate)
		}
	}

	// Native activity/event must NOT be claimed current: herdr is proposed
	// (P1b), everyone else unsupported.
	herdr, _ := Capabilities("herdr")
	if herdr.NativeBusy != CapProposed || herdr.NativeEventWait != CapProposed {
		t.Errorf("herdr native busy/event must be proposed (not current): busy=%v event=%v", herdr.NativeBusy, herdr.NativeEventWait)
	}
	for _, name := range []string{"tmux", "zellij", "cmux", "orca"} {
		c, _ := Capabilities(name)
		if c.NativeBusy != CapUnsupported || c.NativeEventWait != CapUnsupported {
			t.Errorf("%s native busy/event must be unsupported: busy=%v event=%v", name, c.NativeBusy, c.NativeEventWait)
		}
	}

	if _, err := Capabilities("nope"); err == nil {
		t.Fatal("unknown backend must fail closed")
	}
}

// fakeBin writes na executable `name` that prints `stderr` (first line) to
// stderr and exits 1; when stderr is empty it exits 0. Used for generic
// operational-failure fakes.
func fakeBin(t *testing.T, name, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	if stderr != "" {
		sb.WriteString(fmt.Sprintf("echo %q >&2\n", stderr))
		sb.WriteString("exit 1\n")
	} else {
		sb.WriteString("exit 0\n")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sb.String()), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeTmuxDead writes a tmux whose list-panes reports authoritative absence.
func fakeTmuxDead(t *testing.T, dir string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		`echo "can't find window" >&2` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

// fakeHerdrDead writes a herdr whose pane-get reports pane_not_found
// (authoritative absence).
func fakeHerdrDead(t *testing.T, dir string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--session" ]; then shift 2; fi` + "\n" +
		`if [ "$1" = "pane" ] && [ "$2" = "get" ]; then echo '{"error":{"code":"pane_not_found","message":"missing"}}' >&2; exit 1; fi` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

// fakeListDead writes a JSON-list backend (zellij/cmux/orca) that returns an
// EMPTY authoritative result (exact absence).
func fakeListDead(t *testing.T, dir, name, emptyJSON string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		`echo '` + emptyJSON + `'` + "\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

// oldPathDir writes a fake binary into a fresh temp dir via `write` and
// returns that dir (meant to be prepended to PATH).
func oldPathDir(t *testing.T, write func(dir string)) string {
	t.Helper()
	dir := t.TempDir()
	write(dir)
	return dir
}

// TestObservationBindsAndNormalizesFakes runs the typed observation over each
// of the five session adapters and asserts exact-absence / operational-error
// classification is never confused (BEO-16 mandatory contract).
func TestObservationBindsAndNormalizesFakes(t *testing.T) {
	// tmux operational-failure (permission denied) fake.
	tmuxErrDir := fakeBin(t, "tmux", "permission denied")

	cases := []struct {
		name   string
		binDir string
		mk     func() Backend
		handle string
		want   LifecycleState
	}{
		{name: "tmux dead", binDir: oldPathDir(t, func(d string) { fakeTmuxDead(t, d) }), mk: func() Backend { return &TmuxBackend{} }, handle: "s:p", want: LifecycleDead},
		{name: "tmux operational failure", binDir: tmuxErrDir, mk: func() Backend { return &TmuxBackend{} }, handle: "s:p", want: LifecycleUnknown},
		{name: "herdr dead", binDir: oldPathDir(t, func(d string) { fakeHerdrDead(t, d) }), mk: func() Backend { return NewHerdrBackend("") }, handle: "s:p", want: LifecycleDead},
		{name: "zellij dead", binDir: oldPathDir(t, func(d string) { fakeListDead(t, d, "zellij", "[]") }), mk: func() Backend { return NewZellijBackend("s") }, handle: "s:terminal_1", want: LifecycleDead},
		{name: "zellij operational failure", binDir: fakeBin(t, "zellij", "connection refused"), mk: func() Backend { return NewZellijBackend("s") }, handle: "s:terminal_1", want: LifecycleUnknown},
		{name: "cmux dead", binDir: oldPathDir(t, func(d string) { fakeListDead(t, d, "cmux", `{"result":{"workspaces":[]}}`) }), mk: func() Backend { return newCmuxBackend() }, handle: "ws|surf", want: LifecycleDead},
		{name: "cmux operational failure", binDir: fakeBin(t, "cmux", "connection refused"), mk: func() Backend { return newCmuxBackend() }, handle: "ws|surf", want: LifecycleUnknown},
		{name: "orca dead", binDir: oldPathDir(t, func(d string) { fakeListDead(t, d, "orca", `{"terminals":[]}`) }), mk: func() Backend { return NewOrcaBackend() }, handle: "ctr:term", want: LifecycleDead},
		{name: "orca operational failure", binDir: fakeBin(t, "orca", "connection refused"), mk: func() Backend { return NewOrcaBackend() }, handle: "ctr:term", want: LifecycleUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := os.Getenv("PATH")
			t.Setenv("PATH", tc.binDir+string(os.PathListSeparator)+old)
			obs := ObserveBoundEndpoint(tc.mk(), tc.handle, "")
			if obs.Lifecycle != tc.want {
				t.Fatalf("lifecycle = %v (state=%v) want %v (detail=%q)", obs.Lifecycle, obs.State(), tc.want, obs.Detail)
			}
			if tc.want == LifecycleDead {
				if !obs.Absent() {
					t.Fatalf("dead/current reading must be Absent: %+v", obs)
				}
			} else {
				if obs.Absent() {
					t.Fatalf("ambiguous reading must not authorize recovery (Absent): %+v", obs)
				}
				if obs.Lifecycle != LifecycleUnknown {
					t.Fatalf("operational failure lifecycle = %v, want unknown (not dead)", obs.Lifecycle)
				}
			}
		})
	}
}
