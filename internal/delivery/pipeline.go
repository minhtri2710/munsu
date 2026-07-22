package delivery

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/capability"
)

// Pipeline defines the interface for delivery operations.
// Each method maps to a phase of the delivery lifecycle.
type Pipeline interface {
	// RunPRCheck arms a PR merge poll for the given task.
	RunPRCheck(homeDir, id, prURL string) error
	// RunNoMistakes runs the no-mistakes validation pipeline.
	RunNoMistakes(intent string, skip []string) error
	// MergeLocal fast-forward merges the soldier branch into the local default branch.
	MergeLocal(homeDir, id string) error
}

// Compile-time checks that adapters implement Pipeline.
var (
	_ Pipeline = (*GHAxiAdapter)(nil)
	_ Pipeline = (*NoMistakesAdapter)(nil)
	_ Pipeline = (*GitLocalAdapter)(nil)
	_ Pipeline = (*CompositeAdapter)(nil)
)

// --- GHAxiAdapter ---

// GHAxiAdapter implements Pipeline operations via the gh-axi CLI.
// It checks the GitHub capability state on construction — if gh-axi is
// not Ready, operations fail closed.
type GHAxiAdapter struct {
	state capability.State
}

// NewGHAxiAdapter returns a new GHAxiAdapter after probing gh-axi availability.
// Returns an adapter with Ready state when gh-axi is on PATH, Absent otherwise.
func NewGHAxiAdapter() *GHAxiAdapter {
	return &GHAxiAdapter{state: ProbeGitHubCapability()}
}

// RunPRCheck arms a PR merge poll via PRCheck. Fails closed if gh-axi
// is not available.
func (a *GHAxiAdapter) RunPRCheck(homeDir, id, prURL string) error {
	if a.state != capability.Ready {
		return fmt.Errorf("GHAxiAdapter: GitHub capability not ready (state=%s): gh-axi required for PR check", a.state)
	}
	return PRCheck(homeDir, id, prURL)
}

// RunNoMistakes is not supported on GHAxiAdapter.
func (a *GHAxiAdapter) RunNoMistakes(intent string, skip []string) error {
	return fmt.Errorf("GHAxiAdapter: RunNoMistakes not supported — use NoMistakesAdapter")
}

// MergeLocal is not supported on GHAxiAdapter.
func (a *GHAxiAdapter) MergeLocal(homeDir, id string) error {
	return fmt.Errorf("GHAxiAdapter: MergeLocal not supported — use GitLocalAdapter")
}

// --- GlabAdapter ---

// GlabAdapter implements Pipeline operations for GitLab MRs via the glab CLI.
// It checks the GitLab capability state on construction — if glab is
// not Ready, operations fail closed.
type GlabAdapter struct {
	state capability.State
}

// NewGlabAdapter returns a new GlabAdapter after probing glab availability.
func NewGlabAdapter() *GlabAdapter {
	return &GlabAdapter{state: ProbeGitLabCapability()}
}

// RunMRCheck arms an MR merge poll for the given task using GitLab status paths.
// Fails closed if glab is not available.
func (a *GlabAdapter) RunMRCheck(homeDir, id, mrURL string) error {
	if a.state != capability.Ready {
		return fmt.Errorf("GlabAdapter: GitLab capability not ready (state=%s): glab required for MR check", a.state)
	}
	return MRLiveCheck(homeDir, id, mrURL)
}

// RunPRCheck is not supported on GlabAdapter for GitHub-style PRs.
func (a *GlabAdapter) RunPRCheck(homeDir, id, prURL string) error {
	return fmt.Errorf("GlabAdapter: RunPRCheck not supported for GitHub PRs — use GHAxiAdapter")
}

// RunNoMistakes is not supported on GlabAdapter.
func (a *GlabAdapter) RunNoMistakes(intent string, skip []string) error {
	return fmt.Errorf("GlabAdapter: RunNoMistakes not supported — use NoMistakesAdapter")
}

// MergeLocal is not supported on GlabAdapter.
func (a *GlabAdapter) MergeLocal(homeDir, id string) error {
	return fmt.Errorf("GlabAdapter: MergeLocal not supported — use GitLocalAdapter")
}

// --- NoMistakesAdapter ---

// NoMistakesAdapter implements Pipeline operations via the no-mistakes CLI.
type NoMistakesAdapter struct{}

// NewNoMistakesAdapter returns a new NoMistakesAdapter.
func NewNoMistakesAdapter() *NoMistakesAdapter {
	return &NoMistakesAdapter{}
}

// RunPRCheck is not supported on NoMistakesAdapter.
func (a *NoMistakesAdapter) RunPRCheck(homeDir, id, prURL string) error {
	return fmt.Errorf("NoMistakesAdapter: RunPRCheck not supported — use GHAxiAdapter")
}

// RunNoMistakes runs the no-mistakes validation pipeline via NoMistakesRun.
func (a *NoMistakesAdapter) RunNoMistakes(intent string, skip []string) error {
	return NoMistakesRun(intent, skip)
}

// MergeLocal is not supported on NoMistakesAdapter.
func (a *NoMistakesAdapter) MergeLocal(homeDir, id string) error {
	return fmt.Errorf("NoMistakesAdapter: MergeLocal not supported — use GitLocalAdapter")
}

// --- GitLocalAdapter ---

// GitLocalAdapter implements Pipeline operations via local git commands.
type GitLocalAdapter struct{}

// NewGitLocalAdapter returns a new GitLocalAdapter.
func NewGitLocalAdapter() *GitLocalAdapter {
	return &GitLocalAdapter{}
}

// RunPRCheck is not supported on GitLocalAdapter.
func (a *GitLocalAdapter) RunPRCheck(homeDir, id, prURL string) error {
	return fmt.Errorf("GitLocalAdapter: RunPRCheck not supported — use GHAxiAdapter")
}

// RunNoMistakes is not supported on GitLocalAdapter.
func (a *GitLocalAdapter) RunNoMistakes(intent string, skip []string) error {
	return fmt.Errorf("GitLocalAdapter: RunNoMistakes not supported — use NoMistakesAdapter")
}

// MergeLocal fast-forward merges via MergeLocal.
func (a *GitLocalAdapter) MergeLocal(homeDir, id string) error {
	return MergeLocal(homeDir, id)
}

// --- CompositeAdapter ---

// CompositeAdapter routes each Pipeline method to the correct adapter.
type CompositeAdapter struct {
	ghAxi      *GHAxiAdapter
	glab       *GlabAdapter
	noMistakes *NoMistakesAdapter
	gitLocal   *GitLocalAdapter
}

// NewCompositeAdapter returns a CompositeAdapter that delegates each method
// to the appropriate domain-specific adapter.
func NewCompositeAdapter() *CompositeAdapter {
	return &CompositeAdapter{
		ghAxi:      NewGHAxiAdapter(),
		glab:       NewGlabAdapter(),
		noMistakes: NewNoMistakesAdapter(),
		gitLocal:   NewGitLocalAdapter(),
	}
}

// RunPRCheck routes to the correct adapter based on the provider detected
// from the URL. GitHub PRs go through GHAxiAdapter; GitLab MRs through
// GlabAdapter. Unrecognized URLs fail closed.
func (a *CompositeAdapter) RunPRCheck(homeDir, id, prURL string) error {
	provider, _, _, _, _, err := ParseProviderURL(prURL)
	if err != nil {
		return fmt.Errorf("CompositeAdapter: unrecognized PR/MR URL: %w", err)
	}
	switch provider {
	case "github":
		return a.ghAxi.RunPRCheck(homeDir, id, prURL)
	case "gitlab":
		return a.glab.RunMRCheck(homeDir, id, prURL)
	default:
		return fmt.Errorf("CompositeAdapter: unknown provider %q for URL %s", provider, prURL)
	}
}

func (a *CompositeAdapter) RunNoMistakes(intent string, skip []string) error {
	return a.noMistakes.RunNoMistakes(intent, skip)
}

func (a *CompositeAdapter) MergeLocal(homeDir, id string) error {
	return a.gitLocal.MergeLocal(homeDir, id)
}
