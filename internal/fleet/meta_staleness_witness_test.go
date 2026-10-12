package fleet

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// TestMetaMtimeWitnessAdvancesAfterStep is the one table for the fleet steps
// that advance a task's .meta mtime, the staleness witness of
// orchestrator.AcceptOrRefuseStale. Each step has a row that refuses a .check
// written before the step and a row that accepts a .check written after it.
func TestMetaMtimeWitnessAdvancesAfterStep(t *testing.T) {
	for _, s := range []struct {
		name  string
		setup func(*testing.T) (homeDir, taskID string, step func() error)
	}{
		{"spawn after ConfirmSpawn", spawnWitnessStep},
		{"acknowledged captain nudge", nudgeWitnessStep},
	} {
		t.Run(s.name+"/blocks", func(t *testing.T) {
			homeDir, taskID, step := s.setup(t)
			metaPath, checkPath := witnessPaths(t, homeDir, taskID)
			past := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(metaPath, past, past); err != nil {
				t.Fatal(err)
			}
			writeWitnessCheck(t, checkPath, past.Add(time.Hour))
			if err := orchestrator.AcceptOrRefuseStale(checkPath); err != nil {
				t.Fatalf("check newer than meta before the step: %v, want accepted", err)
			}
			if err := step(); err != nil {
				t.Fatalf("step: %v", err)
			}
			err := orchestrator.AcceptOrRefuseStale(checkPath)
			if err == nil || !strings.Contains(err.Error(), "refused stale check (meta newer)") {
				t.Fatalf("check written before the step = %v, want the stale-check refusal", err)
			}
		})
		t.Run(s.name+"/allows", func(t *testing.T) {
			homeDir, taskID, step := s.setup(t)
			metaPath, checkPath := witnessPaths(t, homeDir, taskID)
			if err := step(); err != nil {
				t.Fatalf("step: %v", err)
			}
			fi, err := os.Stat(metaPath)
			if err != nil {
				t.Fatal(err)
			}
			writeWitnessCheck(t, checkPath, fi.ModTime().Add(time.Second))
			if err := orchestrator.AcceptOrRefuseStale(checkPath); err != nil {
				t.Fatalf("check written after the step = %v, want accepted", err)
			}
		})
	}
}

func spawnWitnessStep(t *testing.T) (string, string, func() error) {
	t.Helper()
	homeDir := t.TempDir()
	taskID := "spawn-witness"
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"window": "w1"}); err != nil {
		t.Fatal(err)
	}
	r := &Runner{homeDir: homeDir, args: Args{ID: taskID}}
	return homeDir, taskID, r.touchMetaAfterConfirm
}

func nudgeWitnessStep(t *testing.T) (string, string, func() error) {
	t.Helper()
	parentHome, captainHome, id, _ := newGuardNudgeValidFixture(t)
	endpoint := &testNudgeEndpoint{result: NudgeResult{Acknowledged: true}}
	return parentHome, taskIDForCaptain(id), func() error {
		return sendNudge(parentHome, Info{ID: id, Home: captainHome}, endpoint)
	}
}

// witnessPaths returns the task's .meta path and the companion .check path
// AcceptOrRefuseStale pairs with it.
func witnessPaths(t *testing.T, homeDir, taskID string) (string, string) {
	t.Helper()
	metaPath, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return metaPath, strings.TrimSuffix(metaPath, ".meta") + ".check"
}

func writeWitnessCheck(t *testing.T, checkPath string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(checkPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}
