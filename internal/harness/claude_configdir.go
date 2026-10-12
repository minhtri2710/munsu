package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeConfigDirSuffix names the per-task claude config dir under the home
// state dir (<durable key>.<suffix>): a fenced claude seat's CLAUDE_CONFIG_DIR.
// Retirement removes it, transcripts included, with the other state artifacts.
const ClaudeConfigDirSuffix = "claude-config"

// claudeConfigLinks are the entries of the Human's config dir a seat runs with:
// settings, instructions, skills, agents, commands, styles, hooks and plugins.
// Each is a symlink, and the fence resolves links, so the seat reads them but
// cannot write them. Everything else (sessions, transcripts, memory, history,
// caches) is claude's mutable state, which claude creates seat-private.
var claudeConfigLinks = []string{"settings.json", "CLAUDE.md", "rules", "skills", "agents", "commands", "output-styles", "hooks", "plugins"}

// claudeGlobalKeys are the keys of the Human's global config (.claude.json) a
// seat starts from, so it starts with no onboarding or API-key dialog. Folder
// trust is copied separately, entry by entry: munsu grants no trust of its own.
var claudeGlobalKeys = []string{"hasCompletedOnboarding", "lastOnboardingVersion", "oauthAccount", "userID", "customApiKeyResponses"}

// ClaudeConfig is the Human's claude configuration a seat's config dir is built
// from, and the keychain item the seat logs in with.
type ClaudeConfig struct {
	// Dir holds settings, skills, hooks and plugins: CLAUDE_CONFIG_DIR, else ~/.claude.
	Dir string
	// GlobalConfig is .claude.json: inside Dir when CLAUDE_CONFIG_DIR is set, else ~/.claude.json.
	GlobalConfig string
	// SecureStorage is the seat's CLAUDE_SECURESTORAGE_CONFIG_DIR. Claude names
	// its keychain item "Claude Code-credentials" and, once CLAUDE_CONFIG_DIR is
	// set, appends "-" and the first 8 hex digits of sha256 of that dir;
	// CLAUDE_SECURESTORAGE_CONFIG_DIR replaces the dir in that suffix, and an
	// empty value drops the suffix. So the seat names the Human's item: empty for
	// the default login, else the Human's CLAUDE_CONFIG_DIR. The variable is
	// undocumented; read from the claude 2.1.296 bundle.
	SecureStorage string
}

// HumanClaudeConfig returns the claude configuration the Human uses. A
// CLAUDE_CONFIG_DIR inside stateDir (a launch started from inside a seat
// inherits the seat's dir) is ignored, so a seat dir is never built from another.
func HumanClaudeConfig(stateDir string) (ClaudeConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ClaudeConfig{}, fmt.Errorf("resolving the Human's claude config dir: %w", err)
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		d = filepath.Clean(d)
		if _, ok := within(stateDir, d); !ok {
			return ClaudeConfig{Dir: d, GlobalConfig: filepath.Join(d, ".claude.json"), SecureStorage: d}, nil
		}
	}
	return ClaudeConfig{Dir: filepath.Join(home, ".claude"), GlobalConfig: filepath.Join(home, ".claude.json")}, nil
}

// BuildClaudeConfigDir builds the per-task claude config dir dst from c: a
// symlink to each of the Human's claudeConfigLinks that exists, and a private
// .claude.json holding claudeGlobalKeys and the Human's folder-trust answers.
// An existing dst is kept and never written: it may belong to a live seat,
// which can replace its entries, and dst is only ever renamed into place
// complete.
func BuildClaudeConfigDir(c ClaudeConfig, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking claude config dir %s: %w", dst, err)
	}
	tmp := dst + ".building"
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("clearing %s: %w", tmp, err)
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return fmt.Errorf("building claude config dir: %w", err)
	}
	if err := fillClaudeConfigDir(c, tmp); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("building claude config dir from %s: %w", c.Dir, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("installing claude config dir %s: %w", dst, err)
	}
	return nil
}

func fillClaudeConfigDir(c ClaudeConfig, dir string) error {
	for _, name := range claudeConfigLinks {
		src := filepath.Join(c.Dir, name)
		if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.Symlink(src, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(c.GlobalConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := claudeGlobalCopy(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(c.GlobalConfig), err)
	}
	return os.WriteFile(filepath.Join(dir, ".claude.json"), out, 0o600)
}

// claudeGlobalCopy returns the seat's .claude.json from the Human's (raw, nil
// when absent): claudeGlobalKeys, and projects reduced to their trust answers.
func claudeGlobalCopy(raw []byte) ([]byte, error) {
	src := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &src); err != nil {
			return nil, err
		}
	}
	out := map[string]any{}
	for _, k := range claudeGlobalKeys {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	var projects map[string]struct {
		Trust *bool `json:"hasTrustDialogAccepted"`
	}
	if p, ok := src["projects"]; ok {
		if err := json.Unmarshal(p, &projects); err != nil {
			return nil, fmt.Errorf("projects: %w", err)
		}
	}
	trust := map[string]any{}
	for path, p := range projects {
		if p.Trust != nil {
			trust[path] = map[string]bool{"hasTrustDialogAccepted": *p.Trust}
		}
	}
	out["projects"] = trust
	return json.MarshalIndent(out, "", "  ")
}
