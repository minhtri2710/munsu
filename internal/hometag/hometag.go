// Package hometag derives a short, stable tag from a munsu home path.
// The tag is used to namespace session window names so multiple
// fleet instances on the same machine never collide.
package hometag

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Canonical returns the physical absolute path for homeDir.
// Symlinks and macOS /tmp -> /private/tmp are collapsed so the same
// installation always hashes to the same tag.
func Canonical(homeDir string) string {
	if homeDir == "" {
		return homeDir
	}
	abs, err := filepath.Abs(homeDir)
	if err != nil {
		return filepath.Clean(homeDir)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// Tag returns a 6-character lowercase hex prefix for homeDir.
func Tag(homeDir string) string {
	h := sha256.Sum256([]byte(Canonical(homeDir)))
	return fmt.Sprintf("%x", h[:3])
}

// WorkspaceTag returns the backend container label for a munsu home. Primary
// homes keep the legacy hash-only label; marked secondmate homes get a readable
// prefix while retaining the hash to prevent cross-installation collisions.
func WorkspaceTag(homeDir string) string {
	canon := Canonical(homeDir)
	tag := Tag(canon)
	data, err := os.ReadFile(filepath.Join(canon, ".munsu-secondmate-home"))
	if err != nil {
		// Fall back to the caller path when the home is not yet fully resolvable.
		data, err = os.ReadFile(filepath.Join(homeDir, ".munsu-secondmate-home"))
		if err != nil {
			return tag
		}
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 4)
	if len(lines) != 3 || strings.TrimSpace(lines[0]) != "munsu-v2" {
		return tag
	}
	id := labelComponent(lines[1])
	if id == "" {
		return tag
	}
	return "2ndmate-" + id + "-" + tag
}

func labelComponent(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
