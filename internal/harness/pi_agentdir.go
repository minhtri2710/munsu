package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OrchestrationSkill is the munsu skill (`munsu init --skill` installs it) that
// a soldier must never load; every other skill of the Human loads as usual.
const OrchestrationSkill = "munsu-ops"

// PiProjectSettingsRelPath is the worktree file whose skills filter blocks
// OrchestrationSkill among the project skills pi loads from every ancestor
// .agents/skills of the cwd; pi filters those only by project settings.
const PiProjectSettingsRelPath = ".pi/settings.json"

// PiAgentDirSuffix names the per-task agent dir under the home state dir
// (<durable key>.<suffix>); retirement removes it with the other state artifacts.
const PiAgentDirSuffix = "pi-agent"

// piSkillBlock is the skills-array entry that excludes OrchestrationSkill by
// directory name wherever pi discovers it.
const piSkillBlock = "!" + OrchestrationSkill

// PiProjectSettings returns the exact content of PiProjectSettingsRelPath.
func PiProjectSettings() []byte {
	return []byte("{\n  \"skills\": [\"" + piSkillBlock + "\"]\n}\n")
}

// HumanPiAgentDir returns the agent dir the Human's pi uses: PI_CODING_AGENT_DIR,
// else ~/.pi/agent. A PI_CODING_AGENT_DIR inside stateDir (a launch started from
// inside a soldier inherits the soldier's generated dir) is ignored, so a
// per-task dir is never built from another.
func HumanPiAgentDir(stateDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving the Human's pi agent dir: %w", err)
	}
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		d = piPath(d, home, home)
		if _, ok := within(stateDir, d); !ok {
			return d, nil
		}
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// BuildPiAgentDir builds the per-task pi agent dir dst from the Human's agent
// dir src. Every entry is a symlink to src's entry (auth, models, npm packages,
// extensions, skills, prompts and themes resolve as the Human's do) except
// settings.json, a private copy whose skills array blocks OrchestrationSkill.
// Relative local paths in the copy are rewritten absolute against src, because
// pi resolves them from the agent dir. An existing dst is kept: it may belong to
// a live soldier, and dst is only ever renamed into place complete.
func BuildPiAgentDir(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking pi agent dir %s: %w", dst, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("building pi agent dir: %w", err)
	}
	tmp := dst + ".building"
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("clearing %s: %w", tmp, err)
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return fmt.Errorf("building pi agent dir: %w", err)
	}
	if err := fillPiAgentDir(src, tmp, dst, home); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("building pi agent dir from %s: %w", src, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("installing pi agent dir %s: %w", dst, err)
	}
	return nil
}

func fillPiAgentDir(src, dir, dst, home string) error {
	entries, err := os.ReadDir(src)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.Name() == "settings.json" {
			continue
		}
		if err := os.Symlink(filepath.Join(src, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(filepath.Join(src, "settings.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := piSettingsCopy(raw, src, dst, home)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), out, 0o600)
}

// piSettingsCopy returns the Human's settings (raw, nil when absent) with the
// skill block added and paths made to mean the same from the per-task dir dst.
func piSettingsCopy(raw []byte, src, dst, home string) ([]byte, error) {
	s := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("settings.json: %w", err)
		}
	}
	if list, ok := s["packages"].([]any); ok {
		for i, x := range list {
			switch p := x.(type) {
			case string:
				list[i] = piSource(p, home, src)
			case map[string]any:
				if source, ok := p["source"].(string); ok {
					p["source"] = piSource(source, home, src)
				}
			}
		}
	}
	for _, key := range []string{"extensions", "skills", "prompts", "themes"} {
		list, ok := s[key].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(list)+1)
		for _, x := range list {
			p, ok := x.(string)
			if !ok {
				continue // pi reads only strings here
			}
			if key == "skills" && strings.HasPrefix(p, "+") && strings.Contains(p, OrchestrationSkill) {
				continue // a force-include would re-enable the blocked skill
			}
			kept = append(kept, piEntry(p, home, src, dst))
		}
		s[key] = kept
	}
	skills, _ := s["skills"].([]any)
	s["skills"] = append(skills, piSkillBlock)
	return json.MarshalIndent(s, "", "  ")
}

// piEntry rewrites one entry of a resource array (extensions, skills, prompts,
// themes) so it selects the same files from the per-task dir dst as it did
// from the Human's dir src.
//
// pi's grammar is prefix-only ("!" glob exclude, "+" exact include, "-" exact
// exclude; package-manager.js getOverridePatterns); an entry with no prefix and
// no glob character is a plain path resolved from the agent dir. Plain paths
// and patterns are matched against the file's path relative to the agent dir,
// its basename and its absolute path (matchesAnyPattern, matchesAnyExactPattern),
// and pi never resolves symlinks. So:
//   - a relative entry that stays inside src is kept: the symlinks give the
//     same relative paths from dst;
//   - an absolute entry inside src is rooted at dst, so its files still have
//     the same relative paths and its absolute patterns still match;
//   - a relative entry that escapes src becomes absolute against src, because
//     the same ".." would land elsewhere from dst;
//   - anything else (absolute outside src, builtin:) is unchanged.
func piEntry(e, home, src, dst string) string {
	prefix := ""
	if e != "" && strings.ContainsRune("!+-", rune(e[0])) {
		prefix, e = e[:1], e[1:]
	}
	if strings.HasPrefix(e, "builtin:") {
		return prefix + e
	}
	if prefix == "" && !strings.ContainsAny(e, "*?") {
		e = expandPiPath(e, home) // only plain paths are expanded, as pi does
	}
	if filepath.IsAbs(e) {
		if rel, ok := within(src, e); ok {
			e = filepath.Join(dst, rel)
		}
	} else if _, ok := within(src, filepath.Join(src, e)); !ok {
		e = filepath.Join(src, e)
	}
	return prefix + e
}

// within reports p relative to dir when p is dir or inside it.
func within(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// piSource makes a local source absolute against base and leaves remote
// sources (npm:, git:, ...) alone, as pi's isLocalPath does.
func piSource(s, home, base string) string {
	for _, p := range []string{"npm:", "git:", "github:", "http:", "https:", "ssh:"} {
		if strings.HasPrefix(s, p) {
			return s
		}
	}
	return piPath(s, home, base)
}

// expandPiPath expands ~ and file:// in a trimmed local path, leaving the rest as written.
func expandPiPath(p, home string) string {
	p = strings.TrimSpace(p)
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	case strings.HasPrefix(p, "file://"):
		return strings.TrimPrefix(p, "file://")
	}
	return p
}

// piPath is pi's normalizePath for a local path: expanded and, when relative,
// resolved against base.
func piPath(p, home, base string) string {
	p = expandPiPath(p, home)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}
