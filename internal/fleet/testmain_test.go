package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
)

const fleetTestMainIsolated = "MUNSU_FLEET_TESTMAIN_ISOLATED"

// testMainFixtures is set by the integration lane's fixture file.
var testMainFixtures func() (func(), error)

func TestMain(m *testing.M) {
	if os.Getenv(fleetTestMainIsolated) != "" {
		os.Exit(m.Run())
	}
	if err := os.Setenv(fleetTestMainIsolated, "1"); err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: set isolation sentinel: %v\n", err)
		os.Exit(1)
	}

	cleanupHome, err := testutil.IsolateUserHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fleet TestMain: isolate user home: %v\n", err)
		os.Exit(1)
	}
	cleanupFixtures := func() {}
	if testMainFixtures != nil {
		if cleanupFixtures, err = testMainFixtures(); err != nil {
			fmt.Fprintf(os.Stderr, "fleet TestMain: setup fixtures: %v\n", err)
			cleanupHome()
			os.Exit(1)
		}
	}

	code := m.Run()
	cleanupFixtures()
	cleanupHome()
	os.Exit(code)
}

func TestPackageRunsInAnIsolatedUserHome(t *testing.T) {
	pkgHome := os.Getenv(testutil.IsolatedHomeEnv)
	if pkgHome == "" {
		t.Fatal("TestMain did not isolate the user home")
	}
	pi, err := harness.HumanPiAgentDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(pkgHome, ".pi", "agent"); pi != want {
		t.Errorf("HumanPiAgentDir = %q, want %q", pi, want)
	}
	env, err := currentSeatEnv()
	if err != nil {
		t.Fatal(err)
	}
	if env.UserHome != pkgHome {
		t.Errorf("currentSeatEnv().UserHome = %q, want %q", env.UserHome, pkgHome)
	}
}
