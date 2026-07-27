//go:build windows

package afk

import "errors"

func stopProcess(int) error {
	return errors.New("AFK process termination capability unavailable on Windows")
}
