//go:build integration

package fleet

import (
	"github.com/minhtri2710/munsu/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestPRMerge_FleetSyncReadsMeta verifies PRMerge reads meta correctly.
// It exercises the code path after the merge step by checking meta reading.
func TestPRMerge_FleetSyncReadsMeta(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	homeDir := t.TempDir()

	// Write meta with a project name and delivery identity
	ident := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     999999,
		URL:        "https://github.com/minhtri2710/munsu/pull/999999",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    "abc123def456abc123def456abc123def456abc1",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
	meta := ident.ToMeta()
	meta["project"] = "munsu"
	if err := home.WriteMeta(homeDir, "test-merge-task", meta); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// PRMerge requires gh-axi. Using a non-existent PR should fail at
	// the gh-axi merge step, proving the meta was readable.
	err := PRMerge(homeDir, "test-merge-task", "https://github.com/minhtri2710/munsu/pull/999999", nil, nil)

	// Should fail because PR #999999 doesn't exist (gh-axi merge will error)
	if err == nil {
		t.Error("expected error for non-existent PR merge")
	}

	// The error should be about gh-axi or PR not found, not about meta reading
	if err != nil && strings.Contains(err.Error(), "meta") {
		t.Errorf("error should not be about meta reading: %v", err)
	}
}

// TestCheckScriptFleetSyncPattern verifies the generated check.sh contains
// the exact expected 'fleet sync' shell pattern with a real PR.
func TestCheckScriptFleetSyncPattern(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	requireGH(t)

	homeDir := t.TempDir()

	// Write meta with a project
	meta := map[string]string{
		"project": "munsu",
	}
	if err := home.WriteMeta(homeDir, "pattern-task", meta); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// Use a real PR URL (PR #24 from the munsu repo)
	prURL := "https://github.com/minhtri2710/munsu/pull/24"
	if err := PRCheck(homeDir, "pattern-task", prURL, preparedCheckAuth(t, "pattern-task")); err != nil {
		t.Fatalf("PRCheck: %v", err)
	}

	checkPath := filepath.Join(home.StateDir(homeDir), "pattern-task.check")
	data, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatalf("reading check script: %v", err)
	}
	script := string(data)

	// The fleet sync line should be in the merged: true path
	if !strings.Contains(script, `echo "merged: true"`) {
		t.Errorf("should have merged: true branch, got:\n%s", script)
	}

	// Verify the structure: fleet sync after merged: true
	lines := strings.Split(script, "\n")
	foundMergeTrue := false
	foundFleetSync := false
	for _, line := range lines {
		if strings.Contains(line, `echo "merged: true"`) {
			foundMergeTrue = true
		}
		if strings.Contains(line, "fleet sync") {
			foundFleetSync = true
			if !foundMergeTrue {
				t.Error("fleet sync should appear after merged: true")
			}
		}
	}
	if !foundFleetSync {
		t.Error("check.sh should contain 'fleet sync' command")
	}

	// Verify the specific fleet sync shell command pattern
	if !strings.Contains(script, `munsu --home "$HOME_DIR" fleet sync "$PROJECT" 2>/dev/null || echo "Warning: fleet sync for ${PROJECT} failed" >&2`) {
		t.Errorf("check.sh should contain exact fleet sync shell command, got:\n%s", script)
	}
}

// TestFleetSyncEndToEnd tests the fleet sync mechanism end-to-end using the
func TestFleetSyncEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	homeDir := t.TempDir()
	projectsDir := filepath.Join(homeDir, "projects")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("creating projects dir: %v", err)
	}

	// Typed fleet config replaces the legacy projects.md: fleet sync reads
	// the project registry from data/projects.json. The legacy +yolo flag is
	// expressed as requireNoMistakes=false in the project overlay.
	falseVal := false
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
	}); err != nil {
		t.Fatalf("writing fleet base: %v", err)
	}
	if err := config.StoreCaptainRegistry(homeDir, config.CaptainRegistryDocument{
		SchemaVersion: config.CaptainRegistrySchemaVersion,
	}); err != nil {
		t.Fatalf("writing captain registry: %v", err)
	}
	if err := config.StoreProjectRegistry(homeDir, config.ProjectRegistryDocument{
		SchemaVersion: config.ProjectRegistrySchemaVersion,
		Projects: []config.ProjectRecord{
			{Name: "test-project", Path: "test project", Mode: "ship", Config: config.ProjectOverlay{RequireNoMistakes: &falseVal}},
		},
	}); err != nil {
		t.Fatalf("writing projects registry: %v", err)
	}

	// Create a bare repo as upstream
	remoteDir := filepath.Join(homeDir, "remote.git")
	cmd := exec.Command("git", "init", "--bare", remoteDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s", out)
	}

	// Create a local clone from the bare repo
	repoDir := filepath.Join(projectsDir, "test-project")
	cmd = exec.Command("git", "clone", remoteDir, repoDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %s", out)
	}

	// Configure and make initial commit in the clone, then push
	gitEnv := append(os.Environ(),
		"GIT_CEILING_DIRECTORIES="+homeDir,
	)
	for _, cfg := range []string{"user.email test@test.com", "user.name Test"} {
		parts := strings.Split(cfg, " ")
		c := exec.Command("git", append([]string{"config"}, parts...)...)
		c.Dir = repoDir
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %s", cfg, out)
		}
	}
	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# test"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = repoDir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = repoDir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	// Detect default branch and push
	branchOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("detecting branch: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(branchOut))
	cmd = exec.Command("git", "-C", repoDir, "push", "-u", "origin", defaultBranch)
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git push: %s", out)
	}

	// Build munsu binary
	projectRoot := findProjectRoot(t)
	if projectRoot == "" {
		t.Skip("could not find project root")
	}
	binaryPath := filepath.Join(homeDir, "munsu")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/munsu/")
	buildCmd.Dir = projectRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("building munsu: %s, %v", string(out), err)
	}

	// Run fleet sync
	cmd2 := exec.Command(binaryPath, "--home", homeDir, "fleet", "sync", "test-project")
	out, err2 := cmd2.CombinedOutput()
	if err2 != nil {
		t.Fatalf("fleet sync failed: %s, %v", string(out), err2)
	}

	// Verify synced output mentions project name
	if !strings.Contains(string(out), "test-project") {
		t.Errorf("expected fleet sync output to mention project name, got: %s", string(out))
	}
}

// findProjectRoot finds the project root by looking for go.mod
func findProjectRoot(t *testing.T) string {
	t.Helper()

	candidates := []string{
		"/Users/beowulf/.no-mistakes/worktrees/f11e85832040/01KXFYZQ1V0E2APRRED4PZF79Q",
	}

	wd, _ := os.Getwd()
	dir := wd
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "go.mod")); err == nil {
			return c
		}
	}

	return ""
}

// --- PRMerge wiring tests ---

// mockGitHubClient implements GitHubClient for testing.
type mockGitHubClient struct{}

func (m *mockGitHubClient) MergePR(owner, repo string, number int, method string) error {
	return nil
}

func (m *mockGitHubClient) ViewPRState(owner, repo string, number int) (string, error) {
	return "OPEN", nil
}

func (m *mockGitHubClient) ViewPRJSON(owner, repo string, number int, fields string) ([]byte, error) {
	return nil, nil
}

func (m *mockGitHubClient) CaptureIdentity(prURL string) (*domain.DeliveryIdentity, error) {
	return nil, nil
}

func (m *mockGitHubClient) ViewIssueState(owner, repo string, number int) (string, error) {
	return "OPEN", nil
}

// TestPRMerge_WiresReconcileMergeDelivery verifies that ReconcileMergeDelivery
// is called after a successful merge mutation, with the correct parameters.
func TestPRMerge_WiresReconcileMergeDelivery(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-wire"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	// Write meta with a valid delivery identity
	ident := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "testowner",
		Repo:       "testrepo",
		Number:     42,
		URL:        prURL,
		BaseRef:    "main",
		HeadRef:    "feature",
		HeadSHA:    "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		CapturedAt: "2024-01-01T00:00:00Z",
	}
	if err := home.WriteMeta(homeDir, taskID, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Save and replace all injectable dependencies
	savedClient := DefaultGitHubClient
	savedFetch := fetchLiveIdentity
	savedReconcile := ReconcileMergeDelivery

	var calledHome, calledID, calledURL string
	DefaultGitHubClient = func() (GitHubClient, error) {
		return &mockGitHubClient{}, nil
	}
	fetchLiveIdentity = func(prURL string) (*domain.DeliveryIdentity, error) {
		// Return the same identity as stored
		return ident, nil
	}
	ReconcileMergeDelivery = func(homeDir, taskID, prURL string, _ *taskauthority.Authority) (*MergeDeliveryResult, error) {
		calledHome = homeDir
		calledID = taskID
		calledURL = prURL
		return &MergeDeliveryResult{
			Outcome:       MergeOutcomeMerged,
			RemoteKnown:   true,
			ProviderState: "MERGED",
			MergedSHA:     "abc123def456abc123def456abc123def456abc1",
			PRNumber:      42,
			Detail:        "provider confirms PR #42 is merged",
		}, nil
	}

	defer func() {
		DefaultGitHubClient = savedClient
		fetchLiveIdentity = savedFetch
		ReconcileMergeDelivery = savedReconcile
	}()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := PRMerge(homeDir, taskID, prURL, nil, nil)

	// Close and restore
	w.Close()
	os.Stdout = oldStdout

	// Read captured output
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := r.Read(b)
		if n > 0 {
			buf.Write(b[:n])
		}
		if err != nil {
			break
		}
	}
	output := buf.String()

	// Verify reconciliation was called with correct parameters
	if calledHome != homeDir {
		t.Errorf("ReconcileMergeDelivery called with homeDir=%q, want %q", calledHome, homeDir)
	}
	if calledID != taskID {
		t.Errorf("ReconcileMergeDelivery called with id=%q, want %q", calledID, taskID)
	}
	if calledURL != prURL {
		t.Errorf("ReconcileMergeDelivery called with prURL=%q, want %q", calledURL, prURL)
	}

	// Verify no error (Merged outcome is not an error)
	if err != nil {
		t.Errorf("expected nil error for Merged outcome, got: %v", err)
	}

	// Verify output starts with remote truth
	if !strings.HasPrefix(output, "Remote truth:") {
		t.Errorf("output should start with 'Remote truth:', got:\n%s", output)
	}

	// Verify AXI block is present
	if !strings.Contains(output, "merge-delivery:") {
		t.Errorf("output should contain AXI block 'merge-delivery:', got:\n%s", output)
	}
	if !strings.Contains(output, "outcome: merged") {
		t.Errorf("output should contain 'outcome: merged', got:\n%s", output)
	}
}

// TestPRMerge_WireRenderOutput verifies that the output rendering leads with
// remote truth and includes the expected format for each outcome type.
func TestPRMerge_WireRenderOutput(t *testing.T) {
	// Test the Render function directly for each outcome type
	tests := []struct {
		name     string
		result   *MergeDeliveryResult
		wantLead string // prefix that output should start with
		wantAXI  string // string that should be in AXI block
		isError  bool
	}{
		{
			name: "merged",
			result: &MergeDeliveryResult{
				Outcome:       MergeOutcomeMerged,
				ProviderState: "MERGED",
				MergedSHA:     "abc123",
				RemoteKnown:   true,
				PRNumber:      42,
				MergeMethod:   "squash",
			},
			wantLead: "Remote truth: merged, SHA=abc123",
			wantAXI:  "outcome: merged",
			isError:  false,
		},
		{
			name: "already-merged",
			result: &MergeDeliveryResult{
				Outcome:       MergeOutcomeAlreadyMerged,
				ProviderState: "MERGED",
				MergedSHA:     "def456",
				RemoteKnown:   true,
				PRNumber:      42,
			},
			wantLead: "Remote truth: merged, SHA=def456",
			wantAXI:  "outcome: already-merged",
			isError:  false,
		},
		{
			name: "open",
			result: &MergeDeliveryResult{
				Outcome:       MergeOutcomeOpen,
				ProviderState: "OPEN",
				HeadSHA:       "abc123",
				RemoteKnown:   true,
				PRNumber:      42,
				Detail:        "PR #42 is still open (head=abc123); merge did not take effect",
			},
			wantLead: "Remote truth: open, head=abc123",
			wantAXI:  "outcome: open",
			isError:  true,
		},
		{
			name: "remote-unknown",
			result: &MergeDeliveryResult{
				Outcome:     MergeOutcomeRemoteUnknown,
				RemoteKnown: false,
				Detail:      "provider snapshot failed: timeout",
			},
			wantLead: "Remote truth: unreachable",
			wantAXI:  "remote-known: false",
			isError:  true,
		},
		{
			name: "failed",
			result: &MergeDeliveryResult{
				Outcome:       MergeOutcomeFailed,
				ProviderState: "CLOSED",
				RemoteKnown:   true,
				PRNumber:      42,
				Detail:        "PR #42 is closed but not merged (state=CLOSED)",
			},
			wantLead: "Remote truth: closed",
			wantAXI:  "outcome: failed",
			isError:  true,
		},
		{
			name: "escalated",
			result: &MergeDeliveryResult{
				Outcome:     MergeOutcomeRemoteUnknown,
				RemoteKnown: false,
				Escalated:   true,
				Detail:      "persistent remote-unknown: timeout",
			},
			wantLead: "Remote truth: unreachable",
			wantAXI:  "escalated: true",
			isError:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := tc.result.Render()

			// Output must lead with remote truth
			if !strings.HasPrefix(output, tc.wantLead) {
				t.Errorf("output should start with %q, got:\n%s", tc.wantLead, output)
			}

			// AXI block must be present
			if !strings.Contains(output, "merge-delivery:") {
				t.Error("output should contain 'merge-delivery:' AXI block")
			}
			if !strings.Contains(output, tc.wantAXI) {
				t.Errorf("output should contain %q, got:\n%s", tc.wantAXI, output)
			}

			// IsError must match expected
			if got := tc.result.IsError(); got != tc.isError {
				t.Errorf("IsError() = %v, want %v for outcome %s", got, tc.isError, tc.result.Outcome)
			}
		})
	}
}

// TestPRMerge_WireErrorOutcome verifies that PRMerge returns an error for
// outcomes that should produce a non-zero exit code.
func TestPRMerge_WireErrorOutcome(t *testing.T) {
	// Test each outcome type through PRMerge
	tests := []struct {
		name    string
		outcome MergeOutcome
		errors  bool
	}{
		{"merged", MergeOutcomeMerged, false},
		{"already-merged", MergeOutcomeAlreadyMerged, false},
		{"open", MergeOutcomeOpen, true},
		{"remote-unknown", MergeOutcomeRemoteUnknown, true},
		{"failed", MergeOutcomeFailed, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			taskID := "test-error-" + string(tc.outcome)
			prURL := "https://github.com/testowner/testrepo/pull/42"

			ident := &domain.DeliveryIdentity{
				Provider:   "github",
				Owner:      "testowner",
				Repo:       "testrepo",
				Number:     42,
				URL:        prURL,
				BaseRef:    "main",
				HeadRef:    "feature",
				HeadSHA:    "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
				CapturedAt: "2024-01-01T00:00:00Z",
			}
			if err := home.WriteMeta(homeDir, taskID, ident.ToMeta()); err != nil {
				t.Fatalf("WriteMeta: %v", err)
			}

			savedClient := DefaultGitHubClient
			savedFetch := fetchLiveIdentity
			savedReconcile := ReconcileMergeDelivery
			DefaultGitHubClient = func() (GitHubClient, error) {
				return &mockGitHubClient{}, nil
			}
			fetchLiveIdentity = func(prURL string) (*domain.DeliveryIdentity, error) {
				return ident, nil
			}
			ReconcileMergeDelivery = func(homeDir, taskID, prURL string, _ *taskauthority.Authority) (*MergeDeliveryResult, error) {
				return &MergeDeliveryResult{
					Outcome:     tc.outcome,
					RemoteKnown: tc.outcome != MergeOutcomeRemoteUnknown,
					Detail:      "test: " + string(tc.outcome),
				}, nil
			}

			defer func() {
				DefaultGitHubClient = savedClient
				fetchLiveIdentity = savedFetch
				ReconcileMergeDelivery = savedReconcile
			}()

			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := PRMerge(homeDir, taskID, prURL, nil, nil)

			w.Close()
			os.Stdout = oldStdout
			// Drain pipe
			go func() {
				b := make([]byte, 4096)
				for {
					_, err := r.Read(b)
					if err != nil {
						break
					}
				}
			}()

			if tc.errors && err == nil {
				t.Errorf("expected error for outcome %s, got nil", tc.outcome)
			}
			if !tc.errors && err != nil {
				t.Errorf("expected nil error for outcome %s, got: %v", tc.outcome, err)
			}
		})
	}
}
