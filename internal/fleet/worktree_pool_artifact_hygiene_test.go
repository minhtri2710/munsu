//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
)

func TestWorktreePoolArtifactHygieneForceRefusesUnanchoredArtifacts(t *testing.T) {
	opts, auth, worktree := retireScoutFixture(t, false)
	charterPath := filepath.Join(worktree, CharterName)
	before, err := os.ReadFile(charterPath)
	if err != nil {
		t.Fatal(err)
	}

	rec := &recordingTeardown{alive: false}
	opts.Force = true
	_, err = RetireTask(opts, rec, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("forced retirement with unanchored launch artifacts released the worktree")
	}
	if !strings.Contains(err.Error(), "manifest") && !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("error = %v, want artifact-proof refusal", err)
	}
	if len(rec.returned) != 0 {
		t.Fatalf("returned=%v, want lease retained", rec.returned)
	}
	after, err := os.ReadFile(charterPath)
	if err != nil {
		t.Fatalf("unanchored charter was removed: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("unanchored charter changed")
	}
	agg, err := auth.Get(mustTaskID(t, opts.ID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != "active" {
		t.Fatalf("cleanup claim=%+v, want active pending custody", agg.CleanupClaim)
	}
}

func TestWorktreePoolArtifactHygieneForceRefusesTamperedAnchoredArtifact(t *testing.T) {
	opts, auth, worktree := retireScoutFixture(t, true)
	charterPath := filepath.Join(worktree, CharterName)
	if err := os.WriteFile(charterPath, []byte("untrusted replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &recordingTeardown{alive: false}
	opts.Force = true
	_, err := RetireTask(opts, rec, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("forced retirement released a worktree with a tampered anchored artifact")
	}
	if len(rec.returned) != 0 {
		t.Fatalf("returned=%v, want lease retained", rec.returned)
	}
	got, readErr := os.ReadFile(charterPath)
	if readErr != nil {
		t.Fatalf("tampered artifact was removed: %v", readErr)
	}
	if string(got) != "untrusted replacement\n" {
		t.Fatalf("tampered artifact changed to %q", got)
	}
}

func TestWorktreePoolArtifactHygieneAnchoredArtifactsReachProviderOnlyAfterProof(t *testing.T) {
	opts, auth, worktree := retireScoutFixture(t, true)
	agg, err := auth.Get(mustTaskID(t, opts.ID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil || agg.Worktree.LaunchManifest == nil {
		t.Fatal("fixture lacks canonical manifest anchor")
	}
	manifestSHA := agg.Worktree.LaunchManifest.ManifestSHA256
	called := false
	rec := &recordingTeardown{alive: false, onReturn: func() {
		called = true
		for _, name := range append(append([]string(nil), CoreLaunchArtifactNames...), ManifestName) {
			if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(name))); !os.IsNotExist(err) {
				t.Fatalf("provider return began before proven artifact %s was removed: %v", name, err)
			}
		}
		if err := VerifyLaunchArtifacts(worktree, manifestSHA); err == nil {
			t.Fatal("provider return observed launch artifacts still present")
		}
		if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
			t.Fatalf("tracked user content removed before provider return: %v", err)
		}
	}}

	if _, err := RetireTask(opts, rec, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("anchored clean retirement: %v", err)
	}
	if !called || len(rec.returned) != 1 || rec.returned[0] != worktree {
		t.Fatalf("return calls=%v called=%v, want one return after proof", rec.returned, called)
	}
	for _, name := range append(append([]string(nil), CoreLaunchArtifactNames...), ManifestName) {
		if _, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Fatalf("proven launch artifact %s remains after pool return: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatalf("tracked user content was removed: %v", err)
	}
}

func TestWorktreePoolArtifactHygieneCleanUnanchoredTreeHasNoLaunchOwner(t *testing.T) {
	opts, auth, worktree := retireScoutFixture(t, false)
	opts.Force = true
	for _, name := range append(append([]string(nil), CoreLaunchArtifactNames...), ManifestName) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
			t.Fatalf("removing disposable fixture artifact %s: %v", name, err)
		}
	}
	guardDir := filepath.Join(worktree, ".soldier-launch-guard-manifest-test-1")
	if err := os.Remove(filepath.Join(guardDir, "identity")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("removing fixture guard identity: %v", err)
	}
	if err := os.Remove(guardDir); err != nil && !os.IsNotExist(err) {
		t.Fatalf("removing fixture guard directory: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(worktree, "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	rec := &recordingTeardown{alive: false}
	if _, err := RetireTask(opts, rec, fakeRetirementJournals{}, auth); err != nil {
		t.Fatalf("clean unanchored worktree should pass hygiene preflight: %v", err)
	}
	if len(rec.returned) != 1 || rec.returned[0] != worktree {
		t.Fatalf("returned=%v, want one return of clean unanchored worktree", rec.returned)
	}
	after, err := os.ReadFile(filepath.Join(worktree, "README.md"))
	if err != nil {
		t.Fatalf("provider fake removed tracked user content: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("tracked user content changed")
	}
}

func TestWorktreePoolArtifactHygieneRejectsMissingArtifactOnRetry(t *testing.T) {
	opts, auth, worktree := retireScoutFixture(t, true)
	opts.Force = true
	interrupted := &recordingTeardown{alive: false, returnErr: errors.New("simulated provider-return interruption")}
	if _, err := RetireTask(opts, interrupted, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected first retirement to leave cleanup pending")
	}
	agg, err := auth.Get(mustTaskID(t, opts.ID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != "active" {
		t.Fatalf("first cleanup claim=%+v, want active pending custody", agg.CleanupClaim)
	}

	// Disposal completed before provider return failed. A missing canonical
	// manifest is not a completion receipt, so retry must retain custody.
	if _, err := os.Lstat(filepath.Join(worktree, ManifestName)); !os.IsNotExist(err) {
		t.Fatalf("interrupted provider return left manifest present: %v", err)
	}

	retry := &recordingTeardown{alive: false}
	_, err = RetireTask(opts, retry, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("retry treated an absent declared artifact as completed disposal")
	}
	if len(retry.returned) != 0 {
		t.Fatalf("retry returned worktree despite absent artifact evidence: %v", retry.returned)
	}
	agg, err = auth.Get(mustTaskID(t, opts.ID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != "active" {
		t.Fatalf("cleanup claim=%+v, want active pending custody", agg.CleanupClaim)
	}
}

func TestWorktreePoolArtifactHygieneBoundReentryPreservesAnchoredArtifacts(t *testing.T) {
	f := newLaunchFixture(t, "pool-artifact-bound-reentry")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("complete initial launch: %v", err)
	}
	before := f.aggregate()
	if before.Worktree == nil || before.Worktree.LaunchManifest == nil {
		t.Fatal("completed launch lacks canonical worktree manifest")
	}
	worktreePath := f.runner.wtPath
	manifestSHA := before.Worktree.LaunchManifest.ManifestSHA256
	if err := VerifyLaunchArtifacts(worktreePath, manifestSHA); err != nil {
		t.Fatalf("initial launch artifacts are not anchored: %v", err)
	}

	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent re-entry: %v", err)
	}
	f.runner.wtPath = ""
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree re-entry: %v", err)
	}
	if f.runner.wtPath != worktreePath {
		t.Fatalf("re-entry worktree=%q, want canonical bound path %q", f.runner.wtPath, worktreePath)
	}
	if err := VerifyLaunchArtifacts(f.runner.wtPath, manifestSHA); err != nil {
		t.Fatalf("re-entry lost its own anchored artifacts: %v", err)
	}
	after := f.aggregate()
	if after.Worktree == nil || after.Worktree.LaunchManifest == nil || after.Worktree.LaunchManifest.ManifestSHA256 != manifestSHA {
		t.Fatalf("canonical anchor changed during re-entry: %+v", after.Worktree)
	}
}

func TestWorktreePoolArtifactHygieneContaminatedReservationRetainsCustody(t *testing.T) {
	f := newLaunchFixture(t, "pool-artifact-contaminated")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	reservationID := f.runner.wtReservationID()
	reservedPath, ok, err := backend.ReservedWorktreePath(f.homeDir, f.repoPath, reservationID)
	if err != nil || !ok {
		t.Fatalf("ReservedWorktreePath = (%q, %v, %v)", reservedPath, ok, err)
	}
	if err := os.MkdirAll(reservedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(reservedPath, CharterName)
	if err := os.WriteFile(artifact, []byte("unanchored prior content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	originalReturn := returnWorktree
	called := false
	returnWorktree = func(string, string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { returnWorktree = originalReturn })

	if err := f.runner.acquireWorktree(); err == nil {
		t.Fatal("acquisition accepted an unanchored launch artifact")
	}
	gotPath, err := filepath.EvalSymlinks(f.runner.wtPath)
	if err != nil {
		t.Fatalf("resolving acquired path: %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(reservedPath)
	if err != nil {
		t.Fatalf("resolving reservation path: %v", err)
	}
	if gotPath != wantPath {
		t.Fatalf("acquired path=%q, want reservation path %q retained for custody", gotPath, wantPath)
	}
	if err := f.runner.returnWorktreeOnFailure(); err != nil {
		t.Fatalf("failure cleanup should retain contaminated reservation without returning: %v", err)
	}
	if called {
		t.Fatal("contaminated reservation was returned to provider")
	}
	got, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("contaminated artifact was removed: %v", err)
	}
	if string(got) != "unanchored prior content\n" {
		t.Fatalf("contaminated artifact changed to %q", got)
	}
	if resolved, ok, err := backend.ReservedWorktreePath(f.homeDir, f.repoPath, reservationID); err != nil || !ok || resolved != reservedPath {
		t.Fatalf("reservation custody no longer discoverable: (%q, %v, %v)", resolved, ok, err)
	}
}
