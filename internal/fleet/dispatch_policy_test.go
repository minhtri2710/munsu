package fleet

// Policy-matrix tests for the Fleet-boundary dispatch policy (issue #546
// Slice 6). Every parent/home/config combination resolves to exactly one
// policy or fails closed with the named matrix row; a General dispatch can
// never read or mutate Captain-owned state, and a Captain dispatch can never
// fall back to base/local configuration.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// storeFleetPublishedSnapshot writes a valid assigned-snapshot document (a
// non-empty Backend and a 64-hex Digest are required by snapshot validation).
func storeFleetPublishedSnapshot(t *testing.T, homeDir string, projectPath, harness string) {
	t.Helper()
	resolved := fleetconfig.ResolvedProjectConfig{
		Project:        "alpha",
		ProjectPath:    projectPath,
		SoldierHarness: harness,
		Backend:        "tmux",
		Digest:         "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if err := fleetconfig.StorePublishedSnapshot(homeDir, resolved); err != nil {
		t.Fatal(err)
	}
}

// TestDispatchPolicyMatrix is the authoritative policy matrix: every
// parent/home/config combination either resolves the named policy or refuses
// with the named problem row.
func TestDispatchPolicyMatrix(t *testing.T) {
	plainHome := t.TempDir()
	generalHome := t.TempDir()
	writeSpawnSnapshotDocuments(t, generalHome) // fleet base document, no published snapshot

	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-42"); err != nil {
		t.Fatal(err)
	}
	captainWithSnapshot := t.TempDir()
	if err := home.SeedCaptainProvenance(captainWithSnapshot, "sm-7"); err != nil {
		t.Fatal(err)
	}
	storeFleetPublishedSnapshot(t, captainWithSnapshot, captainWithSnapshot, "pi")

	generalWithPublished := t.TempDir()
	storeFleetPublishedSnapshot(t, generalWithPublished, generalWithPublished, "pi")

	// A proven captain home that also carries the fleet base document (a
	// copied general home) is a config-surface contradiction.
	captainWithBase := t.TempDir()
	if err := home.SeedCaptainProvenance(captainWithBase, "sm-9"); err != nil {
		t.Fatal(err)
	}
	privateBase := fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config:        fleetconfig.ProjectOverlay{SoldierHarness: "pi", Backend: "tmux"},
	}
	if err := fleetconfig.StoreFleetBase(captainWithBase, privateBase); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		homeDir     string
		parentRank  string
		wantPolicy  DispatchPolicy
		wantParent  string
		wantProblem DispatchPolicyProblem
	}{
		// Row: general parent + general home + fleet base document → direct.
		{name: "general on general home with base", homeDir: generalHome, parentRank: "general", wantPolicy: DispatchPolicyGeneralDirect, wantParent: "general"},
		// Row: empty rank defaults to the general parent (legacy flat path).
		{name: "empty rank on plain home", homeDir: plainHome, parentRank: "", wantPolicy: DispatchPolicyGeneralDirect, wantParent: "general"},
		// Row: general parent + general home without typed config → direct (legacy).
		{name: "general on plain home without typed config", homeDir: plainHome, parentRank: "general", wantPolicy: DispatchPolicyGeneralDirect, wantParent: "general"},
		// Row: captain parent + proven captain home + published snapshot → mediated.
		{name: "captain on proven home with assigned snapshot", homeDir: captainWithSnapshot, parentRank: "captain", wantPolicy: DispatchPolicyCaptainMediated, wantParent: "sm-7"},

		// Refusal rows (ambiguous parent/home/config combinations).
		{name: "general on captain-owned home", homeDir: captainHome, parentRank: "general", wantProblem: DispatchPolicyProblemGeneralOnCaptainHome},
		{name: "empty rank on captain-owned home", homeDir: captainHome, parentRank: "", wantProblem: DispatchPolicyProblemGeneralOnCaptainHome},
		{name: "general on captain-owned home with snapshot", homeDir: captainWithSnapshot, parentRank: "general", wantProblem: DispatchPolicyProblemGeneralOnCaptainHome},
		{name: "captain on unproven general home with base", homeDir: generalHome, parentRank: "captain", wantProblem: DispatchPolicyProblemCaptainOnUnprovenHome},
		{name: "captain on unproven plain home", homeDir: plainHome, parentRank: "captain", wantProblem: DispatchPolicyProblemCaptainOnUnprovenHome},
		{name: "captain without assigned snapshot", homeDir: captainHome, parentRank: "captain", wantProblem: DispatchPolicyProblemCaptainWithoutAssignedSnapshot},
		{name: "captain home carrying fleet base document", homeDir: captainWithBase, parentRank: "captain", wantProblem: DispatchPolicyProblemConfigSurfaceContradiction},
		{name: "general home carrying published snapshot", homeDir: generalWithPublished, parentRank: "general", wantProblem: DispatchPolicyProblemConfigSurfaceContradiction},
		{name: "unknown parent rank", homeDir: generalHome, parentRank: "soldier", wantProblem: DispatchPolicyProblemUnknownParentRank},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, parent, err := ResolveDispatchPolicy(tc.homeDir, tc.parentRank)
			if tc.wantProblem != "" {
				if err == nil {
					t.Fatalf("ResolveDispatchPolicy(%q,%q) = %q,%q,nil; want refusal %s",
						tc.homeDir, tc.parentRank, policy, parent, tc.wantProblem)
				}
				var perr *DispatchPolicyError
				if !errors.As(err, &perr) {
					t.Fatalf("error %T %v is not a *DispatchPolicyError", err, err)
				}
				if perr.Problem != tc.wantProblem {
					t.Fatalf("problem = %q, want %q (error %v)", perr.Problem, tc.wantProblem, err)
				}
				if perr.HomeDir == "" {
					t.Fatalf("policy error omits home evidence: %+v", perr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveDispatchPolicy(%q,%q): %v", tc.homeDir, tc.parentRank, err)
			}
			if policy != tc.wantPolicy || parent != tc.wantParent {
				t.Fatalf("ResolveDispatchPolicy(%q,%q) = %q,%q; want %q,%q",
					tc.homeDir, tc.parentRank, policy, parent, tc.wantPolicy, tc.wantParent)
			}
		})
	}
}

// TestDispatchPolicyFailsClosedOnMalformedProvenance proves that an
// unreadable or malformed captain marker is a refusal for both parent ranks,
// never a silent general resolution (a copied/mangled captain home must not be
// operated as a General home).
func TestDispatchPolicyFailsClosedOnMalformedProvenance(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, home.CaptainProvenanceMarkerName), []byte("garble\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, rank := range []string{"general", "captain", ""} {
		if _, _, err := ResolveDispatchPolicy(homeDir, rank); err == nil {
			t.Fatalf("rank %q resolved with a malformed provenance marker", rank)
		}
	}
}

// TestRunnerBoundaryRefusesGeneralDispatchIntoCaptainHome proves the Runner's
// Fleet boundary refuses a General dispatch aimed at a Captain-owned home
// before any dispatch mutation: no config, state, or data files are created
// in the Captain home.
func TestRunnerBoundaryRefusesGeneralDispatchIntoCaptainHome(t *testing.T) {
	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-42"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir()) // cwd is not a linked worktree, so spawn authority passes

	r := NewRunner(Args{ID: "task-1", ProjectName: "alpha", HomeDir: captainHome})
	_, err := r.Run()
	var perr *DispatchPolicyError
	if !errors.As(err, &perr) || perr.Problem != DispatchPolicyProblemGeneralOnCaptainHome {
		t.Fatalf("Run err = %v, want GeneralOnCaptainHome policy refusal", err)
	}

	// No dispatch side effects may exist in the Captain home.
	for _, dir := range []string{"state", "data", "config"} {
		if _, statErr := os.Stat(filepath.Join(captainHome, dir)); !os.IsNotExist(statErr) {
			t.Fatalf("General dispatch created %s/ in the Captain home", dir)
		}
	}
	// The provenance marker is the only content.
	entries, err := os.ReadDir(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != home.CaptainProvenanceMarkerName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("Captain home content after refused General dispatch = %v; want only the provenance marker", names)
	}
}

// TestRunnerBoundaryRefusesGeneralOnCaptainHomeBeforeSupervisionRead proves the
// early Fleet boundary fires before checkSupervision: a General dispatch aimed
// at a Captain-owned home whose watcher lease is degraded returns the typed
// GeneralOnCaptainHome row, not the supervision (ErrUnhealthyWatcher) error the
// supervision phase would otherwise raise first.
func TestRunnerBoundaryRefusesGeneralOnCaptainHomeBeforeSupervisionRead(t *testing.T) {
	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-42"); err != nil {
		t.Fatal(err)
	}
	// A present-but-unhealthy watcher lease: parseable (home+pid) but with no
	// fresh beat, so CheckSupervisionForDispatch would fail closed first if the
	// boundary were not enforced before the supervision read.
	leasePath := home.WatcherLeasePath(captainHome)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0755); err != nil {
		t.Fatal(err)
	}
	lease := fmt.Sprintf(`{"home":%q,"pid":%d,"started_at":0,"updated_at":0}`, captainHome, os.Getpid())
	if err := os.WriteFile(leasePath, []byte(lease), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir())

	r := NewRunner(Args{ID: "task-1", ProjectName: "alpha", HomeDir: captainHome})
	_, err := r.Run()
	var perr *DispatchPolicyError
	if !errors.As(err, &perr) || perr.Problem != DispatchPolicyProblemGeneralOnCaptainHome {
		t.Fatalf("Run err = %v, want GeneralOnCaptainHome boundary refusal ahead of the supervision read", err)
	}
}

// TestRunnerBoundaryDoesNotScanCaptainStateForGeneral proves the early boundary
// refuses a General dispatch into a Captain-owned home without ever scanning
// the Captain's endpoint state: the tmux pane resolver (the one piece of state
// the spawn-authority phase would touch) is never invoked.
func TestRunnerBoundaryDoesNotScanCaptainStateForGeneral(t *testing.T) {
	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-42"); err != nil {
		t.Fatal(err)
	}
	scanned := false
	original := tmuxWindowForPane
	t.Cleanup(func() { tmuxWindowForPane = original })
	tmuxWindowForPane = func(pane string) (string, error) {
		scanned = true
		return "@9", nil
	}

	t.Setenv("MUNSU_ROLE", "general")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("TMUX_PANE", "%9")
	t.Chdir(t.TempDir())

	r := NewRunner(Args{ID: "task-1", ProjectName: "alpha", HomeDir: captainHome})
	_, err := r.Run()
	var perr *DispatchPolicyError
	if !errors.As(err, &perr) || perr.Problem != DispatchPolicyProblemGeneralOnCaptainHome {
		t.Fatalf("Run err = %v, want GeneralOnCaptainHome boundary refusal", err)
	}
	if scanned {
		t.Fatalf("General dispatch into a Captain home scanned endpoint state (currentEndpointKind invoked)")
	}
}

// TestRunnerBoundaryAllowsExplicitCaptainFromItsHome proves the early boundary
// does not disturb a legitimate Captain dispatch: a proven Captain home reached
// by an explicit MUNSU_ROLE=captain from the Captain's own home passes the
// boundary and proceeds to the Captain dispatch policy handling.
func TestRunnerBoundaryAllowsExplicitCaptainFromItsHome(t *testing.T) {
	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-7"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_ROLE", "captain")
	t.Chdir(captainHome)

	r := NewRunner(Args{ID: "task-1", HomeDir: captainHome})
	if err := r.resolveHome(); err != nil {
		t.Fatal(err)
	}
	if err := r.checkFleetBoundary(); err != nil {
		t.Fatalf("checkFleetBoundary refused an explicit Captain from its own home: %v", err)
	}
}

// TestRunnerBoundaryCaptainDispatchConsumesAssignedSnapshot proves the Runner
// resolves CaptainMediated with the provenance captain ID when the operation
// home is a proven captain home carrying its assigned snapshot.
func TestRunnerBoundaryCaptainDispatchConsumesAssignedSnapshot(t *testing.T) {
	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-7"); err != nil {
		t.Fatal(err)
	}
	storeFleetPublishedSnapshot(t, captainHome, captainHome, "pi")
	t.Setenv("MUNSU_ROLE", "captain")
	t.Chdir(captainHome) // captain must spawn from its own home

	r := NewRunner(Args{ID: "task-1", ProjectName: "alpha", HomeDir: captainHome})
	if err := r.resolveHome(); err != nil {
		t.Fatal(err)
	}
	if err := r.checkSupervision(); err != nil {
		t.Fatal(err)
	}
	if err := r.checkSpawnAuthority(); err != nil {
		t.Fatal(err)
	}
	if err := r.resolveDispatchPolicy(); err != nil {
		t.Fatal(err)
	}
	if r.dispatchPolicy != DispatchPolicyCaptainMediated || r.parentCaptainID != "sm-7" {
		t.Fatalf("policy = %q parent = %q; want captain-mediated/sm-7", r.dispatchPolicy, r.parentCaptainID)
	}
}

// TestRunnerBoundaryCaptainWithoutAssignedSnapshotRefuses proves a proven
// captain home without an assigned published snapshot refuses at the boundary
// instead of falling through to local or legacy configuration.
func TestRunnerBoundaryCaptainWithoutAssignedSnapshotRefuses(t *testing.T) {
	captainHome := t.TempDir()
	if err := home.SeedCaptainProvenance(captainHome, "sm-42"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_ROLE", "captain")
	t.Chdir(captainHome)

	r := NewRunner(Args{ID: "task-1", ProjectName: "alpha", HomeDir: captainHome})
	_, err := r.Run()
	var perr *DispatchPolicyError
	if !errors.As(err, &perr) || perr.Problem != DispatchPolicyProblemCaptainWithoutAssignedSnapshot {
		t.Fatalf("Run err = %v, want CaptainWithoutAssignedSnapshot refusal", err)
	}
}

// TestDispatchSelectionReadsOnlyThePolicySurface proves dispatchSelection reads
// exactly one config surface per policy: GeneralDirect reads the fleet base
// document and never the captain assignment surface; CaptainMediated reads the
// published snapshot and never the base document; an unresolved policy reads
// nothing (fails closed).
func TestDispatchSelectionReadsOnlyThePolicySurface(t *testing.T) {
	baseHarness := "codex"
	snapshotHarness := "claude"

	writeSurfaces := func(t *testing.T, homeDir string) {
		t.Helper()
		base := fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness: baseHarness,
				Backend:        "tmux",
			},
		}
		if err := fleetconfig.StoreFleetBase(homeDir, base); err != nil {
			t.Fatal(err)
		}
		captainPath := filepath.Join(homeDir, "captains", "alpha")
		storeFleetPublishedSnapshot(t, homeDir, captainPath, snapshotHarness)
	}

	t.Run("general-direct reads the fleet base, never the published snapshot", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSurfaces(t, homeDir)
		r := &Runner{homeDir: homeDir, args: Args{ID: "t", ProjectName: "alpha"}, dispatchPolicy: DispatchPolicyGeneralDirect}
		sel, ok := r.dispatchSelection()
		if !ok || sel.Harness != baseHarness {
			t.Fatalf("selection = %+v, ok=%v; want base harness %q (not snapshot %q)", sel, ok, baseHarness, snapshotHarness)
		}
	})

	t.Run("captain-mediated reads the assigned published snapshot, never the base", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSurfaces(t, homeDir)
		r := &Runner{homeDir: homeDir, args: Args{ID: "t", ProjectName: "alpha"}, dispatchPolicy: DispatchPolicyCaptainMediated}
		sel, ok := r.dispatchSelection()
		if !ok || sel.Harness != snapshotHarness {
			t.Fatalf("selection = %+v, ok=%v; want assigned snapshot harness %q", sel, ok, snapshotHarness)
		}
	})

	t.Run("unresolved policy reads nothing", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSurfaces(t, homeDir)
		r := &Runner{homeDir: homeDir, args: Args{ID: "t", ProjectName: "alpha"}}
		if _, ok := r.dispatchSelection(); ok {
			t.Fatal("dispatch selection resolved without a resolved policy")
		}
	})

	t.Run("profiles come from the policy surface", func(t *testing.T) {
		homeDir := t.TempDir()
		base := fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				Backend: "tmux",
				DispatchProfiles: []fleetconfig.DispatchProfile{
					{Name: "docs", Match: []string{"docs"}, Harness: "pi"},
				},
			},
		}
		if err := fleetconfig.StoreFleetBase(homeDir, base); err != nil {
			t.Fatal(err)
		}
		r := &Runner{homeDir: homeDir, args: Args{ID: "docs-task", ProjectName: "alpha"}, dispatchPolicy: DispatchPolicyGeneralDirect}
		sel, ok := r.dispatchSelection()
		if !ok || sel.Harness != "pi" {
			t.Fatalf("selection = %+v, ok=%v; want base profile harness pi", sel, ok)
		}
	})
}

// TestDispatchPolicyStats is a compile-time guard that the policy constants
// stay the explicit two-choice surface (no third value silently appears).
func TestDispatchPolicyConstants(t *testing.T) {
	got := []DispatchPolicy{DispatchPolicyGeneralDirect, DispatchPolicyCaptainMediated}
	want := []DispatchPolicy{"general-direct", "captain-mediated"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatch policy constants = %v, want %v", got, want)
	}
}

// The policy error text names the exact refusing evidence so an operator never
// sees an unexplained boundary refusal.
func TestDispatchPolicyErrorCarriesMatrixEvidence(t *testing.T) {
	homeDir := t.TempDir()
	if err := home.SeedCaptainProvenance(homeDir, "sm-1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := ResolveDispatchPolicy(homeDir, "general")
	var perr *DispatchPolicyError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %T %v, want *DispatchPolicyError", err, err)
	}
	for _, want := range []string{"dispatch policy", "general-parent-on-captain-home", "sm-1", homeDir} {
		if !strings.Contains(perr.Error(), want) {
			t.Fatalf("error %q does not carry %q", perr.Error(), want)
		}
	}
}
