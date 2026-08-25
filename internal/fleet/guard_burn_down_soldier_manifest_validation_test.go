package fleet

import (
	"strings"
	"testing"
)

const guardManifestTestSHA = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func guardManifestEntry(path string) ManifestEntry {
	return ManifestEntry{Path: path, SHA256: guardManifestTestSHA, Policy: DisposalPolicyCleanable}
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManifest(tc.m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateManifest error = %v, want %q", err, tc.want)
			}
		})
	}
}
