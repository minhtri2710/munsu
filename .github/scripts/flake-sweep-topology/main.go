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

func defaultsValues(node *yaml.Node, label string) (shell, workingDirectory *yaml.Node, err error) {
	if node == nil {
		return nil, nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s defaults are not a mapping", label)
	}
	run, ok := mapping(node, "run")
	if !ok {
		return nil, nil, nil
	}
	if run.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s defaults.run is not a mapping", label)
	}
	shell, _ = mapping(run, "shell")
	workingDirectory, _ = mapping(run, "working-directory")
	return shell, workingDirectory, nil
}

func validShell(node *yaml.Node, label string) error {
	if node == nil {
		return nil
	}
	value, ok := scalar(node)
	if !ok || (value != "bash" && value != "sh") {
		return fmt.Errorf("%s shell must be bash or sh", label)
	}
	return nil
}

func validDirectory(node *yaml.Node, label string) error {
	if node == nil {
		return nil
	}
	value, ok := scalar(node)
	if !ok || value != "." {
		return fmt.Errorf("%s working-directory must be the current directory", label)
	}
	return nil
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
	document := root.Content[0]
	workflowDefaults, _ := mapping(document, "defaults")
	workflowShell, workflowDirectory, err := defaultsValues(workflowDefaults, "workflow")
	if err != nil {
		return err
	}
	jobs, ok := mapping(document, "jobs")
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
	jobDefaults, _ := mapping(invariants, "defaults")
	jobShell, jobDirectory, err := defaultsValues(jobDefaults, "invariants job")
	if err != nil {
		return err
	}
	if jobShell == nil {
		jobShell = workflowShell
	}
	if jobDirectory == nil {
		jobDirectory = workflowDirectory
	}
	if err := validShell(jobShell, "effective invariants job"); err != nil {
		return err
	}
	if err := validDirectory(jobDirectory, "effective invariants job"); err != nil {
		return err
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
		if has(step, "shell") {
			continue
		}
		stepDirectory, _ := mapping(step, "working-directory")
		if stepDirectory == nil {
			stepDirectory = jobDirectory
		}
		if err := validDirectory(stepDirectory, "effective invariants step"); err != nil {
			continue
		}
		run, found := mapping(step, "run")
		value, runScalar := scalar(run)
		if !found || !runScalar {
			continue
		}
		// This exactness rule ends the regress rather than expressing a strictness
		// preference: arbitrary shell reachability cannot be decided, so only the
		// literal single command is accepted and all other scripts fail closed.
		if strings.TrimSpace(value) == ".github/scripts/flake-sweep.sh applied" {
			return nil
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
