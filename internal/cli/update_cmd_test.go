package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
)

func TestUpdateCaptainsFlagReachesCaptainStep(t *testing.T) {
	savedUpdateIn := doUpdateIn
	doUpdateIn = func(string) error { return nil }
	t.Cleanup(func() { doUpdateIn = savedUpdateIn })

	for _, tc := range []struct {
		name     string
		captains bool
		register bool
		broken   bool
		step     string
		wantStep bool
		wantErr  string
	}{
		{name: "without --captains stops after self-update", step: "No captains registered", wantStep: false},
		{name: "with --captains reaches captain step", captains: true, step: "No captains registered", wantStep: true},
		{name: "with --captains fast-forwards a registered captain", captains: true, register: true, step: "fast-forwarded", wantStep: true},
		{name: "with --captains a failed converge fails the command", captains: true, broken: true, step: "registry validation", wantStep: true, wantErr: "broken: provenance validation failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			if tc.register {
				parent, _, project := seedUnlaunchedCaptain(t, "update-ff", seamStaticIntegrationPort{})
				homeDir = parent
				if err := os.WriteFile(filepath.Join(project, "NOTES.md"), []byte("advance\n"), 0644); err != nil {
					t.Fatal(err)
				}
				runGit(t, project, "add", "NOTES.md")
				runGit(t, project, "commit", "-m", "advance main")
			}
			if tc.broken {
				if err := fleet.Register(homeDir, "broken", t.TempDir(), "", "broken"); err != nil {
					t.Fatal(err)
				}
			}
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
			err := root.Execute()
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("%v: err = %v, want %q\n%s", args, err, tc.wantErr, buf.String())
			}
			out := buf.String()
			if !strings.Contains(out, "Updated munsu to") {
				t.Fatalf("%v did not report the self-update:\n%s", args, out)
			}
			if got := strings.Contains(out, tc.step); got != tc.wantStep {
				t.Fatalf("%v: %q in output = %t, want %t\n%s", args, tc.step, got, tc.wantStep, out)
			}
		})
	}
}
