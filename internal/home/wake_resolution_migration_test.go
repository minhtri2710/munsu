package home

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyWakeResolutionBlocksAffectedWritesWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(home string) error
	}{
		{name: "resolve", act: func(home string) error { return ResolveWake(home, "lease-1", "100:1", "again") }},
		{name: "claim", act: func(home string) error {
			_, err := ClaimWakes(home, "consumer", 60, 1)
			return err
		}},
		{name: "reclaim", act: func(home string) error {
			_, err := ReclaimExpiredLeases(home)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "general")
			legacy := "100\tlease-1\t100:1\tchecked\n"
			writeLegacyWakeResolutions(t, home, legacy)
			if err := os.WriteFile(WakeQueuePath(home), []byte("101\t1\tsignal\ttask\tpayload\n"), 0644); err != nil {
				t.Fatal(err)
			}

			before := snapshotTree(t, home)
			err := tc.act(home)
			if err == nil {
				t.Fatalf("%s succeeded with legacy wake resolution state", tc.name)
			}
			want := "legacy wake resolution state requires migration; run: munsu migrate wake-resolutions plan --home '" + home + "' --plan-out '" + filepath.Join(os.TempDir(), "munsu-wake-resolution-general.plan.json") + "'"
			if err.Error() != want {
				t.Fatalf("%s error = %q, want %q", tc.name, err.Error(), want)
			}
			after := snapshotTree(t, home)
			if before != after {
				t.Fatalf("legacy detection mutated home\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestWakeResolutionMigrationRejectsInvalidSourcesBeforeStaging(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{name: "corrupt", data: "100\tlease-1\t100:1\tchecked\ncorrupt\n", want: "invalid legacy wake resolution record"},
		{name: "output collision", data: "100\tlease/1\t100:1\tchecked\n101\tlease_1\t100:1\tchecked\n", want: "filename collision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "general")
			writeLegacyWakeResolutions(t, home, tc.data)
			if _, err := PlanWakeResolutionMigration(home); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("PlanWakeResolutionMigration error = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(home, "state", ".wake-resolution-migration")); !os.IsNotExist(err) {
				t.Fatalf("planning invalid source created migration state: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(home, wakeResolutionDir))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.data {
				t.Fatalf("invalid source changed: %q", data)
			}
		})
	}
}

func TestWakeResolutionMigrationArchivesInstallsReceiptsAndResumes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "general")
	legacy := "100\tlease-1\t100:1\tchecked\n101\tlease-2\t101:1\tsecond\n"
	writeLegacyWakeResolutions(t, home, legacy)

	plan, err := PlanWakeResolutionMigration(home)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceDigest != sha256Hex([]byte(legacy)) || plan.HomeDir != mustEvalSymlinks(t, home) || plan.RecordCount != len(plan.Outputs) {
		t.Fatalf("bad plan = %+v", plan)
	}
	if plan.OutputManifestDigest == "" || plan.Outputs[0].Digest == "" {
		t.Fatalf("plan missing output manifest evidence: %+v", plan)
	}

	receipt, err := ApplyWakeResolutionMigration(plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.SourceDigest != plan.SourceDigest || receipt.HomeIdentity != plan.HomeIdentity || receipt.RecordCount != 2 || receipt.OutputManifestDigest != plan.OutputManifestDigest {
		t.Fatalf("receipt = %+v, plan = %+v", receipt, plan)
	}
	if got, err := readWakeResolution(home, "lease-1", "100:1"); err != nil || got.State != "completed" || got.Summary != "checked" {
		t.Fatalf("migrated record = %+v err=%v", got, err)
	}
	archiveBytes, err := os.ReadFile(filepath.Join(home, "state", ".wake-resolution-migration", "archive", plan.SourceDigest+".legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if string(archiveBytes) != legacy {
		t.Fatalf("archive = %q, want exact legacy source", archiveBytes)
	}

	again, err := ApplyWakeResolutionMigration(plan)
	if err != nil {
		t.Fatalf("idempotent reapply failed: %v", err)
	}
	if again.CompletedAt != receipt.CompletedAt || again.SourceDigest != receipt.SourceDigest {
		t.Fatalf("idempotent reapply changed receipt: before=%+v after=%+v", receipt, again)
	}
}

func TestWakeResolutionMigrationAppliesExactPlan(t *testing.T) {
	home := filepath.Join(t.TempDir(), "general")
	writeLegacyWakeResolutions(t, home, "100\tlease-1\t100:1\tchecked\n")
	plan, err := PlanWakeResolutionMigration(home)
	if err != nil {
		t.Fatal(err)
	}
	writeLegacyWakeResolutions(t, home, "100\tlease-1\t100:1\tchanged\n")
	if _, err := ApplyWakeResolutionMigration(plan); err == nil || !strings.Contains(err.Error(), "source digest changed") {
		t.Fatalf("ApplyWakeResolutionMigration error = %v, want source digest changed", err)
	}
}

func TestWakeResolutionMigrationRejectsTamperedPlans(t *testing.T) {
	home := filepath.Join(t.TempDir(), "general")
	writeLegacyWakeResolutions(t, home, "100\tlease-1\t100:1\tchecked\n")
	base, err := PlanWakeResolutionMigration(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyWakeResolutionMigration(nil); err == nil || !strings.Contains(err.Error(), "plan is required") {
		t.Fatalf("nil plan error = %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(plan *WakeResolutionMigrationPlan)
		want   string
	}{
		{name: "foreign source", want: "source path mismatch", mutate: func(plan *WakeResolutionMigrationPlan) {
			plan.SourcePath = filepath.Join(t.TempDir(), "state", ".wake-resolutions")
		}},
		{name: "traversal output", want: "invalid output filename", mutate: func(plan *WakeResolutionMigrationPlan) {
			plan.Outputs[0].FileName = "../evil.json"
		}},
		{name: "altered manifest", want: "output manifest digest mismatch", mutate: func(plan *WakeResolutionMigrationPlan) {
			plan.OutputManifestDigest = strings.Repeat("0", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := cloneWakeResolutionPlan(t, base)
			tc.mutate(plan)
			if _, err := ApplyWakeResolutionMigration(plan); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ApplyWakeResolutionMigration error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWakeResolutionMigrationRevalidatesIdentityAndSymlinkTarget(t *testing.T) {
	t.Run("home identity", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "captain")
		if err := os.MkdirAll(home, 0755); err != nil {
			t.Fatal(err)
		}
		if err := WriteHomeIdentity(home, "captain-a", RankCaptain); err != nil {
			t.Fatal(err)
		}
		writeLegacyWakeResolutions(t, home, "100\tlease-1\t100:1\tchecked\n")
		plan, err := PlanWakeResolutionMigration(home)
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteHomeIdentity(home, "captain-b", RankCaptain); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyWakeResolutionMigration(plan); err == nil || !strings.Contains(err.Error(), "home identity changed") {
			t.Fatalf("ApplyWakeResolutionMigration error = %v, want home identity changed", err)
		}
	})

	t.Run("symlink retarget", func(t *testing.T) {
		root := t.TempDir()
		a := filepath.Join(root, "a")
		b := filepath.Join(root, "b")
		link := filepath.Join(root, "link")
		writeLegacyWakeResolutions(t, a, "100\tlease-1\t100:1\tchecked\n")
		writeLegacyWakeResolutions(t, b, "100\tlease-1\t100:1\tchecked\n")
		if err := os.Symlink(a, link); err != nil {
			t.Fatal(err)
		}
		plan, err := PlanWakeResolutionMigration(link)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(b, link); err != nil {
			t.Fatal(err)
		}
		plan.HomeDir = link
		if _, err := ApplyWakeResolutionMigration(plan); err == nil || !strings.Contains(err.Error(), "home identity changed") {
			t.Fatalf("ApplyWakeResolutionMigration error = %v, want symlink identity rejection", err)
		}
	})
}

func TestWakeResolutionMigrationRejectsCorruptIdempotentEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, home string, plan *WakeResolutionMigrationPlan, receipt *WakeResolutionMigrationReceipt)
		want   string
	}{
		{name: "archive missing", want: "archive", mutate: func(t *testing.T, home string, plan *WakeResolutionMigrationPlan, receipt *WakeResolutionMigrationReceipt) {
			if err := os.Remove(receipt.ArchivePath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "archive changed", want: "archive digest mismatch", mutate: func(t *testing.T, home string, plan *WakeResolutionMigrationPlan, receipt *WakeResolutionMigrationReceipt) {
			if err := os.WriteFile(receipt.ArchivePath, []byte("changed"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "output changed", want: "digest mismatch", mutate: func(t *testing.T, home string, plan *WakeResolutionMigrationPlan, receipt *WakeResolutionMigrationReceipt) {
			path := filepath.Join(home, wakeResolutionDir, plan.Outputs[0].FileName)
			if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "extra output", want: "unexpected output file", mutate: func(t *testing.T, home string, plan *WakeResolutionMigrationPlan, receipt *WakeResolutionMigrationReceipt) {
			if err := os.WriteFile(filepath.Join(home, wakeResolutionDir, "extra.json"), []byte("{}\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "general")
			writeLegacyWakeResolutions(t, home, "100\tlease-1\t100:1\tchecked\n")
			plan, err := PlanWakeResolutionMigration(home)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := ApplyWakeResolutionMigration(plan)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, home, plan, receipt)
			if _, err := ApplyWakeResolutionMigration(plan); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ApplyWakeResolutionMigration error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWakeResolutionMigrationCrashResume(t *testing.T) {
	for _, phase := range []string{"archive", "stage", "install"} {
		t.Run(phase, func(t *testing.T) {
			t.Cleanup(func() { wakeResolutionMigrationCrashAfter = "" })
			home := filepath.Join(t.TempDir(), "general")
			legacy := "100\tlease-1\t100:1\tchecked\n"
			writeLegacyWakeResolutions(t, home, legacy)
			plan, err := PlanWakeResolutionMigration(home)
			if err != nil {
				t.Fatal(err)
			}

			wakeResolutionMigrationCrashAfter = phase
			if _, err := ApplyWakeResolutionMigration(plan); err == nil || !strings.Contains(err.Error(), "injected crash after "+phase) {
				t.Fatalf("ApplyWakeResolutionMigration crash error = %v", err)
			}
			wakeResolutionMigrationCrashAfter = ""

			if _, err := os.Stat(filepath.Join(home, "state", ".wake-resolution-migration", "receipt.json")); !os.IsNotExist(err) {
				t.Fatalf("crash wrote receipt unexpectedly: %v", err)
			}
			archivePath := filepath.Join(home, "state", ".wake-resolution-migration", "archive", plan.SourceDigest+".legacy")
			archiveBytes, err := os.ReadFile(archivePath)
			if err != nil || string(archiveBytes) != legacy {
				t.Fatalf("archive after crash = %q err=%v", archiveBytes, err)
			}
			if phase == "archive" || phase == "stage" {
				sourceBytes, err := os.ReadFile(filepath.Join(home, wakeResolutionDir))
				if err != nil || string(sourceBytes) != legacy {
					t.Fatalf("source after %s crash = %q err=%v", phase, sourceBytes, err)
				}
			}
			if phase == "install" {
				if err := verifyWakeResolutionDirectoryExact(filepath.Join(home, wakeResolutionDir), plan.Outputs); err != nil {
					t.Fatalf("installed output after crash: %v", err)
				}
			}

			receipt, err := ApplyWakeResolutionMigration(plan)
			if err != nil {
				t.Fatalf("resume after crash failed: %v", err)
			}
			if receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != 1 {
				t.Fatalf("resume receipt = %+v", receipt)
			}
		})
	}
}

func writeLegacyWakeResolutions(t *testing.T, home, data string) {
	t.Helper()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, wakeResolutionDir)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b.WriteString(rel)
		if d.IsDir() {
			b.WriteString("/\n")
			return nil
		}
		b.WriteString(" ")
		b.WriteString(sha256HexFile(t, path))
		b.WriteString(" ")
		b.WriteString(info.Mode().String())
		b.WriteString("\n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	canon, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canon
}

func sha256HexFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256Hex(data)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneWakeResolutionPlan(t *testing.T, plan *WakeResolutionMigrationPlan) *WakeResolutionMigrationPlan {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var cloned WakeResolutionMigrationPlan
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return &cloned
}
