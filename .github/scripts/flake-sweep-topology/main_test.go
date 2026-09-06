package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAcceptsEffectiveAppliedGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.yml")
	workflow := `name: CI
jobs:
  invariants:
    name: Repo invariants
    permissions:
      contents: read
      actions: read
    steps:
      - run: .github/scripts/flake-sweep.sh applied
`
	if err := os.WriteFile(path, []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validate(path); err != nil {
		t.Fatalf("validate(%q): %v", path, err)
	}
}

func TestValidateRejectsConditionalInvariantsJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.yml")
	workflow := `name: CI
jobs:
  invariants:
    name: Repo invariants
    if: ${{ false }}
    permissions:
      contents: read
      actions: read
    steps:
      - run: .github/scripts/flake-sweep.sh applied
`
	if err := os.WriteFile(path, []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validate(path); err == nil {
		t.Fatal("conditional invariants job unexpectedly passed validation")
	}
}
