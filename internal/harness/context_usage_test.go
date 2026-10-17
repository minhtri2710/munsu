package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeSession writes lines as a session file name in dir, modified ago before now.
func writeSession(t *testing.T, dir, name string, ago time.Duration, lines []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-ago)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

// realCwd is a seat cwd given through a symlink, and the symlink-free path
// harnesses key sessions by.
func realCwd(t *testing.T) (cwd, real string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), `wt.x_y:z\w`)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, cwd); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cwd, real
}

// filler is a session line of n bytes that carries no usage.
func filler(n int) string {
	return `{"type":"user","pad":"` + strings.Repeat("x", n) + `"}`
}

func TestContextUsageClaude(t *testing.T) {
	asst := func(in, cc, cr int, side bool) string {
		s := "false"
		if side {
			s = "true"
		}
		return `{"type":"assistant","isSidechain":` + s + `,"message":{"content":[{"type":"text","text":"secret"}],"usage":{"input_tokens":` + strconv.Itoa(in) + `,"cache_creation_input_tokens":` + strconv.Itoa(cc) + `,"cache_read_input_tokens":` + strconv.Itoa(cr) + `,"output_tokens":99}}}`
	}
	boundary := `{"type":"system","subtype":"compact_boundary","isSidechain":false}`
	for _, tc := range []struct {
		name  string
		older []string // an older session file, nil for none
		lines []string // the newest session file, nil for none
		want  int
	}{
		{"normal row sums input and both cache counts", nil, []string{`{"type":"user"}`, asst(2, 300, 9000, false), `{"type":"user","message":{"usage":{"input_tokens":999}}}`}, 9302},
		{"sidechain row ignored", nil, []string{asst(2, 300, 9000, false), asst(1, 1, 1, true)}, 9302},
		{"zero-usage row ignored", nil, []string{asst(2, 300, 9000, false), asst(0, 0, 0, false)}, 9302},
		{"rows before a compaction ignored", nil, []string{asst(0, 0, 90000, false), boundary, asst(5, 0, 0, false)}, 5},
		{"no row after a compaction", nil, []string{asst(0, 0, 90000, false), boundary, `{"type":"user"}`}, 0},
		{"missing file", nil, nil, 0},
		{"newest file wins", []string{asst(0, 0, 777, false)}, []string{asst(0, 0, 42, false)}, 42},
		{"a row beyond the tail window is unknown", nil, []string{asst(0, 0, 42, false), filler(usageTailBytes)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := t.TempDir()
			cwd, real := realCwd(t)
			dir := filepath.Join(cfg, "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(real, "-"))
			if tc.older != nil {
				// The older file sorts last, and a newer non-.jsonl file and a
				// newer *.jsonl directory sit beside them.
				writeSession(t, dir, "z.jsonl", time.Hour, tc.older)
				writeSession(t, dir, "c.txt", -time.Minute, []string{asst(0, 0, 555, false)})
				sub := filepath.Join(dir, "d.jsonl")
				if err := os.Mkdir(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				at := time.Now().Add(time.Minute)
				if err := os.Chtimes(sub, at, at); err != nil {
					t.Fatal(err)
				}
			}
			if tc.lines != nil {
				writeSession(t, dir, "b.jsonl", 0, tc.lines)
			}
			used, window := ContextUsage(Claude, cfg, cwd)
			if used != tc.want || window != 0 {
				t.Fatalf("ContextUsage = %d, %d; want %d, 0", used, window, tc.want)
			}
		})
	}
}

func TestContextUsagePi(t *testing.T) {
	asst := func(stop, usage string) string {
		return `{"type":"message","message":{"role":"assistant","provider":"p","model":"m","stopReason":"` + stop + `","content":[{"type":"text","text":"secret"}],"usage":{` + usage + `}}}`
	}
	// normal is the usage of a real pi 1.2.0 session whose footer read 3.6%/256k.
	normal := asst("stop", `"input":9241,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":9246`)
	models := `{"providers":{"q":{"models":[{"id":"m","contextWindow":1}]},"p":{"models":[{"id":"other","contextWindow":1},{"id":"m","contextWindow":256000}]}}}`
	for _, tc := range []struct {
		name         string
		models       string // models.json, "" for none, "link:<json>" for a symlink to it, "fifo" for a FIFO
		lines        []string
		used, window int
	}{
		{"normal row with a models.json window", models, []string{`{"type":"session"}`, normal}, 9246, 256000},
		{"totalTokens wins over the field sum", models, []string{asst("stop", `"input":1,"output":2,"cacheRead":30,"cacheWrite":400,"totalTokens":500`)}, 500, 256000},
		{"no totalTokens sums input, output and both caches", models, []string{asst("stop", `"input":1,"output":2,"cacheRead":30,"cacheWrite":400`)}, 433, 256000},
		{"aborted and failed replies ignored", models, []string{normal, asst("aborted", `"totalTokens":5`), asst("error", `"totalTokens":6`)}, 9246, 256000},
		{"non-message and non-assistant rows ignored", models, []string{normal,
			`{"type":"branch_summary","message":{"role":"assistant","stopReason":"stop","usage":{"totalTokens":7}}}`,
			`{"type":"message","message":{"role":"toolResult","stopReason":"stop","usage":{"totalTokens":8}}}`}, 9246, 256000},
		{"rows before a compaction ignored", models, []string{asst("stop", `"totalTokens":90000`), `{"type":"compaction"}`, normal}, 9246, 256000},
		{"no row after a compaction", models, []string{normal, `{"type":"compaction"}`}, 0, 0},
		{"missing file", models, nil, 0, 0},
		{"model not in models.json shows tokens only", `{"providers":{"p":{"models":[{"id":"other","contextWindow":1}]}}}`, []string{normal}, 9246, 0},
		{"no models.json shows tokens only", "", []string{normal}, 9246, 0},
		{"the model under another provider only shows tokens only", `{"providers":{"q":{"models":[{"id":"m","contextWindow":1}]}}}`, []string{normal}, 9246, 0},
		{"a symlinked models.json shows tokens only", "link:" + models, []string{normal}, 9246, 0},
		{"a FIFO models.json shows tokens only", "fifo", []string{normal}, 9246, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := t.TempDir()
			cwd, real := realCwd(t)
			path := filepath.Join(agent, "models.json")
			switch {
			case tc.models == "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case strings.HasPrefix(tc.models, "link:"):
				target := filepath.Join(t.TempDir(), "models.json")
				if err := os.WriteFile(target, []byte(strings.TrimPrefix(tc.models, "link:")), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case tc.models != "":
				if err := os.WriteFile(path, []byte(tc.models), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.lines != nil {
				writeSession(t, filepath.Join(agent, "sessions", "--"+regexp.MustCompile(`[/\\:]`).ReplaceAllString(strings.TrimPrefix(real, "/"), "-")+"--"), "s.jsonl", 0, tc.lines)
			}
			used, window := ContextUsage(Pi, agent, cwd)
			if used != tc.used || window != tc.window {
				t.Fatalf("ContextUsage = %d, %d; want %d, %d", used, window, tc.used, tc.window)
			}
		})
	}
}

// Seats munsu does not launch, and a harness with no session format, read nothing.
func TestContextUsageOtherHarnesses(t *testing.T) {
	cwd, _ := realCwd(t)
	for _, h := range []string{Agy, Codex, Opencode, Grok, ""} {
		if used, window := ContextUsage(h, t.TempDir(), cwd); used != 0 || window != 0 {
			t.Errorf("ContextUsage(%q) = %d, %d; want 0, 0", h, used, window)
		}
	}
}
