//go:build integration

package fleet

import (
	"fmt"
	"os"
	"testing"
)

const fleetTestMainIsolated = "MUNSU_FLEET_TESTMAIN_ISOLATED"

func TestMain(m *testing.M) {
	if os.Getenv(fleetTestMainIsolated) != "" {
		os.Exit(m.Run())
	}

	nmHome, err := setupFleetTestIsolation()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: %v\n", err)
		os.Exit(1)
	}

	cleanupFixtures, err := setupFleetTestFixtures()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: setup fixtures: %v\n", err)
		_ = cleanupFleetTestIsolation(nmHome)
		os.Exit(1)
	}

	code := m.Run()
	cleanupFixtures()
	if err := cleanupFleetTestIsolation(nmHome); err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: remove NM_HOME: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func setupFleetTestIsolation() (string, error) {
	nmHome, err := os.MkdirTemp("", "munsu-fleet-test-")
	if err != nil {
		return "", fmt.Errorf("create NM_HOME: %w", err)
	}
	if err := os.Unsetenv("NO_MISTAKES_GATE"); err != nil {
		_ = os.RemoveAll(nmHome)
		return "", fmt.Errorf("unset NO_MISTAKES_GATE: %w", err)
	}
	if err := os.Setenv("NM_HOME", nmHome); err != nil {
		_ = os.RemoveAll(nmHome)
		return "", fmt.Errorf("set NM_HOME: %w", err)
	}
	if err := os.Setenv(fleetTestMainIsolated, "1"); err != nil {
		_ = os.RemoveAll(nmHome)
		return "", fmt.Errorf("set isolation sentinel: %w", err)
	}
	return nmHome, nil
}

func cleanupFleetTestIsolation(nmHome string) error {
	if nmHome == "" {
		return nil
	}
	return os.RemoveAll(nmHome)
}
