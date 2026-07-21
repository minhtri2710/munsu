package captain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/session"
)

// --- RecoverTransaction tests ---

func TestRecoverTransaction_FullOk(t *testing.T) {
	parent := t.TempDir()
	smHome := seedCaptainForTest(t, parent, "sm-ok")

	// Write task meta so the captain appears "launched and alive".
	writeCaptainMeta(t, parent, "sm-ok", smHome, "win-ok")

	// Create required config files for config-validation step.
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.WriteFile(filepath.Join(smHome, "config", "captain-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "config", "soldier-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "data", "captains.md"), []byte("# captains\n"), 0644)

	// Wire backend to report alive + allow new windows.
	origBK := newSessionBackend
	origBF := backendForTask
	origLP := lookPath
	defer func() {
		newSessionBackend = origBK
		backendForTask = origBF
		lookPath = origLP
	}()
	backendForTask = func(parentHome string, meta map[string]string) (session.Backend, string, error) {
		return &fakeBackend{AliveFn: func(string) bool { return true }}, "herdr", nil
	}
	newSessionBackend = func(string) (session.Backend, string, error) {
		return &fakeBackend{
			NewWindowFn: func(_, _ string) (string, error) { return "win-new", nil },
			AliveFn:     func(string) bool { return true },
		}, "herdr", nil
	}
	lookPath = func(string) (string, error) { return "/usr/local/bin/pi", nil }

	tx := &RecoverTransaction{}
	res := tx.Recover(parent, Info{ID: "sm-ok", Home: smHome})

	if len(res.Steps) != 9 {
		t.Fatalf("expected 9 steps, got %d", len(res.Steps))
	}

	// All nine steps should be ok or skipped.
	for _, s := range res.Steps {
		if s.State == StepFailed {
			t.Errorf("step %q failed unexpectedly: %s", s.Name, s.Detail)
		}
	}

	// Provenance step must be ok.
	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepOk {
		t.Errorf("provenance step: got %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}

	// Relaunch step must be ok (alive, no action).
	relaunch := findStep(res.Steps, "relaunch-pane")
	if relaunch == nil || relaunch.State != StepOk {
		t.Fatalf("relaunch-pane step expected ok, got %v", relaunch)
	}
}

func TestRecoverTransaction_ProvenanceFailureSkipsAll(t *testing.T) {
	parent := t.TempDir()
	// Home dir exists but has no provenance marker.
	smHome := filepath.Join(parent, "captains", "sm-bad")
	os.MkdirAll(smHome, 0755)

	tx := &RecoverTransaction{}
	res := tx.Recover(parent, Info{ID: "sm-bad", Home: smHome})

	if len(res.Steps) != 9 {
		t.Fatalf("expected 9 steps, got %d", len(res.Steps))
	}

	// Provenance must fail.
	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepFailed {
		t.Errorf("provenance step expected failed, got %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}

	// All subsequent steps must be skipped.
	for i := 1; i < len(res.Steps); i++ {
		if res.Steps[i].State != StepSkipped {
			t.Errorf("step %d (%s) expected skipped, got %s", i, res.Steps[i].Name, res.Steps[i].State)
		}
	}
}

func TestRecoverTransaction_EmptyHomeFailed(t *testing.T) {
	tx := &RecoverTransaction{}
	res := tx.Recover(t.TempDir(), Info{ID: "empty", Home: ""})

	if len(res.Steps) != 9 {
		t.Fatalf("expected 9 steps, got %d", len(res.Steps))
	}

	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepFailed {
		t.Errorf("provenance should fail for empty home: %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}
	if !strings.Contains(res.Steps[0].Detail, "missing home path") {
		t.Errorf("detail should mention missing home: %s", res.Steps[0].Detail)
	}
}

func TestRecoverTransaction_DeadLaunchedRelaunches(t *testing.T) {
	parent := t.TempDir()
	smHome := seedCaptainForTest(t, parent, "sm-dead")
	writeCaptainMeta(t, parent, "sm-dead", smHome, "win-dead")

	// captain-harness config so Launch resolves pi.
	configDir := filepath.Join(parent, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "captain-harness"), []byte("pi\n"), 0644)

	// Also create captain home config files for config-validation step.
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.WriteFile(filepath.Join(smHome, "config", "captain-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "config", "soldier-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "data", "captains.md"), []byte("# captains\n"), 0644)

	origBK := newSessionBackend
	origBF := backendForTask
	origLP := lookPath
	defer func() {
		newSessionBackend = origBK
		backendForTask = origBF
		lookPath = origLP
	}()
	backendForTask = func(parentHome string, meta map[string]string) (session.Backend, string, error) {
		return &fakeBackend{AliveFn: func(string) bool { return false }}, "herdr", nil
	}
	newSessionBackend = func(string) (session.Backend, string, error) {
		return &fakeBackend{
			NewWindowFn: func(_, _ string) (string, error) { return "win-new", nil },
			AliveFn:     func(string) bool { return true },
		}, "herdr", nil
	}
	lookPath = func(string) (string, error) { return "/usr/local/bin/pi", nil }

	tx := &RecoverTransaction{}
	res := tx.Recover(parent, Info{ID: "sm-dead", Home: smHome})

	if len(res.Steps) != 9 {
		t.Fatalf("expected 9 steps, got %d", len(res.Steps))
	}

	// Provenance ok.
	if res.Steps[0].State != StepOk {
		t.Errorf("provenance expected ok, got %s", res.Steps[0].State)
	}

	// Relaunch step must be ok (relaunched successfully).
	relaunch := findStep(res.Steps, "relaunch-pane")
	if relaunch == nil {
		t.Fatal("missing relaunch-pane step")
	}
	if relaunch.State != StepOk {
		t.Errorf("relaunch-pane expected ok, got %s: %s", relaunch.State, relaunch.Detail)
	}
	if !strings.Contains(relaunch.Detail, "relaunched") {
		t.Errorf("detail should mention relaunch: %s", relaunch.Detail)
	}
}

func TestRecoverTransaction_StepsString(t *testing.T) {
	res := &RecoverResult{Steps: []StepResult{
		{Name: "provenance", State: StepOk, Detail: "valid"},
		{Name: "config-validation", State: StepFailed, Detail: "missing config"},
		{Name: "relaunch-pane", State: StepSkipped, Detail: "not launched"},
	}}
	s := res.StepsString()
	if !strings.Contains(s, "provenance: ok (valid)") {
		t.Errorf("StepsString missing ok step: %s", s)
	}
	if !strings.Contains(s, "config-validation: FAILED (missing config)") {
		t.Errorf("StepsString missing failed step: %s", s)
	}
	if !strings.Contains(s, "relaunch-pane: skipped (not launched)") {
		t.Errorf("StepsString missing skipped step: %s", s)
	}
}

func TestRecoverTransaction_EmptyStepsString(t *testing.T) {
	empty := (&RecoverResult{}).StepsString()
	if empty != "no recovery steps" {
		t.Errorf("empty StepsString() = %q", empty)
	}
}

// findStep locates a step by name in the result slice.
func findStep(steps []StepResult, name string) *StepResult {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}
