//go:build darwin || linux

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func execRealGit(path string, args []string) error {
	argv := append([]string{path}, args...)
	return unix.Exec(path, argv, os.Environ())
}
