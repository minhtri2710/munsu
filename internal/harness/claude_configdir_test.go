package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHumanClaudeConfig(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "munsu", "state")
	t.Setenv("HOME", home)
	def := ClaudeConfig{Dir: filepath.Join(home, ".claude"), GlobalConfig: filepath.Join(home, ".claude.json")}
	custom := filepath.Join(home, "claude-work")
	tests := []struct {
		name, env string
		want      ClaudeConfig
	}{
		{"unset uses ~/.claude and ~/.claude.json with the default keychain item", "", def},
		{"an absolute dir holds its own .claude.json and names its keychain item", custom, ClaudeConfig{Dir: custom, GlobalConfig: filepath.Join(custom, ".claude.json"), SecureStorage: custom}},
		{"a relative dir, which claude itself refuses, is ignored", "rel/claude", def},
		{"a dir inside the state dir is another seat's", filepath.Join(state, "T-1."+ClaudeConfigDirSuffix), def},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", tt.env)
			if got, err := HumanClaudeConfig(state); err != nil || got != tt.want {
				t.Fatalf("HumanClaudeConfig = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func claudeConfigFixture(t *testing.T) (ClaudeConfig, string) {
	t.Helper()
	home := t.TempDir()
	c := ClaudeConfig{Dir: filepath.Join(home, ".claude"), GlobalConfig: filepath.Join(home, ".claude.json")}
	for _, d := range []string{"skills", "hooks", "plugins", "projects", "sessions"} {
		if err := os.MkdirAll(filepath.Join(c.Dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		filepath.Join(c.Dir, "settings.json"): `{"hooks":{}}`, filepath.Join(c.Dir, "CLAUDE.md"): "rules", filepath.Join(c.Dir, "history.jsonl"): "{}",
		c.GlobalConfig: `{"hasCompletedOnboarding":true,"oauthAccount":{"a":1},"numStartups":9,"mcpServers":{"x":{}},
			"projects":{"/w/trusted":{"hasTrustDialogAccepted":true,"allowedTools":["Bash"]},"/w/no":{"hasTrustDialogAccepted":false},"/w/unasked":{"lastCost":1}}}`,
	} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(home, "munsu", "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	return c, filepath.Join(state, "T-1."+ClaudeConfigDirSuffix)
}

func TestBuildClaudeConfigDir(t *testing.T) {
	t.Run("config is linked, state is the seat's own, and .claude.json carries only the start keys and trust", func(t *testing.T) {
		c, dst := claudeConfigFixture(t)
		before, _ := os.ReadFile(c.GlobalConfig)
		if err := BuildClaudeConfigDir(c, seatBuildDirOf(dst), dst); err != nil {
			t.Fatal(err)
		}
		var names []string
		entries, _ := os.ReadDir(dst)
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if want := []string{".claude.json", "CLAUDE.md", "hooks", "plugins", "settings.json", "skills"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("entries = %v, want %v (no projects, sessions or history from the Human)", names, want)
		}
		for _, name := range names[1:] {
			if got, err := os.Readlink(filepath.Join(dst, name)); err != nil || got != filepath.Join(c.Dir, name) {
				t.Errorf("%s: readlink = %q, %v; want the Human's entry", name, got, err)
			}
		}
		fi, err := os.Lstat(filepath.Join(dst, ".claude.json"))
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
			t.Fatalf(".claude.json lstat = %v, %v; want a private regular 0600 file", fi, err)
		}
		raw, _ := os.ReadFile(filepath.Join(dst, ".claude.json"))
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"hasCompletedOnboarding": true, "oauthAccount": map[string]any{"a": 1.0},
			"projects": map[string]any{"/w/trusted": map[string]any{"hasTrustDialogAccepted": true}, "/w/no": map[string]any{"hasTrustDialogAccepted": false}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf(".claude.json = %s, want %v", raw, want)
		}
		if after, _ := os.ReadFile(c.GlobalConfig); string(after) != string(before) {
			t.Fatalf("the Human's .claude.json was modified")
		}
		if names := dirNames(t, seatBuildDirOf(dst)); len(names) != 0 {
			t.Fatalf("build left %v behind in the seat build dir", names)
		}
	})
	t.Run("an absent config gives an empty seat dir with no trust", func(t *testing.T) {
		home := t.TempDir()
		dst := filepath.Join(home, "state", "T-1."+ClaudeConfigDirSuffix)
		if err := os.Mkdir(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := BuildClaudeConfigDir(ClaudeConfig{Dir: filepath.Join(home, "none"), GlobalConfig: filepath.Join(home, "none.json")}, seatBuildDirOf(dst), dst); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(dst, ".claude.json"))
		if entries, _ := os.ReadDir(dst); len(entries) != 1 || strings.TrimSpace(string(raw)) != "{\n  \"projects\": {}\n}" {
			t.Fatalf("entries = %v, .claude.json = %s", entries, raw)
		}
	})
	t.Run("a malformed .claude.json installs nothing", func(t *testing.T) {
		for name, body := range map[string]string{"not json": "{", "projects not an object": `{"projects":[]}`} {
			t.Run(name, func(t *testing.T) {
				c, dst := claudeConfigFixture(t)
				if err := os.WriteFile(c.GlobalConfig, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := BuildClaudeConfigDir(c, seatBuildDirOf(dst), dst); err == nil || !strings.Contains(err.Error(), "building claude config dir from") || !strings.Contains(err.Error(), ".claude.json") {
					t.Fatalf("err = %v, want the .claude.json build refusal", err)
				}
				if _, err := os.Lstat(dst); !os.IsNotExist(err) {
					t.Errorf("%s exists after a failed build: %v", dst, err)
				}
				if names := dirNames(t, seatBuildDirOf(dst)); len(names) != 0 {
					t.Errorf("failed build left %v behind in the seat build dir", names)
				}
			})
		}
	})
}
