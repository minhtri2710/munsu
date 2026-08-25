//go:build windows

package fleet

import (
	"os"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestDurableAppendStatusEncodedIDRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	id := "rel.1"
	appended, err := durableAppendStatus(tmp, id, "done: first")
	if err != nil || !appended {
		t.Fatalf("first append = (%v, %v), want (true, nil)", appended, err)
	}
	lines, err := home.ReadStatus(tmp, id)
	if err != nil || len(lines) != 1 || lines[0] != "done: first" {
		t.Fatalf("encoded read after append = %v (%v), want [done: first]", lines, err)
	}
	appended2, err := durableAppendStatus(tmp, id, "done: first")
	if err != nil || appended2 {
		t.Fatalf("duplicate append = (%v, %v), want (false, nil) dedup via encoded read", appended2, err)
	}
	sp, err := home.StatusFilePath(tmp, id)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sp); err != nil || string(data) != "done: first\n" {
		t.Fatalf("encoded status file %q = %q (%v), want %q", sp, data, err, "done: first\n")
	}
}

func TestSnapshotEncodedIDStatusReads(t *testing.T) {
	homeDir, _ := canonicalHomeWithTask(t, "rel.1")
	if err := home.WriteMeta(homeDir, "rel.1", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "rel.1", "working [key=k1]: seeded"); err != nil {
		t.Fatal(err)
	}
	snap, err := Snapshot(homeDir, SnapshotDependencies{CurrentState: NewCanonicalCurrentState()})
	if err != nil {
		t.Fatalf("Snapshot with encoded id: %v", err)
	}
	var found *TaskSnapshot
	for i := range snap.Tasks {
		if snap.Tasks[i].ID == "rel.1" {
			found = &snap.Tasks[i]
		}
	}
	if found == nil {
		t.Fatalf("snapshot has no rel.1 task: %+v", snap.Tasks)
	}
	if found.LastStatus != "working [key=k1]: seeded" {
		t.Errorf("LastStatus = %q, want %q", found.LastStatus, "working [key=k1]: seeded")
	}
	if len(found.OpenActivities) != 1 || found.OpenActivities[0].Key != "k1" {
		t.Errorf("OpenActivities = %+v, want one key k1", found.OpenActivities)
	}
}

func TestTeardownCleanupTargetsEncodedProjections(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	id := "rel.1"
	meta := map[string]string{"kind": "ship"}
	if err := home.WriteMeta(tmp, id, meta); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, id, "working: seeded"); err != nil {
		t.Fatal(err)
	}

	metaPath, err := taskMetaFilePath(tmp, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("removing teardown meta target %q: %v", metaPath, err)
	}
	residualPaths, err := cleanupResidualArtifactPaths(tmp, id, meta)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range residualPaths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			t.Fatalf("removing teardown residual target %q: %v", p, err)
		}
	}

	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("encoded meta %q still present after teardown cleanup", metaPath)
	}
	statusPath, err := home.StatusFilePath(tmp, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statusPath); !os.IsNotExist(err) {
		t.Fatalf("encoded status %q still present after teardown cleanup", statusPath)
	}
	if _, err := Snapshot(tmp, SnapshotDependencies{CurrentState: NewCanonicalCurrentState()}); err != nil {
		t.Fatalf("snapshot after teardown cleanup = %v, want no fail-closed leftover", err)
	}
}
