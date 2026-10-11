package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// seatWorld is one watched home with a single live seat "seat-1".
type seatWorld struct {
	t    *testing.T
	home string
	got  []string
	err  error
}

func newSeatWorld(t *testing.T) *seatWorld {
	t.Helper()
	t.Setenv("PATH", "/usr/bin:/bin")
	w := &seatWorld{t: t, home: t.TempDir()}
	if err := mhome.WriteMeta(w.home, "seat-1", map[string]string{"window": "@w"}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *seatWorld) cfg(key, val string) {
	w.t.Helper()
	if err := config.Set(w.home, key, val); err != nil {
		w.t.Fatal(err)
	}
}

// rawCfg writes a config file the way a hand edit does, bypassing validation.
func (w *seatWorld) rawCfg(key, val string) {
	w.t.Helper()
	dir := filepath.Join(w.home, "config")
	if err := os.MkdirAll(dir, 0700); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key), []byte(val+"\n"), 0600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *seatWorld) say(line string, n int) {
	w.t.Helper()
	for i := 0; i < n; i++ {
		if err := mhome.AppendStatus(w.home, "seat-1", line); err != nil {
			w.t.Fatal(err)
		}
	}
}

// silentFor backdates the seat's status file, as if it last spoke d ago.
func (w *seatWorld) silentFor(d time.Duration) {
	w.t.Helper()
	p, err := mhome.StatusFilePath(w.home, "seat-1")
	if err != nil {
		w.t.Fatal(err)
	}
	past := time.Now().Add(-d)
	if err := os.Chtimes(p, past, past); err != nil {
		w.t.Fatal(err)
	}
}

// cycle runs one watcher cycle over a live pane and records the wakes it queued
// as "kind: payload", in queue order.
func (w *seatWorld) cycle() {
	w.t.Helper()
	resetRecovery()
	_, w.err = RunCycleWithProbeAndSender(w.home, aliveTaskProbe{}, testCycleSender{}, NoopWatcherHooks{}, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{})
	records, err := DrainWakes(w.home)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, r := range records {
		w.got = append(w.got, r.Kind+": "+r.Payload)
	}
}

func (w *seatWorld) cycles(n int) {
	w.t.Helper()
	for i := 0; i < n; i++ {
		w.cycle()
	}
}

const (
	idleWake   = "stale: pane @w idle beyond threshold"
	nudgeWake  = "stale: seat repeated the same failed status line (nudge: steer it)"
	endWake    = "stale: seat repeated the same failed status line (end: stuck, fail or relaunch it)"
	failedLine = "failed: cannot reach the build server"
)

// TestSeatLivenessIncidents pins the seat liveness rule: a silent seat, and a
// seat repeating one failing status line, each notify once per incident, and the
// thresholds come from config.
func TestSeatLivenessIncidents(t *testing.T) {
	rows := []struct {
		name    string
		steps   func(w *seatWorld)
		want    []string
		wantErr string
	}{
		{"silent-below-configured-threshold", func(w *seatWorld) {
			w.cfg("seat-silent-after", "10m")
			w.say("note: waiting for review", 1)
			w.silentFor(6 * time.Minute)
			w.cycle()
		}, nil, ""},
		{"silent-once-per-incident", func(w *seatWorld) {
			w.cfg("seat-silent-after", "10m")
			w.say("note: waiting for review", 1)
			w.silentFor(11 * time.Minute)
			w.cycles(3)
		}, []string{idleWake}, ""},
		{"silent-incident-resets-when-the-seat-speaks", func(w *seatWorld) {
			w.cfg("seat-silent-after", "10m")
			w.say("note: waiting for review", 1)
			w.silentFor(11 * time.Minute)
			w.cycle()
			w.say("note: waiting for review", 1)
			w.cycle()
			w.silentFor(11 * time.Minute)
			w.cycle()
		}, []string{idleWake, idleWake}, ""},
		{"repeat-below-nudge", func(w *seatWorld) {
			w.say(failedLine, 4)
			w.cycles(2)
		}, []string{"signal: " + failedLine}, ""},
		{"repeat-nudge-once", func(w *seatWorld) {
			w.say(failedLine, 1)
			w.cycle()
			w.say(failedLine, 4)
			w.cycles(3)
		}, []string{"signal: " + failedLine, nudgeWake}, ""},
		{"repeat-end-once-after-nudge", func(w *seatWorld) {
			w.say(failedLine, 1)
			w.cycle()
			w.say(failedLine, 4)
			w.cycle()
			w.say(failedLine, 3)
			w.cycles(3)
		}, []string{"signal: " + failedLine, nudgeWake, endWake}, ""},
		{"repeat-honors-configured-thresholds", func(w *seatWorld) {
			w.cfg("seat-repeat-nudge", "2")
			w.cfg("seat-repeat-end", "3")
			w.say(failedLine, 1)
			w.cycle()
			w.say(failedLine, 1)
			w.cycle()
			w.say(failedLine, 1)
			w.cycle()
		}, []string{"signal: " + failedLine, nudgeWake, endWake}, ""},
		{"first-sight-at-nudge", func(w *seatWorld) {
			w.say(failedLine, 5)
			w.cycles(2)
		}, []string{"signal: " + failedLine, nudgeWake}, ""},
		{"first-sight-at-end", func(w *seatWorld) {
			w.say(failedLine, 8)
			w.cycles(2)
		}, []string{"signal: " + failedLine, endWake}, ""},
		{"end-stays-sticky", func(w *seatWorld) {
			for i := 0; i < 12; i++ {
				w.say(failedLine, 1)
				w.cycle()
			}
		}, []string{"signal: " + failedLine, nudgeWake, endWake}, ""},
		{"repeat-ends-when-the-line-changes", func(w *seatWorld) {
			w.say(failedLine, 1)
			w.cycle()
			w.say(failedLine, 5)
			w.cycle()
			w.say("failed: the build server is down", 1)
			w.cycles(2)
		}, []string{"signal: " + failedLine, nudgeWake, "signal: failed: the build server is down"}, ""},
		{"repeat-counts-only-failed-and-blocked-lines", func(w *seatWorld) {
			w.say("needs-decision: pick a branch", 1)
			w.cycle()
			w.say("needs-decision: pick a branch", 8)
			w.cycles(2)
		}, []string{"signal: needs-decision: pick a branch"}, ""},
		{"repeat-names-the-blocked-verb", func(w *seatWorld) {
			w.say("blocked: waiting on credentials", 1)
			w.cycle()
			w.say("blocked: waiting on credentials", 4)
			w.cycle()
		}, []string{"signal: blocked: waiting on credentials", "stale: seat repeated the same blocked status line (nudge: steer it)"}, ""},
		{"invalid-threshold-fails-the-cycle-closed", func(w *seatWorld) {
			w.rawCfg("seat-repeat-end", "3")
			w.say(failedLine, 1)
			w.cycle()
		}, nil, `config seat-repeat-end 3: want an integer greater than seat-repeat-nudge 5`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			w := newSeatWorld(t)
			row.steps(w)
			if !slices.Equal(w.got, row.want) {
				t.Fatalf("wakes = %q, want %q", w.got, row.want)
			}
			switch {
			case row.wantErr == "" && w.err != nil:
				t.Fatalf("cycle error = %v, want none", w.err)
			case row.wantErr != "" && (w.err == nil || !strings.Contains(w.err.Error(), row.wantErr)):
				t.Fatalf("cycle error = %v, want it to contain %q", w.err, row.wantErr)
			}
		})
	}
}

// TestSeatLivenessIgnoresRepeatsOfAnUplinkReport pins the report-owned exception:
// a failed line the uplink already delivered as its own wake is not a repeat
// incident, however many times it is appended.
func TestSeatLivenessIgnoresRepeatsOfAnUplinkReport(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	generalHome, identity := directGeneralHome(t)
	reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
	if _, _, err := AppendReportStatus(generalHome, "direct-task", "failed", "direct dispatch failure", "default"); err != nil {
		t.Fatal(err)
	}
	lines, err := mhome.ReadStatus(generalHome, "direct-task")
	if err != nil || len(lines) == 0 {
		t.Fatalf("ReadStatus = %q, %v", lines, err)
	}
	for i := 0; i < 8; i++ {
		if err := mhome.AppendStatus(generalHome, "direct-task", lines[len(lines)-1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DrainWakes(generalHome); err != nil {
		t.Fatal(err)
	}
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(generalHome, aliveTaskProbe{}, testCycleSender{}, NoopWatcherHooks{}, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatal(err)
	}
	records, err := DrainWakes(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("wakes = %#v, want none for a repeated uplink-report line", records)
	}
}

// TestAfkBeatAlarmOncePerIncident pins the dead-watcher alarm: one wedge entry
// per incident, and a new one after the beat has been fresh again.
func TestAfkBeatAlarmOncePerIncident(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	writeBeat := func(t *testing.T, h string, age time.Duration) {
		t.Helper()
		p := mhome.WatcherBeatPath(h)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(fmt.Sprintf("%d 1", now.Add(-age).Unix())), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wedges := func(d *Daemon) []string {
		var out []string
		for _, e := range d.digester.entries {
			out = append(out, fmt.Sprintf("%s: %s", e.Type, e.Payload))
		}
		return out
	}
	rows := []struct {
		name  string
		steps func(t *testing.T, d *Daemon)
		want  []string
	}{
		{"stale-beat-alarms-once", func(t *testing.T, d *Daemon) {
			writeBeat(t, d.homeDir, 10*time.Minute)
			for i := 0; i < 3; i++ {
				d.checkBeat(now.Add(time.Duration(i) * 30 * time.Second))
			}
		}, []string{"wedge: watcher beat stale: age=10m0s threshold=5m0s"}},
		{"fresh-beat-never-alarms", func(t *testing.T, d *Daemon) {
			writeBeat(t, d.homeDir, time.Second)
			d.checkBeat(now)
		}, nil},
		{"missing-beat-stays-quiet-within-the-grace", func(t *testing.T, d *Daemon) {
			d.checkBeat(now.Add(StaleThreshold() - time.Second))
		}, nil},
		{"missing-beat-alarms-once-after-the-grace", func(t *testing.T, d *Daemon) {
			d.checkBeat(now.Add(StaleThreshold()))
			d.checkBeat(now.Add(StaleThreshold() + 30*time.Second))
		}, []string{"wedge: watcher beat never set"}},
		{"incident-ends-when-the-beat-is-fresh", func(t *testing.T, d *Daemon) {
			writeBeat(t, d.homeDir, 10*time.Minute)
			d.checkBeat(now)
			writeBeat(t, d.homeDir, time.Second)
			d.checkBeat(now.Add(time.Second))
			writeBeat(t, d.homeDir, 10*time.Minute)
			d.checkBeat(now.Add(2 * time.Second))
		}, []string{
			"wedge: watcher beat stale: age=10m0s threshold=5m0s",
			"wedge: watcher beat stale: age=10m2s threshold=5m0s",
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			h := t.TempDir()
			d := &Daemon{homeDir: h, digester: NewDigester(h), startedAt: now}
			row.steps(t, d)
			if got := wedges(d); !slices.Equal(got, row.want) {
				t.Fatalf("digest entries = %q, want %q", got, row.want)
			}
		})
	}
}
