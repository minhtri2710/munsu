// Package orchestrator provides supervision loops, wake queue management,
// AFK away-mode supervision daemon, mailbox outbox, and uplink reports.
package orchestrator

import "time"

// StartAFK starts the AFK away-mode daemon.
func StartAFK(homeDir string) error {
	_ = ReadBeatStatus(homeDir, time.Now())
	return Start(homeDir)
}

func IsAFKActive(homeDir string) bool { return IsActive(homeDir) }

func DisableAFK(homeDir string) error { return Disable(homeDir) }
