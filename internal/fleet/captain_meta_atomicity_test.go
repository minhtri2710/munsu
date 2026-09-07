package fleet

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

type blockingNudgeEndpoint struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingNudgeEndpoint) Nudge(string, map[string]string, string) (NudgeResult, error) {
	close(e.started)
	<-e.release
	return NudgeResult{Status: "submitted", Acknowledged: true}, nil
}

type blockingProbeEndpoint struct {
	started chan struct{}
	release chan struct{}
	result  CaptainProbeResult
}

func (e *blockingProbeEndpoint) Probe(string, map[string]string) (CaptainProbeResult, error) {
	close(e.started)
	<-e.release
	return e.result, nil
}

type finalBlockingProbeEndpoint struct {
	calls   int
	started chan struct{}
	release chan struct{}
}

func (e *finalBlockingProbeEndpoint) Probe(string, map[string]string) (CaptainProbeResult, error) {
	e.calls++
	if e.calls == relaunchProofAttempts {
		close(e.started)
		<-e.release
	}
	return CaptainProbeResult{Absent: true}, nil
}

func TestValidateCaptainBindingRefusesMismatches(t *testing.T) {
	home := t.TempDir()
	sm := Info{ID: "binding-test", Home: home}
	canonicalHome, err := canonicalCaptainHome(home)
	if err != nil {
		t.Fatal(err)
	}
	expected := captainBinding{kind: "captain", smID: sm.ID, home: canonicalHome, window: "window-1", backend: "backend-1"}

	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{name: "identity", mutate: func(meta map[string]string) { meta["kind"] = "soldier" }, wantErr: "captain binding changed: kind"},
		{name: "home", mutate: func(meta map[string]string) { meta["home"] = t.TempDir() }, wantErr: "captain binding changed: home"},
		{name: "backend", mutate: func(meta map[string]string) { meta["backend"] = "backend-2" }, wantErr: "captain binding changed: backend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := map[string]string{
				"kind":    expected.kind,
				"sm_id":   expected.smID,
				"home":    expected.home,
				"window":  expected.window,
				"backend": expected.backend,
			}
			tt.mutate(meta)
			if err := validateCaptainBinding(meta, sm, expected); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateCaptainBinding error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCaptainMetaAtomicitySuccessfulMutations(t *testing.T) {
	t.Run("clear guard preserves concurrent metadata", func(t *testing.T) {
		parent := t.TempDir()
		home := seedCaptainForTest(t, parent, "clear-success")
		writeCaptainMeta(t, parent, "clear-success", home, "w1")
		writeRelaunchGuard(t, parent, "clear-success", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		meta, err := mhome.ReadMeta(parent, taskIDForCaptain("clear-success"))
		if err != nil {
			t.Fatal(err)
		}
		binding := captainBinding{kind: meta["kind"], smID: meta["sm_id"], home: meta["home"], window: meta["window"], backend: meta["backend"]}
		if err := mhome.UpdateMeta(parent, taskIDForCaptain("clear-success"), func(meta map[string]string) error {
			meta["sentinel"] = "preserved"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := clearRelaunchGuard(parent, Info{ID: "clear-success", Home: home}, binding); err != nil {
			t.Fatal(err)
		}
		meta, err = mhome.ReadMeta(parent, taskIDForCaptain("clear-success"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("clear persisted metadata: %v", meta)
		if meta["sentinel"] != "preserved" {
			t.Fatalf("sentinel = %q, want preserved", meta["sentinel"])
		}
		if _, ok := meta["relaunch_liveness"]; ok {
			t.Fatal("clear left relaunch_liveness")
		}
		if _, ok := meta[relaunchGuardUntilField]; ok {
			t.Fatal("clear left relaunch_guard_until")
		}
	})

	t.Run("consult normalizes and expires guard", func(t *testing.T) {
		parent := t.TempDir()
		home := seedCaptainForTest(t, parent, "consult-success")
		writeCaptainMeta(t, parent, "consult-success", home, "w1")
		writeRelaunchGuard(t, parent, "consult-success", "not-a-deadline")
		refused, remaining, err := consultRelaunchGuard(parent, taskIDForCaptain("consult-success"), time.Now())
		if err != nil || !refused || remaining <= 0 {
			t.Fatalf("consult normalization = refused=%v remaining=%s err=%v", refused, remaining, err)
		}
		meta, err := mhome.ReadMeta(parent, taskIDForCaptain("consult-success"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("consult normalized metadata: %v", meta)
		if _, err := strconv.ParseInt(meta[relaunchGuardUntilField], 10, 64); err != nil {
			t.Fatalf("normalized deadline = %q: %v", meta[relaunchGuardUntilField], err)
		}
		if err := mhome.UpdateMeta(parent, taskIDForCaptain("consult-success"), func(meta map[string]string) error {
			meta["relaunch_guard_until"] = strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
			meta["sentinel"] = "preserved"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		refused, remaining, err = consultRelaunchGuard(parent, taskIDForCaptain("consult-success"), time.Now())
		if err != nil || refused || remaining != 0 {
			t.Fatalf("consult expiry = refused=%v remaining=%s err=%v", refused, remaining, err)
		}
		meta, err = mhome.ReadMeta(parent, taskIDForCaptain("consult-success"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("consult expired metadata: %v", meta)
		if meta["sentinel"] != "preserved" {
			t.Fatalf("sentinel = %q, want preserved", meta["sentinel"])
		}
		if _, ok := meta["relaunch_liveness"]; ok {
			t.Fatal("expired consult left relaunch_liveness")
		}
	})

	t.Run("prove alive preserves concurrent metadata", func(t *testing.T) {
		parent := t.TempDir()
		home := seedCaptainForTest(t, parent, "prove-success")
		writeCaptainMeta(t, parent, "prove-success", home, "w1")
		writeRelaunchGuard(t, parent, "prove-success", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		probe := &blockingProbeEndpoint{started: make(chan struct{}), release: make(chan struct{}), result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}
		result := make(chan struct {
			proven bool
			err    error
		}, 1)
		go func() {
			proven, err := proveRelaunch(parent, Info{ID: "prove-success", Home: home}, probe, func(time.Duration) {}, time.Now)
			result <- struct {
				proven bool
				err    error
			}{proven, err}
		}()
		<-probe.started
		if err := mhome.UpdateMeta(parent, taskIDForCaptain("prove-success"), func(meta map[string]string) error {
			meta["sentinel"] = "preserved"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		close(probe.release)
		out := <-result
		if !out.proven || out.err != nil {
			t.Fatalf("prove result = proven=%v err=%v", out.proven, out.err)
		}
		meta, err := mhome.ReadMeta(parent, taskIDForCaptain("prove-success"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("prove-alive metadata: %v", meta)
		if meta["sentinel"] != "preserved" {
			t.Fatalf("sentinel = %q, want preserved", meta["sentinel"])
		}
		if _, ok := meta["relaunch_liveness"]; ok {
			t.Fatal("prove alive left relaunch_liveness")
		}
	})

	t.Run("prove failure arms guard and preserves concurrent metadata", func(t *testing.T) {
		parent := t.TempDir()
		home := seedCaptainForTest(t, parent, "prove-arm-success")
		writeCaptainMeta(t, parent, "prove-arm-success", home, "w1")
		probe := &finalBlockingProbeEndpoint{started: make(chan struct{}), release: make(chan struct{})}
		result := make(chan struct {
			proven bool
			err    error
		}, 1)
		go func() {
			proven, err := proveRelaunch(parent, Info{ID: "prove-arm-success", Home: home}, probe, func(time.Duration) {}, time.Now)
			result <- struct {
				proven bool
				err    error
			}{proven, err}
		}()
		<-probe.started
		if err := mhome.UpdateMeta(parent, taskIDForCaptain("prove-arm-success"), func(meta map[string]string) error {
			meta["sentinel"] = "preserved"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		close(probe.release)
		out := <-result
		if out.proven || out.err != nil {
			t.Fatalf("prove arm result = proven=%v err=%v", out.proven, out.err)
		}
		meta, err := mhome.ReadMeta(parent, taskIDForCaptain("prove-arm-success"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("prove-arm metadata: %v", meta)
		if meta["sentinel"] != "preserved" || meta["relaunch_liveness"] != "unproven" {
			t.Fatalf("armed metadata = %v, want sentinel and unproven guard", meta)
		}
	})

	t.Run("send nudge stamps and preserves concurrent metadata", func(t *testing.T) {
		parent, home, id, _ := newGuardNudgeValidFixture(t)
		endpoint := &blockingNudgeEndpoint{started: make(chan struct{}), release: make(chan struct{})}
		result := make(chan error, 1)
		go func() { result <- sendNudge(parent, Info{ID: id, Home: home}, endpoint) }()
		<-endpoint.started
		if err := mhome.UpdateMeta(parent, taskIDForCaptain(id), func(meta map[string]string) error {
			meta["sentinel"] = "preserved"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		close(endpoint.release)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		meta, err := mhome.ReadMeta(parent, taskIDForCaptain(id))
		if err != nil {
			t.Fatal(err)
		}
		marker, err := readNudgeMarker(parent, id)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("send-nudge metadata: %v; marker after send: %v", meta, marker)
		if meta["sentinel"] != "preserved" || meta["applied_commit"] == "" || meta["applied_digest"] == "" {
			t.Fatalf("stamped metadata = %v", meta)
		}
		if marker != nil {
			t.Fatalf("marker = %v, want cleared", marker)
		}
	})
}

func TestSendNudgeRefusesReplacementBinding(t *testing.T) {
	parent, captainHome, id, _ := newGuardNudgeValidFixture(t)
	endpoint := &blockingNudgeEndpoint{started: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- sendNudge(parent, Info{ID: id, Home: captainHome}, endpoint) }()
	<-endpoint.started
	if err := mhome.UpdateMeta(parent, taskIDForCaptain(id), func(meta map[string]string) error {
		meta["window"], meta["backend"], meta["sentinel"] = "replacement-window", "replacement-backend", "preserved"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(endpoint.release)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "captain binding changed") {
		t.Fatalf("sendNudge error = %v, want binding-change refusal", err)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain(id))
	if err != nil {
		t.Fatal(err)
	}
	if meta["window"] != "replacement-window" || meta["backend"] != "replacement-backend" || meta["sentinel"] != "preserved" {
		t.Fatalf("replacement metadata changed: %v", meta)
	}
	marker, err := readNudgeMarker(parent, id)
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("nudge marker was cleared after binding refusal")
	}
	if _, ok := meta["applied_commit"]; ok {
		t.Fatal("replacement received applied_commit")
	}
	if _, ok := meta["applied_digest"]; ok {
		t.Fatal("replacement received applied_digest")
	}
}

func TestProveRelaunchRefusesReplacementBinding(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-probe")
	writeCaptainMeta(t, parent, "atomic-probe", captainHome, "w1")
	writeRelaunchGuard(t, parent, "atomic-probe", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	probe := &blockingProbeEndpoint{started: make(chan struct{}), release: make(chan struct{}), result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}
	result := make(chan struct {
		proven bool
		err    error
	}, 1)
	go func() {
		proven, err := proveRelaunch(parent, Info{ID: "atomic-probe", Home: captainHome}, probe, func(time.Duration) {}, time.Now)
		result <- struct {
			proven bool
			err    error
		}{proven, err}
	}()
	<-probe.started
	if err := mhome.UpdateMeta(parent, taskIDForCaptain("atomic-probe"), func(meta map[string]string) error {
		meta["window"], meta["backend"] = "replacement-window", "replacement-backend"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(probe.release)
	out := <-result
	if out.proven || out.err == nil || !strings.Contains(out.err.Error(), "captain binding changed") {
		t.Fatalf("proveRelaunch result = proven=%v err=%v, want binding-change refusal", out.proven, out.err)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("atomic-probe"))
	if err != nil {
		t.Fatal(err)
	}
	if meta["window"] != "replacement-window" || meta["backend"] != "replacement-backend" || meta["relaunch_liveness"] != "unproven" {
		t.Fatalf("replacement recovery metadata changed: %v", meta)
	}
}

func TestProveRelaunchRefusesReplacementBindingOnArm(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-arm-replacement")
	writeCaptainMeta(t, parent, "atomic-arm-replacement", captainHome, "w1")
	probe := &finalBlockingProbeEndpoint{started: make(chan struct{}), release: make(chan struct{})}
	result := make(chan struct {
		proven bool
		err    error
	}, 1)
	go func() {
		proven, err := proveRelaunch(parent, Info{ID: "atomic-arm-replacement", Home: captainHome}, probe, func(time.Duration) {}, time.Now)
		result <- struct {
			proven bool
			err    error
		}{proven, err}
	}()
	<-probe.started
	if err := mhome.UpdateMeta(parent, taskIDForCaptain("atomic-arm-replacement"), func(meta map[string]string) error {
		meta["window"], meta["backend"] = "replacement-window", "replacement-backend"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(probe.release)
	out := <-result
	if out.proven || out.err == nil || !strings.Contains(out.err.Error(), "captain binding changed") {
		t.Fatalf("proveRelaunch result = proven=%v err=%v, want binding-change refusal", out.proven, out.err)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("atomic-arm-replacement"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("replacement received relaunch_liveness: %v", meta)
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("replacement received %s: %v", relaunchGuardUntilField, meta)
	}
}

func TestClearRelaunchGuardRefusesReplacementBinding(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-clear-replacement")
	writeCaptainMeta(t, parent, "atomic-clear-replacement", captainHome, "w1")
	writeRelaunchGuard(t, parent, "atomic-clear-replacement", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	probe := &blockingProbeEndpoint{started: make(chan struct{}), release: make(chan struct{}), result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Probe: probe}}
	result := make(chan StepResult, 1)
	go func() { result <- tx.stepRelaunch(parent, Info{ID: "atomic-clear-replacement", Home: captainHome}) }()
	<-probe.started
	if err := mhome.UpdateMeta(parent, taskIDForCaptain("atomic-clear-replacement"), func(meta map[string]string) error {
		meta["window"], meta["backend"] = "replacement-window", "replacement-backend"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(probe.release)
	step := <-result
	if step.State != StepFailed || !strings.Contains(step.Detail, "captain binding changed") {
		t.Fatalf("step = %+v, want binding-change refusal", step)
	}
	meta, err := mhome.ReadMeta(parent, taskIDForCaptain("atomic-clear-replacement"))
	if err != nil {
		t.Fatal(err)
	}
	if meta["relaunch_liveness"] != "unproven" {
		t.Fatalf("replacement guard changed: %v", meta)
	}
}

func TestConsultRelaunchGuardRefusesForeignMeta(t *testing.T) {
	parent := t.TempDir()
	taskID := taskIDForCaptain("foreign-consult")
	if err := mhome.WriteMeta(parent, taskID, map[string]string{"kind": "soldier"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	refused, _, err := consultRelaunchGuard(parent, taskID, time.Now())
	if err == nil {
		t.Fatal("consultRelaunchGuard accepted a meta with kind=soldier")
	}
	if refused {
		t.Fatal("a refused consult reported refused=true, which callers read as an armed guard")
	}
	if !strings.Contains(err.Error(), "expected captain") {
		t.Errorf("error = %v, want it to name the expected kind", err)
	}
}

// TestProveRelaunchRefusesForeignMetaOnArm proves the arm path refuses a task
// meta that is not a captain's instead of creating guard state on it.
func TestProveRelaunchRefusesForeignMetaOnArm(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "foreign-arm")
	if err := mhome.WriteMeta(parent, taskIDForCaptain("foreign-arm"), map[string]string{"kind": "soldier"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	proven, err := proveRelaunch(parent, Info{ID: "foreign-arm", Home: captainHome},
		&testProbeEndpoint{result: CaptainProbeResult{Absent: true}},
		func(time.Duration) {}, time.Now)
	if proven {
		t.Fatal("proveRelaunch proved liveness against an absent endpoint")
	}
	if err == nil || !strings.Contains(err.Error(), "expected captain") {
		t.Fatalf("error = %v, want a refusal naming the expected kind", err)
	}
	meta, readErr := mhome.ReadMeta(parent, taskIDForCaptain("foreign-arm"))
	if readErr != nil {
		t.Fatalf("ReadMeta: %v", readErr)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Error("a refused arm wrote guard state onto a foreign meta")
	}
}

// TestClearRelaunchGuardLeavesUnarmedMetaAlone proves an unarmed guard writes
// nothing at all, so the clear that runs on every proven-alive captain is a
// read and not a rewrite of its meta.
func TestClearRelaunchGuardLeavesUnarmedMetaAlone(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "unarmed-clear")
	writeCaptainMeta(t, parent, "unarmed-clear", captainHome, "w1")
	p, err := mhome.MetaFilePath(parent, taskIDForCaptain("unarmed-clear"))
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat meta: %v", err)
	}
	beforeBody, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta: %v", err)
	}

	if err := clearRelaunchGuard(parent, Info{ID: "unarmed-clear"}, captainBinding{}); err != nil {
		t.Fatalf("clearRelaunchGuard: %v", err)
	}

	after, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat meta back: %v", err)
	}
	afterBody, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta back: %v", err)
	}
	if string(beforeBody) != string(afterBody) {
		t.Fatalf("unarmed clear changed meta: before=%q after=%q", beforeBody, afterBody)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("unarmed clear replaced the meta file: mtime %s -> %s", before.ModTime(), after.ModTime())
	}
}

// TestClearRelaunchGuardOnAbsentMetaWritesNothing proves a captain with no task
// meta is left with none, rather than having one created by the clear that runs
// on every proven-alive captain.
func TestClearRelaunchGuardOnAbsentMetaWritesNothing(t *testing.T) {
	parent := t.TempDir()
	taskID := taskIDForCaptain("absent-clear")
	p, err := mhome.MetaFilePath(parent, taskID)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}

	if err := clearRelaunchGuard(parent, Info{ID: "absent-clear"}, captainBinding{}); err != nil {
		t.Fatalf("clearRelaunchGuard: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Fatalf("os.Stat(%s) = %v, want not-exist: the clear created a meta", p, statErr)
	}
}
