package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/bootstrap"
)

func TestSessionRuntimeIdentityContractIncludesCompleteTypedIdentity(t *testing.T) {
	id := completeTestRuntimeIdentity()

	identity := sessionRuntimeIdentityContract(id)
	if identity == nil {
		t.Fatal("runtime identity contract is nil")
	}
	if identity.ProtocolVersion != 7 || identity.RunningExecutable.Path != "/opt/munsu" || identity.RunningExecutable.Digest != "abc" {
		t.Fatalf("runtime identity contract missing executable/protocol: %+v", identity)
	}
	if identity.PATHExecutable.Path != "/usr/bin/munsu" || identity.Build.VCSModified != true || identity.Build.VCSRevision != "abc123" {
		t.Fatalf("runtime identity contract missing path/provenance: %+v", identity)
	}
	if len(identity.SourceCheckouts) != 1 || identity.SourceCheckouts[0].Revision != "abc123" {
		t.Fatalf("runtime identity contract missing source checkouts: %+v", identity.SourceCheckouts)
	}
	if identity.Watcher == nil || identity.Watcher.ExecutableDigest != "abc" {
		t.Fatalf("runtime identity contract missing watcher digest: %+v", identity.Watcher)
	}
	if len(identity.Captains) != 1 || identity.Captains[0].SourceCheckout == nil || identity.Captains[0].SourceCheckout.Revision != "cap123" {
		t.Fatalf("runtime identity contract missing captain identity: %+v", identity.Captains)
	}
	if len(identity.Integrations) != 1 || identity.Integrations[0].ContentDigest != "content123" || identity.Integrations[0].ManifestPath != "/manifest.json" {
		t.Fatalf("runtime identity contract missing integration digest: %+v", identity.Integrations)
	}
	if len(identity.Skew) != 1 || identity.Skew[0].Classification != "path_shadowing" || identity.Skew[0].Remediation == "" {
		t.Fatalf("skew contract = %+v", identity.Skew)
	}
}

func TestSessionRuntimeIdentityContractEncodesJSONAndTOON(t *testing.T) {
	payload := Response[SessionStart]{
		SchemaVersion: SchemaVersion,
		Kind:          "session.start",
		Status:        "success",
		Data: SessionStart{
			Lock:            "acquired",
			Watcher:         "healthy",
			BootstrapOK:     true,
			FleetSyncOK:     true,
			RuntimeIdentity: sessionRuntimeIdentityContract(completeTestRuntimeIdentity()),
			Message:         "ok",
		},
	}
	jsonOut, err := Encode(payload, OutputJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\"runtime_identity\"", "\"running_executable\"", "\"source_checkouts\"", "\"content_digest\"", "\"classification\": \"path_shadowing\""} {
		if !strings.Contains(jsonOut, want) {
			t.Fatalf("JSON contract missing %q:\n%s", want, jsonOut)
		}
	}
	toonOut, err := Encode(payload, OutputTOON)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"runtime_identity:", "running_executable:", "source_checkouts[1]", "content_digest", "content123", "path_shadowing"} {
		if !strings.Contains(toonOut, want) {
			t.Fatalf("TOON contract missing %q:\n%s", want, toonOut)
		}
	}
}

func completeTestRuntimeIdentity() *bootstrap.RuntimeIdentity {
	return &bootstrap.RuntimeIdentity{
		ProtocolVersion:   7,
		RunningExecutable: bootstrap.ExecutableIdentity{Path: "/opt/munsu", Digest: "abc"},
		PATHExecutable:    bootstrap.ExecutableIdentity{Path: "/usr/bin/munsu", Digest: "def"},
		Build:             bootstrap.BuildProvenance{CLIVersion: "0.1.0", ModulePath: "github.com/minhtri2710/munsu", ModuleVersion: "v1.2.3", VCSRevision: "abc123", VCSModified: true, Available: true},
		SourceCheckouts:   []bootstrap.SourceCheckoutIdentity{{Path: "/repo", Revision: "abc123", Dirty: true}},
		Watcher:           &bootstrap.WatcherRuntimeIdentity{Component: "watcher", Home: "/home", Executable: "/opt/munsu", ExecutableDigest: "abc", BuildVersion: "0.1.0", ProtocolVersion: 7, CommitSHA: "abc123", Running: true},
		Captains: []bootstrap.CaptainRuntimeIdentity{{
			ID:             "alpha",
			Home:           "/captain",
			SourceCheckout: &bootstrap.SourceCheckoutIdentity{Path: "/captain", Revision: "cap123", Dirty: false},
			Watcher:        &bootstrap.WatcherRuntimeIdentity{Component: "captain:alpha watcher", ExecutableDigest: "capdigest"},
		}},
		Integrations: []bootstrap.IntegrationRuntimeIdentity{{Harness: "pi", Scope: bootstrap.ScopeProject, State: "drifted", Version: "1.0.0", ManifestPath: "/manifest.json", ManifestSchema: "munsu.integrate/v1", ContentDigest: "content123", Drifted: true, Remediation: "munsu integrate repair --harness pi --scope project"}},
		Skew: []bootstrap.SkewFinding{{
			Classification: bootstrap.SkewPathShadowing,
			Component:      "path:munsu",
			Detail:         "digest differs",
			Remediation:    "Update PATH so /opt/munsu is selected before /usr/bin/munsu.",
		}},
	}
}
