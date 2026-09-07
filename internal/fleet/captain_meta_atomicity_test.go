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
}

func (e *blockingProbeEndpoint) Probe(string, map[string]string) (CaptainProbeResult, error) {
	close(e.started)
	<-e.release
	return CaptainProbeResult{PaneAlive: true, AgentAlive: true}, nil
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
	probe := &blockingProbeEndpoint{started: make(chan struct{}), release: make(chan struct{})}
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

	if err := clearRelaunchGuard(parent, taskIDForCaptain("unarmed-clear")); err != nil {
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

	if err := clearRelaunchGuard(parent, taskID); err != nil {
		t.Fatalf("clearRelaunchGuard: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Fatalf("os.Stat(%s) = %v, want not-exist: the clear created a meta", p, statErr)
	}
}
