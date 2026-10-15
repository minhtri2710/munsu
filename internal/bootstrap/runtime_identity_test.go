package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

func TestCollectRuntimeIdentityIncludesExecutableDigestAndBuildProvenance(t *testing.T) {
	home := t.TempDir()
	exe := writeExecutable(t, home, "bin/munsu", "running-binary")

	id := collectRuntimeIdentity(home, home, "0.9.0-test", runtimeIdentityProbe{
		executable: func() (string, error) { return exe, nil },
		lookPath:   func(string) (string, error) { return exe, nil },
		buildInfo: func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/minhtri2710/munsu", Version: "v0.9.0"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123456789"},
					{Key: "vcs.time", Value: "2026-07-30T00:00:00Z"},
					{Key: "vcs.modified", Value: "false"},
				},
			}, true
		},
	})

	if id.RunningExecutable.Path != exe {
		t.Fatalf("running path = %q, want %q", id.RunningExecutable.Path, exe)
	}
	if id.RunningExecutable.Digest == "" {
		t.Fatal("running executable digest is empty")
	}
	if id.PATHExecutable.Path != exe || id.PATHExecutable.Digest != id.RunningExecutable.Digest {
		t.Fatalf("PATH executable = %+v, running = %+v", id.PATHExecutable, id.RunningExecutable)
	}
	if id.ProtocolVersion != orchestrator.ProtocolVersion {
		t.Fatalf("protocol version = %d, want %d", id.ProtocolVersion, orchestrator.ProtocolVersion)
	}
	if id.Build.CLIVersion != "0.9.0-test" || id.Build.ModulePath != "github.com/minhtri2710/munsu" || id.Build.ModuleVersion != "v0.9.0" {
		t.Fatalf("build provenance missing module info: %+v", id.Build)
	}
	if id.Build.VCSRevision != "abc123456789" || id.Build.VCSModified {
		t.Fatalf("build provenance missing VCS info: %+v", id.Build)
	}
	if len(id.Skew) != 0 {
		t.Fatalf("unexpected skew: %+v", id.Skew)
	}
}

func TestCollectRuntimeIdentityClassifiesSkewWithExactRemediation(t *testing.T) {
	home := t.TempDir()
	running := writeExecutable(t, home, "current/munsu", "current-binary")
	shadow := writeExecutable(t, home, "old/munsu", "old-binary")

	tests := []struct {
		name   string
		probe  runtimeIdentityProbe
		want   SkewClassification
		fixHas string
	}{
		{
			name: "path shadowing",
			probe: runtimeIdentityProbe{
				executable: func() (string, error) { return running, nil },
				lookPath:   func(string) (string, error) { return shadow, nil },
				buildInfo:  cleanBuildInfo,
			},
			want:   SkewPathShadowing,
			fixHas: "Update PATH so " + running + " is selected before " + shadow,
		},
		{
			name: "dirty build",
			probe: runtimeIdentityProbe{
				executable: func() (string, error) { return running, nil },
				lookPath:   func(string) (string, error) { return running, nil },
				buildInfo: func() (*debug.BuildInfo, bool) {
					bi, _ := cleanBuildInfo()
					bi.Settings = append(bi.Settings[:2], debug.BuildSetting{Key: "vcs.modified", Value: "true"})
					return bi, true
				},
			},
			want:   SkewDirtyOrUnverifiableBuild,
			fixHas: "Rebuild munsu from a clean checkout",
		},
		{
			name: "packaged install",
			probe: runtimeIdentityProbe{
				executable: func() (string, error) { return running, nil },
				lookPath:   func(string) (string, error) { return running, nil },
				buildInfo: func() (*debug.BuildInfo, bool) {
					return &debug.BuildInfo{Main: debug.Module{Path: "github.com/minhtri2710/munsu", Version: "v1.2.3"}}, true
				},
			},
			want:   SkewPackagedInstall,
			fixHas: "No mutation required; install from source if commit-level provenance is required",
		},
		{
			name: "watcher mismatch",
			probe: runtimeIdentityProbe{
				executable: func() (string, error) { return running, nil },
				lookPath:   func(string) (string, error) { return running, nil },
				buildInfo:  cleanBuildInfo,
				readWatcher: func(string) *orchestrator.WatcherIdentity {
					return &orchestrator.WatcherIdentity{Home: home, PID: 123, Executable: shadow, BuildVersion: "old", ProtocolVersion: 1, CommitSHA: "def999"}
				},
				validateWatcher: func(string, int) bool { return true },
			},
			want:   SkewWatcherMismatch,
			fixHas: "munsu watch ensure --restart",
		},
		{
			name: "integration mismatch",
			probe: runtimeIdentityProbe{
				executable: func() (string, error) { return running, nil },
				lookPath:   func(string) (string, error) { return running, nil },
				buildInfo:  cleanBuildInfo,
				integrationStatus: func(string, string, string, Scope) (*IntegrationResult, error) {
					if strings.TrimSpace(string(ScopeProject)) == "" {
						t.Fatal("scope not supplied")
					}
					return &IntegrationResult{Harness: "pi", Scope: ScopeProject, State: "drifted", Drifted: true, Message: "digest mismatch"}, nil
				},
			},
			want:   SkewIntegrationMismatch,
			fixHas: "munsu integrate repair --harness pi --scope project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := collectRuntimeIdentity(home, home, "0.9.0-test", tt.probe)
			finding := findSkew(id.Skew, tt.want)
			if finding == nil {
				t.Fatalf("missing skew %q in %+v", tt.want, id.Skew)
			}
			if !strings.Contains(finding.Remediation, tt.fixHas) {
				t.Fatalf("remediation = %q, want containing %q", finding.Remediation, tt.fixHas)
			}
		})
	}
}

func TestCollectRuntimeIdentityObservesCaptainWatcherMismatch(t *testing.T) {
	home := t.TempDir()
	captainHome := t.TempDir()
	running := writeExecutable(t, home, "bin/munsu", "current")
	old := writeExecutable(t, home, "old/munsu", "old")
	oldDigest, err := fileDigest(old)
	if err != nil {
		t.Fatal(err)
	}

	id := collectRuntimeIdentity(home, home, "0.9.0-test", runtimeIdentityProbe{
		executable: func() (string, error) { return running, nil },
		lookPath:   func(string) (string, error) { return running, nil },
		buildInfo:  cleanBuildInfo,
		listCaptains: func(string) ([]fleet.Info, error) {
			return []fleet.Info{{ID: "alpha", Home: captainHome}}, nil
		},
		readWatcher: func(h string) *orchestrator.WatcherIdentity {
			if h == captainHome {
				return &orchestrator.WatcherIdentity{Home: captainHome, PID: 456, Executable: old, BuildVersion: "old", ProtocolVersion: 1, CommitSHA: "def999"}
			}
			return nil
		},
		validateWatcher: func(string, int) bool { return true },
		git: func(dir string, args ...string) (string, error) {
			if strings.Join(args, " ") == "rev-parse --short=12 HEAD" {
				return "caprev123456", nil
			}
			return "", nil
		},
	})

	if len(id.Captains) != 1 || id.Captains[0].ID != "alpha" || id.Captains[0].Watcher == nil {
		t.Fatalf("captain identity not exposed: %+v", id.Captains)
	}
	if id.Captains[0].SourceCheckout == nil || id.Captains[0].SourceCheckout.Revision != "caprev123456" {
		t.Fatalf("captain source checkout not exposed: %+v", id.Captains[0].SourceCheckout)
	}
	if id.Captains[0].Watcher.ExecutableDigest != oldDigest {
		t.Fatalf("watcher executable digest = %q, want %q", id.Captains[0].Watcher.ExecutableDigest, oldDigest)
	}
	finding := findSkew(id.Skew, SkewWatcherMismatch)
	if finding == nil || !strings.Contains(finding.Component, "captain:alpha") {
		t.Fatalf("missing captain watcher mismatch finding: %+v", id.Skew)
	}
	if finding.Remediation != "munsu captain recover alpha" {
		t.Fatalf("captain remediation = %q", finding.Remediation)
	}
}

func TestCollectRuntimeIdentityUsesLinkerFallbackAndDistinguishesPackagedInstall(t *testing.T) {
	oldVersion, oldCommit := orchestrator.BuildVersion, orchestrator.CommitSHA
	orchestrator.BuildVersion = "0.7.0-linker"
	orchestrator.CommitSHA = "linker123456"
	defer func() {
		orchestrator.BuildVersion = oldVersion
		orchestrator.CommitSHA = oldCommit
	}()
	home := t.TempDir()
	running := writeExecutable(t, home, "bin/munsu", "current")

	id := collectRuntimeIdentity(home, home, "", runtimeIdentityProbe{
		executable: func() (string, error) { return running, nil },
		lookPath:   func(string) (string, error) { return running, nil },
		buildInfo: func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/minhtri2710/munsu", Version: "(devel)"}}, true
		},
	})
	if id.Build.CLIVersion != "0.7.0-linker" || id.Build.VCSRevision != "linker123456" {
		t.Fatalf("linker fallback missing: %+v", id.Build)
	}
	if findSkew(id.Skew, SkewPackagedInstall) != nil || findSkew(id.Skew, SkewDirtyOrUnverifiableBuild) != nil {
		t.Fatalf("unexpected build skew with linker fallback: %+v", id.Skew)
	}

	orchestrator.CommitSHA = ""
	id = collectRuntimeIdentity(home, home, "", runtimeIdentityProbe{
		executable: func() (string, error) { return running, nil },
		lookPath:   func(string) (string, error) { return running, nil },
		buildInfo: func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/minhtri2710/munsu", Version: "(devel)"}}, true
		},
	})
	if findSkew(id.Skew, SkewDirtyOrUnverifiableBuild) == nil {
		t.Fatalf("devel build without revision should be unverifiable: %+v", id.Skew)
	}

	id = collectRuntimeIdentity(home, home, "", runtimeIdentityProbe{
		executable: func() (string, error) { return running, nil },
		lookPath:   func(string) (string, error) { return running, nil },
		buildInfo: func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/minhtri2710/munsu", Version: "v1.2.3"}}, true
		},
	})
	if findSkew(id.Skew, SkewPackagedInstall) == nil {
		t.Fatalf("packaged build without revision should be packaged_install: %+v", id.Skew)
	}
}

func TestCollectRuntimeIdentityIncludesIntegrationManifestDigest(t *testing.T) {
	home := t.TempDir()
	running := writeExecutable(t, home, "bin/munsu", "current")
	manifestPath := ManifestPath(home, "pi", ScopeProject, home)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: "munsu.integrate/v1", Harness: "pi", Version: "1.0.0", Scope: string(ScopeProject), ContentDigest: "digest123"}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	id := collectRuntimeIdentity(home, home, "0.9.0-test", runtimeIdentityProbe{
		executable: func() (string, error) { return running, nil },
		lookPath:   func(string) (string, error) { return running, nil },
		buildInfo:  cleanBuildInfo,
		integrationStatus: func(_, _, harnessName string, _ Scope) (*IntegrationResult, error) {
			return &IntegrationResult{Harness: harnessName, Scope: ScopeProject, State: "installed", Version: "1.0.0"}, nil
		},
	})
	var pi IntegrationRuntimeIdentity
	for _, integration := range id.Integrations {
		if integration.Harness == "pi" {
			pi = integration
			break
		}
	}
	if pi.ManifestPath != manifestPath || pi.ManifestSchema != "munsu.integrate/v1" || pi.ContentDigest != "digest123" {
		t.Fatalf("integration manifest identity = %+v", pi)
	}
}

func TestRuntimeIdentityLinesIncludeSkewDetailAndRemediation(t *testing.T) {
	id := &RuntimeIdentity{Skew: []SkewFinding{{
		Classification: SkewPathShadowing,
		Component:      "path:munsu",
		Detail:         "digest differs",
		Remediation:    "Update PATH so /opt/munsu is selected before /usr/bin/munsu.",
	}}}
	lines := strings.Join(RuntimeIdentityLines(id), "\n")
	for _, want := range []string{"skew: path_shadowing", "component=path:munsu", "detail=digest differs", "remediation=Update PATH so /opt/munsu is selected before /usr/bin/munsu."} {
		if !strings.Contains(lines, want) {
			t.Fatalf("runtime identity lines missing %q:\n%s", want, lines)
		}
	}
}

func TestBootstrapRunReturnsRuntimeIdentityEvenWhenSkewExists(t *testing.T) {
	home := t.TempDir()
	running := writeExecutable(t, home, "bin/munsu", "current")
	shadow := writeExecutable(t, home, "shadow/munsu", "shadow")
	saved := defaultRuntimeIdentityProbe
	defaultRuntimeIdentityProbe = func() runtimeIdentityProbe {
		p := saved()
		p.executable = func() (string, error) { return running, nil }
		p.lookPath = func(string) (string, error) { return shadow, nil }
		p.buildInfo = cleanBuildInfo
		p.integrationStatus = func(string, string, string, Scope) (*IntegrationResult, error) {
			return &IntegrationResult{Harness: "pi", Scope: ScopeProject, State: "absent"}, nil
		}
		return p
	}
	defer func() { defaultRuntimeIdentityProbe = saved }()

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.RuntimeIdentity == nil {
		t.Fatal("RuntimeIdentity is nil")
	}
	if findSkew(result.RuntimeIdentity.Skew, SkewPathShadowing) == nil {
		t.Fatalf("expected path shadowing skew, got %+v", result.RuntimeIdentity.Skew)
	}
	if len(result.Tools) == 0 || result.Auth == nil || result.GC == nil {
		t.Fatalf("read-only diagnostics not preserved: tools=%d auth=%v gc=%v", len(result.Tools), result.Auth, result.GC)
	}
}

func cleanBuildInfo() (*debug.BuildInfo, bool) {
	return &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/minhtri2710/munsu", Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123456789"},
			{Key: "vcs.time", Value: "2026-07-30T00:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}, true
}

func writeExecutable(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func findSkew(findings []SkewFinding, class SkewClassification) *SkewFinding {
	for i := range findings {
		if findings[i].Classification == class {
			return &findings[i]
		}
	}
	return nil
}
