//go:build windows

package home

import "os"

func lockWakeFile(file *os.File) error   { return nil }
func unlockWakeFile(file *os.File) error { return nil }
