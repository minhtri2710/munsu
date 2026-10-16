package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// usageTailBytes is how much of a session file's end ContextUsage reads.
// ponytail: a last usage row more than 1 MiB from the end reads as unknown;
// read further back if real sessions show it.
const usageTailBytes = 1 << 20

// ContextUsage reads how many context tokens a launched seat used in its last
// reply, from the newest session file its harness wrote for cwd: dir is the
// seat's claude config dir or pi agent dir. window is the model's context size
// when the harness records it, else 0. used is 0 when unknown: no file, another
// harness, or no reply since the last compaction. Only usage fields are read.
func ContextUsage(harnessName, dir, cwd string) (used, window int) {
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return 0, 0
	}
	switch harnessName {
	case Claude:
		return lastUsage(newestJSONL(filepath.Join(dir, "projects", claudeProjectKey(cwd))), claudeRowUsage), 0
	case Pi:
		// lastUsage stops at the row it returns, so provider and model are that row's.
		var provider, model string
		used = lastUsage(newestJSONL(filepath.Join(dir, "sessions", piSessionKey(cwd))), func(line []byte) (n int, compaction bool) {
			n, compaction, provider, model = piRowUsage(line)
			return n, compaction
		})
		if used == 0 {
			return 0, 0
		}
		return used, piContextWindow(dir, provider, model)
	}
	return 0, 0
}

// claudeProjectKey is claude's projects/ entry for cwd: every character but an
// ASCII letter or digit becomes "-".
// ponytail: claude shortens keys over 200 characters with a hash; such a seat reads as unknown.
func claudeProjectKey(cwd string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
}

// piSessionKey is pi's sessions/ entry for cwd (getDefaultSessionDirPath).
func piSessionKey(cwd string) string {
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(strings.TrimPrefix(cwd, "/")) + "--"
}

// claudeRowUsage is the context a main-thread assistant row reports, and
// whether the row is a compaction boundary.
func claudeRowUsage(line []byte) (int, bool) {
	var r struct {
		Type        string `json:"type"`
		Subtype     string `json:"subtype"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Usage struct {
				Input         int `json:"input_tokens"`
				CacheCreation int `json:"cache_creation_input_tokens"`
				CacheRead     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &r) != nil {
		return 0, false
	}
	if r.Subtype == "compact_boundary" {
		return 0, true
	}
	if r.Type != "assistant" || r.IsSidechain {
		return 0, false
	}
	u := r.Message.Usage
	return u.Input + u.CacheCreation + u.CacheRead, false
}

// piRowUsage is the context an assistant message reports, as pi's own
// calculateContextTokens counts it, and whether the row is a compaction. Like
// pi, an aborted or failed reply reports none.
func piRowUsage(line []byte) (used int, compaction bool, provider, model string) {
	var r struct {
		Type    string `json:"type"`
		Message struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
			Provider   string `json:"provider"`
			Model      string `json:"model"`
			Usage      struct {
				Input       int `json:"input"`
				Output      int `json:"output"`
				CacheRead   int `json:"cacheRead"`
				CacheWrite  int `json:"cacheWrite"`
				TotalTokens int `json:"totalTokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &r) != nil {
		return 0, false, "", ""
	}
	m := r.Message
	switch {
	case r.Type == "compaction":
		return 0, true, "", ""
	case r.Type != "message" || m.Role != "assistant" || m.StopReason == "aborted" || m.StopReason == "error":
		return 0, false, "", ""
	}
	u := m.Usage
	used = u.TotalTokens
	if used == 0 {
		used = u.Input + u.Output + u.CacheRead + u.CacheWrite
	}
	return used, false, m.Provider, m.Model
}

// piContextWindow is the model's contextWindow in the agent dir's models.json, 0 when absent.
func piContextWindow(dir, provider, model string) int {
	raw, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		return 0
	}
	var doc struct {
		Providers map[string]struct {
			Models []struct {
				ID            string `json:"id"`
				ContextWindow int    `json:"contextWindow"`
			} `json:"models"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return 0
	}
	for _, m := range doc.Providers[provider].Models {
		if m.ID == model {
			return m.ContextWindow
		}
	}
	return 0
}

// lastUsage scans the tail of path from its end and returns the first nonzero
// usage row reports, or 0 when a compaction comes first.
func lastUsage(path string, row func(line []byte) (used int, compaction bool)) int {
	lines := tailLines(path)
	for i := len(lines) - 1; i >= 0; i-- {
		used, compaction := row(lines[i])
		if compaction {
			return 0
		}
		if used > 0 {
			return used
		}
	}
	return 0
}

// tailLines is the lines in the last usageTailBytes of path. A line the
// window cuts is not a JSON value, so the row decoders skip it.
func tailLines(path string) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	off := max(0, info.Size()-usageTailBytes)
	buf := make([]byte, info.Size()-off)
	n, _ := f.ReadAt(buf, off)
	return bytes.Split(buf[:n], []byte("\n"))
}

// newestJSONL is the most recently modified regular *.jsonl directly in dir, "" when none.
func newestJSONL(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var at time.Time
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(at) {
			best, at = e.Name(), info.ModTime()
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, best)
}
