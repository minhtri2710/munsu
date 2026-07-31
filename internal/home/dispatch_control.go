package home

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const dispatchControlSchema = "munsu.dispatch-control/v1"

var (
	ErrDispatchDecisionRequired = errors.New("dispatch interpretation requires a decision")
	ErrDispatchHeld             = errors.New("dispatch is held")
)

type DispatchAutonomy string

const (
	DispatchAutonomyManual               DispatchAutonomy = "manual"
	DispatchAutonomySafeReinterpretation DispatchAutonomy = "safe-reinterpretation"
)

type DispatchAction string

const (
	DispatchActionHandoff DispatchAction = "handoff"
	DispatchActionStart   DispatchAction = "start"
	DispatchActionSpawn   DispatchAction = "spawn"
)

type DispatchReadiness struct {
	TaskID          string   `json:"task_id"`
	Generation      string   `json:"generation,omitempty"`
	Ready           bool     `json:"ready"`
	BlockingReasons []string `json:"blocking_reasons,omitempty"`
}

type DispatchEvidence struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Field  string `json:"field,omitempty"`
	Value  string `json:"value,omitempty"`
}

type DispatchDependency struct {
	TaskID    string   `json:"task_id"`
	DependsOn []string `json:"depends_on,omitempty"`
	State     string   `json:"state,omitempty"`
}

type DispatchInterpretation struct {
	SchemaVersion            string              `json:"schema_version"`
	ID                       string              `json:"id"`
	RequestedOrder           []string            `json:"requested_order"`
	ComputedReadiness        []DispatchReadiness `json:"computed_readiness,omitempty"`
	SelectedTasks            []string            `json:"selected_tasks"`
	Evidence                 []DispatchEvidence  `json:"evidence,omitempty"`
	DependencySnapshotDigest string              `json:"dependency_snapshot_digest"`
	ParentInterpretationID   string              `json:"parent_interpretation_id,omitempty"`
	Outcome                  string              `json:"outcome"`
	DecisionKey              string              `json:"decision_key,omitempty"`
	CreatedAt                int64               `json:"created_at"`
}

const (
	DispatchInterpretationAccepted         = "accepted"
	DispatchInterpretationReinterpreted    = "reinterpreted"
	DispatchInterpretationDecisionRequired = "decision-required"
)

type DispatchInterpretationInput struct {
	RequestedOrder         []string
	ComputedReadiness      []DispatchReadiness
	SelectedTasks          []string
	Evidence               []DispatchEvidence
	Dependencies           []DispatchDependency
	ParentInterpretationID string
	SafeReinterpretation   bool
	MaterialAmbiguity      bool
	Autonomy               DispatchAutonomy
}

type DispatchHoldScope struct {
	ProjectIDs  []string `json:"projects,omitempty"`
	TaskIDs     []string `json:"tasks,omitempty"`
	Generations []string `json:"generations,omitempty"`
	ParentIDs   []string `json:"parents,omitempty"`
}

type DispatchHold struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Scope         DispatchHoldScope `json:"scope,omitempty"`
	Actions       []DispatchAction  `json:"actions"`
	Reason        string            `json:"reason"`
	CreatedAt     int64             `json:"created_at"`
	ReleasedAt    int64             `json:"released_at,omitempty"`
}

type DispatchDecision struct {
	SchemaVersion    string `json:"schema_version"`
	Key              string `json:"key"`
	InterpretationID string `json:"interpretation_id"`
	Reason           string `json:"reason"`
	CreatedAt        int64  `json:"created_at"`
	ResolvedAt       int64  `json:"resolved_at,omitempty"`
	Answer           string `json:"answer,omitempty"`
}

type DispatchHoldInput struct {
	ID      string
	Scope   DispatchHoldScope
	Actions []DispatchAction
	Reason  string
}

func dispatchControlDir(homeDir string) string { return filepath.Join(homeDir, "state", ".dispatch") }
func dispatchInterpretationPath(homeDir, id string) string {
	return filepath.Join(dispatchControlDir(homeDir), "interpretations", id+".json")
}
func dispatchHoldPath(homeDir, id string) string {
	return filepath.Join(dispatchControlDir(homeDir), "holds", id+".json")
}
func dispatchDecisionPath(homeDir, key string) string {
	return filepath.Join(dispatchControlDir(homeDir), "decisions", key+".json")
}

func PersistDispatchInterpretation(homeDir string, input DispatchInterpretationInput) (DispatchInterpretation, error) {
	if len(input.RequestedOrder) == 0 {
		return DispatchInterpretation{}, fmt.Errorf("dispatch interpretation requires requested tasks")
	}
	if len(input.SelectedTasks) == 0 {
		input.SelectedTasks = append([]string(nil), input.RequestedOrder...)
	}
	depsDigest, err := dispatchDependencyDigest(input.Dependencies)
	if err != nil {
		return DispatchInterpretation{}, err
	}
	identity, err := json.Marshal(struct {
		Requested []string
		Selected  []string
		Digest    string
		Parent    string
	}{input.RequestedOrder, input.SelectedTasks, depsDigest, input.ParentInterpretationID})
	if err != nil {
		return DispatchInterpretation{}, err
	}
	identityDigest := sha256.Sum256(identity)
	record := DispatchInterpretation{SchemaVersion: dispatchControlSchema, ID: "interpretation-" + hex.EncodeToString(identityDigest[:]), RequestedOrder: append([]string(nil), input.RequestedOrder...), ComputedReadiness: append([]DispatchReadiness(nil), input.ComputedReadiness...), SelectedTasks: append([]string(nil), input.SelectedTasks...), Evidence: append([]DispatchEvidence(nil), input.Evidence...), DependencySnapshotDigest: depsDigest, ParentInterpretationID: input.ParentInterpretationID, Outcome: DispatchInterpretationAccepted, CreatedAt: time.Now().UnixNano()}
	diverged := !equalStrings(input.RequestedOrder, input.SelectedTasks)
	if diverged || input.MaterialAmbiguity {
		dependencySafe := isDependencySafeOrder(input.SelectedTasks, input.Dependencies)
		if input.MaterialAmbiguity || !dependencySafe || !input.SafeReinterpretation || input.Autonomy != DispatchAutonomySafeReinterpretation {
			record.Outcome = DispatchInterpretationDecisionRequired
			record.DecisionKey = record.ID + "-decision"
		} else {
			record.Outcome = DispatchInterpretationReinterpreted
		}
	}
	if err := withDispatchControlLock(homeDir, func() error {
		if err := writeDispatchJSON(dispatchInterpretationPath(homeDir, record.ID), record); err != nil {
			return err
		}
		if record.Outcome == DispatchInterpretationDecisionRequired {
			decision := DispatchDecision{SchemaVersion: dispatchControlSchema, Key: record.DecisionKey, InterpretationID: record.ID, Reason: "material dispatch ambiguity", CreatedAt: time.Now().UnixNano()}
			if err := writeDispatchJSON(dispatchDecisionPath(homeDir, decision.Key), decision); err != nil {
				return err
			}
			hold := DispatchHold{SchemaVersion: dispatchControlSchema, ID: record.DecisionKey + "-hold", Scope: DispatchHoldScope{TaskIDs: append([]string(nil), input.SelectedTasks...)}, Actions: []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn}, Reason: "dispatch decision required", CreatedAt: time.Now().UnixNano()}
			return writeDispatchJSON(dispatchHoldPath(homeDir, hold.ID), hold)
		}
		return nil
	}); err != nil {
		return record, err
	}
	if record.Outcome == DispatchInterpretationDecisionRequired {
		return record, ErrDispatchDecisionRequired
	}
	return record, nil
}

func LoadDispatchInterpretation(homeDir, id string) (DispatchInterpretation, error) {
	return loadDispatchInterpretation(homeDir, id, map[string]bool{})
}

func loadDispatchInterpretation(homeDir, id string, seen map[string]bool) (DispatchInterpretation, error) {
	var record DispatchInterpretation
	if err := readDispatchJSON(dispatchInterpretationPath(homeDir, id), &record); err == nil {
		return record, nil
	} else if !os.IsNotExist(err) {
		return record, err
	}
	if seen[homeDir] {
		return record, os.ErrNotExist
	}
	seen[homeDir] = true
	parentData, err := os.ReadFile(filepath.Join(homeDir, "config", "parent-home"))
	if err != nil {
		return record, err
	}
	parent := strings.TrimSpace(string(parentData))
	if parent == "" {
		return record, os.ErrNotExist
	}
	return loadDispatchInterpretation(parent, id, seen)
}

func dispatchDependencyDigest(deps []DispatchDependency) (string, error) {
	copyDeps := append([]DispatchDependency(nil), deps...)
	sort.Slice(copyDeps, func(i, j int) bool { return copyDeps[i].TaskID < copyDeps[j].TaskID })
	for i := range copyDeps {
		copyDeps[i].DependsOn = append([]string(nil), copyDeps[i].DependsOn...)
		sort.Strings(copyDeps[i].DependsOn)
	}
	data, err := json.Marshal(copyDeps)
	if err != nil {
		return "", fmt.Errorf("encoding dependency snapshot: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func CreateDispatchHold(homeDir string, input DispatchHoldInput) (DispatchHold, error) {
	if input.ID == "" || strings.ContainsAny(input.ID, `/\\`) {
		return DispatchHold{}, fmt.Errorf("dispatch hold ID must be a safe non-empty value")
	}
	if len(input.Actions) == 0 || input.Reason == "" {
		return DispatchHold{}, fmt.Errorf("dispatch hold requires actions and reason")
	}
	hold := DispatchHold{SchemaVersion: dispatchControlSchema, ID: input.ID, Scope: normalizeDispatchHoldScope(input.Scope), Actions: append([]DispatchAction(nil), input.Actions...), Reason: input.Reason, CreatedAt: time.Now().UnixNano()}
	if err := withDispatchControlLock(homeDir, func() error { return writeDispatchJSON(dispatchHoldPath(homeDir, input.ID), hold) }); err != nil {
		return DispatchHold{}, err
	}
	return hold, nil
}

func LoadDispatchDecision(homeDir, key string) (DispatchDecision, error) {
	var decision DispatchDecision
	if err := readDispatchJSON(dispatchDecisionPath(homeDir, key), &decision); err != nil {
		return decision, err
	}
	return decision, nil
}

func ResolveDispatchDecision(homeDir, key, answer string) error {
	if answer == "" {
		return fmt.Errorf("dispatch decision answer must not be empty")
	}
	return withDispatchControlLock(homeDir, func() error {
		var decision DispatchDecision
		if err := readDispatchJSON(dispatchDecisionPath(homeDir, key), &decision); err != nil {
			return err
		}
		if decision.ResolvedAt == 0 {
			decision.ResolvedAt = time.Now().UnixNano()
			decision.Answer = answer
		}
		return writeDispatchJSON(dispatchDecisionPath(homeDir, key), decision)
	})
}

func ReleaseDispatchHold(homeDir, id string) error {
	return withDispatchControlLock(homeDir, func() error {
		var hold DispatchHold
		path := dispatchHoldPath(homeDir, id)
		if err := readDispatchJSON(path, &hold); err != nil {
			return err
		}
		if hold.ReleasedAt == 0 {
			hold.ReleasedAt = time.Now().UnixNano()
		}
		return writeDispatchJSON(path, hold)
	})
}

func CheckDispatchHold(homeDir string, action DispatchAction, taskID, projectID, generation, parentID string) error {
	return withDispatchControlLock(homeDir, func() error {
		// Check watcher health first — degraded mode blocks handoff, start, and spawn.
		if err := CheckWatcherHealthForDispatch(homeDir, action); err != nil {
			return err
		}
		return checkDispatchHoldUnlocked(homeDir, action, taskID, projectID, generation, parentID)
	})
}

func checkDispatchHoldUnlocked(homeDir string, action DispatchAction, taskID, projectID, generation, parentID string) error {
	entries, err := os.ReadDir(filepath.Join(dispatchControlDir(homeDir), "holds"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var hold DispatchHold
		if err := readDispatchJSON(filepath.Join(dispatchControlDir(homeDir), "holds", entry.Name()), &hold); err != nil {
			return err
		}
		if hold.ReleasedAt != 0 || !containsAction(hold.Actions, action) || !holdScopeMatches(hold.Scope, taskID, projectID, generation, parentID) {
			continue
		}
		return fmt.Errorf("%w: %s (%s)", ErrDispatchHeld, hold.ID, hold.Reason)
	}
	return nil
}

func withDispatchControlLock(homeDir string, fn func() error) error {
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		return err
	}
	path := filepath.Join(homeDir, "state", ".dispatch.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockExclusive(file); err != nil {
		return err
	}
	defer unlockFile(file)
	return fn()
}

func normalizeDispatchHoldScope(scope DispatchHoldScope) DispatchHoldScope {
	scope.ProjectIDs = uniqueSorted(scope.ProjectIDs)
	scope.TaskIDs = uniqueSorted(scope.TaskIDs)
	scope.Generations = uniqueSorted(scope.Generations)
	scope.ParentIDs = uniqueSorted(scope.ParentIDs)
	return scope
}
func holdScopeMatches(scope DispatchHoldScope, taskID, projectID, generation, parentID string) bool {
	return (len(scope.TaskIDs) == 0 || containsString(scope.TaskIDs, taskID)) && (len(scope.ProjectIDs) == 0 || containsString(scope.ProjectIDs, projectID)) && (len(scope.Generations) == 0 || containsString(scope.Generations, generation)) && (len(scope.ParentIDs) == 0 || containsString(scope.ParentIDs, parentID))
}
func containsAction(actions []DispatchAction, action DispatchAction) bool {
	for _, candidate := range actions {
		if candidate == action {
			return true
		}
	}
	return false
}
func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func uniqueSorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	result := out[:0]
	for _, value := range out {
		if value != "" && (len(result) == 0 || result[len(result)-1] != value) {
			result = append(result, value)
		}
	}
	return result
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isDependencySafeOrder(selected []string, dependencies []DispatchDependency) bool {
	positions := make(map[string]int, len(selected))
	for i, taskID := range selected {
		if _, exists := positions[taskID]; exists {
			return false
		}
		positions[taskID] = i
	}
	for _, dependency := range dependencies {
		dependentPosition, dependentExists := positions[dependency.TaskID]
		if !dependentExists {
			continue
		}
		for _, prerequisite := range dependency.DependsOn {
			prerequisitePosition, prerequisiteExists := positions[prerequisite]
			if prerequisiteExists && prerequisitePosition > dependentPosition {
				return false
			}
		}
	}
	return true
}
func writeDispatchJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}
func readDispatchJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
