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
	if err := os.Setenv(fleetTestMainIsolated, "1"); err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: set isolation sentinel: %v\n", err)
		os.Exit(1)
	}

	cleanupFixtures, err := setupFleetTestFixtures()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: setup fixtures: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	cleanupFixtures()
	os.Exit(code)
}
