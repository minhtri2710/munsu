package cli

import (
	"testing"
)

// TestDoctorCommandRegistered verifies the doctor subcommand is registered on root.
func TestDoctorCommandRegistered(t *testing.T) {
	root := NewRootCommand()

	doctorCmd, _, err := root.Find([]string{"doctor"})
	if err != nil {
		t.Fatalf("doctor command not found: %v", err)
	}

	if doctorCmd == nil {
		t.Fatal("doctor command is nil")
	}

	if doctorCmd.Use != "doctor" {
		t.Errorf("expected Use 'doctor', got %q", doctorCmd.Use)
	}
}

// TestDoctorHelp verifies doctor --help output without panic.
func TestDoctorHelp(t *testing.T) {
	root := NewRootCommand()

	// Execute with --help flag; should not panic
	root.SetArgs([]string{"doctor", "--help"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("doctor --help failed: %v", err)
	}
}
