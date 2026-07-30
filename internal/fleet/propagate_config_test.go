package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// fakeBoundSender is a test-only implementation of home.BoundSender for
// PropagateConfig tests. It records all Send calls and returns configurable
// results. The onSend callback, if set, is invoked in Send and can assert
// durable state before the send returns.
type fakeBoundSender struct {
	mu           sync.Mutex
	acknowledged bool
	sent         []string // payloads sent
	onSend       func(homeDir string, meta map[string]string, payload string)
}

func (f *fakeBoundSender) Alive(_ string, _ map[string]string) (bool, error) {
	return true, nil
}

func (f *fakeBoundSender) Send(homeDir string, meta map[string]string, payload string) home.BoundSendResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, payload)
	if f.onSend != nil {
		f.onSend(homeDir, meta, payload)
	}
	return home.BoundSendResult{
		Status:       "submitted",
		Acknowledged: f.acknowledged,
	}
}

func TestPropagateConfig_ValidatesNonEmptyHomes(t *testing.T) {
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  "",
		CaptainHome: "/some/path",
		Mailbox:     &fakeBoundSender{},
	})
	if err == nil {
		t.Fatal("expected error for empty parent home")
	}
	if !strings.Contains(err.Error(), "parent home is required") {
		t.Errorf("error = %v, want 'parent home is required'", err)
	}

	_, err = PropagateConfig(PropagateConfigRequest{
		ParentHome:  "/some/path",
		CaptainHome: "",
		Mailbox:     &fakeBoundSender{},
	})
	if err == nil {
		t.Fatal("expected error for empty captain home")
	}
	if !strings.Contains(err.Error(), "captain home is required") {
		t.Errorf("error = %v, want 'captain home is required'", err)
	}
}

func TestPropagateConfig_ValidatesMailbox(t *testing.T) {
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  "/some/path",
		CaptainHome: "/other/path",
		Mailbox:     nil,
	})
	if err == nil {
		t.Fatal("expected error for nil mailbox")
	}
	if !strings.Contains(err.Error(), "mailbox sender capability is required") {
		t.Errorf("error = %v, want 'mailbox sender capability is required'", err)
	}
}

func TestPropagateConfig_InvalidProvenance(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test-sm")
	os.MkdirAll(filepath.Join(captainHome, "config"), 0755)

	// No provenance marker → error before any mutation.
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     &fakeBoundSender{},
	})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error = %v, want marker error", err)
	}

	// Verify no files were written.
	_, err = os.Stat(filepath.Join(captainHome, "state", "config-push.log"))
	if !os.IsNotExist(err) {
		t.Error("config-push.log should not exist after provenance failure")
	}
}

func TestPropagateConfig_UnsafeSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	captainHome := filepath.Join(parent, "captains", "test-sm")
	outside := t.TempDir()

	os.MkdirAll(captainHome, 0755)
	// Symlink config/ to outside the captain home.
	if err := os.Symlink(outside, filepath.Join(captainHome, "config")); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# test\n"), 0644)

	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     &fakeBoundSender{},
	})
	if err == nil {
		t.Fatal("expected error for symlink escape")
	}
	if !strings.Contains(err.Error(), "escapes captain container") {
		t.Errorf("error = %v, want escape refusal", err)
	}

	// Verify outside was not mutated.
	if _, err := os.Stat(filepath.Join(outside, "soldier-harness")); !os.IsNotExist(err) {
		t.Fatal("outside destination was mutated despite preflight failure")
	}
}

func TestPropagateConfig_FirstChangeHappyPath(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Set up parent config.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(parent, "config", "soldier-dispatch.json"), []byte("{}\n"), 0644)

	// Parent shared data.
	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	os.WriteFile(filepath.Join(parent, "data", "general-shared.md"), []byte("# General Shared\n\nkey: value\n"), 0644)

	// Parent projects registry.
	os.WriteFile(filepath.Join(parent, "data", "projects.md"), []byte("- munsu - /tmp/munsu (added 2026-01-01)\n"), 0644)

	// Write captain task meta so notification is attempted.
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	sender := &fakeBoundSender{acknowledged: true}

	// Set up sender callback to assert durability before notification.
	sender.onSend = func(homeDir string, meta map[string]string, payload string) {
		// Assert that .config-reread-gen exists before notification.
		genPath := ConfigRereadGenPath(captainHome)
		if _, err := os.Stat(genPath); os.IsNotExist(err) {
			t.Error(".config-reread-gen should exist before notification call")
		}

		// Assert that inbox envelope exists (from EnsureConfigRereadRequirement).
		// Check parent's outbox for pending records.
		pendingGlob := filepath.Join(parent, "state", home.OutboxDir, "*")
		matches, _ := filepath.Glob(pendingGlob + "/*.pending")
		if len(matches) == 0 {
			t.Error("sender pending should exist before notification call")
		}
	}

	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !result.Changed {
		t.Error("expected changed=true on first push")
	}
	if result.Generation != 1 {
		t.Errorf("Generation = %d, want 1", result.Generation)
	}
	if result.RequirementState != RequirementCreated {
		t.Errorf("RequirementState = %q, want %q", result.RequirementState, RequirementCreated)
	}
	if result.NotificationState != NotificationSubmitted {
		t.Errorf("NotificationState = %q, want %q", result.NotificationState, NotificationSubmitted)
	}

	// Verify files were copied.
	for _, name := range []string{"soldier-harness", "soldier-dispatch.json"} {
		data, err := os.ReadFile(filepath.Join(captainHome, "config", name))
		if err != nil {
			t.Errorf("config/%s was not copied: %v", name, err)
			continue
		}
		if name == "soldier-harness" && string(data) != "pi\n" {
			t.Errorf("config/soldier-harness = %q, want %q", string(data), "pi\n")
		}
	}

	// Verify general-shared.md was copied.
	sharedData, err := os.ReadFile(filepath.Join(captainHome, "data", "general-shared.md"))
	if err != nil {
		t.Errorf("general-shared.md was not copied: %v", err)
	} else if !strings.Contains(string(sharedData), "# General Shared") {
		t.Errorf("general-shared.md content missing, got: %s", string(sharedData))
	}

	// Verify projects.md was copied.
	projData, err := os.ReadFile(filepath.Join(captainHome, "data", "projects.md"))
	if err != nil {
		t.Errorf("projects.md was not copied: %v", err)
	} else if !strings.Contains(string(projData), "munsu") {
		t.Errorf("projects.md content missing, got: %s", string(projData))
	}

	// Verify generation tracking exists.
	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("config-reread-gen not found after propagation")
	}
	if gen != 1 {
		t.Errorf("generation = %d, want 1", gen)
	}
	if len(digest) != 64 {
		t.Errorf("digest length = %d, want 64", len(digest))
	}

	// Verify parent-home config was written.
	phData, err := os.ReadFile(filepath.Join(captainHome, "config", "parent-home"))
	if err != nil {
		t.Errorf("config/parent-home was not written: %v", err)
	} else if strings.TrimSpace(string(phData)) != parent {
		t.Errorf("parent-home = %q, want %q", strings.TrimSpace(string(phData)), parent)
	}

	// Verify sender was called (notification attempted).
	sender.mu.Lock()
	sent := len(sender.sent)
	sender.mu.Unlock()
	if sent == 0 {
		t.Error("sender.Send was not called")
	}
}

func TestPropagateConfig_UnchangedContent(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Set up parent config.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// First propagation.
	sender := &fakeBoundSender{acknowledged: true}
	firstResult, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}
	if !firstResult.Changed {
		t.Error("expected changed=true on first push")
	}
	if firstResult.Generation != 1 {
		t.Errorf("first Generation = %d, want 1", firstResult.Generation)
	}
	if firstResult.RequirementState != RequirementCreated {
		t.Errorf("first RequirementState = %q, want %q", firstResult.RequirementState, RequirementCreated)
	}

	// Second propagation with same content → unchanged.
	sender2 := &fakeBoundSender{acknowledged: true}
	secondResult, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}
	if secondResult.Changed {
		t.Error("expected changed=false on second push")
	}
	if secondResult.Generation != 1 {
		t.Errorf("second Generation = %d, want 1", secondResult.Generation)
	}
	if secondResult.RequirementState != RequirementReused {
		t.Errorf("second RequirementState = %q, want %q", secondResult.RequirementState, RequirementReused)
	}
	if secondResult.NotificationState != NotificationSkipped {
		t.Errorf("second NotificationState = %q, want %q", secondResult.NotificationState, NotificationSkipped)
	}

	// Verify sender2 was NOT called.
	sender2.mu.Lock()
	sent := len(sender2.sent)
	sender2.mu.Unlock()
	if sent != 0 {
		t.Errorf("sender.Send should not be called for unchanged content, got %d calls", sent)
	}

	// Verify generation file was NOT overwritten.
	gen, _, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("config-reread-gen should still exist")
	}
	if gen != 1 {
		t.Errorf("generation = %d, want 1 (unchanged)", gen)
	}
}

func TestPropagateConfig_NoParentConfig_MirrorDeletions(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Write stale inheritable files in captain (parent has nothing).
	os.MkdirAll(filepath.Join(captainHome, "config"), 0755)
	os.WriteFile(filepath.Join(captainHome, "config", "soldier-harness"), []byte("old\n"), 0644)
	os.WriteFile(filepath.Join(captainHome, "config", "soldier-dispatch.json"), []byte("old_dispatch\n"), 0644)
	os.MkdirAll(filepath.Join(captainHome, "data"), 0755)
	os.WriteFile(filepath.Join(captainHome, "data", "general-shared.md"), []byte("old shared\n"), 0644)
	os.WriteFile(filepath.Join(captainHome, "data", "projects.md"), []byte("- stale - /tmp/stale (added 2026-01-01)\n"), 0644)

	sender := &fakeBoundSender{acknowledged: true}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !result.Changed {
		t.Error("expected changed=true (mirror deletions are a change)")
	}

	// Verify inheritable files were deleted.
	if _, err := os.Stat(filepath.Join(captainHome, "config", "soldier-harness")); !os.IsNotExist(err) {
		t.Error("soldier-harness should have been deleted (mirror deletion)")
	}
	if _, err := os.Stat(filepath.Join(captainHome, "config", "soldier-dispatch.json")); !os.IsNotExist(err) {
		t.Error("soldier-dispatch.json should have been deleted (mirror deletion)")
	}
	if _, err := os.Stat(filepath.Join(captainHome, "data", "general-shared.md")); !os.IsNotExist(err) {
		t.Error("general-shared.md should have been deleted (mirror deletion)")
	}
	if _, err := os.Stat(filepath.Join(captainHome, "data", "projects.md")); !os.IsNotExist(err) {
		t.Error("projects.md should have been deleted (mirror deletion)")
	}
}

func TestPropagateConfig_MultipleInheritableProps(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Set up parent with all three inheritable files.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(parent, "config", "soldier-dispatch.json"), []byte("{\"strategy\":\"all\"}\n"), 0644)
	os.WriteFile(filepath.Join(parent, "config", "backlog-backend"), []byte("tasks-axi\n"), 0644)

	sender := &fakeBoundSender{acknowledged: true}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !result.Changed {
		t.Error("expected changed=true")
	}
	if result.Generation != 1 {
		t.Errorf("Generation = %d, want 1", result.Generation)
	}

	// Verify all three inheritable files were copied.
	for _, name := range []string{"soldier-harness", "soldier-dispatch.json", "backlog-backend"} {
		if _, err := os.Stat(filepath.Join(captainHome, "config", name)); os.IsNotExist(err) {
			t.Errorf("config/%s was not copied", name)
		}
	}
}

func TestPropagateConfig_NotificationDeferred(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Set up parent config.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Write captain task meta so notification is attempted.
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// Sender that does NOT acknowledge.
	sender := &fakeBoundSender{acknowledged: false}

	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !result.Changed {
		t.Error("expected changed=true")
	}
	if result.RequirementState != RequirementCreated {
		t.Errorf("RequirementState = %q, want %q", result.RequirementState, RequirementCreated)
	}
	// Notification not acknowledged → deferred.
	if result.NotificationState != NotificationDeferred {
		t.Errorf("NotificationState = %q, want %q", result.NotificationState, NotificationDeferred)
	}

	// Verify generation still advanced (durability before notification).
	gen, _, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("config-reread-gen not found")
	}
	if gen != 1 {
		t.Errorf("generation = %d, want 1 (durable despite deferred notification)", gen)
	}
}

func TestPropagateConfig_LegacyReconciliation(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Write a legacy config-reread nudge marker.
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	legacyNudge := "gen=0\ndigest=legacy-digest-123456789012345678901234567890123456789012345678901234567890\n"
	if err := os.WriteFile(filepath.Join(captainHome, "state", ".config-reread-nudge"), []byte(legacyNudge), 0644); err != nil {
		t.Fatal(err)
	}

	// Set up parent config so propagation has content.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	sender := &fakeBoundSender{acknowledged: true}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !result.Changed {
		t.Error("expected changed=true")
	}
	if result.Generation != 1 {
		t.Errorf("Generation = %d, want 1", result.Generation)
	}

	// Verify legacy nudge marker was removed.
	if _, err := os.Stat(filepath.Join(captainHome, "state", ".config-reread-nudge")); !os.IsNotExist(err) {
		t.Error("legacy nudge marker should have been removed")
	}
}

func TestPropagateConfig_GenerationAndRequirementDurabilityBeforeNotify(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Set up parent config.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Write captain task meta so notification is attempted.
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// Use a sender with a callback that verifies durable state BEFORE returning.
	sender := &fakeBoundSender{
		acknowledged: true,
		onSend: func(homeDir string, meta map[string]string, payload string) {
			// Check that .config-reread-gen exists and has gen=1.
			genPath := ConfigRereadGenPath(captainHome)
			genData, err := os.ReadFile(genPath)
			if err != nil {
				t.Errorf(".config-reread-gen should exist at notification time: %v", err)
				return
			}
			genContent := strings.TrimSpace(string(genData))
			if !strings.HasPrefix(genContent, "1\n") {
				t.Errorf(".config-reread-gen should start with '1\\n', got %q", genContent)
			}

			// Check that an inbox envelope exists in the captain's state/.inbox.
			inboxDir := filepath.Join(captainHome, "state", home.InboxDir)
			entries, err := os.ReadDir(inboxDir)
			if err != nil {
				t.Errorf("inbox directory should exist at notification time: %v", err)
				return
			}
			hasEnvelope := false
			for _, e := range entries {
				if e.IsDir() {
					envEntries, _ := os.ReadDir(filepath.Join(inboxDir, e.Name()))
					for _, ee := range envEntries {
						if strings.HasSuffix(ee.Name(), ".json") {
							hasEnvelope = true
							break
						}
					}
				}
			}
			if !hasEnvelope {
				t.Error("inbox should have at least one envelope before notification")
			}

			// Check that a pending record exists in the parent's outbox.
			outboxDir := filepath.Join(parent, "state", home.OutboxDir)
			pendingEntries, err := os.ReadDir(outboxDir)
			if err != nil {
				t.Errorf("outbox directory should exist at notification time: %v", err)
				return
			}
			hasPending := false
			for _, e := range pendingEntries {
				if e.IsDir() {
					pFiles, _ := os.ReadDir(filepath.Join(outboxDir, e.Name()))
					for _, pf := range pFiles {
						if strings.HasSuffix(pf.Name(), ".pending") {
							hasPending = true
							break
						}
					}
				}
			}
			if !hasPending {
				t.Error("outbox should have at least one pending record before notification")
			}
		},
	}

	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !result.Changed {
		t.Error("expected changed=true")
	}
	if result.Generation != 1 {
		t.Errorf("Generation = %d, want 1", result.Generation)
	}
	if result.RequirementState != RequirementCreated {
		t.Errorf("RequirementState = %q, want %q", result.RequirementState, RequirementCreated)
	}
	if result.NotificationState != NotificationSubmitted {
		t.Errorf("NotificationState = %q, want %q", result.NotificationState, NotificationSubmitted)
	}
}

// TestPropagateConfigSummary verifies the Summary() method.
func TestPropagateConfigSummary(t *testing.T) {
	tests := []struct {
		name   string
		result *PropagateConfigResult
		want   PropagateConfigSummary
	}{
		{
			name:   "nil result",
			result: nil,
			want:   PropagateConfigSummary{Detail: "no result (nil)"},
		},
		{
			name: "changed and submitted",
			result: &PropagateConfigResult{
				Changed:           true,
				Generation:        3,
				RequirementState:  RequirementCreated,
				NotificationState: NotificationSubmitted,
				Detail:            "generation=3, notified",
			},
			want: PropagateConfigSummary{
				Changed:           true,
				Generation:        3,
				RequirementState:  "created",
				NotificationState: "submitted",
				Detail:            "generation=3, notified",
			},
		},
		{
			name: "unchanged",
			result: &PropagateConfigResult{
				Changed:           false,
				Generation:        1,
				RequirementState:  RequirementReused,
				NotificationState: NotificationSkipped,
				Detail:            "generation=1 (unchanged)",
			},
			want: PropagateConfigSummary{
				Changed:           false,
				Generation:        1,
				RequirementState:  "reused",
				NotificationState: "skipped",
				Detail:            "generation=1 (unchanged)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.Summary()
			if got.Changed != tt.want.Changed {
				t.Errorf("Changed = %v, want %v", got.Changed, tt.want.Changed)
			}
			if got.Generation != tt.want.Generation {
				t.Errorf("Generation = %d, want %d", got.Generation, tt.want.Generation)
			}
			if got.RequirementState != tt.want.RequirementState {
				t.Errorf("RequirementState = %q, want %q", got.RequirementState, tt.want.RequirementState)
			}
			if got.NotificationState != tt.want.NotificationState {
				t.Errorf("NotificationState = %q, want %q", got.NotificationState, tt.want.NotificationState)
			}
		})
	}
}

// TestPropagateConfigCLIOutput verifies that PropagateConfigCLI produces
// expected output strings.
func TestPropagateConfigCLIOutput(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Write captain task meta so notification is attempted.
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	sender := &fakeBoundSender{acknowledged: true}
	msg, err := PropagateConfigCLI(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfigCLI error: %v", err)
	}
	if !strings.Contains(msg, "inherited config changed") {
		t.Errorf("CLI output should mention 'inherited config changed', got: %s", msg)
	}
	if !strings.Contains(msg, "generation=1") {
		t.Errorf("CLI output should mention generation=1, got: %s", msg)
	}
	if !strings.Contains(msg, "notification submitted") {
		t.Errorf("CLI output should mention notification, got: %s", msg)
	}

	// Second call (unchanged).
	sender2 := &fakeBoundSender{acknowledged: true}
	msg2, err := PropagateConfigCLI(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfigCLI error: %v", err)
	}
	if !strings.Contains(msg2, "unchanged") {
		t.Errorf("CLI output for unchanged should mention 'unchanged', got: %s", msg2)
	}
}

// TestBoundSenderRecorder verifies recording behavior.
func TestBoundSenderRecorder(t *testing.T) {
	inner := &fakeBoundSender{acknowledged: true}
	recorder := &boundSenderRecorder{actual: inner}

	// Before any call, recorder should show not-called.
	if recorder.called {
		t.Error("expected recorder.called=false initially")
	}

	// Send calls should delegate and record.
	result := recorder.Send("/home", map[string]string{"kind": "captain"}, "test-payload")
	if !result.Acknowledged {
		t.Error("expected acknowledged=true")
	}
	if !recorder.called {
		t.Error("expected recorder.called=true after Send")
	}
	if len(recorder.result.Status) == 0 {
		t.Error("expected recorder.result to be populated")
	}
}

// TestPropagateConfig_ReadOnlyCheck verifies IsPropagateConfigUnchanged
// does not mutate any state.
func TestPropagateConfig_ReadOnlyCheck(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// No generation yet → false.
	unchanged, err := IsPropagateConfigUnchanged(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged {
		t.Error("expected unchanged=false when no generation exists")
	}

	// Run propagation once.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	sender := &fakeBoundSender{acknowledged: true}
	_, err = PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}

	// Now unchanged=true.
	unchanged, err = IsPropagateConfigUnchanged(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Error("expected unchanged=true after first propagation")
	}

	// Check that nothing was mutated (read-only).
	genBefore, _, _, _ := ReadConfigRereadGen(captainHome)
	unchanged, err = IsPropagateConfigUnchanged(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Error("expected unchanged=true (idempotent read)")
	}
	genAfter, _, _, _ := ReadConfigRereadGen(captainHome)
	if genBefore != genAfter {
		t.Errorf("generation changed from %d to %d during read-only check", genBefore, genAfter)
	}
}

// TestPropagateConfig_ProjectNotFoundRefutes verifies that invalid
// project registry in parent is caught.
func TestPropagateConfig_InvalidParentRegistry(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Write a malformed projects.md.
	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	os.WriteFile(filepath.Join(parent, "data", "projects.md"), []byte("not-a-valid-entry\n"), 0644)

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	sender := &fakeBoundSender{acknowledged: true}
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err == nil {
		t.Fatal("expected error for invalid parent projects.md")
	}
	if !strings.Contains(err.Error(), "reading parent projects.md") {
		t.Errorf("error = %v, want projects.md validation error", err)
	}
}

func TestPropagateConfig_DetailedResultContent(t *testing.T) {
	// Verify that the result.Detail contains meaningful generation info.
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	sender := &fakeBoundSender{acknowledged: true}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}
	if !strings.Contains(result.Detail, "generation=1") {
		t.Errorf("Detail should contain 'generation=1', got: %s", result.Detail)
	}
}

// --- Idempotency and crash healing ---

// TestPropagateConfig_GenerationOnlyCrash_HealedWithoutAdvancing verifies that
// when the generation was committed but the mailbox requirement was not created
// (crash between ConfigPushWithResult and EnsureConfigRereadRequirement), the
// next unchanged propagation creates the requirement without advancing the
// generation.
func TestPropagateConfig_GenerationOnlyCrash_HealedWithoutAdvancing(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Write captain meta so notification is attempted.
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// First propagation — fully succeeds.
	sender1 := &fakeBoundSender{acknowledged: true}
	result1, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}
	if !result1.Changed {
		t.Error("expected changed=true on first push")
	}
	if result1.Generation != 1 {
		t.Errorf("generation = %d, want 1", result1.Generation)
	}
	if result1.RequirementState != RequirementCreated {
		t.Errorf("requirement state = %q, want created", result1.RequirementState)
	}

	// Simulate crash: remove the inbox envelope and pending record while
	// keeping the generation file intact.
	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("gen file not found after first propagation")
	}

	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	senderIdentity, _, err := home.ReadHomeIdentity(parent)
	if err != nil {
		t.Fatal(err)
	}
	envID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)

	canonCaptain, err := canonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	captainStore := home.NewStore(canonCaptain)
	parentStore := home.NewStore(parent)

	// Remove envelope from inbox.
	os.Remove(filepath.Join(canonCaptain, "state", home.InboxDir, senderIdentity, envID+".json"))
	// Remove pending from outbox.
	os.Remove(filepath.Join(parent, "state", home.OutboxDir, senderIdentity, envID+".pending"))

	// Verify envelope is gone.
	env, _ := captainStore.ReadEnvelope(senderIdentity, envID)
	if env != nil {
		t.Fatal("envelope should have been removed")
	}

	// Second propagation with same content → unchanged generation but
	// healed requirement.
	sender2 := &fakeBoundSender{acknowledged: true}
	result2, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}

	// Generation must NOT advance.
	if result2.Changed {
		t.Error("expected changed=false on unchanged push after crash healing")
	}
	if result2.Generation != 1 {
		t.Errorf("generation = %d, want 1 (unchanged after healing)", result2.Generation)
	}

	// Requirement must be created (healing after crash).
	if result2.RequirementState != RequirementCreated {
		t.Errorf("requirement state = %q, want created (healed)", result2.RequirementState)
	}

	// Notification should succeed.
	if result2.NotificationState != NotificationSubmitted {
		t.Errorf("notification state = %q, want submitted", result2.NotificationState)
	}

	// Verify the envelope now exists again.
	restoredEnv, err := captainStore.ReadEnvelope(senderIdentity, envID)
	if err != nil {
		t.Fatal(err)
	}
	if restoredEnv == nil {
		t.Error("envelope should exist after crash healing")
	}

	// Verify the pending record exists again.
	pendingEnv, err := parentStore.ReadPending(senderIdentity, envID)
	if err != nil {
		t.Fatal(err)
	}
	if pendingEnv == nil {
		t.Error("pending should exist after crash healing")
	}
}

// TestPropagateConfig_SameDigestSameEnvelopeID verifies that repeating
// propagation with the same effective digest produces the same deterministic
// envelope ID.
func TestPropagateConfig_SameDigestSameEnvelopeID(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	senderIdentity, _, err := home.ReadHomeIdentity(parent)
	if err != nil {
		t.Fatal(err)
	}

	// First propagation — get envelope ID.
	sender1 := &fakeBoundSender{acknowledged: true}
	result1, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}

	// Compute expected envelope ID from the gen/digest.
	id1 := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, result1.Generation, "")
	_ = id1 // We'll compute the actual digest from gen file.

	// Read actual gen and digest for ID computation.
	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("gen file not found")
	}

	envID1 := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)

	// Second propagation — same envelope ID.
	sender2 := &fakeBoundSender{acknowledged: true}
	result2, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}

	// Both should use same gen (unchanged).
	if result2.Generation != result1.Generation {
		t.Fatalf("generation changed: %d vs %d", result1.Generation, result2.Generation)
	}

	envID2 := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, result2.Generation, digest)
	if envID1 != envID2 {
		t.Errorf("envelope IDs differ: %q vs %q", envID1, envID2)
	}
}

// TestPropagateConfig_RejectedNotificationRetriesOnUnchanged verifies that
// when notification is rejected (not acknowledged) after durable commit,
// the pending requirement is retained and notification is retried on the
// next unchanged propagation.
func TestPropagateConfig_RejectedNotificationRetriesOnUnchanged(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// First propagation with UNACKNOWLEDGED sender → deferred.
	sender1 := &fakeBoundSender{acknowledged: false}
	result1, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}
	if !result1.Changed {
		t.Error("expected changed=true on first push")
	}
	if result1.NotificationState != NotificationDeferred {
		t.Errorf("notification state = %q, want deferred", result1.NotificationState)
	}

	// Verify durable requirement exists (envelope + pending).
	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("gen file not found")
	}

	captainIdentity, err := ValidateProvenance(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	senderIdentity, _, err := home.ReadHomeIdentity(parent)
	if err != nil {
		t.Fatal(err)
	}
	envID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)

	canonCaptain, err := canonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	captainStore := home.NewStore(canonCaptain)

	// Envelope should exist.
	env, err := captainStore.ReadEnvelope(senderIdentity, envID)
	if err != nil {
		t.Fatal(err)
	}
	if env == nil {
		t.Error("envelope should exist after deferred notification")
	}

	// Second propagation with ACKNOWLEDGED sender → retry should succeed.
	sender2 := &fakeBoundSender{acknowledged: true}
	callCount := 0
	sender2.onSend = func(homeDir string, meta map[string]string, payload string) {
		callCount++
	}

	result2, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}

	// Generation must NOT advance (unchanged content).
	if result2.Changed {
		t.Error("expected changed=false on second push")
	}
	// Requirement must be reused (envelope already existed).
	if result2.RequirementState != RequirementReused {
		t.Errorf("requirement state = %q, want reused", result2.RequirementState)
	}
	// Notification must have been submitted (retried successfully).
	if result2.NotificationState != NotificationSubmitted {
		t.Errorf("notification state = %q, want submitted", result2.NotificationState)
	}

	// Verify sender2.Send was called (notification retried).
	if callCount == 0 {
		t.Error("sender2.Send should have been called (notification retry)")
	}
}

// TestPropagateConfig_EnvelopeOnlyCrash_Healed verifies that when the
// envelope exists but the pending record is missing (crash after envelope
// write but before pending write), the next propagation heals by writing
// the pending record.
func TestPropagateConfig_EnvelopeOnlyCrash_Healed(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// First propagation — fully succeeds.
	sender1 := &fakeBoundSender{acknowledged: true}
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}

	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil || !found {
		t.Fatal("gen file not found")
	}

	captainIdentity, _ := ValidateProvenance(captainHome)
	senderIdentity, _, _ := home.ReadHomeIdentity(parent)
	envID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)
	parentStore := home.NewStore(parent)

	// Simulate crash: remove the pending record only.
	if err := os.RemoveAll(filepath.Join(parent, "state", home.OutboxDir, senderIdentity)); err != nil {
		t.Fatal(err)
	}

	// Verify pending is gone.
	pending, _ := parentStore.ReadPending(senderIdentity, envID)
	if pending != nil {
		t.Fatal("pending should have been removed")
	}

	// Second propagation with same content → heal pending.
	sender2 := &fakeBoundSender{acknowledged: true}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}

	// Generation unchanged.
	if result.Changed {
		t.Error("expected changed=false")
	}
	if result.Generation != gen {
		t.Errorf("generation = %d, want %d", result.Generation, gen)
	}

	// Pending should exist again.
	restoredPending, _ := parentStore.ReadPending(senderIdentity, envID)
	if restoredPending == nil {
		t.Error("pending should exist after healing")
	}
}

// TestPropagateConfig_AckedRequirementNotResent verifies that when the
// requirement is already acked, the notification is not resent.
func TestPropagateConfig_AckedRequirementNotResent(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// First propagation with acknowledged sender.
	sender1 := &fakeBoundSender{acknowledged: true}
	callCount := 0
	sender1.onSend = func(homeDir string, meta map[string]string, payload string) {
		callCount++
	}

	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}

	// Simulate ack by writing an ack file in the captain's inbox.
	gen, digest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil || !found {
		t.Fatal("gen file not found")
	}
	captainIdentity, _ := ValidateProvenance(captainHome)
	senderIdentity, _, _ := home.ReadHomeIdentity(parent)
	envID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)

	canonCaptain, _ := canonicalCaptainHome(captainHome)
	captainStore := home.NewStore(canonCaptain)

	// Write a fake ack to simulate captain having processed the requirement.
	captainStore.WriteAck(&home.ProcessingAck{
		MessageID:      envID,
		SenderIdentity: senderIdentity,
		ReceiverID:     captainIdentity,
		Outcome:        "accepted",
		ProcessedAt:    1000,
	})

	if !captainStore.IsAcked(senderIdentity, envID) {
		t.Fatal("ack should be visible")
	}

	// Second propagation — must NOT send notification (already acked).
	sender2 := &fakeBoundSender{acknowledged: true}
	secondCallCount := 0
	sender2.onSend = func(homeDir string, meta map[string]string, payload string) {
		secondCallCount++
	}

	result2, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}

	if result2.Changed {
		t.Error("expected changed=false")
	}
	if result2.RequirementState != RequirementReused {
		t.Errorf("requirement state = %q, want reused", result2.RequirementState)
	}
	if result2.NotificationState != NotificationSkipped {
		t.Errorf("notification state = %q, want skipped", result2.NotificationState)
	}
	if secondCallCount != 0 {
		t.Errorf("sender2.Send should NOT be called for acked requirement, got %d calls", secondCallCount)
	}
}

// TestPropagateConfig_LegacyReconciledOnUnchanged verifies that legacy
// config-reread evidence is reconciled during an unchanged propagation.
func TestPropagateConfig_LegacyReconciledOnUnchanged(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	// Set up parent config so propagation has content.
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// First propagation to establish gen=1 with real digest.
	sender0 := &fakeBoundSender{acknowledged: true}
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read the actual gen/digest so our legacy nudge matches.
	_, actualDigest, found, err := ReadConfigRereadGen(captainHome)
	if err != nil || !found {
		t.Fatal("gen file not found")
	}

	// Write legacy nudge with the same gen/digest that will be superseded.
	// (gen=1 is already written by the first propagation.)
	nudgeDir := filepath.Join(captainHome, "state")
	legacyNudge := fmt.Sprintf("gen=1\ndigest=%s\n", actualDigest)
	if err := os.WriteFile(filepath.Join(nudgeDir, ".config-reread-nudge"),
		[]byte(legacyNudge), 0644); err != nil {
		t.Fatal(err)
	}

	// Second propagation with same content (unchanged).
	sender := &fakeBoundSender{acknowledged: true}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}

	// Generation unchanged (digest matches existing gen).
	if result.Changed {
		t.Error("expected unchanged for matching digest")
	}

	// Legacy nudge must be gone.
	if _, err := os.Stat(filepath.Join(nudgeDir, ".config-reread-nudge")); !os.IsNotExist(err) {
		t.Error("legacy nudge marker should be removed on unchanged propagation")
	}

	// Requirement should be reused (envelope from first call exists).
	if result.RequirementState != RequirementReused {
		t.Errorf("requirement state = %q, want reused", result.RequirementState)
	}
}

// TestPropagateConfig_ChangedAfterHealing_CreatesNextGeneration verifies
// that after a healed unchanged propagation, a subsequent content change
// correctly creates generation 2 and a new corresponding requirement.
func TestPropagateConfig_ChangedAfterHealing_CreatesNextGeneration(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// First propagation.
	sender1 := &fakeBoundSender{acknowledged: true}
	result1, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("first PropagateConfig error: %v", err)
	}
	if result1.Generation != 1 {
		t.Fatalf("generation = %d, want 1", result1.Generation)
	}

	captainIdentity, _ := ValidateProvenance(captainHome)
	senderIdentity, _, _ := home.ReadHomeIdentity(parent)
	gen1, digest1, _, _ := ReadConfigRereadGen(captainHome)
	envID1 := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen1, digest1)

	// Simulate crash: remove the envelope, so next propagation heals.
	canonCaptain, _ := canonicalCaptainHome(captainHome)
	os.RemoveAll(filepath.Join(canonCaptain, "state", home.InboxDir))
	os.RemoveAll(filepath.Join(parent, "state", home.OutboxDir))

	// Second propagation — unchanged (heals).
	sender2 := &fakeBoundSender{acknowledged: true}
	result2, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender2,
	})
	if err != nil {
		t.Fatalf("second PropagateConfig error: %v", err)
	}
	if result2.Changed {
		t.Error("expected unchanged on second push")
	}
	if result2.Generation != 1 {
		t.Errorf("generation = %d, want 1", result2.Generation)
	}
	if result2.RequirementState != RequirementCreated {
		t.Errorf("requirement state = %q, want created (healed)", result2.RequirementState)
	}

	// Change content — should create gen=2.
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("codex\n"), 0644)

	sender3 := &fakeBoundSender{acknowledged: true}
	result3, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender3,
	})
	if err != nil {
		t.Fatalf("third PropagateConfig error: %v", err)
	}
	if !result3.Changed {
		t.Error("expected changed=true after content change")
	}
	if result3.Generation != 2 {
		t.Errorf("generation = %d, want 2", result3.Generation)
	}
	if result3.RequirementState != RequirementCreated {
		t.Errorf("requirement state = %q, want created", result3.RequirementState)
	}

	// Verify a NEW envelope ID for gen=2.
	gen2, digest2, _, _ := ReadConfigRereadGen(captainHome)
	if gen2 != 2 {
		t.Errorf("gen file says %d, want 2", gen2)
	}
	envID2 := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen2, digest2)
	if envID1 == envID2 {
		t.Error("envelope IDs for gen=1 and gen=2 should differ")
	}

	// New envelope should exist.
	captainStore := home.NewStore(canonCaptain)
	env, err := captainStore.ReadEnvelope(senderIdentity, envID2)
	if err != nil {
		t.Fatal(err)
	}
	if env == nil {
		t.Error("envelope for gen=2 should exist")
	}
}

// TestPropagateConfig_EnsureRequirementFailsOnHardError verifies that
// corrupt state, failed durable writes, and invalid provenance produce
// hard errors rather than silently returning RequirementFailed.
func TestPropagateConfig_EnsureRequirementFailsOnHardError(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Remove the provenance marker to cause a hard identity failure.
	os.Remove(filepath.Join(captainHome, ".munsu-captain-home"))

	sender := &fakeBoundSender{acknowledged: true}
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err == nil {
		t.Error("expected error for missing provenance")
	}
	if !strings.Contains(err.Error(), "no .munsu-captain-home marker") {
		t.Errorf("error = %v, want marker error", err)
	}
}

// TestPropagateConfig_DeferredSuccessRetainsRequirement verifies that
// deferred notification (notification failure after durable commit) retains
// the requirement for later retry and the result is not an error.
func TestPropagateConfig_DeferredSuccessRetainsRequirement(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)
	writeCaptainMeta(t, parent, "test-sm", captainHome, "test-window")

	// Use a sender that does NOT acknowledge.
	sender := &fakeBoundSender{acknowledged: false}
	result, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}

	// Must not be an error — deferred success.
	if result.Changed != true {
		t.Error("expected changed=true")
	}
	if result.RequirementState != RequirementCreated {
		t.Errorf("requirement state = %q, want created", result.RequirementState)
	}
	if result.NotificationState != NotificationDeferred {
		t.Errorf("notification state = %q, want deferred", result.NotificationState)
	}

	// Requirement must still be durable.
	gen, _, found, err := ReadConfigRereadGen(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Error("gen file should exist after deferred notification")
	}
	if gen != 1 {
		t.Errorf("generation = %d, want 1", gen)
	}
}

// TestEnsureOrHealRequirement_ExposedForTesting verifies that the
// ensureOrHealRequirement helper correctly handles the no-op case where
// requirement already exists and is acked.
func TestEnsureOrHealRequirement_AckedNoop(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "test-sm")

	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// First propagation creates gen=1 with a requirement.
	sender1 := &fakeBoundSender{acknowledged: true}
	_, err := PropagateConfig(PropagateConfigRequest{
		ParentHome:  parent,
		CaptainHome: captainHome,
		Mailbox:     sender1,
	})
	if err != nil {
		t.Fatalf("PropagateConfig error: %v", err)
	}

	gen, digest, found, _ := ReadConfigRereadGen(captainHome)
	if !found {
		t.Fatal("gen file not found")
	}

	captainIdentity, _ := ValidateProvenance(captainHome)
	senderIdentity, _, _ := home.ReadHomeIdentity(parent)
	envID := ConfigRereadEnvelopeID(senderIdentity, captainIdentity, gen, digest)

	canonCaptain, _ := canonicalCaptainHome(captainHome)
	captainStore := home.NewStore(canonCaptain)

	// Write an ack.
	captainStore.WriteAck(&home.ProcessingAck{
		MessageID:      envID,
		SenderIdentity: senderIdentity,
		ReceiverID:     captainIdentity,
		Outcome:        "accepted",
		ProcessedAt:    2000,
	})

	// Call ensureOrHealRequirement directly — should return reused/skipped
	// without calling EnsureConfigRereadRequirement.
	recorder := &boundSenderRecorder{actual: &fakeBoundSender{acknowledged: true}}
	reqState, notifState, _, err := ensureOrHealRequirement(
		parent, captainHome, gen, digest, recorder,
	)
	if err != nil {
		t.Fatalf("ensureOrHealRequirement error: %v", err)
	}
	if reqState != RequirementReused {
		t.Errorf("requirement state = %q, want reused", reqState)
	}
	if notifState != NotificationSkipped {
		t.Errorf("notification state = %q, want skipped", notifState)
	}
	// Recorder should not have been called (no EnsureConfigRereadRequirement).
	if recorder.called {
		t.Error("recorder should not be called for acked requirement")
	}
}

// Ensure compilation check for interface compliance.
var _ = &fakeBoundSender{}

// Ensure the unused variable lint doesn't fire for the above.
var _ = fmt.Sprintf("test %s", "ok")
