package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skills/*
var skillFiles embed.FS

// embeddedSkillNames returns the top-level skill directory names embedded under skills/.
func embeddedSkillNames() ([]string, error) {
	entries, err := fs.ReadDir(skillFiles, "skills")
	if err != nil {
		return nil, fmt.Errorf("reading embedded skills: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// skillExistsAt reports whether the given skill directory already exists at dest.
func skillExistsAt(dest, name string) bool {
	info, err := os.Stat(filepath.Join(dest, name))
	return err == nil && info.IsDir()
}

// confirmOverwrite asks whether to overwrite an existing skill.
// Returns true on confirmation, false otherwise (including non-TTY reads).
func confirmOverwrite(name string) bool {
	fmt.Printf("Skill %q already exists. Overwrite? [y/N] ", name)
	var resp string
	if _, err := fmt.Scanln(&resp); err != nil {
		return false
	}
	switch resp {
	case "y", "Y", "yes", "YES":
		return true
	default:
		return false
	}
}

// installOneSkill writes a single embedded skill (by top-level dir name) under dest.
// If the skill already exists and overwrite is false, it is skipped.
func installOneSkill(dest, name string, overwrite bool) (bool, error) {
	skip := map[string]bool{}
	if !overwrite && skillExistsAt(dest, name) {
		skip[name] = true
	}
	installed, err := installSkills(dest, name, skip)
	if err != nil {
		return false, err
	}
	return len(installed) > 0, nil
}

// installSkills writes embedded skills under dest. When onlyName is non-empty,
// only that top-level skill is installed. skipNames is the set of skills to leave untouched.
func installSkills(dest, onlyName string, skipNames map[string]bool) ([]string, error) {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return nil, fmt.Errorf("creating skills directory %s: %w", dest, err)
	}

	var installed []string
	err := fs.WalkDir(skillFiles, "skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("skills", p)
		if rel == "." {
			return nil
		}
		top := filepath.ToSlash(rel)
		if i := strings.IndexByte(top, '/'); i >= 0 {
			top = top[:i]
		}
		if onlyName != "" && top != onlyName {
			return nil
		}
		if skipNames[top] {
			return nil
		}
		if d.IsDir() {
			if err := os.MkdirAll(filepath.Join(dest, rel), 0755); err != nil {
				return fmt.Errorf("creating %s: %w", rel, err)
			}
			return nil
		}
		data, rerr := skillFiles.ReadFile(p)
		if rerr != nil {
			return fmt.Errorf("reading embedded %s: %w", p, rerr)
		}
		target := filepath.Join(dest, rel)
		if werr := os.WriteFile(target, data, 0644); werr != nil {
			return fmt.Errorf("writing %s: %w", target, werr)
		}
		installed = append(installed, rel)
		return nil
	})
	return installed, err
}

// readEmbeddedSkill returns the concatenated contents of every file in the named
// embedded skill directory, in lexical order. Returns an error if the skill does not exist.
func readEmbeddedSkill(name string) (string, error) {
	if _, err := fs.Stat(skillFiles, filepath.Join("skills", name)); err != nil {
		return "", fmt.Errorf("skill %q not found", name)
	}
	var b strings.Builder
	files, err := fs.ReadDir(skillFiles, filepath.Join("skills", name))
	if err != nil {
		return "", fmt.Errorf("reading skill %s: %w", name, err)
	}
	for _, e := range files {
		if e.IsDir() {
			continue
		}
		data, rerr := skillFiles.ReadFile(filepath.Join("skills", name, e.Name()))
		if rerr != nil {
			return "", fmt.Errorf("reading %s/%s: %w", name, e.Name(), rerr)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String(), nil
}
