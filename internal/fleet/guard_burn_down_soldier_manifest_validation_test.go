package fleet

import (
	"strings"
	"testing"
)

const guardManifestTestSHA = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func guardManifestEntry(path string) ManifestEntry {
	return ManifestEntry{Path: path, SHA256: guardManifestTestSHA, Policy: DisposalPolicyCleanable}
}

// guardCoreManifest is a manifest of the core launch artifacts plus extra paths.
func guardCoreManifest(extra ...string) *LaunchManifest {
	var entries []ManifestEntry
	for _, name := range append(append([]string{}, CoreLaunchArtifactNames...), extra...) {
		entries = append(entries, guardManifestEntry(name))
	}
	entries = append(entries, guardManifestEntry(".soldier-launch-guard-test-1/identity"))
	return &LaunchManifest{ManifestVersion: ManifestVersion, Artifacts: entries}
}

func TestGuardBurnDownValidateManifestRefusesInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    *LaunchManifest
		want string
	}{
		{
			name: "nil manifest",
			want: "manifest is nil",
		},
		{
			name: "noncanonical path",
			m: &LaunchManifest{
				ManifestVersion: ManifestVersion,
				Artifacts:       []ManifestEntry{guardManifestEntry("./artifact")},
			},
			want: "is not canonical",
		},
		{
			name: "NUL path",
			m: &LaunchManifest{
				ManifestVersion: ManifestVersion,
				Artifacts:       []ManifestEntry{guardManifestEntry("artifact\x00")},
			},
			want: "contains NUL byte",
		},
		{
			name: "absolute path",
			m: &LaunchManifest{
				ManifestVersion: ManifestVersion,
				Artifacts:       []ManifestEntry{guardManifestEntry("/artifact")},
			},
			want: "manifest entry with absolute path",
		},
		{
			name: "parent traversal",
			m: &LaunchManifest{
				ManifestVersion: ManifestVersion,
				Artifacts:       []ManifestEntry{guardManifestEntry("artifact/../other")},
			},
			want: "is not canonical",
		},
		{
			name: "parent dot-dot",
			m: &LaunchManifest{
				ManifestVersion: ManifestVersion,
				Artifacts:       []ManifestEntry{guardManifestEntry("..")},
			},
			want: "contains parent traversal",
		},
		{
			name: "extra entry no adapter declares",
			m:    guardCoreManifest("rogue.txt"),
			want: "unexpected manifest entry",
		},
		{
			name: "missing core entry",
			m: &LaunchManifest{
				ManifestVersion: ManifestVersion,
				Artifacts:       guardCoreManifest().Artifacts[1:],
			},
			want: "missing manifest entry",
		},
		{
			name: "missing deferred guard identity",
			m:    &LaunchManifest{ManifestVersion: ManifestVersion, Artifacts: guardCoreManifest().Artifacts[:len(CoreLaunchArtifactNames)]},
			want: "missing manifest entry: deferred launch guard identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManifest(tc.m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateManifest error = %v, want %q", err, tc.want)
			}
		})
	}
}
