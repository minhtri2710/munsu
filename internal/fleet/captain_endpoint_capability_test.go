package fleet

import (
	"errors"
	"os"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

type orderedRetireEndpoint struct {
	t              *testing.T
	parent, taskID string
	calls          int
	err            error
}

func (e *orderedRetireEndpoint) Retire(string, map[string]string) error {
	e.calls++
	metaPath, err := mhome.MetaFilePath(e.parent, e.taskID)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		e.t.Fatalf("metadata removed before endpoint retirement: %v", err)
	}
	return e.err
}

func TestRetireSeededCaptainDoesNotRequireEndpoint(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "seeded")
	endpoint := &testRetireEndpoint{}
	if err := Retire(home, parent, false, false, endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint.calls != 0 {
		t.Fatalf("calls=%d, want 0", endpoint.calls)
	}
}

func TestRetireLaunchedCaptainRequiresEndpoint(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "launched")
	writeCaptainMeta(t, parent, "launched", home, "window")
	err := Retire(home, parent, false, false, nil)
	if err == nil || err.Error() != "captain retire endpoint capability is required" {
		t.Fatalf("error=%v", err)
	}
}

func TestRetireInvalidMetadataDoesNotInvokeEndpoint(t *testing.T) {
	for _, mutate := range []func(map[string]string){
		func(m map[string]string) { m["kind"] = "ship" },
		func(m map[string]string) { m["sm_id"] = "other" },
		func(m map[string]string) { m["home"] = t.TempDir() },
		func(m map[string]string) { delete(m, "window") },
	} {
		parent := t.TempDir()
		home := seedCaptainForTest(t, parent, "bad")
		meta := map[string]string{"kind": "captain", "sm_id": "bad", "home": home, "window": "window"}
		canon, _ := canonicalCaptainHome(home)
		meta["home"] = canon
		mutate(meta)
		if err := mhome.WriteMeta(parent, taskIDForCaptain("bad"), meta); err != nil {
			t.Fatal(err)
		}
		endpoint := &testRetireEndpoint{}
		if err := Retire(home, parent, false, false, endpoint); err == nil {
			t.Fatal("expected validation error")
		}
		if endpoint.calls != 0 {
			t.Fatalf("calls=%d, want 0", endpoint.calls)
		}
	}
}

func TestRetireEndpointFailurePreservesDurableState(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "failed")
	writeCaptainMeta(t, parent, "failed", home, "window")
	if err := Register(parent, "failed", home, "", ""); err != nil {
		t.Fatal(err)
	}
	endpoint := &orderedRetireEndpoint{t: t, parent: parent, taskID: taskIDForCaptain("failed"), err: errors.New("teardown failed")}
	if err := Retire(home, parent, true, false, endpoint); err == nil {
		t.Fatal("expected endpoint failure")
	}
	if _, err := mhome.ReadMeta(parent, taskIDForCaptain("failed")); err != nil {
		t.Fatalf("meta lost: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("home lost: %v", err)
	}
	registered, err := ListCaptains(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 {
		t.Fatalf("registry=%v", registered)
	}
}

func TestRetireSuccessInvokesEndpointBeforeCleanup(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "success")
	writeCaptainMeta(t, parent, "success", home, "window")
	if err := Register(parent, "success", home, "", ""); err != nil {
		t.Fatal(err)
	}
	endpoint := &orderedRetireEndpoint{t: t, parent: parent, taskID: taskIDForCaptain("success")}
	if err := Retire(home, parent, true, false, endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("calls=%d", endpoint.calls)
	}
	if _, err := mhome.ReadMeta(parent, taskIDForCaptain("success")); err == nil {
		t.Fatal("meta remains")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("home remains: %v", err)
	}
}

func TestLaunchEndpointFailureWritesNoMetadata(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "failed-launch")
	writeCanonicalPiIntegration(t, home)
	endpoint := failingLaunchEndpoint{err: errors.New("launch failed")}
	err := Launch(home, parent, endpoint)
	if err == nil || !strings.Contains(err.Error(), "launch failed") {
		t.Fatalf("error=%v", err)
	}
	if _, err := mhome.ReadMeta(parent, taskIDForCaptain("failed-launch")); err == nil {
		t.Fatal("metadata written")
	}
}

type failingLaunchEndpoint struct{ err error }

func (e failingLaunchEndpoint) Launch(string, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, e.err
}
func (e failingLaunchEndpoint) Cleanup(string, LaunchResult) error { return nil }
