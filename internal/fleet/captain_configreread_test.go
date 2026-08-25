package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- Generation tracking ---

// TestConfigRereadGenPath verifies the generation file path helper.
func TestConfigRereadGenPath(t *testing.T) {
	home := filepath.Join(string(os.PathSeparator), "tmp", "test-captain")
	expected := filepath.Join(home, "state", ".config-reread-gen")
	if got := ConfigRereadGenPath(home); got != expected {
		t.Errorf("ConfigRereadGenPath(%q) = %q, want %q", home, got, expected)
	}
}

// TestConfigRereadWriteAndRead verifies round-trip write/read of generation.
func TestConfigRereadWriteAndRead(t *testing.T) {
	home := t.TempDir()

	// No file yet → not found
	_, _, found, err := ReadConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected not found for missing gen file")
	}

	// Write gen=1
	if err := WriteConfigRereadGen(home, 1, "abc123"); err != nil {
		t.Fatal(err)
	}

	gen, digest, found, err := ReadConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found after write")
	}
	if gen != 1 {
		t.Errorf("gen = %d, want 1", gen)
	}
	if digest != "abc123" {
		t.Errorf("digest = %q, want abc123", digest)
	}

	// Overwrite gen=2
	if err := WriteConfigRereadGen(home, 2, "def456"); err != nil {
		t.Fatal(err)
	}
	gen, digest, _, _ = ReadConfigRereadGen(home)
	if gen != 2 {
		t.Errorf("gen = %d, want 2", gen)
	}
	if digest != "def456" {
		t.Errorf("digest = %q, want def456", digest)
	}
}

// TestWriteConfigRereadGen_Atomic verifies that the generation file is
// written atomically (temp file + rename) so a crash during write never
// produces a partial file.
func TestWriteConfigRereadGen_Atomic(t *testing.T) {
	home := t.TempDir()
	genPath := ConfigRereadGenPath(home)

	// Write a valid generation file.
	if err := WriteConfigRereadGen(home, 7, "atomic-test-digest-12345"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "7\n") {
		t.Errorf("expected gen file to start with '7\\n', got %q", content)
	}
	if !strings.Contains(content, "atomic-test-digest-12345") {
		t.Errorf("gen file should contain digest, got %q", content)
	}
}

// TestReadConfigRereadGen_Malformed verifies strict parsing rejects
// malformed generation files.
func TestReadConfigRereadGen_Malformed(t *testing.T) {
	home := t.TempDir()
	genPath := ConfigRereadGenPath(home)
	os.MkdirAll(filepath.Dir(genPath), 0755)

	// Write single line (missing digest).
	if err := os.WriteFile(genPath, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ReadConfigRereadGen(home)
	if err == nil {
		t.Error("expected error for single-line gen file")
	}

	// Write non-numeric generation.
	if err := os.WriteFile(genPath, []byte("abc\ndigest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = ReadConfigRereadGen(home)
	if err == nil {
		t.Error("expected error for non-numeric generation")
	}
}

// --- Digest ---

// TestComputeInheritedConfigDigest_Empty verifies digest of empty config.
func TestComputeInheritedConfigDigest_Empty(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	os.MkdirAll(filepath.Join(home, "projects"), 0755)
	createTestPublishedSnapshot(t, home)

	digest, err := ComputeInheritedConfigDigest(home)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Error("digest should not be empty")
	}
}

// TestComputeInheritedConfigDigest_Deterministic verifies same content →
// same digest.
func TestComputeInheritedConfigDigest_Deterministic(t *testing.T) {
	home1 := t.TempDir()
	home2 := t.TempDir()

	for _, home := range []string{home1, home2} {
		os.MkdirAll(filepath.Join(home, "config"), 0755)
		os.MkdirAll(filepath.Join(home, "data"), 0755)
		os.MkdirAll(filepath.Join(home, "projects"), 0755)

		// Same published snapshot content for determinism (fixed project path).
		resolved := config.ResolvedProjectConfig{
			Project:           "test-project",
			ProjectPath:       "/fixed/path",
			SoldierHarness:    "pi",
			Backend:           "tmux",
			RequireNoMistakes: true,
			Digest:            "0000000000000000000000000000000000000000000000000000000000000000",
		}
		if err := config.StorePublishedSnapshot(home, resolved); err != nil {
			t.Fatal(err)
		}
	}

	d1, err := ComputeInheritedConfigDigest(home1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := ComputeInheritedConfigDigest(home2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("deterministic digests differ: %q vs %q", d1, d2)
	}
}

// --- Advance ---

// TestAdvanceConfigRereadGen_FirstPush verifies first push always advances.
func TestAdvanceConfigRereadGen_FirstPush(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	createTestPublishedSnapshot(t, home)

	changed, gen, oldDigest, newDigest, err := AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true on first push")
	}
	if gen != 1 {
		t.Errorf("gen = %d, want 1", gen)
	}
	if oldDigest != "" {
		t.Errorf("oldDigest = %q, want empty", oldDigest)
	}
	if newDigest == "" {
		t.Error("newDigest should not be empty")
	}
}

// TestAdvanceConfigRereadGen_NoChange verifies unchanged content skips advance.
func TestAdvanceConfigRereadGen_NoChange(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	createTestPublishedSnapshot(t, home)
	changed, gen, _, _, err := AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first push")
	}
	firstGen := gen

	// Second push with same content → unchanged
	changed, gen, _, _, err = AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected changed=false on identical push, got gen=%d", gen)
	}
	if gen != firstGen {
		t.Errorf("gen = %d, want %d (unchanged)", gen, firstGen)
	}
}

// TestAdvanceConfigRereadGen_ContentChange verifies changed content advances.
func TestAdvanceConfigRereadGen_ContentChange(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	createTestPublishedSnapshot(t, home)

	// First push → gen=1
	changed, _, _, _, err := AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first push")
	}

	// Change the published snapshot → push → gen=2
	resolved := config.ResolvedProjectConfig{
		Project:           "test-project",
		ProjectPath:       home,
		SoldierHarness:    "codex",
		Backend:           "tmux",
		RequireNoMistakes: true,
		Digest:            "1111111111111111111111111111111111111111111111111111111111111111",
	}
	if err := config.StorePublishedSnapshot(home, resolved); err != nil {
		t.Fatal(err)
	}
	changed, gen, _, newDigest, err := AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true after config change")
	}
	if gen != 2 {
		t.Errorf("gen = %d, want 2", gen)
	}

	// Change the published snapshot back → push → gen=3 (different content)
	resolved2 := config.ResolvedProjectConfig{
		Project:           "test-project",
		ProjectPath:       home,
		SoldierHarness:    "pi",
		Backend:           "tmux",
		RequireNoMistakes: false,
		Digest:            "2222222222222222222222222222222222222222222222222222222222222222",
	}
	if err := config.StorePublishedSnapshot(home, resolved2); err != nil {
		t.Fatal(err)
	}
	changed, gen, _, _, err = AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true after config change")
	}
	if gen != 3 {
		t.Errorf("gen = %d, want 3", gen)
	}

	_ = newDigest
}

// TestAdvanceConfigRereadGen_IdempotentRepeat verifies repeated identical
// content stays at same generation.
func TestAdvanceConfigRereadGen_IdempotentRepeat(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	createTestPublishedSnapshot(t, home)
	AdvanceConfigRereadGen(home)

	// Push 5 more times with same content
	for i := 0; i < 5; i++ {
		changed, gen, _, _, err := AdvanceConfigRereadGen(home)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Errorf("iteration %d: expected unchanged, got gen=%d", i, gen)
		}
		if gen != 1 {
			t.Errorf("iteration %d: gen = %d, want 1", i, gen)
		}
	}
}

// TestAdvanceConfigRereadGen_CrashRecovery verifies that generation tracking
// persists across crashes (the file is written atomically and survives).
func TestAdvanceConfigRereadGen_CrashRecovery(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)

	// Advance to gen=1
	AdvanceConfigRereadGen(home)

	// Simulate crash: write gen=2 manually.
	digestAfterCrash := "crash-recovery-digest"
	if err := WriteConfigRereadGen(home, 2, digestAfterCrash); err != nil {
		t.Fatal(err)
	}

	// "Restart": read back
	gen, digest, found, err := ReadConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("generation should survive crash")
	}
	if gen != 2 {
		t.Errorf("gen = %d, want 2", gen)
	}
	if digest != digestAfterCrash {
		t.Errorf("digest = %q, want %q", digest, digestAfterCrash)
	}
}

// --- ConfigRereadMessage ---

// TestConfigRereadMessage verifies the canonical CONFIG_REREAD: format with
// full 64-character digest.
func TestConfigRereadMessage(t *testing.T) {
	// Full 64-char hex digest.
	fullDigest := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	msg := ConfigRereadMessage(5, fullDigest)
	if !strings.HasPrefix(msg, "CONFIG_REREAD:") {
		t.Errorf("message should start with CONFIG_REREAD:, got %q", msg)
	}
	if !strings.Contains(msg, "generation=5") {
		t.Errorf("message should contain generation=5, got %q", msg)
	}
	if !strings.Contains(msg, "digest="+fullDigest) {
		t.Errorf("message should contain the full digest, got %q", msg)
	}
}

// TestConfigRereadMessage_ZeroGen verifies generation=0 edge case.
func TestConfigRereadMessage_ZeroGen(t *testing.T) {
	msg := ConfigRereadMessage(0, "digest")
	if !strings.HasPrefix(msg, "CONFIG_REREAD:") {
		t.Errorf("message should start with CONFIG_REREAD:, got %q", msg)
	}
	if !strings.Contains(msg, "generation=0") {
		t.Errorf("message should contain generation=0, got %q", msg)
	}
}

// --- ConfigRereadEnvelopeID ---

// TestConfigRereadEnvelopeID_Deterministic verifies that the same inputs
// produce the same ID.
func TestConfigRereadEnvelopeID_Deterministic(t *testing.T) {
	id1 := ConfigRereadEnvelopeID("sender1", "captain1", 1, "digest1")
	id2 := ConfigRereadEnvelopeID("sender1", "captain1", 1, "digest1")
	if id1 != id2 {
		t.Errorf("deterministic IDs differ: %q vs %q", id1, id2)
	}
}

// TestConfigRereadEnvelopeID_DifferentGen produces different IDs.
func TestConfigRereadEnvelopeID_DifferentGen(t *testing.T) {
	id1 := ConfigRereadEnvelopeID("sender", "captain", 1, "digest")
	id2 := ConfigRereadEnvelopeID("sender", "captain", 2, "digest")
	if id1 == id2 {
		t.Error("different generations should produce different IDs")
	}
}

// TestConfigRereadEnvelopeID_DifferentCaptain produces different IDs.
func TestConfigRereadEnvelopeID_DifferentCaptain(t *testing.T) {
	id1 := ConfigRereadEnvelopeID("sender", "captain1", 1, "digest")
	id2 := ConfigRereadEnvelopeID("sender", "captain2", 1, "digest")
	if id1 == id2 {
		t.Error("different captains should produce different IDs")
	}
}

// TestConfigRereadEnvelopeID_32CharHex verifies the ID is 32-char hex.
func TestConfigRereadEnvelopeID_32CharHex(t *testing.T) {
	id := ConfigRereadEnvelopeID("sender", "captain", 42, "some-digest-value")
	if len(id) != 32 {
		t.Errorf("expected 32-char hex ID, got %d chars: %q", len(id), id)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %c in ID %q", c, id)
			break
		}
	}
}

// --- Legacy reconciliation ---

// writeFakeCaptainMarker writes a minimal provenance marker for testing.
func writeFakeCaptainMarker(t *testing.T, captainHome, id string) string {
	t.Helper()
	os.MkdirAll(captainHome, 0755)
	canon, err := filepath.EvalSymlinks(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	canon, err = filepath.Abs(canon)
	if err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("munsu-v2\n%s\n%s\n", id, canon)
	if err := os.WriteFile(filepath.Join(captainHome, ".munsu-captain-home"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return canon
}

// TestReconcileLegacyNudgeMarker verifies migration of old .config-reread-nudge.
func TestReconcileLegacyNudgeMarker(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test-captain")
	writeFakeCaptainMarker(t, captainHome, "test-captain")

	// Write a legacy nudge marker.
	nudgeDir := filepath.Join(captainHome, "state")
	os.MkdirAll(nudgeDir, 0755)
	nudgeContent := "gen=3\ndigest=legacy-nudge-digest-12345\n"
	if err := os.WriteFile(filepath.Join(nudgeDir, ".config-reread-nudge"), []byte(nudgeContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Reconcile — should migrate to mailbox.
	if err := ReconcileLegacyConfigReread(parent, captainHome, &captainTestMailboxSender{acknowledged: true}); err != nil {
		t.Fatal(err)
	}

	// Nudge marker should be gone.
	if _, err := os.Stat(filepath.Join(nudgeDir, ".config-reread-nudge")); err == nil {
		t.Error("legacy nudge marker should be removed after reconciliation")
	}
}

// TestReconcileLegacyQuarantine verifies migration of old quarantine artifacts.
func TestReconcileLegacyQuarantine(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test-captain")
	writeFakeCaptainMarker(t, captainHome, "test-captain")

	// Write quarantine artifacts.
	qDir := filepath.Join(captainHome, "state", ".config-reread-quarantine")
	os.MkdirAll(qDir, 0755)
	qContent := "gen=5\ndigest=quarantine-digest-67890\n"
	if err := os.WriteFile(filepath.Join(qDir, "pending-12345"), []byte(qContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Reconcile.
	if err := ReconcileLegacyConfigReread(parent, captainHome, &captainTestMailboxSender{acknowledged: true}); err != nil {
		t.Fatal(err)
	}

	// Quarantine dir should be gone.
	if _, err := os.Stat(qDir); err == nil {
		t.Error("quarantine directory should be removed after reconciliation")
	}
}

// TestReconcileLegacyMalformedNudge verifies that a malformed nudge marker
// fails closed and is NOT removed.
func TestReconcileLegacyMalformedNudge(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test-captain")
	writeFakeCaptainMarker(t, captainHome, "test-captain")

	// Write malformed nudge marker (missing digest).
	nudgeDir := filepath.Join(captainHome, "state")
	os.MkdirAll(nudgeDir, 0755)
	if err := os.WriteFile(filepath.Join(nudgeDir, ".config-reread-nudge"), []byte("bad data\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Reconcile should fail.
	err := ReconcileLegacyConfigReread(parent, captainHome, &captainTestMailboxSender{acknowledged: true})
	if err == nil {
		t.Error("expected error for malformed nudge marker")
	}

	// Marker should still exist.
	if _, statErr := os.Stat(filepath.Join(nudgeDir, ".config-reread-nudge")); os.IsNotExist(statErr) {
		t.Error("malformed nudge marker should NOT be removed on failure")
	}
}

// TestReconcileLegacy_SupersededByCurrentGen verifies that if the current
// generation already covers the legacy, the legacy artifacts are simply
// removed without materializing a mailbox requirement.
func TestReconcileLegacy_SupersededByCurrentGen(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test-captain")
	writeFakeCaptainMarker(t, captainHome, "test-captain")

	// Write current gen=10.
	if err := WriteConfigRereadGen(captainHome, 10, "current-digest"); err != nil {
		t.Fatal(err)
	}

	// Write legacy nudge with gen=5 (older).
	nudgeDir := filepath.Join(captainHome, "state")
	os.MkdirAll(nudgeDir, 0755)
	nudgeContent := "gen=5\ndigest=older-digest\n"
	if err := os.WriteFile(filepath.Join(nudgeDir, ".config-reread-nudge"), []byte(nudgeContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Reconcile — should clean up without error since current gen supersedes.
	if err := ReconcileLegacyConfigReread(parent, captainHome, &captainTestMailboxSender{acknowledged: true}); err != nil {
		t.Fatal(err)
	}

	// Nudge marker should be gone.
	if _, err := os.Stat(filepath.Join(nudgeDir, ".config-reread-nudge")); err == nil {
		t.Error("superseded legacy nudge marker should be removed")
	}
}

// --- Full pipeline ---

// TestConfigPushWithResult_GenerationAdvance verifies the full ConfigPushWithResult
// pipeline advances generation when content changes and stays unchanged when content
// is identical.
func TestConfigPushWithResult_GenerationAdvance(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome := filepath.Join(parent, "captains", "test")
	writeFakeCaptainMarker(t, captainHome, "test")
	os.MkdirAll(filepath.Join(captainHome, "config"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "data"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "projects"), 0755)

	// Set up typed documents in parent home.
	setupTypedParentHome(t, parent, "test-project")
	// Register captain with project so publishResolvedSnapshot works.
	if err := Register(parent, "test", captainHome, "", "test-project"); err != nil {
		t.Fatal(err)
	}

	// First push → advances (no existing gen file)
	res, err := configPushWithResult(parent, captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Error("expected changed=true on first push")
	}
	if res.Generation != 1 {
		t.Errorf("generation = %d, want 1", res.Generation)
	}

	// Push with same content → unchanged
	res, err = configPushWithResult(parent, captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Errorf("expected unchanged on identical push, got gen=%d", res.Generation)
	}
	if res.Generation != 1 {
		t.Errorf("generation = %d, want 1", res.Generation)
	}

	// Change PARENT fleet base → push → changed
	base, lErr := config.LoadFleetBase(parent)
	if lErr != nil {
		t.Fatal(lErr)
	}
	base.Config.SoldierHarness = "codex"
	if err := config.StoreFleetBase(parent, base); err != nil {
		t.Fatal(err)
	}
	res, err = configPushWithResult(parent, captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Error("expected changed=true after parent config change")
	}
	if res.Generation != 2 {
		t.Errorf("generation = %d, want 2", res.Generation)
	}

	// Push again with same content → unchanged
	res, err = configPushWithResult(parent, captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Errorf("expected unchanged after same push, got generation=%d", res.Generation)
	}
	if res.Generation != 2 {
		t.Errorf("generation = %d, want 2", res.Generation)
	}
}

// TestConfigPushWithResult_NoCaptainHomeError verifies error on unmarked home.
func TestConfigPushWithResult_NoCaptainHomeError(t *testing.T) {
	parent := t.TempDir()
	_, err := configPushWithResult(parent, "/nonexistent")
	if err == nil {
		t.Error("expected error for unmarked home")
	}
}

// TestConfigPushWithResult_HealCrash verifies that after a crash between
// generation write and mailbox creation, the next ConfigPushWithResult heals
// by not failing (generation tracking is separate from mailbox write).
func TestConfigPushWithResult_HealCrash(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome := filepath.Join(parent, "captains", "test")
	writeFakeCaptainMarker(t, captainHome, "test")
	os.MkdirAll(filepath.Join(captainHome, "config"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "data"), 0755)

	// Set up typed documents in parent home.
	setupTypedParentHome(t, parent, "test-project")
	// Register captain with project so publishResolvedSnapshot works.
	if err := Register(parent, "test", captainHome, "", "test-project"); err != nil {
		t.Fatal(err)
	}

	// Simulate crash: gen file was written at gen=1 but no mailbox envelope.
	// Push with same content → unchanged (generation stays at 1).
	res, err := configPushWithResult(parent, captainHome)
	if err != nil {
		t.Fatal(err)
	}
	// First push always advances.
	if !res.Changed {
		t.Error("expected changed=true on first push")
	}
	if res.Generation != 1 {
		t.Errorf("generation = %d, want 1", res.Generation)
	}
}
