package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPiProjectSettingsBlocksOnlyTheOrchestrationSkill(t *testing.T) {
	var got map[string][]string
	if err := json.Unmarshal(PiProjectSettings(), &got); err != nil {
		t.Fatalf("PiProjectSettings is not JSON: %v", err)
	}
	if want := map[string][]string{"skills": {"!" + OrchestrationSkill}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PiProjectSettings = %v, want %v", got, want)
	}
	if OrchestrationSkill != "munsu-ops" {
		t.Fatalf("OrchestrationSkill = %q, want the installed skill name munsu-ops", OrchestrationSkill)
	}
}

func TestHumanPiAgentDir(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "munsu", "state")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	def := filepath.Join(home, ".pi", "agent")
	tests := []struct {
		name, env, want string
	}{
		{"unset uses ~/.pi/agent", "", def},
		{"absolute env wins", "/opt/pi-agent", "/opt/pi-agent"},
		{"tilde env expands", "~/custom-agent", filepath.Join(home, "custom-agent")},
		{"relative env resolves against home", "rel/agent", filepath.Join(home, "rel", "agent")},
		{"env inside the state dir is another task's generated dir", filepath.Join(state, "T-1.pi-agent"), def},
		{"env equal to the state dir is ignored", state, def},
		{"env that merely shares the state prefix is kept", state + "-other", state + "-other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PI_CODING_AGENT_DIR", tt.env)
			got, err := HumanPiAgentDir(state)
			if err != nil || got != tt.want {
				t.Fatalf("HumanPiAgentDir = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestPiEntry(t *testing.T) {
	const home, src, dst = "/home/h", "/home/h/.pi/agent", "/state/T.pi-agent"
	tests := []struct{ name, in, want string }{
		{"relative inside src is kept", "extensions/a.ts", "extensions/a.ts"},
		{"relative escaping src becomes absolute against src", "../shared/a.ts", "/home/h/.pi/shared/a.ts"},
		{"absolute inside src is rooted at dst", src + "/ext/a.ts", dst + "/ext/a.ts"},
		{"absolute outside src is unchanged", "/opt/ext.ts", "/opt/ext.ts"},
		{"tilde plain path expands then roots at dst", "~/.pi/agent/skills/s", dst + "/skills/s"},
		{"file url expands", "file:///opt/e.ts", "/opt/e.ts"},
		{"glob exclude keeps its prefix and stays relative", "!*.test.ts", "!*.test.ts"},
		{"glob exclude escaping src is made absolute", "!../x/*.ts", "!/home/h/.pi/x/*.ts"},
		{"absolute glob inside src is rooted at dst", "!" + src + "/skills/*", "!" + dst + "/skills/*"},
		{"exact include inside src is rooted at dst", "+" + src + "/skills/foo", "+" + dst + "/skills/foo"},
		{"prefixed entries are not tilde-expanded", "-~/x", "-~/x"},
		{"builtin is unchanged", "builtin:foo", "builtin:foo"},
		{"builtin that looks like an escaping path is unchanged", "builtin:a/../../x", "builtin:a/../../x"},
		{"empty entry is unchanged", "", ""},
		{"minus prefix is kept and the rest rewritten", "-../x", "-/home/h/.pi/x"},
		{"glob with a tilde is not expanded", "~/*.ts", "~/*.ts"},
		{"the agent dir's parent is outside src", "..", "/home/h/.pi"},
		{"a dot-dot-prefixed name inside src is kept", "..hidden/a.ts", "..hidden/a.ts"},
		{"surrounding space is trimmed from a plain path", " /opt/ext.ts ", "/opt/ext.ts"},
		{"excluded builtin is unchanged", "!builtin:foo", "!builtin:foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := piEntry(tt.in, home, src, dst); got != tt.want {
				t.Fatalf("piEntry(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPiSourceLeavesRemoteSourcesAlone(t *testing.T) {
	const home, base = "/home/h", "/home/h/.pi/agent"
	for in, want := range map[string]string{
		"npm:pkg@1":        "npm:pkg@1",
		"git:host/r":       "git:host/r",
		"github:o/r":       "github:o/r",
		"https://x/y":      "https://x/y",
		"http://x/y":       "http://x/y",
		"~":                "/home/h",
		"/abs/./d/../pkg":  "/abs/pkg",
		"ssh:git@h:r":      "ssh:git@h:r",
		"./local/pkg":      "/home/h/.pi/agent/local/pkg",
		"../sibling":       "/home/h/.pi/sibling",
		"~/pkgs/p":         "/home/h/pkgs/p",
		"/abs/pkg":         "/abs/pkg",
		"file:///abs/pkg2": "/abs/pkg2",
	} {
		if got := piSource(in, home, base); got != want {
			t.Errorf("piSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPiSettingsCopy(t *testing.T) {
	const home, src, dst = "/home/h", "/home/h/.pi/agent", "/state/T.pi-agent"
	decode := func(t *testing.T, raw string) map[string]any {
		t.Helper()
		out, err := piSettingsCopy([]byte(raw), src, dst, home)
		if err != nil {
			t.Fatalf("piSettingsCopy: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, out)
		}
		return m
	}
	t.Run("absent settings hold only the block", func(t *testing.T) {
		out, err := piSettingsCopy(nil, src, dst, home)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string][]string
		if err := json.Unmarshal(out, &m); err != nil || !reflect.DeepEqual(m, map[string][]string{"skills": {"!" + OrchestrationSkill}}) {
			t.Fatalf("got %s, %v", out, err)
		}
	})
	t.Run("the block is appended last and other keys survive", func(t *testing.T) {
		m := decode(t, `{"theme":"dark","skills":["a","!b"]}`)
		if m["theme"] != "dark" || !reflect.DeepEqual(m["skills"], []any{"a", "!b", "!" + OrchestrationSkill}) {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("a force-include of the orchestration skill is dropped", func(t *testing.T) {
		m := decode(t, `{"skills":["+`+OrchestrationSkill+`","+/x/`+OrchestrationSkill+`","+other","-`+OrchestrationSkill+`"]}`)
		if !reflect.DeepEqual(m["skills"], []any{"+other", "-" + OrchestrationSkill, "!" + OrchestrationSkill}) {
			t.Fatalf("skills = %v", m["skills"])
		}
	})
	t.Run("non-string resource entries are dropped", func(t *testing.T) {
		m := decode(t, `{"extensions":["e.ts",7,{"a":1}],"prompts":[null]}`)
		if !reflect.DeepEqual(m["extensions"], []any{"e.ts"}) || !reflect.DeepEqual(m["prompts"], []any{}) {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("entries of every resource key are rewritten", func(t *testing.T) {
		m := decode(t, `{"extensions":["../x.ts"],"prompts":["`+src+`/p"],"themes":["../t","/opt/t"]}`)
		if !reflect.DeepEqual(m["extensions"], []any{"/home/h/.pi/x.ts"}) ||
			!reflect.DeepEqual(m["prompts"], []any{dst + "/p"}) ||
			!reflect.DeepEqual(m["themes"], []any{"/home/h/.pi/t", "/opt/t"}) {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("a force-include of the orchestration skill in another resource key is kept", func(t *testing.T) {
		m := decode(t, `{"extensions":["+`+OrchestrationSkill+`"]}`)
		if !reflect.DeepEqual(m["extensions"], []any{"+" + OrchestrationSkill}) {
			t.Fatalf("extensions = %v", m["extensions"])
		}
	})
	t.Run("package sources, string and object, resolve against src", func(t *testing.T) {
		m := decode(t, `{"packages":["./local","npm:p",{"source":"../o","x":1},{"noSource":true},3]}`)
		pk := m["packages"].([]any)
		if pk[0] != "/home/h/.pi/agent/local" || pk[1] != "npm:p" || pk[4] != float64(3) {
			t.Fatalf("packages = %v", pk)
		}
		if o := pk[2].(map[string]any); o["source"] != "/home/h/.pi/o" || o["x"] != float64(1) {
			t.Fatalf("object package = %v", o)
		}
	})
	t.Run("malformed settings refuse", func(t *testing.T) {
		if _, err := piSettingsCopy([]byte(`{"skills":`), src, dst, home); err == nil || !strings.Contains(err.Error(), "settings.json") {
			t.Fatalf("err = %v, want a settings.json error", err)
		}
	})
}

func agentDirFixture(t *testing.T) (home, src, dst string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	src = filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(filepath.Join(src, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"auth.json": `{"k":"v"}`, "models.json": `{}`} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(home, "munsu", "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, src, filepath.Join(state, "T-1.pi-agent")
}

func TestBuildPiAgentDir(t *testing.T) {
	t.Run("every entry is the Human's symlink except a private settings copy", func(t *testing.T) {
		_, src, dst := agentDirFixture(t)
		if err := os.WriteFile(filepath.Join(src, "settings.json"), []byte(`{"skills":["keep"]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := BuildPiAgentDir(src, dst); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"auth.json", "models.json", "extensions"} {
			if got, err := os.Readlink(filepath.Join(dst, name)); err != nil || got != filepath.Join(src, name) {
				t.Errorf("%s: readlink = %q, %v; want %q", name, got, err, filepath.Join(src, name))
			}
		}
		fi, err := os.Lstat(filepath.Join(dst, "settings.json"))
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
			t.Fatalf("settings.json lstat = %v, %v; want a private regular 0600 file", fi, err)
		}
		if runtime.GOOS != "windows" {
			if fi, err := os.Stat(dst); err != nil || fi.Mode().Perm() != 0o700 {
				t.Fatalf("agent dir stat = %v, %v; want a private 0700 dir", fi, err)
			}
		}
		raw, _ := os.ReadFile(filepath.Join(dst, "settings.json"))
		if !strings.Contains(string(raw), `"keep"`) || !strings.Contains(string(raw), "!"+OrchestrationSkill) {
			t.Fatalf("settings copy = %s", raw)
		}
		if orig, _ := os.ReadFile(filepath.Join(src, "settings.json")); string(orig) != `{"skills":["keep"]}` {
			t.Fatalf("the Human's settings.json was modified: %s", orig)
		}
		if _, err := os.Lstat(dst + ".building"); !os.IsNotExist(err) {
			t.Fatalf("building dir left behind: %v", err)
		}
	})
	t.Run("an absent agent dir still gets the settings block", func(t *testing.T) {
		home, _, dst := agentDirFixture(t)
		if err := BuildPiAgentDir(filepath.Join(home, "no-such-agent"), dst); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(dst)
		if len(entries) != 1 || entries[0].Name() != "settings.json" {
			t.Fatalf("entries = %v, want only settings.json", entries)
		}
	})
	t.Run("an existing dir is kept untouched", func(t *testing.T) {
		_, src, dst := agentDirFixture(t)
		if err := os.MkdirAll(dst, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(dst, "live")
		if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := BuildPiAgentDir(src, dst); err != nil {
			t.Fatal(err)
		}
		if entries, _ := os.ReadDir(dst); len(entries) != 1 || entries[0].Name() != "live" {
			t.Fatalf("existing dir was rebuilt: %v", entries)
		}
	})
	t.Run("a stale building dir from a crashed build is cleared", func(t *testing.T) {
		_, src, dst := agentDirFixture(t)
		if err := os.MkdirAll(dst+".building", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst+".building", "stale"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := BuildPiAgentDir(src, dst); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(dst, "stale")); !os.IsNotExist(err) {
			t.Fatalf("stale entry survived into the installed dir: %v", err)
		}
	})
	t.Run("malformed settings install nothing", func(t *testing.T) {
		_, src, dst := agentDirFixture(t)
		if err := os.WriteFile(filepath.Join(src, "settings.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := BuildPiAgentDir(src, dst)
		if err == nil || !strings.Contains(err.Error(), "settings.json") {
			t.Fatalf("err = %v, want a settings.json refusal", err)
		}
		for _, p := range []string{dst, dst + ".building"} {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Errorf("%s exists after a failed build: %v", p, err)
			}
		}
	})
	t.Run("an unreadable destination parent refuses", func(t *testing.T) {
		_, src, dst := agentDirFixture(t)
		blocker := filepath.Join(filepath.Dir(dst), "file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := BuildPiAgentDir(src, filepath.Join(blocker, "T.pi-agent")); err == nil || !strings.Contains(err.Error(), "checking pi agent dir") {
			t.Fatalf("BuildPiAgentDir under a regular file = %v, want the checking refusal", err)
		}
	})
	t.Run("an agent dir that is not a directory refuses and installs nothing", func(t *testing.T) {
		home, _, dst := agentDirFixture(t)
		notDir := filepath.Join(home, "agent-file")
		if err := os.WriteFile(notDir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := BuildPiAgentDir(notDir, dst); err == nil || !strings.Contains(err.Error(), "building pi agent dir from") {
			t.Fatalf("err = %v, want the build refusal", err)
		}
		if _, err := os.Lstat(dst); !os.IsNotExist(err) {
			t.Fatalf("dst exists after a failed build: %v", err)
		}
	})
	t.Run("an unreadable settings.json refuses and installs nothing", func(t *testing.T) {
		_, src, dst := agentDirFixture(t)
		if err := os.Mkdir(filepath.Join(src, "settings.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := BuildPiAgentDir(src, dst); err == nil || !strings.Contains(err.Error(), "building pi agent dir from") {
			t.Fatalf("err = %v, want the build refusal", err)
		}
		if _, err := os.Lstat(dst); !os.IsNotExist(err) {
			t.Fatalf("dst exists after a failed build: %v", err)
		}
	})
}
