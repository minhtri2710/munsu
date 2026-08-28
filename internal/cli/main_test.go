package cli

import (
	"fmt"
	"os"
	"testing"
)

const cliTestMainIsolated = "MUNSU_CLI_TESTMAIN_ISOLATED"

// TestMain neutralizes ambient gate state, including the NM_HOME git-common-dir
// trigger. A test whose verdict depends on the environment of whoever ran it
// must opt in explicitly.
func TestMain(m *testing.M) {
	if os.Getenv(cliTestMainIsolated) != "" {
		os.Exit(m.Run())
	}

	nmHome, err := os.MkdirTemp("", "munsu-cli-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/cli TestMain: create NM_HOME: %v\n", err)
		os.Exit(1)
	}
	if err := os.Unsetenv("NO_MISTAKES_GATE"); err != nil {
		fmt.Fprintf(os.Stderr, "internal/cli TestMain: unset NO_MISTAKES_GATE: %v\n", err)
		_ = os.RemoveAll(nmHome)
		os.Exit(1)
	}
	if err := os.Setenv("NM_HOME", nmHome); err != nil {
		fmt.Fprintf(os.Stderr, "internal/cli TestMain: set NM_HOME: %v\n", err)
		_ = os.RemoveAll(nmHome)
		os.Exit(1)
	}
	if err := os.Setenv(cliTestMainIsolated, "1"); err != nil {
		fmt.Fprintf(os.Stderr, "internal/cli TestMain: set isolation sentinel: %v\n", err)
		_ = os.RemoveAll(nmHome)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(nmHome); err != nil {
		fmt.Fprintf(os.Stderr, "internal/cli TestMain: remove NM_HOME: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
