package delivery

import "fmt"

// Pipeline defines the interface for delivery operations.
// Each method maps to a phase of the delivery lifecycle.
type Pipeline interface {
	// RunPRCheck arms a PR merge poll for the given task.
	RunPRCheck(homeDir, id, prURL string) error
	// RunNoMistakes runs the no-mistakes validation pipeline.
	RunNoMistakes(intent string, skip []string) error
	// MergeLocal fast-forward merges the crew branch into the local default branch.
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
type GHAxiAdapter struct{}

// NewGHAxiAdapter returns a new GHAxiAdapter.
func NewGHAxiAdapter() *GHAxiAdapter {
	return &GHAxiAdapter{}
}

// RunPRCheck arms a PR merge poll via PRCheck.
func (a *GHAxiAdapter) RunPRCheck(homeDir, id, prURL string) error {
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
	noMistakes *NoMistakesAdapter
	gitLocal   *GitLocalAdapter
}

// NewCompositeAdapter returns a CompositeAdapter that delegates each method
// to the appropriate domain-specific adapter.
func NewCompositeAdapter() *CompositeAdapter {
	return &CompositeAdapter{
		ghAxi:      NewGHAxiAdapter(),
		noMistakes: NewNoMistakesAdapter(),
		gitLocal:   NewGitLocalAdapter(),
	}
}

func (a *CompositeAdapter) RunPRCheck(homeDir, id, prURL string) error {
	return a.ghAxi.RunPRCheck(homeDir, id, prURL)
}

func (a *CompositeAdapter) RunNoMistakes(intent string, skip []string) error {
	return a.noMistakes.RunNoMistakes(intent, skip)
}

func (a *CompositeAdapter) MergeLocal(homeDir, id string) error {
	return a.gitLocal.MergeLocal(homeDir, id)
}
