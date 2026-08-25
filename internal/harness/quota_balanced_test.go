package harness

import (
	"errors"
	"testing"
)

// stubQuotaProvider returns a fixed JSON string for testing.
type stubQuotaProvider struct {
	output string
	err    error
}

func (s *stubQuotaProvider) Run() (string, error) {
	return s.output, s.err
}

func newQuotaSelectorWithProvider(provider QuotaAxiProvider) quotaSelector {
	return &quotaBalancedSelector{provider: provider}
}

// TestQuotaSelector_UnavailableQuota verifies that when quota-axi is not
// on PATH (returns an error), the selector falls back to the first candidate.
func TestQuotaSelector_UnavailableQuota(t *testing.T) {
	provider := &stubQuotaProvider{err: errors.New("quota-axi: not found")}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex", Model: "gpt-5.2-codex", Effort: "high"},
		{Harness: "claude", Model: "claude-sonnet-4", Effort: "medium"},
	}

	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("unavailable quota: got %q, want codex (first candidate)", got.Harness)
	}
	if got.Model != "gpt-5.2-codex" {
		t.Errorf("unavailable quota model: got %q, want gpt-5.2-codex", got.Model)
	}
	if got.Effort != "high" {
		t.Errorf("unavailable quota effort: got %q, want high", got.Effort)
	}
}

// TestQuotaSelector_UnparseableJSON verifies fallback when quota data is malformed.
func TestQuotaSelector_UnparseableJSON(t *testing.T) {
	provider := &stubQuotaProvider{output: "{invalid json}"}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "pi", Model: "default-model"},
		{Harness: "grok"},
	}

	got := sel.selectCandidate(candidates)
	if got.Harness != "pi" {
		t.Errorf("unparseable quota: got %q, want pi (first candidate)", got.Harness)
	}
}

// TestQuotaSelector_EmptyProvidersList verifies fallback when quota data has no providers.
func TestQuotaSelector_EmptyProvidersList(t *testing.T) {
	provider := &stubQuotaProvider{output: `{"providers": []}`}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
		{Harness: "claude"},
	}

	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("empty providers: got %q, want codex (first candidate)", got.Harness)
	}
}

// TestQuotaSelector_StaleQuota_FreshWins verifies that a fresh candidate wins
// over a stale candidate when the stale margin is below the 20-point threshold.
func TestQuotaSelector_StaleQuota_FreshWins(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 60},
				{"id": "weekly", "kind": "general", "percentRemaining": 70}
			]},
			{"provider": "claude", "state": {"status": "stale"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 75},
				{"id": "seven_day", "kind": "general", "percentRemaining": 80}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
		{Harness: "claude"},
	}

	// codex (fresh, min=60) vs claude (stale, min=75). 75 < 60+20, so fresh wins.
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("stale close margin: got %q, want codex (fresh wins)", got.Harness)
	}
}

// TestQuotaSelector_StaleQuota_StaleWinsWithBigMargin verifies that a stale
// candidate wins when its minimum is at least 20 points higher than the best fresh.
func TestQuotaSelector_StaleQuota_StaleWinsWithBigMargin(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 30},
				{"id": "weekly", "kind": "general", "percentRemaining": 40}
			]},
			{"provider": "claude", "state": {"status": "stale"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 75},
				{"id": "seven_day", "kind": "general", "percentRemaining": 80}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
		{Harness: "claude"},
	}

	// codex (fresh, min=30) vs claude (stale, min=75). 75 >= 30+20, so stale wins.
	got := sel.selectCandidate(candidates)
	if got.Harness != "claude" {
		t.Errorf("stale big margin: got %q, want claude (stale wins with margin)", got.Harness)
	}
}

// TestQuotaSelector_StaleQuota_ExactMarginBoundary verifies the boundary:
// stale min of 49 with fresh min of 31 → 49 < 31+20 (51) → fresh wins.
func TestQuotaSelector_StaleQuota_ExactMarginBoundary(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 31}
			]},
			{"provider": "claude", "state": {"status": "stale"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 49}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
		{Harness: "claude"},
	}

	// 49 < 31+20 (51), so fresh wins (below threshold).
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("exact boundary (49 vs 31): got %q, want codex (fresh wins below threshold)", got.Harness)
	}
}

// TestQuotaSelector_DeterministicFallback verifies that firstMatchSelector
// always returns the first candidate (stable across calls).
func TestQuotaSelector_DeterministicFallback(t *testing.T) {
	sel := &firstMatchSelector{}

	// Empty candidates returns empty.
	got := sel.selectCandidate(nil)
	if got.Harness != "" {
		t.Errorf("empty candidates: got %q, want empty", got.Harness)
	}

	candidates := []DispatchCandidate{
		{Harness: "pi", Model: "flash", Effort: "low"},
		{Harness: "claude", Model: "sonnet", Effort: "high"},
		{Harness: "codex", Model: "gpt-5", Effort: "medium"},
	}

	// First call returns first candidate.
	got = sel.selectCandidate(candidates)
	if got.Harness != "pi" || got.Model != "flash" || got.Effort != "low" {
		t.Errorf("deterministic fallback: got %+v, want pi/flash/low", got)
	}

	// Second call returns same result (deterministic).
	got2 := sel.selectCandidate(candidates)
	if got2.Harness != "pi" {
		t.Errorf("deterministic fallback not stable: got %q on second call", got2.Harness)
	}
}

// TestQuotaSelector_ReadyPathTwoAdapters verifies that with two candidate
// adapters and real quota data, the selector picks the one with the higher
// minimum remaining GENERAL quota.
func TestQuotaSelector_ReadyPathTwoAdapters(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 80},
				{"id": "weekly", "kind": "general", "percentRemaining": 90}
			]},
			{"provider": "pi", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 40},
				{"id": "seven_day", "kind": "general", "percentRemaining": 50}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex", Model: "gpt-5.2-codex", Effort: "80"},
		{Harness: "pi", Model: "opencode-go/deepseek-v4-flash", Effort: "medium"},
	}

	// codex min=80, pi min=40 → codex wins.
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("two adapters ready path: got %q, want codex (higher min)", got.Harness)
	}
	if got.Model != "gpt-5.2-codex" {
		t.Errorf("two adapters model: got %q, want gpt-5.2-codex", got.Model)
	}
	if got.Effort != "80" {
		t.Errorf("two adapters effort: got %q, want 80", got.Effort)
	}
}

// TestQuotaSelector_ReadyPathTwoAdapters_EqualScore uses two candidates with
// equal minimum quota, verifying first-index tiebreak.
func TestQuotaSelector_ReadyPathTwoAdapters_EqualScore(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 50},
				{"id": "weekly", "kind": "general", "percentRemaining": 50}
			]},
			{"provider": "claude", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 50},
				{"id": "seven_day", "kind": "general", "percentRemaining": 50}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
		{Harness: "claude"},
	}

	// Both min=50, equal → first index (codex) wins.
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("equal score tiebreak: got %q, want codex (first index)", got.Harness)
	}
}

// TestQuotaSelector_ModelWindowIgnored verifies that model-scoped quota windows
// are excluded from the selection calculation.
func TestQuotaSelector_ModelWindowIgnored(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 30},
				{"id": "model:codex_bengalfox:gpt-5.2-codex", "kind": "model", "percentRemaining": 100}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
	}

	// Only GENERAL five_hour (30%) should be used, even though model window is 100%.
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("model window ignored: got %q, want codex", got.Harness)
	}
}

// TestQuotaSelector_UnknownHarnessSkipped verifies that a harness not in the
// quotaProvider map is skipped, falling through to the next candidate.
func TestQuotaSelector_UnknownHarnessSkipped(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 80}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "unknown-harness"},
		{Harness: "codex"},
	}

	// unknown-harness is skipped, codex is scored and wins.
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("unknown harness skipped: got %q, want codex", got.Harness)
	}
}

// TestFirstMatchSelector_EmptyCandidates verifies empty input handling.
func TestFirstMatchSelector_EmptyCandidates(t *testing.T) {
	sel := &firstMatchSelector{}
	got := sel.selectCandidate(nil)
	if got.Harness != "" {
		t.Errorf("empty: got %q, want empty", got.Harness)
	}
	got = sel.selectCandidate([]DispatchCandidate{})
	if got.Harness != "" {
		t.Errorf("empty slice: got %q, want empty", got.Harness)
	}
}

// TestSelectProfile_UsesSelector verifies that SelectProfile delegates to the
// correct selector based on strategy and falls back to first on unknown strategy.
func TestSelectProfile_UsesSelector(t *testing.T) {
	profiles := []DispatchProfile{
		{Name: "first", Harness: "codex"},
		{Name: "general", Harness: "claude"},
	}

	// Unknown strategy → firstMatchSelector → first profile.
	got := SelectProfile(profiles, "unknown-strategy")
	if got != "codex" {
		t.Errorf("unknown strategy: got %q, want codex", got)
	}

	// Empty strategy → firstMatchSelector → first profile.
	got = SelectProfile(profiles, "")
	if got != "codex" {
		t.Errorf("empty strategy: got %q, want codex", got)
	}
}

// TestQuotaSelector_NoUsableWindows verifies fallback when quota data exists
// but no harness has GENERAL windows.
func TestQuotaSelector_NoUsableWindows(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "model:codex_bengalfox:gpt-5.2-codex", "kind": "model", "percentRemaining": 100}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
	}

	// codex has only model windows, no GENERAL windows → falls back to first.
	got := sel.selectCandidate(candidates)
	if got.Harness != "codex" {
		t.Errorf("no usable windows: got %q, want codex (first candidate)", got.Harness)
	}
}

// TestQuotaSelector_StaleOnly verifies that when all candidates are stale,
// the one with the highest minimum is chosen.
func TestQuotaSelector_StaleOnly(t *testing.T) {
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "stale"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 30},
				{"id": "weekly", "kind": "general", "percentRemaining": 40}
			]},
			{"provider": "pi", "state": {"status": "stale"}, "windows": [
				{"id": "five_hour", "kind": "general", "percentRemaining": 70},
				{"id": "seven_day", "kind": "general", "percentRemaining": 80}
			]}
		]
	}`
	provider := &stubQuotaProvider{output: quotaJSON}
	sel := newQuotaSelectorWithProvider(provider)

	candidates := []DispatchCandidate{
		{Harness: "codex"},
		{Harness: "pi"},
	}

	// Both stale, pi min=70 > codex min=30 → pi wins.
	got := sel.selectCandidate(candidates)
	if got.Harness != "pi" {
		t.Errorf("stale only: got %q, want pi (highest min among stale)", got.Harness)
	}
}
