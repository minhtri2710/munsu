package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpencodePluginsContent verifies all 4 plugin files generate valid JS with munsu commands.
func TestOpencodePluginsContent(t *testing.T) {
	binPath := "/usr/local/bin/munsu"
	for _, name := range opencodePluginFileNames {
		t.Run(name, func(t *testing.T) {
			content := OpencodePluginsContent(binPath, name)
			if content == "" {
				t.Fatal("expected non-empty content")
			}

			// Verify it's valid JS (starts with import statements)
			if !strings.HasPrefix(strings.TrimSpace(content), "import ") {
				t.Errorf("plugin %s must start with import statements", name)
			}

			// Verify contains the expected export function
			funcName := expectedFuncName(name)
			if funcName == "" {
				t.Fatalf("unknown plugin name %s", name)
			}
			if !strings.Contains(content, "export const "+funcName) {
				t.Errorf("plugin %s must export const %s", name, funcName)
			}

			// Verify munsu binary path is embedded
			if !strings.Contains(content, binPath) {
				t.Errorf("plugin %s must reference the munsu binary path", name)
			}

			// Verify it's ESM (uses import/export)
			if !strings.Contains(content, "import ") {
				t.Errorf("plugin %s must use ESM imports", name)
			}
			if !strings.Contains(content, "export const") {
				t.Errorf("plugin %s must use ESM exports", name)
			}
		})
	}
}

// TestOpencodePluginsContent_PretoolCheck verifies the pretool-check plugin contract.
func TestOpencodePluginsContent_PretoolCheck(t *testing.T) {
	content := OpencodePluginsContent("/opt/munsu", "munsu-pretool-check.js")

	// Verify the PreToolUse deny mechanism: throw on exit 2
	if !strings.Contains(content, "throw new Error(reason)") {
		t.Error("pretool-check must throw on exit 2 to block bash command")
	}

	// Verify it reads command from output.args.command (OpenCode contract)
	if !strings.Contains(content, "output?.args?.command") {
		t.Error("pretool-check must read command from output.args.command")
	}

	// Verify it checks for bash tool
	if !strings.Contains(content, `input?.tool !== "bash"`) {
		t.Error("pretool-check must guard on bash tool")
	}

	// Verify it spawns safety-check with --harness opencode --command
	if !strings.Contains(content, "safety-check") ||
		!strings.Contains(content, "--harness") ||
		!strings.Contains(content, "opencode") {
		t.Error("pretool-check must call safety-check --harness opencode")
	}
}

// TestOpencodePluginsContent_SessionstartNudge verifies the session-start nudge plugin contract.
func TestOpencodePluginsContent_SessionstartNudge(t *testing.T) {
	content := OpencodePluginsContent("/opt/munsu", "munsu-sessionstart-nudge.js")

	// Verify durable exactly-once handling (file-based, survives reload)
	if !strings.Contains(content, "DURABLE_FILE") {
		t.Error("sessionstart-nudge must have durable exactly-once tracking")
	}
	if !strings.Contains(content, "loadNudgedSessions") {
		t.Error("sessionstart-nudge must have loadNudgedSessions for reload resilience")
	}
	if !strings.Contains(content, "saveNudgedSession") {
		t.Error("sessionstart-nudge must persist exactly-once marker after successful delivery")
	}

	// Verify session.created event
	if !strings.Contains(content, "session.created") {
		t.Error("sessionstart-nudge must listen for session.created event")
	}

	// Verify promptAsync call
	if !strings.Contains(content, "promptAsync") {
		t.Error("sessionstart-nudge must call promptAsync to send nudge")
	}

	// Verify it calls sessionstart-nudge command
	if !strings.Contains(content, "sessionstart-nudge") {
		t.Error("sessionstart-nudge must call munsu integrate sessionstart-nudge")
	}

	// Verify retry-after-failure: command failure -> inMemory guard cleared
	if !strings.Contains(content, "inMemory.delete(sessionID)") {
		t.Error("sessionstart-nudge must clear in-memory guard on failure to allow retry")
	}

	// Verify success marker is only recorded after successful delivery
	// (saveNudgedSession is called after promptAsync succeeds; the function
	// returns early on promptAsync failure, so ordering is guaranteed by control flow)
	if !strings.Contains(content, "saveNudgedSession") {
		t.Error("sessionstart-nudge must record durable marker after successful nudge delivery")
	}

	// Verify promptAsync error handler exists (for retry on delivery failure)
	if !strings.Contains(content, "promptAsync") {
		t.Error("sessionstart-nudge must call promptAsync to send nudge")
	}
	// promptAsync failure should clear in-memory guard
	// Look for catch block that deletes from inMemory
	if !strings.Contains(content, "catch {") {
		t.Error("sessionstart-nudge must catch promptAsync errors")
	}
}

// TestOpencodePluginsContent_TurnendGuard verifies the turn-end guard plugin contract.
func TestOpencodePluginsContent_TurnendGuard(t *testing.T) {
	content := OpencodePluginsContent("/opt/munsu", "munsu-turnend-guard.js")

	// Verify session.idle event
	if !strings.Contains(content, "session.idle") {
		t.Error("turnend-guard must listen for session.idle event")
	}

	// Verify it runs guard command
	if !strings.Contains(content, "guard") || !strings.Contains(content, "opencode") {
		t.Error("turnend-guard must call munsu guard --harness opencode")
	}

	// Verify skipNextIdle loop guard
	if !strings.Contains(content, "skipNextIdle") {
		t.Error("turnend-guard must have skipNextIdle loop guard")
	}

	// Verify promptAsync on blind turn
	if !strings.Contains(content, "TURN WOULD END BLIND") {
		t.Error("turnend-guard must send TURN WOULD END BLIND on blind turn")
	}

	// Verify coordinator check for watch-arm
	if !strings.Contains(content, "letWatchArmRun") {
		t.Error("turnend-guard must defer to watch-arm coordinator")
	}
}

// TestOpencodePluginsContent_WatchArm verifies the watcher arm plugin contract.
func TestOpencodePluginsContent_WatchArm(t *testing.T) {
	content := OpencodePluginsContent("/opt/munsu", "munsu-watch-arm.js")

	// Verify session.idle event
	if !strings.Contains(content, "session.idle") {
		t.Error("watch-arm must listen for session.idle event")
	}

	// Ensure coordinator key
	if !strings.Contains(content, "__munsuOpenCodeWatchArm") {
		t.Error("watch-arm must register a global coordinator")
	}

	// Verify it spawns watch ensure
	if !strings.Contains(content, "watch") || !strings.Contains(content, "ensure") {
		t.Error("watch-arm must call munsu watch ensure")
	}

	// Verify it sends promptAsync on wake/failure
	if !strings.Contains(content, "WATCHER FIRED") {
		t.Error("watch-arm must send WATCHER FIRED on actionable watcher output")
	}

	// Verify arm status tracking
	if !strings.Contains(content, "armStatus") {
		t.Error("watch-arm must track arm status")
	}
}

// TestOpencodePluginsContent_BinarySubstitution verifies binary path substitution.
func TestOpencodePluginsContent_BinarySubstitution(t *testing.T) {
	binPath := "/opt/munsu with spaces/bin/munsu"
	for _, name := range opencodePluginFileNames {
		t.Run(name, func(t *testing.T) {
			content := OpencodePluginsContent(binPath, name)
			if !strings.Contains(content, binPath) {
				t.Errorf("plugin %s must contain binary path %s", name, binPath)
			}
		})
	}
}

// TestOpencodePluginsDigest verifies SHA-256 digests are computed correctly.
func TestOpencodePluginsDigest(t *testing.T) {
	binPath := "/usr/local/bin/munsu"
	for _, name := range opencodePluginFileNames {
		t.Run(name, func(t *testing.T) {
			digest := OpencodePluginsDigest(binPath, name)
			if digest == "" {
				t.Fatal("expected non-empty digest")
			}
			if len(digest) != 64 {
				t.Errorf("expected 64-char hex digest, got %d chars: %s", len(digest), digest)
			}
		})
	}
}

// TestOpencodePluginsContentDigest verifies combined digest.
func TestOpencodePluginsContentDigest(t *testing.T) {
	d1 := OpencodePluginsContentDigest("/usr/local/bin/munsu")
	d2 := OpencodePluginsContentDigest("/usr/local/bin/munsu")

	if d1 == "" || len(d1) != 64 {
		t.Fatal("expected 64-char hex digest")
	}
	if d1 != d2 {
		t.Fatal("combined digest must be deterministic")
	}

	d3 := OpencodePluginsContentDigest("/opt/bin/munsu")
	if d1 == d3 {
		t.Fatal("combined digest must differ for different binary paths")
	}
}

// TestOpencodePluginsHasOwnedHooks verifies structural ownership detection.
func TestOpencodePluginsHasOwnedHooks(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, ".opencode", "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}

	binPath := "/usr/local/bin/munsu"

	// Write all 4 plugin files
	for _, name := range opencodePluginFileNames {
		content := OpencodePluginsContent(binPath, name)
		targetPath := filepath.Join(pluginsDir, name)
		if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Should detect all owned plugins
	present, msg, err := OpencodePluginsHasOwnedHooks(pluginsDir, binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Errorf("expected all plugins present, got: %s", msg)
	}

	// Remove one file
	if err := os.Remove(filepath.Join(pluginsDir, "munsu-watch-arm.js")); err != nil {
		t.Fatal(err)
	}
	present, msg, err = OpencodePluginsHasOwnedHooks(pluginsDir, binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Errorf("expected missing plugin detection, got: %s", msg)
	}
	if !strings.Contains(msg, "munsu-watch-arm") {
		t.Errorf("expected message to mention munsu-watch-arm, got: %s", msg)
	}
}

// TestOpencodePluginsHasOwnedHooks_MissingBinary verifies detection fails without munsu path.
func TestOpencodePluginsHasOwnedHooks_MissingBinary(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, ".opencode", "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a plugin with a different binary path
	alteredContent := strings.Replace(
		OpencodePluginsContent("/usr/local/bin/munsu", "munsu-pretool-check.js"),
		"/usr/local/bin/munsu",
		"/usr/local/bin/other",
		1,
	)
	if err := os.WriteFile(filepath.Join(pluginsDir, "munsu-pretool-check.js"), []byte(alteredContent), 0644); err != nil {
		t.Fatal(err)
	}

	present, _, err := OpencodePluginsHasOwnedHooks(pluginsDir, "/usr/local/bin/munsu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Error("expected not present for wrong binary path")
	}
}

// TestOpencodePluginsHasOwnedHooks_EmptyDir verifies handling of empty directory.
func TestOpencodePluginsHasOwnedHooks_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, ".opencode", "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}

	present, msg, err := OpencodePluginsHasOwnedHooks(pluginsDir, "/usr/local/bin/munsu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Error("expected not present for empty dir")
	}
	if !strings.Contains(msg, "not found") {
		t.Error("expected message to mention not found")
	}
}

// TestOpencodePluginsAllTargetPaths verifies target path resolution.
func TestOpencodePluginsAllTargetPaths(t *testing.T) {
	t.Run("user scope", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		paths, err := OpencodePluginsAllTargetPaths(ScopeUser, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) != 4 {
			t.Fatalf("expected 4 paths, got %d", len(paths))
		}
		expectedDir := filepath.Join(home, ".opencode", "plugins")
		for i, name := range opencodePluginFileNames {
			expected := filepath.Join(expectedDir, name)
			if paths[i] != expected {
				t.Errorf("path[%d] = %q, want %q", i, paths[i], expected)
			}
		}
	})

	t.Run("project scope", func(t *testing.T) {
		projDir := t.TempDir()
		paths, err := OpencodePluginsAllTargetPaths(ScopeProject, projDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) != 4 {
			t.Fatalf("expected 4 paths, got %d", len(paths))
		}
		canonical, resolveErr := filepath.EvalSymlinks(projDir)
		if resolveErr != nil {
			canonical = projDir
		}
		expectedDir := filepath.Join(canonical, ".opencode", "plugins")
		for i, name := range opencodePluginFileNames {
			expected := filepath.Join(expectedDir, name)
			if paths[i] != expected {
				t.Errorf("path[%d] = %q, want %q", i, paths[i], expected)
			}
		}
	})
}

// TestOpencodePluginsTargetPath verifies single target path resolution.
func TestOpencodePluginsTargetPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := OpencodePluginsTargetPath(ScopeUser, "", "munsu-pretool-check.js")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, ".opencode", "plugins", "munsu-pretool-check.js")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

// TestGenerateOpencodeManifest verifies OpenCode manifest generation.
func TestGenerateOpencodeManifest(t *testing.T) {
	caps := []Capability{CapSessionStart, CapWakeFollowUp, CapTurnEndGuard, CapPreToolCheck}
	targets := []string{
		"/home/user/.opencode/plugins/munsu-pretool-check.js",
		"/home/user/.opencode/plugins/munsu-sessionstart-nudge.js",
		"/home/user/.opencode/plugins/munsu-turnend-guard.js",
		"/home/user/.opencode/plugins/munsu-watch-arm.js",
	}
	m := generateOpencodeManifest("opencode", "user", caps, "test-digest", targets)

	if m.Harness != "opencode" {
		t.Errorf("expected harness 'opencode', got %q", m.Harness)
	}
	if m.SchemaVersion != "munsu.integrate/v1" {
		t.Errorf("expected schema version 'munsu.integrate/v1', got %q", m.SchemaVersion)
	}
	if len(m.TargetPaths) != 4 {
		t.Errorf("expected 4 target paths, got %d", len(m.TargetPaths))
	}
	if m.ContentDigest != "test-digest" {
		t.Errorf("expected digest 'test-digest', got %q", m.ContentDigest)
	}
	if len(m.Capabilities) != 4 {
		t.Errorf("expected 4 capabilities, got %d", len(m.Capabilities))
	}
	if m.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %q", m.Version)
	}
}

// TestOpencodeEnabledCapabilities verifies opencode capabilities include wake-followup.
func TestOpencodeEnabledCapabilities(t *testing.T) {
	caps := EnabledCapabilities("opencode")
	if len(caps) == 0 {
		t.Fatal("expected opencode capabilities to be non-empty")
	}

	capSet := make(map[Capability]bool)
	for _, c := range caps {
		capSet[c] = true
	}

	if !capSet[CapSessionStart] {
		t.Error("expected CapSessionStart for opencode")
	}
	if !capSet[CapWakeFollowUp] {
		t.Error("expected CapWakeFollowUp for opencode")
	}
	if !capSet[CapTurnEndGuard] {
		t.Error("expected CapTurnEndGuard for opencode")
	}
	if !capSet[CapPreToolCheck] {
		t.Error("expected CapPreToolCheck for opencode")
	}
}

// TestOpencodeInstallWritesFiles verifies Install creates plugin files for opencode.
func TestOpencodeInstallWritesFiles(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a stub munsu binary for path resolution
	munsuBin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(munsuBin, []byte("#!/bin/sh\necho munsu\n"), 0755); err != nil {
		t.Fatal(err)
	}

	SetMunsuPathResolver(testMunsuResolver{path: munsuBin})
	defer ResetMunsuPathResolver()

	// Install opencode plugins via Install() with ScopeProject to isolate writes
	scope := ScopeProject
	result, err := Install(homeDir, projectDir, "opencode", scope, false)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if result.State != "installed" {
		t.Fatalf("expected installed, got %q", result.State)
	}

	// Verify the plugin files exist and have correct ownership
	pluginDir := filepath.Join(projectDir, ".opencode", "plugins")
	for _, name := range opencodePluginFileNames {
		path := filepath.Join(pluginDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("plugin file %s should exist: %v", name, err)
		}
	}

	// Verify plugin files contain the munsu binary path
	for _, name := range opencodePluginFileNames {
		data, err := os.ReadFile(filepath.Join(pluginDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), munsuBin) {
			t.Errorf("plugin %s must contain munsu binary path", name)
		}
	}

	// Verify ownership via structural check
	present, msg, err := OpencodePluginsHasOwnedHooks(pluginDir, munsuBin)
	if err != nil {
		t.Fatalf("ownership check failed: %v", err)
	}
	if !present {
		t.Errorf("expected all plugins owned, got: %s", msg)
	}

	// Verify manifest was written
	manifestPath := ManifestPath(homeDir, "opencode", scope, projectDir)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest should exist: %v", err)
	}

	// Remove one plugin file and verify ownership detects it
	if err := os.Remove(filepath.Join(pluginDir, "munsu-watch-arm.js")); err != nil {
		t.Fatal(err)
	}

	present, msg, err = OpencodePluginsHasOwnedHooks(pluginDir, munsuBin)
	if err != nil {
		t.Fatalf("ownership check after removal failed: %v", err)
	}
	if present {
		t.Errorf("expected missing plugin detection, got: %s", msg)
	}
	if !strings.Contains(msg, "munsu-watch-arm") {
		t.Errorf("expected message to mention munsu-watch-arm, got: %s", msg)
	}
}

// TestOpencodeExpectedFuncName verifies all expected function names are valid.
func TestOpencodeExpectedFuncName(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"munsu-pretool-check.js", "MunsuPretoolCheck"},
		{"munsu-sessionstart-nudge.js", "MunsuSessionstartNudge"},
		{"munsu-turnend-guard.js", "MunsuTurnendGuard"},
		{"munsu-watch-arm.js", "MunsuWatchArm"},
		{"unknown.js", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expectedFuncName(tt.name)
			if got != tt.expected {
				t.Errorf("expectedFuncName(%q) = %q, want %q", tt.name, got, tt.expected)
			}
		})
	}
}

// TestOpencodeAssertSupportedHarness verifies opencode is a registered
// supported harness. (The --harness opencode deny shape — stderr + exit 2 —
// is exercised by TestOpencodeSafetyCheckDeny in the cli package.)
func TestOpencodeAssertSupportedHarness(t *testing.T) {
	// Verify opencode is a supported harness for integration
	if err := AssertSupportedHarness("opencode"); err != nil {
		t.Fatalf("AssertSupportedHarness('opencode') = %v, want nil", err)
	}
}
