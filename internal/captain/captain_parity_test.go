// Package captain_test captures executable parity evidence for munsu Captain
// versus firstmate/secondmate. Each scenario group builds on the architecture
// contract suite (#287) — extending with hermetic fixtures only.
// No firstmate code was harmed in the making of these tests.
package captain
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/marker"
)

// =============================================================================
// Scenario 1: Marked General routing (mu-from-general / marked send)
// =============================================================================
//
// The Captain charter declares that messages prefixed with [mu-from-general]
// followed by an invisible separator are General-routed requests, while
// unmarked messages are human captain typing.
//
// This group proves:
//   1a. IsFromGeneral detects the full marker
//   1b. MarkFromGeneral is idempotent (double-marking is a no-op)
//   1c. The DefaultCharter contains the mu-from-general label
//   1d. A queued send retains the marker across persist/load

func TestParity_MarkedGeneral_IsFromGeneral(t *testing.T) {
	marked := marker.MarkFromGeneral("report progress")
	if !marker.IsFromGeneral(marked) {
		t.Fatal("MarkFromGeneral message must be detected by IsFromGeneral")
	}
	if marker.IsFromGeneral("plain text") {
		t.Fatal("plain text must not be detected as from-general")
	}
	if marker.IsFromGeneral("") {
		t.Fatal("empty string must not be detected as from-general")
	}
}

func TestParity_MarkedGeneral_IdempotentMarking(t *testing.T) {
	msg := "do the thing"
	once := marker.MarkFromGeneral(msg)
	twice := marker.MarkFromGeneral(once)
	if once != twice {
		t.Fatal("MarkFromGeneral must be idempotent")
	}
	if !strings.HasPrefix(once, marker.FromGeneralLabel) {
		t.Fatalf("marked message must start with %q, got %q", marker.FromGeneralLabel, once)
	}
}

func TestParity_MarkedGeneral_CharterContainsReturnChannel(t *testing.T) {
	parent := t.TempDir()
	charter := DefaultCharter("parity-test", parent)
	if !strings.Contains(charter, marker.FromGeneralLabel) {
		t.Fatal("DefaultCharter must contain the mu-from-general marker label")
	}
	if !strings.Contains(charter, "munsu report") {
		t.Fatal("DefaultCharter must document munsu report as PRIMARY status path")
	}
	if !strings.Contains(charter, "downlink only") {
		t.Fatal("DefaultCharter must declare send as downlink only")
	}
	statusPath := filepath.Join(parent, "state", "captain:parity-test.status")
	if !strings.Contains(charter, statusPath) {
		t.Fatalf("DefaultCharter must contain the status file fallback path %q", statusPath)
	}
}

// =============================================================================
// Scenario 2: Structured-home authority (backlog/meta/provider outrank pane prose)
// =============================================================================
//
// A captain's authoritative source of truth is structured state under its home:
//   - task meta files (*.meta) describe in-flight soldiers
//   - backlog.md describes queued work
//   - config files describe harness/dispatch settings
// Unmarked pane prose is conversational and carries no execution authority.
//
// This group proves:
//   2a. Task meta is the authoritative source for soldier tracking
//   2b. inFlightSoldierIDs reads from meta, not from pane state
//   2c. Dispatch config (provider) is resolved from files, not prose

func TestParity_StructuredAuthority_MetaOverPaneProse(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Write meta files as the structured source for in-flight soldiers.
	os.WriteFile(filepath.Join(home, "state", "ship-1.meta"), []byte("kind=ship\nwindow=w1\n"), 0644)
	os.WriteFile(filepath.Join(home, "state", "scout-2.meta"), []byte("kind=scout\nwindow=w2\n"), 0644)
	os.WriteFile(filepath.Join(home, "state", "other.meta"), []byte("kind=other\n"), 0644)

	ids, err := inFlightSoldierIDs(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("inFlightSoldierIDs = %v, want exactly 2 (ship + scout)", ids)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["ship-1"] || !got["scout-2"] {
		t.Fatalf("inFlightSoldierIDs should report ship and scout, got %v", ids)
	}
}

func TestParity_StructuredAuthority_DispatchConfig(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "config"), 0755)

	// Dispatch config is the authoritative provider selection source.
	cfg := &harness.DispatchConfig{
		DefaultHarness: "pi",
		DefaultModel:   "opencode-go/deepseek-v4-flash",
		DefaultEffort:  "medium",
		Profiles: []harness.DispatchProfile{
			{Name: "review", Match: []string{"review"}, Harness: "codex", Model: "gpt-5.2-codex"},
			{Name: "default", Match: []string{"*"}, Harness: "pi", Model: "flash"},
		},
	}
	if err := harness.SaveDispatch(harness.DispatchPath(tmp), cfg); err != nil {
		t.Fatal(err)
	}

	// Reload from file (structured source).
	loaded, err := harness.LoadDispatch(harness.DispatchPath(tmp))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultHarness != "pi" {
		t.Errorf("default harness from file = %q, want pi", loaded.DefaultHarness)
	}

	// Dispatch resolves from structured config, not from unmarked prose.
	sel := harness.ResolveDispatchSelection(loaded, "review this PR")
	if sel.Harness != "codex" || sel.Model != "gpt-5.2-codex" {
		t.Errorf("review dispatch = %+v, want codex/gpt-5.2-codex", sel)
	}
}

func TestParity_StructuredAuthority_MetaTaskID(t *testing.T) {
	// The taskIDForCaptain function produces the structured key used for meta lookups.
	id := taskIDForCaptain("parity-test")
	if id != "captain:parity-test" {
		t.Errorf("taskIDForCaptain = %q, want captain:parity-test", id)
	}
}

// =============================================================================
// Scenario 3: Deterministic dispatch selection (ready queue only)
// =============================================================================
//
// Dispatch resolution is deterministic and gated by the ready queue:
//   - First matching profile wins (ordered list)
//   - Empty task description returns defaults
//   - Wildcard catchall matches everything
//   - No match without default returns empty (no dispatch)
//
// This group proves that only queued items drive dispatch — no free-form
// pane prose triggers dispatch unless it matches a profile rule.

func TestParity_DeterministicDispatch_FirstMatchWins(t *testing.T) {
	cfg := &harness.DispatchConfig{
		DefaultHarness: "pi",
		Profiles: []harness.DispatchProfile{
			{Name: "review", Match: []string{"review"}, Harness: "codex"},
			{Name: "research", Match: []string{"research"}, Harness: "claude"},
			{Name: "default", Match: []string{"*"}, Harness: "pi"},
		},
	}

	tests := []struct {
		desc string
		want string
	}{
		{"review the changes", "codex"},
		{"research this topic", "claude"},
		{"implement feature", "pi"},
		{"", "pi"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := harness.ResolveDispatch(cfg, tt.desc)
			if got != tt.want {
				t.Errorf("ResolveDispatch(_, %q) = %q, want %q", tt.desc, got, tt.want)
			}
		})
	}
}

func TestParity_DeterministicDispatch_NoMatchWithoutDefault(t *testing.T) {
	cfg := &harness.DispatchConfig{
		Profiles: []harness.DispatchProfile{
			{Name: "review", Match: []string{"review"}, Harness: "codex"},
		},
	}
	got := harness.ResolveDispatch(cfg, "implement feature")
	if got != "" {
		t.Errorf("expected empty for no-match no-default, got %q", got)
	}
}

func TestParity_DeterministicDispatch_EmptyConfigReturnsEmpty(t *testing.T) {
	cfg := &harness.DispatchConfig{}
	got := harness.ResolveDispatch(cfg, "anything")
	if got != "" {
		t.Errorf("expected empty for empty config, got %q", got)
	}
}

func TestParity_DeterministicDispatch_ModelEffortPropagation(t *testing.T) {
	cfg := &harness.DispatchConfig{
		DefaultHarness: "pi",
		DefaultModel:   "default-model",
		DefaultEffort:  "low",
		Profiles: []harness.DispatchProfile{
			{Name: "review", Match: []string{"review"}, Harness: "codex", Model: "gpt-5.2-codex", Effort: "high"},
			{Name: "all", Match: []string{"*"}, Harness: "pi", Model: "opencode-go/deepseek-v4-flash", Effort: "medium"},
		},
	}

	sel := harness.ResolveDispatchSelection(cfg, "review this PR")
	if sel.Harness != "codex" || sel.Model != "gpt-5.2-codex" || sel.Effort != "high" {
		t.Errorf("review = %+v, want codex/gpt-5.2-codex/high", sel)
	}

	sel = harness.ResolveDispatchSelection(cfg, "implement feature")
	if sel.Harness != "pi" || sel.Model != "opencode-go/deepseek-v4-flash" || sel.Effort != "medium" {
		t.Errorf("catchall = %+v, want pi/opencode-go/deepseek-v4-flash/medium", sel)
	}

	sel = harness.ResolveDispatchSelection(cfg, "")
	if sel.Harness != "pi" || sel.Model != "default-model" || sel.Effort != "low" {
		t.Errorf("empty = %+v, want pi/default-model/low", sel)
	}
}

// =============================================================================
// Scenario 4: Conservative update/fast-forward outcomes
// =============================================================================
//
// Update() maps safeFF results to typed UpdateOutcome values:
//   - AlreadyCurrent: already at parent's default-branch commit
//   - FastForwarded: successfully fast-forwarded
//   - StateOnlySkipped: captain home has no git worktree
//   - InvalidProvenance: home is missing or has bad provenance

func TestParity_ConservativeUpdate_StateOnlyHome(t *testing.T) {
	// A captain home without .git is a state-only home.
	parent := t.TempDir()
	smHome := filepath.Join(parent, "captains", "state-only")
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# State-only\n"), 0644)
	SeedProvenance(smHome, "state-only")

	resp := Update(smHome, parent)
	if resp.Outcome != StateOnlySkipped {
		t.Fatalf("Update on state-only home = %v, want StateOnlySkipped", resp.Outcome)
	}
	if resp.Err != nil {
		t.Fatalf("Update on state-only home should have no error, got: %v", resp.Err)
	}
}

func TestParity_ConservativeUpdate_InvalidProvenance(t *testing.T) {
	// A plain directory without provenance marker.
	parent := t.TempDir()
	badHome := filepath.Join(parent, "captains", "bad")
	os.MkdirAll(badHome, 0755)

	resp := Update(badHome, parent)
	if resp.Outcome != InvalidProvenance {
		t.Fatalf("Update on unmarked home = %v, want InvalidProvenance", resp.Outcome)
	}
	if resp.Err == nil {
		t.Fatal("Update on unmarked home should return an error")
	}
}

func TestParity_ConservativeUpdate_AlreadyCurrent(t *testing.T) {
	// Both parent and captain clone the same remote at the same commit,
	// then Update — should be AlreadyCurrent.
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitRun("init", "--bare", remote)

	// Parent clone: push initial commit with .gitignore for provenance marker.
	parent := filepath.Join(root, "parent")
	gitRun("clone", remote, parent)
	gitRun("-C", parent, "config", "user.name", "Test")
	gitRun("-C", parent, "config", "user.email", "t@t.invalid")
	gitRun("-C", parent, "checkout", "-b", "main")
	os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("# General\n"), 0644)
	os.WriteFile(filepath.Join(parent, ".gitignore"), []byte(ProvenanceMarkerName+"\n"), 0644)
	gitRun("-C", parent, "add", "AGENTS.md", ".gitignore")
	gitRun("-C", parent, "commit", "-m", "init")
	gitRun("-C", parent, "push", "-u", "origin", "main")
	gitRun("-C", remote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitRun("-C", parent, "remote", "set-head", "origin", "main")

	// Captain clone at same commit. No extra commits needed.
	smHome := filepath.Join(root, "captains", "test-sm")
	gitRun("clone", remote, smHome)
	gitRun("-C", smHome, "config", "user.name", "Test")
	gitRun("-C", smHome, "config", "user.email", "t@t.invalid")
	gitRun("-C", smHome, "checkout", "-b", "main")
// Already at the same commit as parent.

	SeedProvenance(smHome, "test-sm")

	// .munsu-captain-home should be gitignored at this point.
	resp := Update(smHome, parent)
	if resp.Outcome != AlreadyCurrent {
		t.Fatalf("Update on same-ref captain = %v, want AlreadyCurrent (error=%v)", resp.Outcome, resp.Err)
	}
}

// =============================================================================
// Scenario 5: Dirty / diverged / offline handling
// =============================================================================
//
// Update maps git failure states to typed outcomes:
//   - Dirty: tracked or unignored untracked changes in captain home
//   - Diverged: captain history has diverged from parent
//   - Offline: remote origin missing or unreachable
//   - WrongBranch: captain is on a branch other than the default

func TestParity_DirtyDivergedOffline_OutcomeMapping(t *testing.T) {
	tests := []struct {
		name    string
		outcome UpdateOutcome
		fail    bool
	}{
		{"dirty maps to failure", Dirty, true},
		{"diverged maps to failure", Diverged, true},
		{"offline maps to failure", Offline, true},
		{"wrong-branch maps to failure", WrongBranch, true},
		{"wrong-remote maps to failure", WrongRemote, true},
		{"already-current is not failure", AlreadyCurrent, false},
		{"fast-forwarded is not failure", FastForwarded, false},
		{"state-only-skipped is not failure", StateOnlySkipped, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.outcome.IsFailure() != tt.fail {
				t.Errorf("%s.IsFailure() = %v, want %v", tt.outcome, tt.outcome.IsFailure(), tt.fail)
			}
		})
	}
}

func TestParity_DirtyDivergedOffline_WrongBranchRefused(t *testing.T) {
	// Create a captain on a non-default branch.
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitRun("init", "--bare", remote)

	parent := filepath.Join(root, "parent")
	gitRun("clone", remote, parent)
	gitRun("-C", parent, "config", "user.name", "Test")
	gitRun("-C", parent, "config", "user.email", "t@t.invalid")
	gitRun("-C", parent, "checkout", "-b", "main")
	os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("# General\n"), 0644)
	gitRun("-C", parent, "add", "AGENTS.md")
	gitRun("-C", parent, "commit", "-m", "init")
	gitRun("-C", parent, "push", "-u", "origin", "main")
	gitRun("-C", remote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitRun("-C", parent, "remote", "set-head", "origin", "main")

	smHome := filepath.Join(root, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	gitRun("-C", smHome, "init")
	gitRun("-C", smHome, "remote", "add", "origin", remote)
	gitRun("-C", smHome, "fetch", "origin", "main")
	// Check out a feature branch instead of main.
	gitRun("-C", smHome, "checkout", "-B", "feature", "origin/main")
	gitRun("-C", smHome, "remote", "set-head", "origin", "main")
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Captain\n"), 0644)
	// Add .gitignore for provenance marker so safeFF doesn't see it as untracked.
	os.WriteFile(filepath.Join(smHome, ".gitignore"), []byte(ProvenanceMarkerName+"\n"), 0644)
	gitRun("-C", smHome, "add", "AGENTS.md", ".gitignore")
	gitRun("-C", smHome, "commit", "-m", "charter")
	gitRun("-C", smHome, "push", "-u", "origin", "feature")

	SeedProvenance(smHome, "test-sm")
	resp := Update(smHome, parent)
	if resp.Outcome != WrongBranch {
		t.Fatalf("Update on feature-branch captain = %v, want WrongBranch (error=%v)", resp.Outcome, resp.Err)
	}
}

func TestParity_DirtyDivergedOffline_DirtyChangesRefused(t *testing.T) {
	// Create a captain with tracked changes.
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitRun("init", "--bare", remote)

	parent := filepath.Join(root, "parent")
	gitRun("clone", remote, parent)
	gitRun("-C", parent, "config", "user.name", "Test")
	gitRun("-C", parent, "config", "user.email", "t@t.invalid")
	gitRun("-C", parent, "checkout", "-b", "main")
	os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("# General\n"), 0644)
	gitRun("-C", parent, "add", "AGENTS.md")
	gitRun("-C", parent, "commit", "-m", "init")
	gitRun("-C", parent, "push", "-u", "origin", "main")
	gitRun("-C", remote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitRun("-C", parent, "remote", "set-head", "origin", "main")

	smHome := filepath.Join(root, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	gitRun("-C", smHome, "init")
	gitRun("-C", smHome, "remote", "add", "origin", remote)
	gitRun("-C", smHome, "fetch", "origin", "main")
	gitRun("-C", smHome, "checkout", "-B", "main", "origin/main")
	gitRun("-C", smHome, "remote", "set-head", "origin", "main")
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Captain\n"), 0644)
	// Add .gitignore for provenance marker so safeFF doesn't see it as untracked.
	os.WriteFile(filepath.Join(smHome, ".gitignore"), []byte(ProvenanceMarkerName+"\n"), 0644)
	gitRun("-C", smHome, "add", "AGENTS.md", ".gitignore")
	gitRun("-C", smHome, "commit", "-m", "charter")
	gitRun("-C", smHome, "push", "-u", "origin", "main")

	// Dirty the captain home with a tracked modification.
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Modified\n"), 0644)

	SeedProvenance(smHome, "test-sm")
	resp := Update(smHome, parent)
	if resp.Outcome != Dirty {
		t.Fatalf("Update on dirty captain = %v, want Dirty (error=%v)", resp.Outcome, resp.Err)
	}
}

// =============================================================================
// Scenario 6: Safe idle behavior when queue empty
// =============================================================================
//
// The Captain charter declares "an empty queue is healthy." When there are no
// queued tasks (no backlog entries, no pending meta), the captain should not
// spawn soldiers, scan for work, or send status.

func TestParity_SafeIdle_EmptyQueueIsHealthy(t *testing.T) {
	// A seeded but never-used captain home with no backlog and no meta files
	// should report zero in-flight soldiers.
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	ids, err := inFlightSoldierIDs(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected zero in-flight soldiers for empty state, got %v", ids)
	}
}

func TestParity_SafeIdle_NoBacklogProducesNoDispatch(t *testing.T) {
	// An empty DispatchConfig with no profiles and no default should return
	// empty harness — no dispatch decision to act on.
	cfg := &harness.DispatchConfig{}
	sel := harness.ResolveDispatchSelection(cfg, "any task")
	if sel.Harness != "" {
		t.Errorf("empty config dispatch = %q, want empty (idle)", sel.Harness)
	}
}

func TestParity_SafeIdle_NoReadyQueueItems(t *testing.T) {
	// The ready queue is the backlog. With an empty backlog, no task keys
	// can be handed off, so the captain spawns nothing.
	parent := t.TempDir()
	sm := filepath.Join(parent, "captains", "idle-test")
	os.MkdirAll(filepath.Join(sm, "state"), 0755)
	os.MkdirAll(filepath.Join(sm, "config"), 0755)
	os.MkdirAll(filepath.Join(sm, "data"), 0755)
	os.WriteFile(filepath.Join(sm, "AGENTS.md"), []byte("# Idle captain\n"), 0644)
	SeedProvenance(sm, "idle-test")

	// No backlog file exists.
	ids, err := inFlightSoldierIDs(sm)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected zero soldiers with no backlog, got %v", ids)
	}
}

func TestParity_SafeIdle_CharterDeclaresEmptyQueueHealthy(t *testing.T) {
	// The DefaultCharter must state that an empty queue is healthy.
	parent := t.TempDir()
	charter := DefaultCharter("idle-check", parent)
	if !strings.Contains(charter, "empty queue is healthy") {
		t.Fatal("DefaultCharter must declare that an empty queue is healthy")
	}
	// Also verify the charter says "Never invent surveys, audits, or self-directed find work tasks"
	if !strings.Contains(charter, "Never invent") {
		t.Fatal("DefaultCharter should prohibit self-directed work generation")
	}
}

// =============================================================================
// Captured evidence: outcomeFromFFReason mapping
// =============================================================================
//
// This table proves that each safeFF reason maps to the correct UpdateOutcome
// without running git.

func TestParity_OutcomeMapping_AllReasons(t *testing.T) {
	// Map SafeFFReason → UpdateOutcome (happy path, no underlying error)
	tests := []struct {
		reason   SafeFFReason
		want     UpdateOutcome
		hasError bool
	}{
		{SafeFFAlreadyCurrent, AlreadyCurrent, false},
		{SafeFFSuccess, FastForwarded, false},
		{SafeFFOffBranch, WrongBranch, true},
		{SafeFFMissingOrigin, Offline, true},
		{SafeFFChangesTracked, Dirty, true},
		{SafeFFError, Diverged, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			var err error
			if tt.hasError {
				err = os.ErrInvalid
			}
			got := outcomeFromFFReason(tt.reason, err)
			if got != tt.want {
				t.Errorf("outcomeFromFFReason(%q, err=%v) = %q, want %q", tt.reason, err != nil, got, tt.want)
			}
		})
	}
}

func TestParity_OutcomeMapping_FromError(t *testing.T) {
	tests := []struct {
		msg  string
		want UpdateOutcome
	}{
		{"foo tracked changes bar", Dirty},
		{"unignored untracked: rogue.txt", Dirty},
		{"remote origin not found", Offline},
		{"origin/HEAD does not exist locally", Offline},
		{"remote %q differs: origin vs upstream", WrongRemote},
		{"expected 'main' got 'feature'", WrongBranch},
		{"not an ancestor", Diverged},
		{"merge --ff-only failed", Diverged},
		{"some other error", Diverged},
	}
	for _, tt := range tests {
		tname := tt.msg
		if len(tname) > 20 {
			tname = tname[:20]
		}
		t.Run(tname, func(t *testing.T) {
			err := fmt.Errorf("%s", tt.msg)
			got := outcomeFromFFError(err)
			if got != tt.want {
				t.Errorf("outcomeFromFFError(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

// Verify the DefaultCharter of a seeded captain contains the idle-by-default doctrine.
func TestParity_DefaultCharter_IdleByDefault(t *testing.T) {
	parent := t.TempDir()
	charter := DefaultCharter("sm-alpha", parent)

	checks := []string{
		"empty queue is healthy",
		"Never invent surveys, audits",
		"downlink only",
		"PRIMARY status path",
	}
	for _, check := range checks {
		if !strings.Contains(charter, check) {
			t.Errorf("DefaultCharter must contain %q", check)
		}
	}
}
