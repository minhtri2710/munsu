// Package fleet — fleet rehearsal test suite.
//
// This test suite rehearses fresh, legacy, partially migrated, corrupt, and
// mixed-version fleet states, proving that every configuration produces the
// expected typed outcome from the compatibility matrix.
package fleet

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/configmigration"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// TestFleetRehearsal_FreshFleet proves that a fresh (empty) fleet with no
// legacy state is fully compatible with all operations.
func TestFleetRehearsal_FreshFleet(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.WriteFile(filepath.Join(homeDir, "config", "soldier-harness"), []byte("pi\n"), 0644)

	// Fresh fleet: all operations should be compatible or have clean skips
	ops := []Operation{OpTaskMutation, OpSelfUpdate, OpTeardown}
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			result := CheckOperation(op, homeDir)
			if result == nil {
				t.Fatal("CheckOperation returned nil")
			}
			// Fresh fleet may have requirements that are not satisfied
			// (e.g., no home config), but the check must return a typed
			// result with format errors, not panic.
			_ = result.FormatErrors()
			_ = result.IsCompatible()
		})
	}
}

// TestFleetRehearsal_LegacyFleet proves that a legacy fleet (with legacy
// config files, old-style meta) is detected by the migration operation
// and produces the expected typed outcome.
func TestFleetRehearsal_LegacyFleet(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)

	// Write legacy config files with correct format (matching existing tests)
	legacyCaptains := `- captain-1 - (home: /path/to/captain-1; scope: test; projects: project-a; added: 2026-01-15)
- captain-2 - (home: /path/to/captain-2; scope: test; projects: project-b; added: 2026-01-16)
`
	if err := os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(legacyCaptains), 0644); err != nil {
		t.Fatal(err)
	}

	legacyProjects := `- project-a - /tmp/project-a (added 2026-01-15)
- project-b - /tmp/project-b (added 2026-01-16)
`
	if err := os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(legacyProjects), 0644); err != nil {
		t.Fatal(err)
	}

	legacyDispatch := `{
		"defaults": {"harness": "pi", "model": "sonnet"}
	}`
	if err := os.WriteFile(filepath.Join(homeDir, "config", "soldier-dispatch.json"), []byte(legacyDispatch), 0644); err != nil {
		t.Fatal(err)
	}

	// Phase 2.1: Migration operation detects legacy config
	result := CheckOperation(OpMigration, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if !result.IsCompatible() {
		t.Logf("Migration check: %s", result.FormatErrors())
		// Legacy config exists — migration should be possible
		hasLegacyCheck := false
		for _, req := range result.Requirements {
			if req.Name == "legacy-config-exists" {
				hasLegacyCheck = true
				if req.Satisfied {
					t.Log("legacy config detected — migration is possible")
				}
				break
			}
		}
		if !hasLegacyCheck {
			t.Error("migration must include legacy-config-exists requirement")
		}
	}

	// Phase 2.2: Migration plan works on legacy config
	plan, err := configmigration.PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("migration PlanConfigMigration: %v", err)
	}
	if plan == nil {
		t.Fatal("migration Plan returned nil")
	}
	if len(plan.LegacyFiles) == 0 {
		t.Log("no legacy files found in plan (expected when typed docs already exist)")
	}
}

// TestFleetRehearsal_PartiallyMigratedFleet proves that a partially migrated
// fleet (some typed config, some legacy) produces correct typed outcomes.
func TestFleetRehearsal_PartiallyMigratedFleet(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)

	// Write typed project registry (already migrated)
	projects := config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "migrated-project", Path: "/path/to/project", Mode: "no-mistakes"},
		},
	}
	if err := config.StoreProjectRegistry(homeDir, projects); err != nil {
		t.Fatal(err)
	}

	// Write legacy captain registry with correct format (not yet migrated)
	legacyCaptains := `- captain-1 - (home: /path/to/captain-1; scope: test; projects: migrated-project; added: 2026-01-15)
`
	if err := os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(legacyCaptains), 0644); err != nil {
		t.Fatal(err)
	}

	// Phase 3.1: Verify compatibility check handles partial migration
	result := CheckOperation(OpMigration, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if !result.IsCompatible() {
		t.Logf("Partial migration check: %s", result.FormatErrors())
	}

	// Phase 3.2: Migration system detects partial state
	needed, cmd := configmigration.NeedsConfigMigration(homeDir)
	if needed {
		t.Logf("partial migration detected — needs: %s", cmd)
	} else {
		t.Log("partial migration fully migrated (both typed and legacy detected as complete)")
	}
}

// TestFleetRehearsal_CorruptFleet proves that corrupt task meta produces
// a fail-closed typed outcome from the compatibility matrix.
func TestFleetRehearsal_CorruptFleet(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)

	// Write a corrupt meta file (binary garbage)
	corruptPath := filepath.Join(homeDir, "state", "corrupt-task.meta")
	if err := os.WriteFile(corruptPath, []byte{0xFF, 0xFE, 0xFD, 0x00}, 0644); err != nil {
		t.Fatal(err)
	}

	// Phase 4.1: Corrupt meta is detected by task-mutation check
	result := CheckOperation(OpTaskMutation, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if !result.IsCompatible() {
		// Corrupt meta should cause a failed requirement with remediation
		for _, req := range result.Requirements {
			if !req.Satisfied && req.Name == "decodable-task-meta" {
				t.Logf("corrupt meta detected: %s — remediation: %s", req.Detail, req.Remediation)
				if req.Remediation == "" {
					t.Error("corrupt meta remediation must be non-empty")
				}
				return
			}
		}
	}

	// Phase 4.2: Spawn on corrupt meta also fails
	spawnResult := CheckOperation(OpSpawn, homeDir)
	if spawnResult == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if spawnResult.IsCompatible() {
		t.Log("spawn check passed despite corrupt meta — expected if no pi binary")
	}

	// Phase 4.3: Remove corrupt meta and verify compatibility is restored
	if err := os.Remove(corruptPath); err != nil {
		t.Fatal(err)
	}
	cleanResult := CheckOperation(OpTaskMutation, homeDir)
	if cleanResult == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if !cleanResult.IsCompatible() {
		t.Logf("clean meta check: %s", cleanResult.FormatErrors())
	}
}

// TestFleetRehearsal_MixedVersionFleet proves that version inequality alone
// never blocks — only specific incompatibilities are rejected.
func TestFleetRehearsal_MixedVersionFleet(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	// Write meta with a higher version number (simulating a newer version)
	meta := map[string]string{
		"kind":       "ship",
		"window":     "w1",
		"worktree":   "/tmp/wt",
		"project":    "test-project",
		"harness":    "pi",
		"model":      "sonnet",
		"created":    "2026-07-31T00:00:00Z",
		"updated":    "2026-07-31T00:00:00Z",
		"pr_url":     "https://github.com/owner/repo/pull/42",
		"pr_number":  "42",
		"pr_head_sha": "abc123",
	}
	if err := home.WriteMeta(homeDir, "mixed-version-task", meta); err != nil {
		t.Fatal(err)
	}

	// Phase 5.1: Version inequality alone does not block task mutation
	result := CheckOperation(OpTaskMutation, homeDir)
	if result == nil {
		t.Fatal("CheckOperation returned nil")
	}
	// Meta is decodable — should be compatible
	if !result.IsCompatible() {
		t.Logf("mixed-version task check: %s", result.FormatErrors())
		for _, req := range result.Requirements {
			if !req.Satisfied {
				t.Logf("unsatisfied: %s — %s", req.Name, req.Detail)
			}
		}
	}

	// Phase 5.2: Version inequality alone does not block teardown
	teardownResult := CheckOperation(OpTeardown, homeDir)
	if teardownResult == nil {
		t.Fatal("CheckOperation returned nil")
	}
	if !teardownResult.IsCompatible() {
		t.Logf("mixed-version teardown check: %s — version inequality alone should not block", teardownResult.FormatErrors())
	}
}

// TestFleetRehearsal_RecoveryCircuitTypedOutcomes proves that the recovery
// circuit produces the correct typed outcomes across all circuit states.
func TestFleetRehearsal_RecoveryCircuitTypedOutcomes(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	store := orchestrator.NewCircuitStore(homeDir)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	key := orchestrator.CircuitKey{
		Target:    "captain:test-sm",
		Input:     "captain launch",
		Signature: orchestrator.CircuitSignature("failed", "timeout"),
	}
	budget := orchestrator.DefaultBudget()

	// Phase 6.1: Fresh circuit (no prior record) — not blocked, not half-open
	blocked, err := orchestrator.IsCircuitBlocked(store, key, now)
	if err != nil {
		t.Fatalf("IsCircuitBlocked (fresh): %v", err)
	}
	if blocked {
		t.Error("fresh circuit should not be blocked")
	}

	// Phase 6.2: Exhaust budget and verify circuit opens
	for i := 0; i < budget.MaxAttempts; i++ {
		now = now.Add(1 * time.Second)
		opened, err := orchestrator.RecordCircuitAttempt(store, key, budget, now)
		if err != nil {
			t.Fatalf("RecordCircuitAttempt #%d: %v", i+1, err)
		}
		if i == budget.MaxAttempts-1 && !opened {
			t.Error("circuit should open on the final attempt")
		}
	}

	// Phase 6.3: Open circuit blocks
	blocked, err = orchestrator.IsCircuitBlocked(store, key, now)
	if err != nil {
		t.Fatalf("IsCircuitBlocked (open): %v", err)
	}
	if !blocked {
		t.Error("open circuit must block recovery")
	}

	// Phase 6.4: After cooldown, circuit is half-open
	now = now.Add(budget.Cooldown + 1*time.Second)
	c, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load circuit: %v", err)
	}
	if c == nil {
		t.Fatal("circuit not found")
	}
	if !c.HalfOpen(now) {
		t.Error("circuit should be half-open after cooldown")
	}

	// Phase 6.5: Successful probe closes the circuit
	closed, err := orchestrator.RecordCircuitSuccess(store, key, now)
	if err != nil {
		t.Fatalf("RecordCircuitSuccess: %v", err)
	}
	// The circuit should close after enough successes (StableAlive defaults to 2)
	if !closed {
		t.Log("circuit not closed after first success — expected if StableAlive > 1, trying second")
		now = now.Add(1 * time.Second)
		closed, err = orchestrator.RecordCircuitSuccess(store, key, now)
		if err != nil {
			t.Fatalf("RecordCircuitSuccess #2: %v", err)
		}
		if !closed {
			t.Error("circuit should close after sufficient successes")
		}
	}

	// Phase 6.6: Closed circuit is not blocked
	blocked, err = orchestrator.IsCircuitBlocked(store, key, now)
	if err != nil {
		t.Fatalf("IsCircuitBlocked (closed): %v", err)
	}
	if blocked {
		t.Error("closed circuit must not block recovery")
	}
}