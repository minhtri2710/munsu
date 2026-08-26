package fleet

import (
	"strings"
	"testing"
)

func TestGuardBurnDownBindingDocumentValidationRefusals(t *testing.T) {
	t.Run("binding requires project and captain", func(t *testing.T) {
		doc := bindingDoc{Bindings: []bindingRecord{{ProjectID: "", CaptainID: "captain-a"}}}
		if err := validateBindingDoc(&doc); err == nil || !strings.Contains(err.Error(), "binding record requires project and captain") {
			t.Fatalf("validateBindingDoc error = %v, want missing-field refusal", err)
		}
	})

	t.Run("project can have one owner", func(t *testing.T) {
		doc := bindingDoc{Bindings: []bindingRecord{
			{ProjectID: "project-a", CaptainID: "captain-a"},
			{ProjectID: "project-a", CaptainID: "captain-b"},
		}}
		if err := validateBindingDoc(&doc); err == nil || !strings.Contains(err.Error(), "owned by more than one captain") {
			t.Fatalf("validateBindingDoc error = %v, want duplicate-project refusal", err)
		}
	})

	t.Run("captain can own one project", func(t *testing.T) {
		doc := bindingDoc{Bindings: []bindingRecord{
			{ProjectID: "project-a", CaptainID: "captain-a"},
			{ProjectID: "project-b", CaptainID: "captain-a"},
		}}
		if err := validateBindingDoc(&doc); err == nil || !strings.Contains(err.Error(), "owns more than one project") {
			t.Fatalf("validateBindingDoc error = %v, want duplicate-captain refusal", err)
		}
	})
}
