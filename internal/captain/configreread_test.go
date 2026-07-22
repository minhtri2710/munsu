package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// fakeBackendForConfigReread implements session.Backend for testing the
// config-reread injection through the acknowledged agent-prompt seam.
type fakeBackendForConfigReread struct {
	sendKeysCalls []string
	captureOutput string
	alive         bool
	captureError  bool
}

func (f *fakeBackendForConfigReread) NewWindow(_, _ string) (string, error) { return "test-window", nil }
func (f *fakeBackendForConfigReread) SendKeys(_, text string) error {
	f.sendKeysCalls = append(f.sendKeysCalls, text)
	return nil
}
func (f *fakeBackendForConfigReread) Capture(_ string, _ int) (string, error) {
	if f.captureError {
		return "", fmt.Errorf("capture failed")
	}
	return f.captureOutput, nil
}
func (f *fakeBackendForConfigReread) Alive(_ string) bool { return f.alive }
func (f *fakeBackendForConfigReread) Teardown(_ string) error { return nil }

// TestConfigRereadGenPath verifies the generation file path helper.
func TestConfigRereadGenPath(t *testing.T) {
	home := "/tmp/test-captain"
	expected := "/tmp/test-captain/state/.config-reread-gen"
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

// TestComputeInheritedConfigDigest_Empty verifies digest of empty config.
func TestComputeInheritedConfigDigest_Empty(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	os.MkdirAll(filepath.Join(home, "projects"), 0755)

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

		// Write soldier-harness config
		config.Set(home, "soldier-harness", "pi")
		// Write general-shared.md
		os.WriteFile(filepath.Join(home, "data", "general-shared.md"), []byte("shared content\n"), 0644)
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

// TestAdvanceConfigRereadGen_FirstPush verifies first push always advances.
func TestAdvanceConfigRereadGen_FirstPush(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "config"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)

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

	// First push → advances
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

	// First push → gen=1
	changed, _, _, _, err := AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first push")
	}

	// Write a config file → push → gen=2
	config.Set(home, "soldier-harness", "codex")
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

	// Delete the config file → push → gen=3
	os.Remove(filepath.Join(home, "config", "soldier-harness"))
	changed, gen, _, _, err = AdvanceConfigRereadGen(home)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true after config deletion")
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

	config.Set(home, "soldier-harness", "pi")

	// First push → gen=1
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

// TestConfigRereadNudgeMarker verifies write, read, and remove.
func TestConfigRereadNudgeMarker(t *testing.T) {
	home := t.TempDir()

	// No marker → not found
	_, _, found, err := ReadConfigRereadNudgeMarker(home)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected not found for missing marker")
	}

	// Write marker
	if err := WriteConfigRereadNudgeMarker(home, 3, "digest123"); err != nil {
		t.Fatal(err)
	}

	gen, digest, found, err := ReadConfigRereadNudgeMarker(home)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found after write")
	}
	if gen != 3 {
		t.Errorf("gen = %d, want 3", gen)
	}
	if digest != "digest123" {
		t.Errorf("digest = %q, want digest123", digest)
	}

	// Remove
	RemoveConfigRereadNudgeMarker(home)
	_, _, found, _ = ReadConfigRereadNudgeMarker(home)
	if found {
		t.Error("expected not found after remove")
	}
}

// TestConfigRereadInjectMessage verifies the literal CONFIG_REREAD: format.
func TestConfigRereadInjectMessage(t *testing.T) {
	msg := ConfigRereadInjectMessage(5, "abcdef1234567890")
	if !strings.HasPrefix(msg, "CONFIG_REREAD:") {
		t.Errorf("message should start with CONFIG_REREAD:, got %q", msg)
	}
	if !strings.Contains(msg, "generation=5") {
		t.Errorf("message should contain generation=5, got %q", msg)
	}
	if !strings.Contains(msg, "digest=abcdef123456") {
		t.Errorf("message should contain digest prefix, got %q", msg)
	}
}

// TestIsPaneSafeForInject_Empty verifies detection of empty composer.
func TestIsPaneSafeForInject_Empty(t *testing.T) {
	bk := &fakeBackendForConfigReread{
		captureOutput: "\u276F \n", // Claude prompt glyph
		alive:         true,
	}

	safe, verdict, err := IsPaneSafeForInject(bk, "test-window")
	if err != nil {
		t.Fatal(err)
	}
	if !safe {
		t.Errorf("expected safe (empty composer), got verdict=%s", verdict)
	}
}

// TestIsPaneSafeForInject_Pending verifies detection of pending content.
func TestIsPaneSafeForInject_Pending(t *testing.T) {
	bk := &fakeBackendForConfigReread{
		captureOutput: "\u276F type something\n",
		alive:         true,
	}

	safe, verdict, err := IsPaneSafeForInject(bk, "test-window")
	if err != nil {
		t.Fatal(err)
	}
	if safe {
		t.Errorf("expected unsafe (pending composer), got verdict=%s", verdict)
	}
}

// TestInjectConfigReread verifies the injection sends correct sentinel-marked
// CONFIG_REREAD message when composer is safe.
func TestInjectConfigReread(t *testing.T) {
	bk := &fakeBackendForConfigReread{
		captureOutput: "\u276F \n",
		alive:         true,
	}

	gen := 7
	digest := "fedcba9876543210"
	if err := InjectConfigReread(bk, "test-window", gen, digest); err != nil {
		t.Fatal(err)
	}

	if len(bk.sendKeysCalls) != 1 {
		t.Fatalf("expected 1 sendKeys call, got %d", len(bk.sendKeysCalls))
	}
	msg := bk.sendKeysCalls[0]
	if !strings.HasPrefix(msg, SentinelMark) {
		t.Errorf("message should start with sentinel marker, got %q", msg)
	}
	if !strings.Contains(msg, "CONFIG_REREAD:") {
		t.Errorf("message should contain CONFIG_REREAD:, got %q", msg)
	}
	if !strings.Contains(msg, "generation=7") {
		t.Errorf("message should contain generation=7, got %q", msg)
	}
}

// TestInjectConfigReread_UnsafeComposer verifies no inject when unsafe.
func TestInjectConfigReread_UnsafeComposer(t *testing.T) {
	bk := &fakeBackendForConfigReread{
		captureOutput: "\u276F some typed text\n",
		alive:         true,
	}

	if err := InjectConfigReread(bk, "test-window", 1, "digest"); err == nil {
		t.Error("expected error for unsafe composer")
	} else if !strings.Contains(err.Error(), "not safe") {
		t.Errorf("error should mention unsafe, got: %v", err)
	}

	if len(bk.sendKeysCalls) > 0 {
		t.Errorf("expected no sendKeys for unsafe composer, got %d calls", len(bk.sendKeysCalls))
	}
}

// TestInjectConfigReread_CaptureFailure verifies error handling on capture failure.
func TestInjectConfigReread_CaptureFailure(t *testing.T) {
	// Backend whose Capture returns an error
	bk := &fakeBackendForConfigReread{
		captureOutput: "",
		alive:         true,
	}
	bk.captureError = true

	if err := InjectConfigReread(bk, "test-window", 1, "digest"); err == nil {
		t.Error("expected error for capture failure")
	}
}

// TestConfigRereadQuarantine verifies quarantine preserves nudge marker data.
func TestConfigRereadQuarantine(t *testing.T) {
	home := t.TempDir()

	// No marker → quarantine returns empty
	qPath, err := QuarantineConfigRereadNudge(home)
	if err != nil {
		t.Fatal(err)
	}
	if qPath != "" {
		t.Errorf("expected empty quarantine path for no marker, got %q", qPath)
	}

	// Write marker then quarantine
	if err := WriteConfigRereadNudgeMarker(home, 5, "quarantine-test"); err != nil {
		t.Fatal(err)
	}

	qPath, err = QuarantineConfigRereadNudge(home)
	if err != nil {
		t.Fatal(err)
	}
	if qPath == "" {
		t.Fatal("expected non-empty quarantine path")
	}
	if !strings.Contains(qPath, ".config-reread-quarantine") {
		t.Errorf("quarantine path should contain quarantine dir, got %q", qPath)
	}

	// Marker should be gone
	_, _, found, _ := ReadConfigRereadNudgeMarker(home)
	if found {
		t.Error("marker should be removed after quarantine")
	}

	// Quarantine file should contain the data
	data, err := os.ReadFile(qPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "gen=5") {
		t.Errorf("quarantine file should contain gen=5, got %q", string(data))
	}
	if !strings.Contains(string(data), "digest=quarantine-test") {
		t.Errorf("quarantine file should contain digest, got %q", string(data))
	}
}

// TestConfigPushWithResult_GenerationAdvance verifies the full ConfigPushWithResult
// pipeline advances generation when content changes and stays unchanged when content
// is identical.
func TestConfigPushWithResult_GenerationAdvance(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test")
	if err := SeedWithParent("test", captainHome, parent, ""); err != nil {
		t.Fatal(err)
	}
	// SeedWithParent already calls ConfigPush, so generation is at 1.
	// Read the current state to confirm.
	_, _, found, _ := ReadConfigRereadGen(captainHome)
	if !found {
		t.Fatal("generation should exist after seed")
	}

	// Push with same seed content → unchanged
	res, err := ConfigPushWithResult(parent, captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Errorf("expected unchanged on identical push after seed, got gen=%d", res.Generation)
	}

	// Change parent config → push → changed
	if err := config.Set(parent, "soldier-harness", "codex"); err != nil {
		t.Fatal(err)
	}
	res, err = ConfigPushWithResult(parent, captainHome)
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
	res, err = ConfigPushWithResult(parent, captainHome)
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
	_, err := ConfigPushWithResult(parent, "/nonexistent")
	if err == nil {
		t.Error("expected error for unmarked home")
	}
}
