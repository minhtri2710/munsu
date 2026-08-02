package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/taskauthority"
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

// PersistDispatchInterpretation is the home serialization adapter for a
// fully-formed interpretation input. It delegates the deterministic identity,
// dependency snapshot digest, and outcome classification to
// internal/taskauthority and persists the legacy home-path projection records
// (interpretation, and on decision-required the decision and task-scoped
// hold). It contains no interpretation rules.
func PersistDispatchInterpretation(homeDir string, input DispatchInterpretationInput) (DispatchInterpretation, error) {
	result, err := taskauthority.ClassifyInterpretation(taskauthority.InterpretationInput{
		RequestedOrder:         input.RequestedOrder,
		Dependencies:           toTaskAuthorityDependencies(input.Dependencies),
		Autonomy:               taskauthority.DispatchAutonomy(input.Autonomy),
		ParentInterpretationID: input.ParentInterpretationID,
		Evidence:               toTaskAuthorityEvidence(input.Evidence),
		ComputedReadiness:      toTaskAuthorityReadiness(input.ComputedReadiness),
		SelectedTasks:          input.SelectedTasks,
		SafeReinterpretation:   input.SafeReinterpretation,
		MaterialAmbiguity:      input.MaterialAmbiguity,
	})
	if err != nil {
		return DispatchInterpretation{}, err
	}
	record := toHomeInterpretation(result.Record)
	if err := persistDispatchInterpretationRecords(homeDir, record, result.Decision, result.Hold); err != nil {
		return record, err
	}
	if result.Record.Outcome == taskauthority.DispatchInterpretationDecisionRequired {
		return record, ErrDispatchDecisionRequired
	}
	return record, nil
}

// persistDispatchInterpretationRecords writes the interpretation record and,
// when the outcome is decision-required, the decision and hold records to
// their legacy home paths under the dispatch control lock.
func persistDispatchInterpretationRecords(homeDir string, record DispatchInterpretation, decision *taskauthority.DispatchDecision, hold *taskauthority.DispatchHold) error {
	return withDispatchControlLock(homeDir, func() error {
		if err := writeDispatchJSON(dispatchInterpretationPath(homeDir, record.ID), record); err != nil {
			return err
		}
		if decision == nil {
			return nil
		}
		if err := writeDispatchJSON(dispatchDecisionPath(homeDir, decision.Key), toHomeDecision(*decision)); err != nil {
			return err
		}
		return writeDispatchJSON(dispatchHoldPath(homeDir, hold.ID), toHomeHold(*hold))
	})
}

// toHomeInterpretation renders a canonical taskauthority interpretation
// record as the legacy home record shape stamped with the dispatch-control
// schema, preserving the deterministic identity and digest byte-for-byte.
func toHomeInterpretation(rec taskauthority.DispatchInterpretation) DispatchInterpretation {
	return DispatchInterpretation{
		SchemaVersion:            dispatchControlSchema,
		ID:                       rec.ID,
		RequestedOrder:           append([]string(nil), rec.RequestedOrder...),
		ComputedReadiness:        toHomeReadiness(rec.ComputedReadiness),
		SelectedTasks:            append([]string(nil), rec.SelectedTasks...),
		Evidence:                 toHomeEvidence(rec.Evidence),
		DependencySnapshotDigest: rec.DependencySnapshotDigest,
		ParentInterpretationID:   rec.ParentInterpretationID,
		Outcome:                  rec.Outcome,
		DecisionKey:              rec.DecisionKey,
		CreatedAt:                rec.CreatedAt,
	}
}

func toHomeDecision(dec taskauthority.DispatchDecision) DispatchDecision {
	return DispatchDecision{
		SchemaVersion:    dispatchControlSchema,
		Key:              dec.Key,
		InterpretationID: dec.InterpretationID,
		Reason:           dec.Reason,
		CreatedAt:        dec.CreatedAt,
		ResolvedAt:       dec.ResolvedAt,
		Answer:           dec.Answer,
	}
}

func toHomeHold(hold taskauthority.DispatchHold) DispatchHold {
	actions := make([]DispatchAction, 0, len(hold.Actions))
	for _, action := range hold.Actions {
		actions = append(actions, DispatchAction(action))
	}
	return DispatchHold{
		SchemaVersion: dispatchControlSchema,
		ID:            hold.ID,
		Scope:         toHomeHoldScope(hold.Scope),
		Actions:       actions,
		Reason:        hold.Reason,
		CreatedAt:     hold.CreatedAt,
		ReleasedAt:    hold.ReleasedAt,
	}
}

func toHomeReadiness(readiness []taskauthority.DispatchReadiness) []DispatchReadiness {
	out := make([]DispatchReadiness, 0, len(readiness))
	for _, item := range readiness {
		out = append(out, DispatchReadiness{
			TaskID:          item.TaskID,
			Generation:      item.Generation,
			Ready:           item.Ready,
			BlockingReasons: append([]string(nil), item.BlockingReasons...),
		})
	}
	return out
}

func toHomeEvidence(evidence []taskauthority.DispatchEvidence) []DispatchEvidence {
	out := make([]DispatchEvidence, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, DispatchEvidence{Source: item.Source, Path: item.Path, Field: item.Field, Value: item.Value})
	}
	return out
}

func toHomeHoldScope(scope taskauthority.DispatchHoldScope) DispatchHoldScope {
	return DispatchHoldScope{
		ProjectIDs:  append([]string(nil), scope.ProjectIDs...),
		TaskIDs:     append([]string(nil), scope.TaskIDs...),
		Generations: append([]string(nil), scope.Generations...),
		ParentIDs:   append([]string(nil), scope.ParentIDs...),
	}
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

func LoadDispatchDecision(homeDir, key string) (DispatchDecision, error) {
	var decision DispatchDecision
	if err := readDispatchJSON(dispatchDecisionPath(homeDir, key), &decision); err != nil {
		return decision, err
	}
	return decision, nil
}

// CheckDispatchHold evaluates only durable Dispatch Holds. Degraded
// supervision is a separate orchestration gate (fleet/CLI), never part of a
// dispatch-hold or lifecycle check (Task 4.3, ADR-0007 §8).
func CheckDispatchHold(homeDir string, action DispatchAction, taskID, projectID, generation, parentID string) error {
	return withDispatchControlLock(homeDir, func() error {
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
