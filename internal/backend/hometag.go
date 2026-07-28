// Package hometag derives a short, stable tag from a munsu home path.
// The tag is used to namespace session window names so multiple
// fleet instances on the same machine never collide.
package backend

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
)

// Canonical returns the physical absolute path for homeDir.
// Symlinks and macOS /tmp -> /private/tmp are collapsed so the same
// installation always hashes to the same tag.
func Canonical(homeDir string) string {
	return home.Canonical(homeDir)
}

// Tag returns a 6-character lowercase hex prefix for homeDir.
func Hometag(homeDir string) string {
	h := sha256.Sum256([]byte(Canonical(homeDir)))
	return fmt.Sprintf("%x", h[:3])
}

// WorkspaceTag returns the backend container label for a munsu home. Primary
// homes keep the legacy hash-only label; marked captain homes get a readable
// prefix while retaining the hash to prevent cross-installation collisions.
func WorkspaceTag(homeDir string) string {
	canon := Canonical(homeDir)
	tag := Hometag(canon)
	data, err := os.ReadFile(filepath.Join(canon, ".munsu-captain-home"))
	if err != nil {
		// Fall back to the caller path when the home is not yet fully resolvable.
		data, err = os.ReadFile(filepath.Join(homeDir, ".munsu-captain-home"))
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
	return "captain-" + id + "-" + tag
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
