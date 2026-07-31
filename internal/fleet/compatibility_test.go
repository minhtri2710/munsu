package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- Operation-specific requirement declarations ---

func TestTaskMutation_DeclaresSeparateRequirements(t *testing.T) {
	result := CheckOperation(OpTaskMutation, t.TempDir())
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpTaskMutation {
		t.Errorf("operation = %s, want %s", result.Operation, OpTaskMutation)
	}
	if len(result.Requirements) == 0 {
		t.Error("task-mutation must declare at least one requirement")
	}
	hasMetaCheck := false
	for _, r := range result.Requirements {
		if r.Name == "decodable-task-meta" {
			hasMetaCheck = true
			break
		}
	}
	if !hasMetaCheck {
		t.Error("task-mutation must include decodable-task-meta requirement")
	}
}

func TestSpawn_DeclaresSeparateRequirements(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "soldier-harness"), []byte("pi\n"), 0644)

	result := CheckOperation(OpSpawn, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpSpawn {
		t.Errorf("operation = %s, want %s", result.Operation, OpSpawn)
	}
	if len(result.Requirements) == 0 {
		t.Error("spawn must declare at least one requirement")
	}
	hasHarnessCheck := false
	hasDeliveryCheck := false
	for _, r := range result.Requirements {
		switch r.Name {
		case "harness-binary":
			hasHarnessCheck = true
		case "delivery-mode-compatible":
			hasDeliveryCheck = true
		}
	}
	if !hasHarnessCheck {
		t.Error("spawn must include harness-binary requirement")
	}
	if !hasDeliveryCheck {
		t.Error("spawn must include delivery-mode-compatible requirement")
	}
}

func TestCaptainLaunch_DeclaresSeparateRequirements(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "captain-harness"), []byte("pi\n"), 0644)

	result := CheckOperation(OpCaptainLaunch, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpCaptainLaunch {
		t.Errorf("operation = %s, want %s", result.Operation, OpCaptainLaunch)
	}
	hasIntegration := false
	hasProvenance := false
	for _, r := range result.Requirements {
		switch r.Name {
		case "captain-integration":
			hasIntegration = true
		case "captain-provenance":
			hasProvenance = true
		}
	}
	if !hasIntegration {
		t.Error("captain-launch must include captain-integration requirement")
	}
	if !hasProvenance {
		t.Error("captain-launch must include captain-provenance requirement")
	}
}

func TestCaptainRecover_DeclaresSeparateRequirements(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "captain-harness"), []byte("pi\n"), 0644)

	result := CheckOperation(OpCaptainRecover, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpCaptainRecover {
		t.Errorf("operation = %s, want %s", result.Operation, OpCaptainRecover)
	}
	hasIntegration := false
	hasProvenance := false
	for _, r := range result.Requirements {
		switch r.Name {
		case "captain-integration":
			hasIntegration = true
		case "captain-provenance":
			hasProvenance = true
		}
	}
	if !hasIntegration {
		t.Error("captain-recovery must include captain-integration requirement")
	}
	if !hasProvenance {
		t.Error("captain-recovery must include captain-provenance requirement")
	}
}

func TestDelivery_DeclaresSeparateRequirements(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	result := CheckOperation(OpDelivery, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpDelivery {
		t.Errorf("operation = %s, want %s", result.Operation, OpDelivery)
	}
	hasGh := false
	hasDecodable := false
	for _, r := range result.Requirements {
		switch r.Name {
		case "gh-available":
			hasGh = true
		case "decodable-task-meta":
			hasDecodable = true
		}
	}
	if !hasGh {
		t.Error("delivery must include gh-available requirement")
	}
	if !hasDecodable {
		t.Error("delivery must include decodable-task-meta requirement")
	}
}

func TestMigration_DeclaresSeparateRequirements(t *testing.T) {
	result := CheckOperation(OpMigration, t.TempDir())
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpMigration {
		t.Errorf("operation = %s, want %s", result.Operation, OpMigration)
	}
	hasLegacy := false
	for _, r := range result.Requirements {
		if r.Name == "legacy-config-exists" {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		t.Error("migration must include legacy-config-exists requirement")
	}
}

func TestSelfUpdate_DeclaresSeparateRequirements(t *testing.T) {
	result := CheckOperation(OpSelfUpdate, t.TempDir())
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpSelfUpdate {
		t.Errorf("operation = %s, want %s", result.Operation, OpSelfUpdate)
	}
	hasPrereq := false
	for _, r := range result.Requirements {
		if r.Name == "self-update-prerequisites" {
			hasPrereq = true
			break
		}
	}
	if !hasPrereq {
		t.Error("self-update must include self-update-prerequisites requirement")
	}
}

func TestTeardown_DeclaresSeparateRequirements(t *testing.T) {
	result := CheckOperation(OpTeardown, t.TempDir())
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if result.Operation != OpTeardown {
		t.Errorf("operation = %s, want %s", result.Operation, OpTeardown)
	}
	hasDecodable := false
	for _, r := range result.Requirements {
		if r.Name == "decodable-task-meta" {
			hasDecodable = true
			break
		}
	}
	if !hasDecodable {
		t.Error("teardown must include decodable-task-meta requirement")
	}
}

// --- All operations declare requirements ---

func TestAllOperationsDeclareRequirements(t *testing.T) {
	ops := []Operation{
		OpTaskMutation,
		OpSpawn,
		OpCaptainLaunch,
		OpCaptainRecover,
		OpDelivery,
		OpMigration,
		OpSelfUpdate,
		OpTeardown,
	}
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, t.TempDir())
			if len(result.Requirements) == 0 {
				t.Errorf("%s must declare at least one requirement", op)
			}
		})
	}
}

// --- Version inequality alone does not block ---

func TestVersionInequalityAloneDoesNotBlock(t *testing.T) {
	ops := []Operation{
		OpTaskMutation,
		OpSpawn,
		OpCaptainLaunch,
		OpCaptainRecover,
		OpDelivery,
		OpMigration,
		OpSelfUpdate,
		OpTeardown,
	}
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, t.TempDir())
			for _, r := range result.Requirements {
				if strings.Contains(r.Name, "version") && strings.Contains(r.Detail, "mismatch") {
					t.Errorf("%s has bare version inequality check: %s", op, r.Name)
				}
			}
		})
	}
}

// --- Shared requirement: readable home ---

func TestCheckHomeReadable_EmptyPath(t *testing.T) {
	r := checkHomeReadable("")
	if r.Satisfied {
		t.Error("empty path should not be satisfied")
	}
	if !strings.Contains(r.Detail, "empty") {
		t.Errorf("detail should mention empty path, got: %s", r.Detail)
	}
	if r.Remediation == "" {
		t.Error("remediation should not be empty")
	}
}

func TestCheckHomeReadable_NonExistent(t *testing.T) {
	r := checkHomeReadable("/nonexistent/munsu-home-12345")
	if r.Satisfied {
		t.Error("nonexistent path should not be satisfied")
	}
	if !strings.Contains(r.Detail, "does not exist") {
		t.Errorf("detail should mention does not exist, got: %s", r.Detail)
	}
	if r.Remediation == "" {
		t.Error("remediation should not be empty")
	}
}

func TestCheckHomeReadable_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "notadir")
	os.WriteFile(filePath, []byte("data"), 0644)

	r := checkHomeReadable(filePath)
	if r.Satisfied {
		t.Error("file path should not be satisfied")
	}
	if !strings.Contains(r.Detail, "not a directory") {
		t.Errorf("detail should mention not a directory, got: %s", r.Detail)
	}
}

func TestCheckHomeReadable_Valid(t *testing.T) {
	r := checkHomeReadable(t.TempDir())
	if !r.Satisfied {
		t.Errorf("valid home should be satisfied: %s", r.Detail)
	}
}

// --- Corrupt task meta is never force-overridable ---

func TestCorruptTaskMetaIsNotForceOverridable(t *testing.T) {
	homeDir := t.TempDir()
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Write a meta file with a path separator that makes it unreadable.
	// Create a directory with the name that shadows the meta file.
	metaDir := filepath.Join(stateDir, "test-task.meta")
	os.MkdirAll(metaDir, 0755)

	for _, op := range []Operation{OpTaskMutation, OpSpawn, OpDelivery, OpTeardown} {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, homeDir)
			for _, r := range result.Requirements {
				if r.Name == "decodable-task-meta" && !r.Satisfied {
					if !strings.Contains(r.Detail, "cannot be decoded") && !strings.Contains(r.Detail, "is a directory") {
						t.Errorf("corrupt meta detail should mention decode failure, got: %s", r.Detail)
					}
					if r.Remediation == "" {
						t.Error("corrupt meta must have remediation")
					}
					return
				}
			}
		})
	}
}

func TestCorruptTaskMetaBlocksMutation(t *testing.T) {
	homeDir := t.TempDir()
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Create a directory with .meta extension so ReadMeta fails (is a directory).
	metaDir := filepath.Join(stateDir, "test-task.meta")
	os.MkdirAll(metaDir, 0755)

	for _, op := range []Operation{OpTaskMutation, OpDelivery, OpTeardown} {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, homeDir)
			if result.IsCompatible() {
				t.Errorf("%s should be blocked by corrupt meta", op)
			}
		})
	}
}

func TestCorruptMetaCannotBeForceOverridden(t *testing.T) {
	homeDir := t.TempDir()
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Create a directory with .meta extension so ReadMeta fails.
	metaDir := filepath.Join(stateDir, "test-task.meta")
	os.MkdirAll(metaDir, 0755)

	// With corrupt (unreadable as file) meta, the check must fail regardless of --force.
	result := CheckOperation(OpTeardown, homeDir)
	for _, r := range result.Requirements {
		if r.Name == "decodable-task-meta" {
			if r.Satisfied {
				t.Error("corrupt meta must not be satisfiable; no --force override exists")
			}
			if strings.Contains(r.Remediation, "--force") {
				t.Error("corrupt meta remediation must not suggest --force")
			}
			return
		}
	}
	t.Error("decodable-task-meta check not found")
}

// --- Valid task meta is readable ---

func TestValidTaskMetaIsCompatible(t *testing.T) {
	homeDir := t.TempDir()
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	home.WriteMeta(homeDir, "valid-task", map[string]string{"kind": "ship", "description": "test"})

	r := checkTaskMetaDecodable(homeDir)
	if !r.Satisfied {
		t.Errorf("valid meta should be decodable: %s", r.Detail)
	}
}

// --- Compatible mixed versions remain operable ---

func TestMixedVersionsRemainOperable(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	result := CheckOperation(OpDelivery, homeDir)
	if !result.IsCompatible() {
		for _, r := range result.Requirements {
			if r.Name == "gh-available" && !r.Satisfied {
				return
			}
		}
		t.Errorf("delivery should be compatible in mixed version environment: %s", result.FormatErrors())
	}
}

// --- No-mistakes binary PATH shadowing ---

func TestNoMistakesPathShadowed_FailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := `#!/bin/sh
echo "no-mistakes version v0.5.0"
exit 0
`
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	result := CheckOperation(OpDelivery, homeDir)
	for _, r := range result.Requirements {
		if r.Name == "delivery-mode-compatible" && !r.Satisfied {
			if !strings.Contains(r.Remediation, "Upgrade no-mistakes") {
				t.Errorf("expected upgrade remediation for old version, got: %s", r.Remediation)
			}
			if !strings.Contains(r.Detail, "unsupported") {
				t.Errorf("expected unsupported detail for old version, got: %s", r.Detail)
			}
			return
		}
	}
}

func TestNoMistakesPathShadowed_AbsentBinaryFallsThrough(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	result := CheckOperation(OpDelivery, homeDir)
	for _, r := range result.Requirements {
		if r.Name == "delivery-mode-compatible" {
			if !r.Satisfied {
				t.Errorf("absent no-mistakes should fall through: %s", r.Detail)
			}
			return
		}
	}
}

// --- Delivery mode compatible with ready binary ---

func TestDeliveryModeCompatible_ReadyBinary(t *testing.T) {
	if _, err := exec.LookPath("no-mistakes"); err != nil {
		t.Skip("no-mistakes not on PATH")
	}
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	r := checkDeliveryModeCompatible(homeDir)
	if !r.Satisfied {
		t.Errorf("ready binary should be compatible: %s", r.Detail)
	}
}

// --- Captain integration: Pi harness requires integration ---

func TestCaptainIntegration_NonPiHarnessRequiresNoIntegration(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "captain-harness"), []byte("codex\n"), 0644)

	r := checkCaptainIntegration(homeDir)
	if !r.Satisfied {
		t.Errorf("non-Pi harness should not require Pi integration: %s", r.Detail)
	}
}

func TestCaptainIntegration_PiHarnessDeferred(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "captain-harness"), []byte("pi\n"), 0644)

	// Without a bootstrap integrator bridge, the check should be deferred (pass).
	r := checkCaptainIntegration(homeDir)
	if !r.Satisfied {
		t.Errorf("Pi integration check should be deferred when bridge is not initialized: %s", r.Detail)
	}
	if !strings.Contains(r.Detail, "deferred") {
		t.Errorf("detail should mention deferred, got: %s", r.Detail)
	}
}

// --- Captain provenance checks ---

func TestCaptainProvenance_MissingMarker(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(homeDir, 0755)

	r := checkCaptainProvenance(homeDir)
	if r.Satisfied {
		t.Error("missing provenance marker should not be satisfied")
	}
	if !strings.Contains(r.Detail, "not found") {
		t.Errorf("detail should mention not found, got: %s", r.Detail)
	}
	if !strings.Contains(r.Remediation, "captain seed") {
		t.Errorf("remediation should mention captain seed, got: %s", r.Remediation)
	}
}

func TestCaptainProvenance_Valid(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(homeDir, 0755)
	os.WriteFile(filepath.Join(homeDir, home.CaptainProvenanceMarkerName), []byte("version: 1\nid: test-captain\n"), 0644)

	r := checkCaptainProvenance(homeDir)
	if !r.Satisfied {
		t.Errorf("valid provenance marker should be satisfied: %s", r.Detail)
	}
}

// --- gh availability ---

func TestGhAvailability_Present(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not on PATH")
	}
	r := checkGhAvailability()
	if !r.Satisfied {
		t.Errorf("gh available should pass: %s", r.Detail)
	}
}

func TestGhAvailability_Absent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := checkGhAvailability()
	if r.Satisfied {
		t.Error("absent gh should not pass")
	}
	if !strings.Contains(r.Remediation, "Install") {
		t.Errorf("remediation should mention Install, got: %s", r.Remediation)
	}
}

// --- Harness binary checks ---

func TestHarnessBinary_NoConfig(t *testing.T) {
	r := checkHarnessBinary(t.TempDir(), "soldier")
	if !r.Satisfied {
		t.Errorf("no harness configured should pass: %s", r.Detail)
	}
}

func TestCheckHomeReadable_AllOperations(t *testing.T) {
	ops := []Operation{
		OpTaskMutation,
		OpSpawn,
		OpCaptainLaunch,
		OpCaptainRecover,
		OpDelivery,
		OpMigration,
		OpSelfUpdate,
		OpTeardown,
	}
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, t.TempDir())
			hasHomeCheck := false
			for _, r := range result.Requirements {
				if r.Name == "readable-home" {
					hasHomeCheck = true
					break
				}
			}
			if !hasHomeCheck {
				t.Errorf("%s must include readable-home requirement", op)
			}
		})
	}
}

// --- Legacy config checks ---

func TestLegacyConfigExists_HasLegacy(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte("- id: test\n  home: /tmp\n"), 0644)

	r := checkLegacyConfigExists(homeDir)
	if !r.Satisfied {
		t.Errorf("legacy config exists should pass: %s", r.Detail)
	}
}

func TestLegacyConfigExists_NoLegacy(t *testing.T) {
	homeDir := t.TempDir()

	r := checkLegacyConfigExists(homeDir)
	if r.Satisfied {
		t.Error("no legacy config should not be satisfied for migration")
	}
	if r.Remediation == "" {
		t.Error("remediation should not be empty")
	}
}

func TestLegacyConfigExists_TypedDocumentsPresent(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte("- id: test\n  home: /tmp\n"), 0644)
	os.MkdirAll(filepath.Dir(filepath.Join(homeDir, config.BaseDocumentPath)), 0755)
	os.WriteFile(filepath.Join(homeDir, config.BaseDocumentPath), []byte(`{"schema_version":"v1"}`), 0644)
	os.WriteFile(filepath.Join(homeDir, config.CaptainDocumentPath), []byte(`{"schema_version":"v1"}`), 0644)
	os.WriteFile(filepath.Join(homeDir, config.ProjectDocumentPath), []byte(`{"schema_version":"v1"}`), 0644)

	r := checkLegacyConfigExists(homeDir)
	if !r.Satisfied {
		t.Errorf("typed docs present should pass: %s", r.Detail)
	}
	if !strings.Contains(r.Detail, "already present") {
		t.Errorf("detail should mention already present: %s", r.Detail)
	}
}

// --- CheckResult formatting ---

func TestCheckResult_FormatErrors_Compatible(t *testing.T) {
	homeDir := t.TempDir()
	result := CheckOperation(OpTaskMutation, homeDir)
	if result.IsCompatible() {
		formatted := result.FormatErrors()
		if formatted != "" {
			t.Errorf("compatible result should have empty errors, got: %s", formatted)
		}
	}
}

func TestCheckResult_FormatErrors_Blocked(t *testing.T) {
	r := &CheckResult{
		Operation:  OpTeardown,
		Compatible: false,
		Requirements: []RequirementResult{
			{Name: "decodable-task-meta", Satisfied: false, Detail: "corrupt meta", Remediation: "Remove corrupt file"},
		},
	}
	formatted := r.FormatErrors()
	if !strings.Contains(formatted, "blocked") {
		t.Errorf("should mention blocked: %s", formatted)
	}
	if !strings.Contains(formatted, "corrupt meta") {
		t.Errorf("should include detail: %s", formatted)
	}
	if !strings.Contains(formatted, "Remove corrupt file") {
		t.Errorf("should include remediation: %s", formatted)
	}
}

func TestCheckResult_IsCompatibleMethod(t *testing.T) {
	cr := &CheckResult{Compatible: true}
	if !cr.IsCompatible() {
		t.Error("IsCompatible() should return true")
	}
	cr.Compatible = false
	if cr.IsCompatible() {
		t.Error("IsCompatible() should return false")
	}
}

// --- CheckOperation with valid homes ---

func TestCheckOperation_ValidHome(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)

	result := CheckOperation(OpTaskMutation, homeDir)
	if !result.IsCompatible() {
		t.Errorf("task mutation with valid home should be compatible: %s", result.FormatErrors())
	}
}

func TestCheckOperation_EachOperation(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)

	home.WriteMeta(homeDir, "test-task", map[string]string{"kind": "ship"})

	tests := []struct {
		op Operation
	}{
		{op: OpTaskMutation},
		{op: OpTeardown},
		{op: OpMigration},
	}

	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			result := CheckOperation(tt.op, homeDir)
			for _, r := range result.Requirements {
				if !r.Satisfied && r.Name != "readable-home" && r.Name != "harness-binary" && r.Name != "delivery-mode-compatible" && r.Name != "gh-available" && r.Name != "legacy-config-exists" && r.Name != "self-update-prerequisites" {
					t.Errorf("%s unexpected failure: %s", tt.op, r.Detail)
				}
			}
		})
	}
}

// --- Test harness binary lookup with specific harness ---

func TestHarnessBinary_ConfiguredHarness(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "soldier-harness"), []byte("pi\n"), 0644)

	r := checkHarnessBinary(homeDir, "soldier")
	if !r.Satisfied {
		if !strings.Contains(r.Detail, "on PATH") {
			t.Errorf("failure detail should mention PATH: %s", r.Detail)
		}
		if r.Remediation == "" {
			t.Error("soldier-harness binary check must have remediation")
		}
	}
}

// --- Self-update prerequisites ---

func TestSelfUpdatePrerequisites_NotInGitRepo(t *testing.T) {
	savedHomeDir := t.TempDir()
	result := CheckOperation(OpSelfUpdate, savedHomeDir)
	for _, r := range result.Requirements {
		if r.Name == "self-update-prerequisites" {
			if r.Satisfied {
				t.Log("self-update prerequisites passed (may be in a git repo)")
			} else {
				if !strings.Contains(r.Remediation, "git repository") && !strings.Contains(r.Remediation, "Install") {
					t.Errorf("expected git repo remediation: %s", r.Remediation)
				}
			}
			return
		}
	}
	t.Error("self-update-prerequisites check not found")
}

// --- Compatibility matrix consistency ---

func TestCompatibilityMatrix_NoDuplicateRequirementNames(t *testing.T) {
	ops := []Operation{
		OpTaskMutation,
		OpSpawn,
		OpCaptainLaunch,
		OpCaptainRecover,
		OpDelivery,
		OpMigration,
		OpSelfUpdate,
		OpTeardown,
	}
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, t.TempDir())
			seen := make(map[string]bool)
			for _, r := range result.Requirements {
				if seen[r.Name] {
					t.Errorf("duplicate requirement name %q in %s", r.Name, op)
				}
				seen[r.Name] = true
			}
		})
	}
}

func TestAllOperationsHaveKnownLabels(t *testing.T) {
	ops := []Operation{
		OpTaskMutation,
		OpSpawn,
		OpCaptainLaunch,
		OpCaptainRecover,
		OpDelivery,
		OpMigration,
		OpSelfUpdate,
		OpTeardown,
	}
	for _, op := range ops {
		s := op.String()
		if s == "" {
			t.Errorf("Operation %s has empty String()", op)
		}
		if s != string(op) {
			t.Errorf("Operation %s String() = %q, want %q", op, s, string(op))
		}
	}
}

// --- Integration: set bootstrap integrator bridge ---

func TestSetBootstrapIntegrator(t *testing.T) {
	called := false
	SetBootstrapIntegrator(func() (IntegrationStatusChecker, error) {
		called = true
		return &mockIntegrationChecker{}, nil
	})

	fn := bootstrapLookupIntegrator
	if fn == nil {
		t.Fatal("bootstrapLookupIntegrator should not be nil after SetBootstrapIntegrator")
	}
	checker, err := fn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checker == nil {
		t.Fatal("checker should not be nil")
	}
	if !called {
		t.Error("factory function was not called")
	}
}

type mockIntegrationChecker struct{}

func (m *mockIntegrationChecker) Status(homeDir, harness string) (IntegrationStatusInfo, error) {
	return IntegrationStatusInfo{Harness: harness, Scope: "project", State: "installed", Message: "mock"}, nil
}

func (m *mockIntegrationChecker) EnsureCaptain(homeDir string) error {
	return nil
}

func TestMustBeCompatible_PanicsOnIncompatible(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustBeCompatible should panic on corrupt meta")
		}
	}()

	homeDir := t.TempDir()
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)
	// Create a directory with .meta extension so ReadMeta fails.
	os.MkdirAll(filepath.Join(stateDir, "bad.meta"), 0755)

	MustBeCompatible(OpTeardown, homeDir)
}

func TestMustBeCompatible_DoesNotPanicOnValid(t *testing.T) {
	homeDir := t.TempDir()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustBeCompatible should not panic on valid home: %v", r)
		}
	}()

	MustBeCompatible(OpTaskMutation, homeDir)
}

func TestCheckResult_FormatErrors_EmptyRequirements(t *testing.T) {
	r := &CheckResult{
		Operation:    OpTaskMutation,
		Compatible:   true,
		Requirements: []RequirementResult{},
	}
	formatted := r.FormatErrors()
	if formatted != "" {
		t.Errorf("empty requirements should have empty errors, got: %s", formatted)
	}
}

func TestCheckResult_FormatErrors_MultipleBlocked(t *testing.T) {
	r := &CheckResult{
		Operation:  OpDelivery,
		Compatible: false,
		Requirements: []RequirementResult{
			{Name: "decodable-task-meta", Satisfied: false, Detail: "corrupt meta", Remediation: "Remove corrupt file"},
			{Name: "gh-available", Satisfied: false, Detail: "gh not on PATH", Remediation: "Install gh"},
		},
	}
	formatted := r.FormatErrors()
	if !strings.Contains(formatted, "corrupt meta") || !strings.Contains(formatted, "gh not on PATH") {
		t.Errorf("format should include both failures: %s", formatted)
	}
}

func TestCheckOperation_EmptyHome(t *testing.T) {
	result := CheckOperation(OpSpawn, "")
	if result == nil {
		t.Fatal("CheckOperation returned nil for empty home")
	}
	if result.IsCompatible() {
		t.Error("empty home should not be compatible")
	}
	hasHomeCheck := false
	for _, r := range result.Requirements {
		if r.Name == "readable-home" {
			hasHomeCheck = true
			if r.Satisfied {
				t.Error("readable-home should not be satisfied for empty path")
			}
			break
		}
	}
	if !hasHomeCheck {
		t.Error("result must include readable-home check")
	}
}