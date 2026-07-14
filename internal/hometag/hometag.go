// Package hometag derives a short, stable tag from a munsu home path.
// The tag is used to namespace session window names so multiple
// fleet instances on the same machine never collide.
package hometag

import (
	"crypto/sha256"
	"fmt"
)

// Tag returns a 6-character lowercase hex prefix for homeDir.
func Tag(homeDir string) string {
	h := sha256.Sum256([]byte(homeDir))
	return fmt.Sprintf("%x", h[:3])
}
