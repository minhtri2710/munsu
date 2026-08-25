package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func mapping(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], true
		}
	}
	return nil, false
}

func has(node *yaml.Node, key string) bool {
	_, ok := mapping(node, key)
	return ok
}

func scalar(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	return node.Value, true
}

// optionalTrue reports whether a supported boolean field disables execution.
// Unknown YAML shapes fail closed because a workflow rewrite must become a
// false red, never silently remove the merge gate.
func optionalTrue(node *yaml.Node) (bool, error) {
	if node == nil {
		return false, nil
	}
	value, ok := scalar(node)
	if !ok || (value != "true" && value != "false") {
		return false, fmt.Errorf("boolean field has an unsupported value")
	}
	return value == "true", nil
}

func validate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read workflow: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("cannot parse workflow: %w", err)
	}
	if len(root.Content) != 1 {
		return fmt.Errorf("workflow has no document")
	}
	jobs, ok := mapping(root.Content[0], "jobs")
	if !ok || jobs.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow has no jobs mapping")
	}
	invariants, ok := mapping(jobs, "invariants")
	if !ok || invariants.Kind != yaml.MappingNode {
		return fmt.Errorf("required invariants job is missing")
	}
	name, ok := mapping(invariants, "name")
	nameValue, scalarOK := scalar(name)
	if !ok || !scalarOK || nameValue != "Repo invariants" {
		return fmt.Errorf("invariants job name is not Repo invariants")
	}
	if has(invariants, "if") {
		return fmt.Errorf("invariants job is conditional")
	}
	jobContinueOnError, _ := mapping(invariants, "continue-on-error")
	if disabled, err := optionalTrue(jobContinueOnError); err != nil {
		return fmt.Errorf("invariants job has an unsupported continue-on-error field")
	} else if disabled {
		return fmt.Errorf("invariants job is allowed to fail")
	}
	permissions, ok := mapping(invariants, "permissions")
	if !ok || permissions.Kind != yaml.MappingNode {
		return fmt.Errorf("invariants job has no permissions")
	}
	for _, permission := range []string{"contents", "actions"} {
		node, found := mapping(permissions, permission)
		value, permissionScalar := scalar(node)
		if !found || !permissionScalar || value != "read" {
			return fmt.Errorf("invariants job lacks %s: read permission", permission)
		}
	}
	steps, ok := mapping(invariants, "steps")
	if !ok || steps.Kind != yaml.SequenceNode {
		return fmt.Errorf("invariants steps is not a sequence")
	}
	for _, step := range steps.Content {
		if step.Kind != yaml.MappingNode {
			return fmt.Errorf("invariants contains a non-mapping step")
		}
		if has(step, "if") {
			continue
		}
		stepContinueOnError, _ := mapping(step, "continue-on-error")
		continueOnError, err := optionalTrue(stepContinueOnError)
		if err != nil {
			return fmt.Errorf("invariants step has an unsupported continue-on-error field")
		}
		if continueOnError {
			continue
		}
		run, found := mapping(step, "run")
		value, runScalar := scalar(run)
		if !found || !runScalar {
			continue
		}
		for _, line := range strings.Split(value, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if trimmed == ".github/scripts/flake-sweep.sh applied" {
				return nil
			}
		}
	}
	return fmt.Errorf("invariants job has no effective applied gate; branch protection requires Repo invariants as a required context")
}

func main() {
	path := ".github/workflows/ci.yml"
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: flake-sweep-topology [workflow]")
		os.Exit(2)
	}
	if err := validate(path); err != nil {
		fmt.Fprintf(os.Stderr, "::error::workflow topology is not an effective applied gate: %v\n", err)
		os.Exit(1)
	}
}
