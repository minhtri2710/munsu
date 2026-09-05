package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

func writeAllowlist(t *testing.T, homeDir, content string) {
	t.Helper()
	if err := os.MkdirAll(config.ConfigDir(homeDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ModelAllowlistPath(homeDir), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestModelAllowlistPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	got := ModelAllowlistPath(home)
	want := filepath.Join(home, "config", ModelAllowlistKey)
	if got != want {
		t.Fatalf("ModelAllowlistPath = %q, want %q", got, want)
	}
}

func TestPolicyHome(t *testing.T) {
	t.Run("no parent-home is own root", func(t *testing.T) {
		home := t.TempDir()
		if got := PolicyHome(home); got != home {
			t.Fatalf("PolicyHome = %q, want %q", got, home)
		}
	})
	t.Run("captain home defers to parent", func(t *testing.T) {
		parent := t.TempDir()
		captain := filepath.Join(t.TempDir(), "captains", "alpha")
		if err := config.Set(captain, "parent-home", parent); err != nil {
			t.Fatal(err)
		}
		if got := PolicyHome(captain); got != parent {
			t.Fatalf("PolicyHome = %q, want parent %q", got, parent)
		}
	})
}

func TestModelAllowlistPresent(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		ok, err := ModelAllowlistPresent(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("absent policy should not be present")
		}
	})
	t.Run("file present", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:sonnet\n")
		ok, err := ModelAllowlistPresent(home)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("file policy should be present")
		}
	})
	t.Run("absent file is absent", func(t *testing.T) {
		home := t.TempDir()
		// No allowlist file: the policy must be absent.
		ok, err := ModelAllowlistPresent(home)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("absent policy file must report absent")
		}
	})
	t.Run("captain home sees parent policy", func(t *testing.T) {
		parent := t.TempDir()
		writeAllowlist(t, parent, "pi:sonnet\n")
		captain := filepath.Join(t.TempDir(), "captains", "alpha")
		if err := config.Set(captain, "parent-home", parent); err != nil {
			t.Fatal(err)
		}
		ok, err := ModelAllowlistPresent(captain)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("captain home should see parent policy")
		}
	})
}

func TestLoadModelAllowlist(t *testing.T) {
	t.Run("absent policy", func(t *testing.T) {
		allowed, present, err := LoadModelAllowlist(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if present {
			t.Fatal("absent policy must not be present")
		}
		if len(allowed) != 0 {
			t.Fatalf("allowed = %v, want empty", allowed)
		}
	})
	t.Run("parses identities, ignores blanks and comments", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "# fleet policy\n\npi:opencode-go/deepseek-v4-flash\n  codex:gpt-5.2-codex  \n")
		allowed, present, err := LoadModelAllowlist(home)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			t.Fatal("policy should be present")
		}
		if !allowed["pi:opencode-go/deepseek-v4-flash"] {
			t.Error("missing pi identity")
		}
		if !allowed["codex:gpt-5.2-codex"] {
			t.Error("missing codex identity (whitespace should be trimmed)")
		}
	})
	t.Run("uppercase policy identities canonicalized at parse", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "PI:sonnet\nCodex:gpt-5.2-codex\n")
		allowed, present, err := LoadModelAllowlist(home)
		if err != nil {
			t.Fatalf("uppercase identities should canonicalize to registry case, got: %v", err)
		}
		if !present || !allowed["pi:sonnet"] || !allowed["codex:gpt-5.2-codex"] {
			t.Fatalf("expected canonicalized identities, got %v (present=%v)", allowed, present)
		}
	})
	t.Run("model may contain colons", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:provider/model:variant\n")
		allowed, _, err := LoadModelAllowlist(home)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed["pi:provider/model:variant"] {
			t.Fatal("identity with colons in model should parse")
		}
	})
	t.Run("empty policy fails closed", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "# only a comment\n\n")
		_, present, err := LoadModelAllowlist(home)
		if err == nil {
			t.Fatal("empty policy must fail closed")
		}
		if !present {
			t.Fatal("present empty policy must report present")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error should mention empty policy, got: %v", err)
		}
	})
	t.Run("malformed entries fail closed", func(t *testing.T) {
		for _, content := range []string{
			"not-an-identity",
			":missing-harness",
			"pi:",
			"unknown-harness:some-model",
			"pi:sonnet\nbroken",
		} {
			home := t.TempDir()
			writeAllowlist(t, home, content)
			_, present, err := LoadModelAllowlist(home)
			if err == nil {
				t.Errorf("policy %q must fail closed", content)
			}
			if !present {
				t.Errorf("malformed policy %q must report present", content)
			}
		}
	})
}

func TestValidateModelAllowlist(t *testing.T) {
	if err := ValidateModelAllowlist("pi:sonnet\ncodex:gpt-5.2-codex\n"); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	if err := ValidateModelAllowlist(""); err != nil {
		t.Fatalf("empty deny-all policy should be writable: %v", err)
	}
	if err := ValidateModelAllowlist("# comment\n\n"); err != nil {
		t.Fatalf("comment-only policy should be writable: %v", err)
	}
	if err := ValidateModelAllowlist("garbage-line"); err == nil {
		t.Fatal("malformed policy should be rejected at write time")
	}
}

func TestCanonicalHarness(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{name: "lowercase canonical", input: "pi", want: "pi", ok: true},
		{name: "uppercase canonicalized", input: "PI", want: "pi", ok: true},
		{name: "mixed case canonicalized", input: "Codex", want: "codex", ok: true},
		{name: "whitespace trimmed", input: "  pi  ", want: "pi", ok: true},
		{name: "default is unset", input: "default", want: "", ok: false},
		{name: "empty is unset", input: "", want: "", ok: false},
		{name: "alias/prefix rejected", input: "pi-codex", want: "", ok: false},
		{name: "process-style name rejected", input: "claude code", want: "", ok: false},
		{name: "unknown rejected", input: "copilot", want: "", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CanonicalHarness(tc.input)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("CanonicalHarness(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCheckModelAllowed(t *testing.T) {
	t.Run("absent policy preserves compatibility", func(t *testing.T) {
		if err := CheckModelAllowed(t.TempDir(), Pi, "any-model"); err != nil {
			t.Fatalf("absent policy must not enforce: %v", err)
		}
	})
	t.Run("allowed identity passes", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:opencode-go/deepseek-v4-flash\n")
		if err := CheckModelAllowed(home, Pi, "opencode-go/deepseek-v4-flash"); err != nil {
			t.Fatalf("allowed identity rejected: %v", err)
		}
	})
	t.Run("denied identity fails closed with allowed values and correction", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:opencode-go/deepseek-v4-flash\n")
		err := CheckModelAllowed(home, Pi, "claude-sonnet-4-20250515")
		if err == nil {
			t.Fatal("denied identity must fail closed")
		}
		msg := err.Error()
		if !strings.Contains(msg, "claude-sonnet-4-20250515") {
			t.Errorf("error must name the denied model, got: %v", err)
		}
		if !strings.Contains(msg, "pi:opencode-go/deepseek-v4-flash") {
			t.Errorf("error must list allowed values, got: %v", err)
		}
		if !strings.Contains(msg, "munsu config set "+ModelAllowlistKey) {
			t.Errorf("error must include correction, got: %v", err)
		}
	})
	t.Run("empty identity with policy present fails closed", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:sonnet\n")
		if err := CheckModelAllowed(home, "", "sonnet"); err == nil {
			t.Fatal("empty harness under an active policy must fail closed")
		}
		if err := CheckModelAllowed(home, Pi, ""); err == nil {
			t.Fatal("empty model under an active policy must fail closed (a runtime default cannot bypass the policy)")
		}
		if err := CheckModelAllowed(home, "", ""); err == nil {
			t.Fatal("fully unresolved identity under an active policy must fail closed")
		}
	})
	t.Run("empty identity without policy stays compatible", func(t *testing.T) {
		if err := CheckModelAllowed(t.TempDir(), Pi, ""); err != nil {
			t.Fatalf("absent policy with empty model must stay compatible: %v", err)
		}
		if err := CheckModelAllowed(t.TempDir(), "", ""); err != nil {
			t.Fatalf("absent policy with empty identity must stay compatible: %v", err)
		}
	})
	t.Run("harness identity checked case-insensitively", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:sonnet\n")
		for _, h := range []string{"pi", "Pi", "PI"} {
			if err := CheckModelAllowed(home, h, "sonnet"); err != nil {
				t.Fatalf("harness %q should canonicalize to pi: %v", h, err)
			}
		}
	})
	t.Run("non-canonical harness with policy fails closed", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "pi:sonnet\n")
		if err := CheckModelAllowed(home, "pi-codex", "sonnet"); err == nil {
			t.Fatal("non-canonical harness under an active policy must fail closed")
		}
	})
	t.Run("denied in general policy applies to captain home", func(t *testing.T) {
		parent := t.TempDir()
		writeAllowlist(t, parent, "codex:gpt-5.2-codex\n")
		captain := filepath.Join(t.TempDir(), "captains", "alpha")
		if err := config.Set(captain, "parent-home", parent); err != nil {
			t.Fatal(err)
		}
		if err := CheckModelAllowed(captain, Pi, "claude-sonnet-4-20250515"); err == nil {
			t.Fatal("captain-context identity must be checked against the general policy")
		}
		if err := CheckModelAllowed(captain, Codex, "gpt-5.2-codex"); err != nil {
			t.Fatalf("allowed general identity rejected for captain context: %v", err)
		}
	})
	t.Run("empty policy fails closed at check", func(t *testing.T) {
		home := t.TempDir()
		writeAllowlist(t, home, "\n")
		if err := CheckModelAllowed(home, Pi, "sonnet"); err == nil {
			t.Fatal("empty policy must fail closed at check")
		}
	})
}
