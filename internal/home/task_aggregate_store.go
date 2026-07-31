package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ReadTaskAggregate(homeDir, taskID, generation string) (*TaskAggregate, error) {
	if err := validateTaskID(taskID); err != nil {
		return nil, err
	}
	if err := validateTaskGeneration(generation); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(homeDir, taskAggregateRelPath(taskID, generation)))
	if err != nil {
		return nil, err
	}
	var agg TaskAggregate
	if err := json.Unmarshal(data, &agg); err != nil {
		return nil, err
	}
	if err := validateTaskAggregate(agg); err != nil {
		return nil, err
	}
	return &agg, nil
}

func ReadCurrentTaskAggregate(homeDir, taskID string) (*TaskAggregate, bool, error) {
	if err := validateTaskID(taskID); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(filepath.Join(homeDir, taskAggregateDir, taskID, taskCurrentFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	generation := strings.TrimSpace(string(data))
	if generation == "" {
		return nil, false, fmt.Errorf("empty current task generation for %s", taskID)
	}
	agg, err := ReadTaskAggregate(homeDir, taskID, generation)
	if err != nil {
		return nil, false, err
	}
	return agg, true, nil
}

func CurrentTaskGeneration(homeDir, taskID, fallback string) (string, error) {
	if agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID); err != nil {
		return "", err
	} else if ok {
		return agg.Generation, nil
	}
	return fallback, nil
}

func WriteTaskAggregate(homeDir string, agg TaskAggregate) error {
	if err := validateTaskAggregate(agg); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(homeDir, taskAggregateRelPath(agg.TaskID, agg.Generation)), agg); err != nil {
		return err
	}
	if agg.Current {
		return writeTextFile(filepath.Join(homeDir, taskAggregateDir, agg.TaskID, taskCurrentFile), agg.Generation+"\n")
	}
	return nil
}

func CreateTaskAggregate(homeDir, taskID, owner, definition, kind, project string) (*TaskAggregate, error) {
	agg := &TaskAggregate{SchemaVersion: taskAggregateSchema, TaskID: taskID, Generation: "1", Current: true, Owner: owner, Definition: definition, Kind: kind, Project: project, State: "queued"}
	if agg.Owner == "" {
		agg.Owner = fallbackTaskOwner(homeDir, "state")
	}
	if err := WriteTaskAggregate(homeDir, *agg); err != nil {
		return nil, err
	}
	return agg, nil
}

func UpdateCurrentTaskAggregateState(homeDir, taskID, state, detail string) (*TaskAggregate, bool, error) {
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil || !ok {
		return nil, ok, err
	}
	updated := *agg
	updated.State = state
	updated.StateDetail = detail
	updated.AuditSources = append(updated.AuditSources, TaskAggregateEvidence{Kind: "status", Path: filepath.ToSlash(filepath.Join("state", taskID+".status")), Field: "state", Value: state})
	if err := WriteTaskAggregate(homeDir, updated); err != nil {
		return nil, true, err
	}
	return &updated, true, nil
}

func DeleteTaskAggregate(homeDir, taskID, generation string) error {
	if err := validateTaskID(taskID); err != nil {
		return err
	}
	if err := validateTaskGeneration(generation); err != nil {
		return err
	}
	path := filepath.Join(homeDir, taskAggregateRelPath(taskID, generation))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	currentPath := filepath.Join(homeDir, taskAggregateDir, taskID, taskCurrentFile)
	if data, err := os.ReadFile(currentPath); err == nil && strings.TrimSpace(string(data)) == generation {
		if err := os.Remove(currentPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func UpdateCurrentTaskAggregateOwner(homeDir, taskID, owner string) (*TaskAggregate, bool, error) {
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil || !ok {
		return nil, ok, err
	}
	updated := *agg
	updated.Owner = owner
	if err := WriteTaskAggregate(homeDir, updated); err != nil {
		return nil, true, err
	}
	return &updated, true, nil
}

func UpdateCurrentTaskAggregateKind(homeDir, taskID, kind string) (*TaskAggregate, bool, error) {
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil || !ok {
		return nil, ok, err
	}
	updated := *agg
	updated.Kind = kind
	if err := WriteTaskAggregate(homeDir, updated); err != nil {
		return nil, true, err
	}
	return &updated, true, nil
}

func ListTaskAggregates(homeDir string) ([]TaskAggregate, error) {
	root := filepath.Join(homeDir, taskAggregateDir)
	var out []TaskAggregate
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var agg TaskAggregate
		if err := json.Unmarshal(data, &agg); err != nil {
			return err
		}
		if err := validateTaskAggregate(agg); err != nil {
			return err
		}
		out = append(out, agg)
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return aggregateKey(out[i]) < aggregateKey(out[j]) })
	return out, nil
}

func ListCurrentTaskAggregates(homeDir string) ([]TaskAggregate, error) {
	root := filepath.Join(homeDir, taskAggregateDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []TaskAggregate
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		agg, ok, err := ReadCurrentTaskAggregate(homeDir, entry.Name())
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, *agg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return aggregateKey(out[i]) < aggregateKey(out[j]) })
	return out, nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func writeTextFile(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicWrite(path, []byte(data))
}
