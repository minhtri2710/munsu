//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// On darwin Launch runs the captain under its probed fence from its seat, on
// exactly the PATH the fence denies and from its own pi agent dir, and records
// the fence; elsewhere it launches as before and records why it is unfenced.
func TestLaunchRunsTheCaptainUnderItsSeat(t *testing.T) {
	parent, captainHome := launchableCaptain(t)
	if err := Launch(captainHome, parent, launchCaptureEndpoint{}, nil, fakeIntegrationPort{}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(captainHome, ".captain-launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	meta, err := home.ReadMeta(parent, taskIDForCaptain("registered-id"))
	if err != nil {
		t.Fatal(err)
	}
	seat := shQuote("captain") + " " + shQuote("seat") + " " + shQuote("--") + " " + shQuote("/usr/bin/sandbox-exec")
	if runtime.GOOS != "darwin" {
		if meta["fence"] != "unfenced" || !strings.Contains(meta["fence_reason"], "no write fence") || strings.Contains(script, seat) || !strings.Contains(script, `:"$PATH"`) {
			t.Fatalf("meta %v, script:\n%s\nwant an unfenced launch on the pane's PATH", meta, script)
		}
		return
	}
	if meta["fence"] != "applied" || len(meta["fence_profile_digest"]) != 64 {
		t.Fatalf("meta %v, want the applied fence recorded", meta)
	}
	canonical, err := canonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	agentDir, err := piAgentDirPath(canonical, taskIDForCaptain("registered-id"))
	if err != nil {
		t.Fatal(err)
	}
	pathDirs := strings.Join(seatPathDirs(canonical), string(os.PathListSeparator))
	for _, want := range []string{seat, "export PI_CODING_AGENT_DIR=" + shQuote(agentDir) + "\n", "export PATH=" + shQuote(pathDirs) + "\n"} {
		if !strings.Contains(script, want) {
			t.Errorf("launch script missing %q:\n%s", want, script)
		}
	}
	if fi, err := os.Lstat(filepath.Join(home.StateDir(captainHome), "captain-docs")); err != nil || !fi.IsDir() {
		t.Errorf("captain docs dir not made: %v", err)
	}
}
