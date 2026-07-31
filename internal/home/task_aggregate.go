package home

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	taskAggregateSchema       = "munsu.task-aggregate/v1"
	taskAggregateMigrationDir = "state/.task-aggregate-migration"
	taskAuthorityDir          = "state/.task-authority"
	taskAggregateDir          = "state/.task-authority/aggregates"
	taskAggregateQuarantine   = "state/.task-authority/quarantine"
	taskCurrentFile           = "current"
)

type TaskAggregateSource struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Field string `json:"field"`
}

type TaskAggregateEvidence struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Field string `json:"field"`
	Value string `json:"value"`
}

type TaskAggregateCandidate struct {
	TaskID           string              `json:"task_id"`
	Generation       string              `json:"generation"`
	Owner            string              `json:"owner,omitempty"`
	Definition       string              `json:"definition,omitempty"`
	State            string              `json:"state,omitempty"`
	StateDetail      string              `json:"state_detail,omitempty"`
	Project          string              `json:"project,omitempty"`
	Kind             string              `json:"kind,omitempty"`
	Current          bool                `json:"current"`
	Inactive         bool                `json:"inactive,omitempty"`
	Projection       bool                `json:"projection"`
	Source           TaskAggregateSource `json:"source"`
	OwnerSource      TaskAggregateSource `json:"owner_source,omitempty"`
	DefinitionSource TaskAggregateSource `json:"definition_source,omitempty"`
	StateSource      TaskAggregateSource `json:"state_source,omitempty"`
}

type TaskAggregate struct {
	SchemaVersion string                  `json:"schema_version"`
	TaskID        string                  `json:"task_id"`
	Generation    string                  `json:"generation"`
	Current       bool                    `json:"current"`
	Owner         string                  `json:"owner"`
	Definition    string                  `json:"definition,omitempty"`
	State         string                  `json:"state,omitempty"`
	StateDetail   string                  `json:"state_detail,omitempty"`
	Project       string                  `json:"project,omitempty"`
	Kind          string                  `json:"kind,omitempty"`
	Projections   []TaskAggregateEvidence `json:"projections,omitempty"`
	AuditSources  []TaskAggregateEvidence `json:"audit_sources,omitempty"`
}

type TaskAggregateQuarantineRecord struct {
	SchemaVersion string                  `json:"schema_version"`
	TaskID        string                  `json:"task_id"`
	Generation    string                  `json:"generation,omitempty"`
	Reason        string                  `json:"reason"`
	Evidence      []TaskAggregateEvidence `json:"evidence"`
}

type TaskAggregateMigrationPlan struct {
	SchemaVersion string                          `json:"schema_version"`
	HomeDir       string                          `json:"home_dir"`
	HomeIdentity  string                          `json:"home_identity"`
	SourceDigest  string                          `json:"source_digest"`
	RecordCount   int                             `json:"record_count"`
	Aggregates    []TaskAggregate                 `json:"aggregates"`
	Quarantined   []TaskAggregateQuarantineRecord `json:"quarantined,omitempty"`
}

type TaskAggregateMigrationReceipt struct {
	HomeDir      string `json:"home_dir"`
	HomeIdentity string `json:"home_identity"`
	SourceDigest string `json:"source_digest"`
	RecordCount  int    `json:"record_count"`
	CompletedAt  int64  `json:"completed_at"`
}

func ResolveTaskAggregate(candidates []TaskAggregateCandidate) (*TaskAggregate, *TaskAggregateQuarantineRecord) {
	if len(candidates) == 0 {
		return nil, &TaskAggregateQuarantineRecord{SchemaVersion: taskAggregateSchema, Reason: "no task records"}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidateKey(candidates[i]) < candidateKey(candidates[j]) })
	taskID := candidates[0].TaskID
	generation := candidates[0].Generation
	current := false
	for _, c := range candidates {
		current = current || c.Current
	}

	owners := map[string][]TaskAggregateEvidence{}
	definitions := map[string][]TaskAggregateEvidence{}
	states := map[string][]TaskAggregateEvidence{}
	projectionStates := map[string][]TaskAggregateEvidence{}
	for _, c := range candidates {
		if c.Owner != "" && !c.Projection {
			owners[c.Owner] = append(owners[c.Owner], evidence(ownerSource(c), c.Owner))
		}
		if c.Definition != "" {
			definitions[c.Definition] = append(definitions[c.Definition], evidence(definitionSource(c), c.Definition))
		}
		if c.State != "" {
			if c.Projection {
				projectionStates[c.State] = append(projectionStates[c.State], evidence(stateSource(c), c.State))
			} else {
				states[c.State] = append(states[c.State], evidence(stateSource(c), c.State))
			}
		}
	}
	if len(owners) != 1 {
		return nil, quarantineFromValues(taskID, generation, "conflicting active owners", owners)
	}
	if len(definitions) > 1 {
		return nil, quarantineFromValues(taskID, generation, "conflicting active definitions", definitions)
	}
	if len(states) > 1 {
		return nil, quarantineFromValues(taskID, generation, "conflicting active states", states)
	}
	if len(states) == 0 {
		if len(projectionStates) > 1 {
			return nil, quarantineFromValues(taskID, generation, "conflicting active states", projectionStates)
		}
		states = projectionStates
	}

	owner := onlyKey(owners)
	definition := onlyKey(definitions)
	state := onlyKey(states)
	agg := &TaskAggregate{SchemaVersion: taskAggregateSchema, TaskID: taskID, Generation: generation, Current: current, Owner: owner, Definition: definition, State: state}
	seenAudit := map[TaskAggregateEvidence]bool{}
	seenProjection := map[TaskAggregateEvidence]bool{}
	for _, c := range candidates {
		if c.Owner == owner && !c.Projection {
			seenAudit[evidence(ownerSource(c), c.Owner)] = true
		}
		if c.Definition == definition && c.Definition != "" {
			ev := evidence(definitionSource(c), c.Definition)
			if c.Projection {
				seenProjection[ev] = true
			} else {
				seenAudit[ev] = true
			}
		}
		if c.State == state && c.State != "" {
			ev := evidence(stateSource(c), c.State)
			if c.Projection {
				seenProjection[ev] = true
			} else {
				seenAudit[ev] = true
			}
		}
		if agg.Project == "" && c.Project != "" {
			agg.Project = c.Project
		}
		if agg.Kind == "" && c.Kind != "" {
			agg.Kind = c.Kind
		}
		if agg.StateDetail == "" && c.StateDetail != "" && c.State == state {
			agg.StateDetail = c.StateDetail
		}
	}
	if audit := sortedEvidence(seenAudit); len(audit) > 0 {
		agg.AuditSources = audit
	}
	if projections := sortedEvidence(seenProjection); len(projections) > 0 {
		agg.Projections = projections
	}
	return agg, nil
}

func validateTaskAggregatePlan(plan *TaskAggregateMigrationPlan) error {
	if plan == nil {
		return fmt.Errorf("task aggregate migration plan is required")
	}
	if plan.SchemaVersion != "munsu.task-aggregate-migration/v1" {
		return fmt.Errorf("invalid task aggregate migration schema %q", plan.SchemaVersion)
	}
	if plan.HomeDir == "" || plan.HomeIdentity == "" || plan.SourceDigest == "" {
		return fmt.Errorf("task aggregate migration plan missing identity")
	}
	if len(plan.SourceDigest) != 64 {
		return fmt.Errorf("invalid task aggregate source digest %q", plan.SourceDigest)
	}
	if plan.RecordCount != len(plan.Aggregates) {
		return fmt.Errorf("record count mismatch: %d aggregates=%d", plan.RecordCount, len(plan.Aggregates))
	}
	seen := map[string]bool{}
	current := map[string]int{}
	for _, agg := range plan.Aggregates {
		if err := validateTaskAggregate(agg); err != nil {
			return err
		}
		key := aggregateKey(agg)
		if seen[key] {
			return fmt.Errorf("duplicate task aggregate %s", key)
		}
		seen[key] = true
		if agg.Current {
			current[agg.TaskID]++
		}
	}
	for taskID, count := range current {
		if count != 1 {
			return fmt.Errorf("task %s has %d current generations", taskID, count)
		}
	}
	quarantineFiles := map[string]bool{}
	for _, q := range plan.Quarantined {
		name := quarantineFileName(q)
		if quarantineFiles[name] {
			return fmt.Errorf("duplicate quarantine filename %s", name)
		}
		quarantineFiles[name] = true
	}
	return nil
}

func validateTaskAggregate(agg TaskAggregate) error {
	if agg.SchemaVersion != taskAggregateSchema {
		return fmt.Errorf("invalid task aggregate schema %q", agg.SchemaVersion)
	}
	if err := validateTaskID(agg.TaskID); err != nil {
		return err
	}
	if err := validateTaskGeneration(agg.Generation); err != nil {
		return err
	}
	if strings.TrimSpace(agg.Owner) == "" {
		return fmt.Errorf("aggregate %s/%s missing owner", agg.TaskID, agg.Generation)
	}
	return nil
}

func validateTaskGeneration(generation string) error {
	if generation == "" || generation == "." || generation == ".." || filepath.Base(generation) != generation || strings.ContainsAny(generation, `/\\`) {
		return fmt.Errorf("invalid task generation %q", generation)
	}
	if n, err := strconv.ParseUint(generation, 10, 64); err != nil || n == 0 {
		return fmt.Errorf("invalid task generation %q", generation)
	}
	return nil
}

func ownerSource(c TaskAggregateCandidate) TaskAggregateSource {
	if c.OwnerSource.Kind != "" || c.OwnerSource.Path != "" || c.OwnerSource.Field != "" {
		return c.OwnerSource
	}
	return c.Source
}

func definitionSource(c TaskAggregateCandidate) TaskAggregateSource {
	if c.DefinitionSource.Kind != "" || c.DefinitionSource.Path != "" || c.DefinitionSource.Field != "" {
		return c.DefinitionSource
	}
	if c.Source.Field == "owner" {
		return TaskAggregateSource{Kind: c.Source.Kind, Path: c.Source.Path, Field: "description"}
	}
	return c.Source
}

func stateSource(c TaskAggregateCandidate) TaskAggregateSource {
	if c.StateSource.Kind != "" || c.StateSource.Path != "" || c.StateSource.Field != "" {
		return c.StateSource
	}
	return TaskAggregateSource{Kind: c.Source.Kind, Path: c.Source.Path, Field: "state"}
}

func evidence(source TaskAggregateSource, value string) TaskAggregateEvidence {
	return TaskAggregateEvidence{Kind: source.Kind, Path: source.Path, Field: source.Field, Value: value}
}

func quarantineFromValues(taskID, generation, reason string, values map[string][]TaskAggregateEvidence) *TaskAggregateQuarantineRecord {
	seen := map[TaskAggregateEvidence]bool{}
	for _, evs := range values {
		for _, ev := range evs {
			seen[ev] = true
		}
	}
	return &TaskAggregateQuarantineRecord{SchemaVersion: taskAggregateSchema, TaskID: taskID, Generation: generation, Reason: reason, Evidence: sortedEvidence(seen)}
}

func sortedEvidence(seen map[TaskAggregateEvidence]bool) []TaskAggregateEvidence {
	out := make([]TaskAggregateEvidence, 0, len(seen))
	for ev := range seen {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return evidenceKey(out[i]) < evidenceKey(out[j]) })
	return out
}

func onlyKey[V any](m map[string]V) string {
	for k := range m {
		return k
	}
	return ""
}

func taskAggregateRelPath(taskID, generation string) string {
	return filepath.Join(taskAggregateDir, taskID, generation+".json")
}

func aggregateKey(agg TaskAggregate) string {
	return agg.TaskID + "\x00" + agg.Generation
}

func quarantineFileName(q TaskAggregateQuarantineRecord) string {
	name := q.TaskID
	if q.Generation != "" {
		name += "-" + q.Generation
	}
	name += "-" + q.Reason
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(name)
	return name + ".json"
}

func candidateKey(c TaskAggregateCandidate) string {
	return c.TaskID + "\x00" + c.Generation + "\x00" + c.Owner + "\x00" + c.Definition + "\x00" + c.State + "\x00" + evidenceKey(evidence(c.Source, ""))
}

func quarantineKey(q TaskAggregateQuarantineRecord) string {
	return q.TaskID + "\x00" + q.Generation + "\x00" + q.Reason
}

func evidenceKey(e TaskAggregateEvidence) string {
	return e.Path + "\x00" + e.Kind + "\x00" + e.Field + "\x00" + e.Value
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "active", "current":
		return true
	default:
		return false
	}
}

func isExplicitFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "inactive", "historical":
		return true
	default:
		return false
	}
}
