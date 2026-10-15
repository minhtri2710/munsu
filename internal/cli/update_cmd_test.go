package cli

import (
	"bytes"
	"strings"
	"testing"
)

// No row registers a captain: one would run fleet.Converge against live
// session endpoints.
func TestUpdateCaptainsFlagReachesCaptainStep(t *testing.T) {
	savedUpdateIn := doUpdateIn
	doUpdateIn = func(string) error { return nil }
	t.Cleanup(func() { doUpdateIn = savedUpdateIn })

	const captainStep = "No captains registered"
	for _, tc := range []struct {
		name         string
		captains     bool
		wantCaptains bool
	}{
		{name: "without --captains stops after self-update", captains: false, wantCaptains: false},
		{name: "with --captains reaches captain step", captains: true, wantCaptains: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			repo := t.TempDir()
			initMunsuRepo(t, repo, "main")
			t.Setenv("MUNSU_HOME", homeDir)

			args := []string{"update", "--repo", repo}
			if tc.captains {
				args = append(args, "--captains")
			}
			root := NewRootCommand()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, buf.String())
			}
			out := buf.String()
			if !strings.Contains(out, "Updated munsu to") {
				t.Fatalf("%v did not report the self-update:\n%s", args, out)
			}
			if got := strings.Contains(out, captainStep); got != tc.wantCaptains {
				t.Fatalf("%v: captain step reached = %t, want %t\n%s", args, got, tc.wantCaptains, out)
			}
		})
	}
}
