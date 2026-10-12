package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
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

// replaceSkill writes the embedded skill name to dest/name, replacing whatever
// is there whole so files dropped from the embed do not linger. It builds the
// copy in dest/.<name>.new, moves any existing entry to dest/.<name>.old,
// renames the copy in, then removes the old entry. Both work paths are cleared
// first, so leftovers from a crashed run never block the next one. It reports
// whether an existing entry was replaced.
// ponytail: fixed work paths; two concurrent runs on one dest can collide.
func replaceSkill(dest, name string) (bool, error) {
	target := filepath.Join(dest, name)
	staging := filepath.Join(dest, "."+name+".new")
	aside := filepath.Join(dest, "."+name+".old")
	for _, p := range []string{staging, aside} {
		if err := os.RemoveAll(p); err != nil {
			return false, fmt.Errorf("clearing %s: %w", p, err)
		}
	}
	if _, err := installSkills(staging, name); err != nil {
		return false, err
	}
	_, statErr := os.Lstat(target)
	replaced := statErr == nil
	if replaced {
		if err := os.Rename(target, aside); err != nil {
			return false, fmt.Errorf("moving aside %s: %w", target, err)
		}
	}
	if err := os.Rename(filepath.Join(staging, name), target); err != nil {
		return false, fmt.Errorf("renaming %s into place: %w", name, err)
	}
	for _, p := range []string{staging, aside} {
		if err := os.RemoveAll(p); err != nil {
			return false, fmt.Errorf("removing %s: %w", p, err)
		}
	}
	return replaced, nil
}

// installSkills writes embedded skills under dest. When onlyName is non-empty,
// only that top-level skill is installed.
func installSkills(dest, onlyName string) ([]string, error) {
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
	if _, err := fs.Stat(skillFiles, path.Join("skills", name)); err != nil {
		return "", fmt.Errorf("skill %q not found", name)
	}
	var b strings.Builder
	files, err := fs.ReadDir(skillFiles, path.Join("skills", name))
	if err != nil {
		return "", fmt.Errorf("reading skill %s: %w", name, err)
	}
	for _, e := range files {
		if e.IsDir() {
			continue
		}
		data, rerr := skillFiles.ReadFile(path.Join("skills", name, e.Name()))
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
