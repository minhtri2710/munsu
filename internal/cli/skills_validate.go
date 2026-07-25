package cli

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	markdownLinkPattern = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	backtickPattern     = regexp.MustCompile("`([^`]+)`")
	skillShowPattern    = regexp.MustCompile(`munsu skill show ([a-z0-9][a-z0-9-]*)`)
)

// validateSkillBundle validates local references and auxiliary skill targets
// for every skill below root. All resolution and traversal rules live behind
// this seam so installed and embedded bundles share the same test surface.
func validateSkillBundle(fsys fs.FS, root string) error {
	skillNames, err := bundleSkillNames(fsys, root)
	if err != nil {
		return err
	}

	knownSkills := make(map[string]bool, len(skillNames))
	for _, name := range skillNames {
		knownSkills[name] = true
	}

	var problems []string
	for _, name := range skillNames {
		skillRoot := path.Join(root, name)
		err := fs.WalkDir(fsys, skillRoot, func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(filename), ".md") {
				return nil
			}
			data, readErr := fs.ReadFile(fsys, filename)
			if readErr != nil {
				return readErr
			}
			content := string(data)
			for _, ref := range localReferences(content) {
				resolved, resolveErr := resolveBundleReference(skillRoot, filename, ref)
				if resolveErr != nil {
					problems = append(problems, fmt.Sprintf("%s: reference %q: %v", filename, ref, resolveErr))
					continue
				}
				if _, statErr := fs.Stat(fsys, resolved); statErr != nil {
					problems = append(problems, fmt.Sprintf("%s: reference %q resolves to missing %s", filename, ref, resolved))
				}
			}
			for _, match := range skillShowPattern.FindAllStringSubmatch(content, -1) {
				if !knownSkills[match[1]] {
					problems = append(problems, fmt.Sprintf("%s: munsu skill show target %q is not embedded", filename, match[1]))
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("validating skill %s: %w", name, err)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("invalid skill bundle references:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}

func bundleSkillNames(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("reading skill bundle %s: %w", root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func localReferences(content string) []string {
	seen := map[string]bool{}
	var refs []string
	add := func(raw string) {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 0 {
			return
		}
		ref := strings.Trim(fields[0], "<>\"'")
		if ref == "" || strings.HasPrefix(ref, "#") || strings.Contains(ref, "://") || strings.HasPrefix(ref, "mailto:") {
			return
		}
		if i := strings.IndexByte(ref, '#'); i >= 0 {
			ref = ref[:i]
		}
		if ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, match := range markdownLinkPattern.FindAllStringSubmatch(line, -1) {
			add(match[1])
		}
		for _, match := range backtickPattern.FindAllStringSubmatch(line, -1) {
			candidate := strings.TrimSpace(match[1])
			if strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, "../") || strings.HasPrefix(candidate, "docs/") || isCompanionMarkdown(candidate) {
				add(candidate)
			}
		}
	}
	return refs
}

func isCompanionMarkdown(candidate string) bool {
	switch candidate {
	case "SKILL.md", "REFERENCE.md", "COMMANDS.md", "SUPERVISION.md":
		return true
	default:
		return false
	}
}

func resolveBundleReference(skillRoot, sourceFile, ref string) (string, error) {
	if strings.HasPrefix(ref, "/") {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	resolved := path.Clean(path.Join(path.Dir(sourceFile), ref))
	if resolved != skillRoot && !strings.HasPrefix(resolved, skillRoot+"/") {
		return "", fmt.Errorf("resolved path %s escapes skill module %s", resolved, skillRoot)
	}
	return resolved, nil
}
