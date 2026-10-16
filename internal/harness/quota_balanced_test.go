package harness

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func quotaReport(providers ...string) string {
	return `{"providers":[` + strings.Join(providers, ",") + `]}`
}

func quotaEntry(provider, status string, windows ...string) string {
	return `{"provider":"` + provider + `","state":{"status":"` + status + `"},"windows":[` + strings.Join(windows, ",") + `]}`
}

func quotaWindow(id, kind, percent string) string {
	return `{"id":"` + id + `","kind":"` + kind + `","percentRemaining":` + percent + `}`
}

func TestPickByQuota(t *testing.T) {
	codexClaude := []DispatchCandidate{{Harness: "codex"}, {Harness: "claude"}}
	for _, tc := range []struct {
		name       string
		candidates []DispatchCandidate
		report     string
		want       string
		wantErr    string
	}{
		{"higher minimum wins", []DispatchCandidate{{Harness: "codex", Model: "gpt-5.2-codex"}, {Harness: "pi"}}, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "80"), quotaWindow("weekly", "general", "90")),
			quotaEntry("pi", "fresh", quotaWindow("five_hour", "general", "40"), quotaWindow("seven_day", "general", "50")),
		), "codex", ""},
		{"equal minimum takes the earlier candidate", codexClaude, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "50")),
			quotaEntry("claude", "fresh", quotaWindow("five_hour", "general", "50")),
		), "codex", ""},
		{"fresh beats a stale lead under the margin", codexClaude, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "31")),
			quotaEntry("claude", "stale", quotaWindow("five_hour", "general", "50")),
		), "codex", ""},
		{"stale beats fresh at the margin", codexClaude, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "30")),
			quotaEntry("claude", "stale", quotaWindow("five_hour", "general", "50")),
		), "claude", ""},
		{"stale only picks the higher minimum", []DispatchCandidate{{Harness: "codex"}, {Harness: "pi"}}, quotaReport(
			quotaEntry("codex", "stale", quotaWindow("five_hour", "general", "30")),
			quotaEntry("pi", "stale", quotaWindow("five_hour", "general", "70")),
		), "pi", ""},
		{"model windows are ignored", codexClaude, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "30"), quotaWindow("model:fable", "model", "100")),
			quotaEntry("claude", "fresh", quotaWindow("five_hour", "general", "60")),
		), "claude", ""},
		{"a harness quota-axi does not know is skipped", []DispatchCandidate{{Harness: "unknown"}, {Harness: "codex"}}, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "80")),
		), "codex", ""},
		{"unparseable output is refused", codexClaude, "{invalid", "", "quota-axi output"},
		{"no providers is refused", codexClaude, quotaReport(), "", "no general quota window for any candidate"},
		{"only model windows is refused", codexClaude, quotaReport(
			quotaEntry("codex", "fresh", quotaWindow("model:fable", "model", "100")),
		), "", "no general quota window for any candidate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickByQuota(tc.candidates, []byte(tc.report))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("pickByQuota = %+v, %v; want error %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got.Harness != tc.want {
				t.Fatalf("pickByQuota = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// TestResolveDispatchSelectionQuotaTool pins the quota port at dispatch: a
// configured quota-axi orders the use candidates and its failure is returned,
// never answered with the first candidate; with no quota tool the first is taken.
func TestResolveDispatchSelectionQuotaTool(t *testing.T) {
	report := quotaReport(
		quotaEntry("codex", "fresh", quotaWindow("five_hour", "general", "10")),
		quotaEntry("pi", "fresh", quotaWindow("five_hour", "general", "90")),
	)
	for _, tc := range []struct {
		name    string
		script  string
		want    string
		wantErr string
	}{
		{name: "no quota tool takes the first candidate", want: "codex"},
		{name: "quota-axi orders the candidates", script: "#!/bin/sh\n[ \"$1\" = --json ] || exit 2\ncat <<'EOF'\n" + report + "\nEOF\n", want: "pi"},
		{name: "quota-axi failure is refused", script: "#!/bin/sh\nexit 1\n", wantErr: "quota-axi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &DispatchConfig{Profiles: []DispatchProfile{{
				Name: "balanced", Match: []string{"*"},
				Use: []DispatchCandidate{{Harness: "codex", Model: "c"}, {Harness: "pi", Model: "p"}},
			}}}
			if tc.script != "" {
				cfg.QuotaTool = testutil.WriteFakeExecutable(t, filepath.Join(t.TempDir(), "quota-axi"), tc.script)
			}
			got, err := ResolveDispatchSelection(cfg, "any task")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || got.Harness != "" {
					t.Fatalf("selection = %+v, %v; want refusal %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got.Harness != tc.want {
				t.Fatalf("selection = %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}
