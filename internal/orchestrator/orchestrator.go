// Package orchestrator provides supervision loops, wake queue management,
// AFK away-mode supervision daemon, mailbox outbox, and uplink reports.
package orchestrator

func IsAFKActive(homeDir string) bool { return IsActive(homeDir) }

func DisableAFK(homeDir string) error { return Disable(homeDir) }
