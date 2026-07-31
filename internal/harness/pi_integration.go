package harness

const CanonicalPiIntegrationName = "munsu-pi-integration.ts"

var piIntegrationAliasNames = []string{
	"munsu-captain-turnend-guard.ts",
	"munsu-captain-pi-watch.ts",
	"fm-primary-turnend-guard.ts",
	"fm-primary-pi-watch.ts",
}

// PiIntegrationAliasNames returns legacy Pi integration names that must not be loaded.
func PiIntegrationAliasNames() []string {
	return append([]string(nil), piIntegrationAliasNames...)
}
