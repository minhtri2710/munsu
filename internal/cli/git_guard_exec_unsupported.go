//go:build !darwin && !linux

package cli

import "fmt"

func execRealGit(path string, args []string) error {
	return fmt.Errorf("process replacement is unavailable on this platform")
}
