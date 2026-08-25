//go:build !windows

package testutil

func installWindowsFake(string) error { return nil }

func fakeExecutablePath(path string) string { return path }
