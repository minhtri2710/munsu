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

type AmbiguousTaskIDError struct {
	Requested string
	Matches   []string
}

func (e *AmbiguousTaskIDError) Error() string {
	return fmt.Sprintf("task ID %q is ambiguous; use one of: %s", e.Requested, strings.Join(e.Matches, ", "))
}

func (e *AmbiguousTaskIDError) CorrectionCommands(command string) []string {
	commands := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		commands = append(commands, command+" "+e.Requested+" --home "+match)
	}
	return commands
}

func ResolveCurrentTaskID(homeDir, taskID string) (string, error) {
	if err := validateTaskID(taskID); err != nil {
		return "", err
	}
	matches, err := scopedCurrentTaskMatches(homeDir, taskID)
	if err != nil {
		return "", err
	}
	if len(matches) > 1 {
		return "", &AmbiguousTaskIDError{Requested: taskID, Matches: matches}
	}
	return taskID, nil
}

func scopedCurrentTaskMatches(homeDir, taskID string) ([]string, error) {
	var matches []string
	if _, ok, err := ReadCurrentTaskAggregate(homeDir, taskID); err != nil {
		return nil, err
	} else if ok {
		matches = append(matches, homeDir)
	}
	captainsRoot := filepath.Join(homeDir, "captains")
	entries, err := os.ReadDir(captainsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return matches, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		captainHome := filepath.Join(captainsRoot, entry.Name())
		if _, ok, err := ReadCurrentTaskAggregate(captainHome, taskID); err != nil {
			return nil, err
		} else if ok {
			matches = append(matches, captainHome)
		}
	}
	return matches, nil
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
	_, unlock, err := acquireMetaLock(homeDir, agg.TaskID)
	if err != nil {
		return fmt.Errorf("write task aggregate: %w", err)
	}
	defer unlock()
	return writeTaskAggregateFilesUnlocked(homeDir, agg)
}

func writeTaskAggregateFilesUnlocked(homeDir string, agg TaskAggregate) error {
	if err := writeJSONFile(filepath.Join(homeDir, taskAggregateRelPath(agg.TaskID, agg.Generation)), agg); err != nil {
		return err
	}
	if agg.Current {
		return writeTextFile(filepath.Join(homeDir, taskAggregateDir, agg.TaskID, taskCurrentFile), agg.Generation+"\n")
	}
	return nil
}

func CreateTaskAggregate(homeDir, taskID, owner, definition, kind, project string) (*TaskAggregate, error) {
	if err := validateTaskID(taskID); err != nil {
		return nil, err
	}
	_, unlock, err := acquireMetaLock(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("create task aggregate: %w", err)
	}
	defer unlock()

	currentPath := filepath.Join(homeDir, taskAggregateDir, taskID, taskCurrentFile)
	generationPath := filepath.Join(homeDir, taskAggregateRelPath(taskID, "1"))
	if _, err := os.Stat(currentPath); err == nil {
		return nil, fmt.Errorf("task aggregate %q already exists", taskID)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Stat(generationPath); err == nil {
		return nil, fmt.Errorf("task aggregate %q already exists", taskID)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	agg := &TaskAggregate{SchemaVersion: taskAggregateSchema, TaskID: taskID, Generation: "1", Current: true, Owner: owner, Definition: definition, Kind: kind, Project: project, State: "queued"}
	if agg.Owner == "" {
		agg.Owner = fallbackTaskOwner(homeDir, "state")
	}
	if err := writeTaskAggregateFilesUnlocked(homeDir, *agg); err != nil {
		return nil, err
	}
	return agg, nil
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

func PreflightTaskTransfer(sourceHome, destinationHome, taskID string) (*TaskAggregate, error) {
	resolvedID, err := ResolveCurrentTaskID(sourceHome, taskID)
	if err != nil {
		return nil, err
	}
	agg, ok, err := ReadCurrentTaskAggregate(sourceHome, resolvedID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("source does not own task aggregate %s", resolvedID)
	}
	if existing, exists, err := ReadCurrentTaskAggregate(destinationHome, resolvedID); err != nil {
		return nil, err
	} else if exists && existing.Generation == agg.Generation {
		return nil, fmt.Errorf("destination already owns task aggregate %s generation %s", resolvedID, agg.Generation)
	}
	for _, rel := range taskProjectionRelPaths(resolvedID) {
		src := filepath.Join(sourceHome, rel)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		dst := filepath.Join(destinationHome, rel)
		if _, err := os.Stat(dst); err == nil {
			return nil, fmt.Errorf("destination projection already exists: %s", rel)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return agg, nil
}

func taskProjectionRelPaths(taskID string) []string {
	return []string{filepath.Join("state", taskID+".meta"), filepath.Join("state", taskID+".status"), filepath.Join("data", taskID, "brief.md")}
}

func UpdateCurrentTaskAggregateOwner(homeDir, taskID, owner string) (*TaskAggregate, bool, error) {
	_, unlock, err := acquireMetaLock(homeDir, taskID)
	if err != nil {
		return nil, false, fmt.Errorf("update task aggregate owner: %w", err)
	}
	defer unlock()
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil || !ok {
		return nil, ok, err
	}
	updated := *agg
	updated.Owner = owner
	if err := writeTaskAggregateFilesUnlocked(homeDir, updated); err != nil {
		return nil, true, err
	}
	return &updated, true, nil
}

func UpdateCurrentTaskAggregateKind(homeDir, taskID, kind string) (*TaskAggregate, bool, error) {
	_, unlock, err := acquireMetaLock(homeDir, taskID)
	if err != nil {
		return nil, false, fmt.Errorf("update task aggregate kind: %w", err)
	}
	defer unlock()
	agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil || !ok {
		return nil, ok, err
	}
	updated := *agg
	updated.Kind = kind
	if err := writeTaskAggregateFilesUnlocked(homeDir, updated); err != nil {
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
