//go:build integration

package backend

import (
	"fmt"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// fakeCmd declares what a fake CLI does: what it prints, what it exits with,
// and where it records the arguments it was called with.
type fakeCmd struct {
	stdout   string // one line on stdout; empty prints nothing
	exitCode int
	argsFile string // when set, each argument is written on its own line
}

// fakeTreehouseOnPath installs a fake `treehouse` in a fresh directory and
// prepends that directory to PATH for the duration of the test.
func fakeTreehouseOnPath(t *testing.T, spec fakeCmd) {
	t.Helper()
	testutil.FakeOnPath(t, "treehouse", spec.script())
}

func (spec fakeCmd) script() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if spec.argsFile != "" {
		fmt.Fprintf(&b, ": > %q\nfor a in \"$@\"; do printf '%%s\\n' \"$a\" >> %q; done\n", spec.argsFile, spec.argsFile)
	}
	if spec.stdout != "" {
		fmt.Fprintf(&b, "printf '%%s\\n' %q\n", spec.stdout)
	}
	fmt.Fprintf(&b, "exit %d\n", spec.exitCode)
	return b.String()
}
