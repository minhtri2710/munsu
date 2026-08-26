package fleet

import (
	"strings"
	"testing"
)

func TestGuardBurnDownRegistryDocumentValidationRefusals(t *testing.T) {
	t.Run("project requires id and name", func(t *testing.T) {
		doc := projectRegistryDoc{Projects: []projectRecord{{
			SchemaVersion: FleetRegistrySchema,
			Path:          "/proj/invalid",
		}}}
		if err := validateProjectRegistry(&doc); err == nil || !strings.Contains(err.Error(), "project record requires id and name") {
			t.Fatalf("validateProjectRegistry error = %v, want missing-field refusal", err)
		}
	})

	t.Run("project IDs must be unique", func(t *testing.T) {
		doc := projectRegistryDoc{Projects: []projectRecord{
			{SchemaVersion: FleetRegistrySchema, ID: "alpha", Name: "alpha"},
			{SchemaVersion: FleetRegistrySchema, ID: "alpha", Name: "other"},
		}}
		if err := validateProjectRegistry(&doc); err == nil || !strings.Contains(err.Error(), `duplicate project "alpha"`) {
			t.Fatalf("validateProjectRegistry error = %v, want duplicate-project refusal", err)
		}
	})

	t.Run("captain schema must be current", func(t *testing.T) {
		doc := captainRegistryDoc{Captains: []captainRecord{{
			SchemaVersion: "munsu.fleet.registry/v0",
			ID:            "captain-a",
			Home:          "/captains/a",
		}}}
		if err := validateCaptainRegistry(&doc); err == nil || !strings.Contains(err.Error(), "has schema") {
			t.Fatalf("validateCaptainRegistry error = %v, want schema refusal", err)
		}
	})
}
