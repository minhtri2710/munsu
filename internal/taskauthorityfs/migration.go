package taskauthorityfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Migration schema and source identities. The v1 aggregate schema is the
// output of `munsu migrate task-aggregates`; Task 2.5 consumes that output
// and the v1 dispatch-control records. Raw .meta/.status/backlog projections
// are owned by the older migration and are never re-collected here.
const (
	// MigrationPlanSchema is the versioned identity of every migration plan,
	// journal, and receipt document.
	MigrationPlanSchema = "munsu.task-authority-migration/v1"
	// V1DispatchSchema is the legacy dispatch-control schema consumed by the
	// migration (holds, interpretations, and decisions).
	V1DispatchSchema = "munsu.dispatch-control/v1"
	// V1SourceSchema is the joined identity of every consumed v1 source
	// schema.
	V1SourceSchema = "munsu.task-aggregate/v1; munsu.dispatch-control/v1"
	// migrationDir is the hidden migration state directory under the home.
	// It is invisible to Store reads and to legacy v1 detection.
	migrationDir = "state/.task-authority-migration"
)

// ErrAlreadyMigrated reports that the home is already migrated: re-running a
// completed migration verifies the receipt and installed targets and never
// rewrites.
var ErrAlreadyMigrated = errors.New("task-authority v2 migration already completed")

// AlreadyMigratedError is a typed, inspectable error for homes whose v1
// task-authority state has already been converted to the v2 authority schema.
type AlreadyMigratedError struct {
	HomeDir      string
	SourceDigest string
}

func (e *AlreadyMigratedError) Error() string {
	return fmt.Sprintf("task-authority v2 migration for %s (digest %s) already completed; no rewrite performed", e.HomeDir, e.SourceDigest)
}

func (e *AlreadyMigratedError) Unwrap() error { return ErrAlreadyMigrated }

// MigrationCommand returns the exact apply command template the operator runs
// after reviewing a plan.
func MigrationCommand() string {
	return "munsu migrate task-authority apply --plan <plan.json>"
}

// SourceFile is one legacy v1 source file captured by a migration plan: its
// home-relative path, schema family, and deterministic SHA-256 digest.
type SourceFile struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	SHA256 string `json:"sha256"`
}

// SourceRecord is one recognized v1 record discovered by a migration plan,
// ordered for review. It is the source-side inventory; the plan targets carry
// the converted v2 records.
type SourceRecord struct {
	Kind       string `json:"kind"`
	SourcePath string `json:"source_path"`
	TaskID     string `json:"task_id,omitempty"`
	Generation string `json:"generation,omitempty"`
	ID         string `json:"id,omitempty"`
	State      string `json:"state,omitempty"`
	Current    bool   `json:"current,omitempty"`
}

// QuarantineRecord explains why one v1 source record cannot be converted
// safely, with its exact source path and evidence. A plan that quarantines
// any record cannot be applied: corrupt or conflicting source remains
// untouched until it is resolved and the plan is re-run.
type QuarantineRecord struct {
	SchemaVersion string   `json:"schema_version"`
	Kind          string   `json:"kind"`
	SourcePath    string   `json:"source_path"`
	TaskID        string   `json:"task_id,omitempty"`
	Generation    string   `json:"generation,omitempty"`
	Reason        string   `json:"reason"`
	Evidence      []string `json:"evidence,omitempty"`
}

// PlannedAudit is one deterministic initial typed audit event the migration
// will commit: operation identity, kind, task binding, reason, and resulting
// phase. Timestamps are intentionally absent so the plan is a pure function
// of the source state; apply stamps one shared timestamp.
type PlannedAudit struct {
	OperationID string                   `json:"operation_id"`
	Kind        string                   `json:"kind"`
	TaskID      string                   `json:"task_id,omitempty"`
	Generation  taskauthority.Generation `json:"generation,omitempty"`
	Reason      string                   `json:"reason,omitempty"`
	After       taskauthority.Phase      `json:"after,omitempty"`
}

// MigrationPlan is the immutable, deterministic plan for converting one
// exact home from legacy v1 task-authority records into the v2 authority
// schema. It captures the canonical home identity and rank, source and target
// schemas, a deterministic source digest, the ordered source inventory, the
// converted v2 target records, the planned initial audit events, and
// quarantine outcomes. Planning writes nothing under the home.
type MigrationPlan struct {
	SchemaVersion   string                                 `json:"schema_version"`
	Command         string                                 `json:"command"`
	HomeDir         string                                 `json:"home_dir"`
	HomeIdentity    string                                 `json:"home_identity"`
	HomeRank        string                                 `json:"home_rank,omitempty"`
	SourceSchema    string                                 `json:"source_schema"`
	TargetSchema    string                                 `json:"target_schema"`
	SourceDigest    string                                 `json:"source_digest"`
	Sources         []SourceFile                           `json:"sources"`
	Records         []SourceRecord                         `json:"records"`
	Aggregates      []taskauthority.Aggregate              `json:"aggregates"`
	Holds           []taskauthority.DispatchHold           `json:"holds"`
	Interpretations []taskauthority.DispatchInterpretation `json:"interpretations"`
	Decisions       []taskauthority.DispatchDecision       `json:"decisions"`
	Audits          []PlannedAudit                         `json:"audits"`
	Quarantined     []QuarantineRecord                     `json:"quarantined"`
	RecordCount     int                                    `json:"record_count"`
}

// MigrationReceipt is the durable summary written only after the v2 targets
// are installed, verified, and the legacy sources are archived.
type MigrationReceipt struct {
	SchemaVersion        string `json:"schema_version"`
	HomeDir              string `json:"home_dir"`
	HomeIdentity         string `json:"home_identity"`
	SourceSchema         string `json:"source_schema"`
	TargetSchema         string `json:"target_schema"`
	SourceDigest         string `json:"source_digest"`
	RecordCount          int    `json:"record_count"`
	TargetManifestDigest string `json:"target_manifest_digest"`
	ArchivePath          string `json:"archive_path"`
	CompletedAt          int64  `json:"completed_at"`
}

// canonicalMigrationHome resolves the home to its canonical absolute path.
func canonicalMigrationHome(homeDir string) (string, error) {
	abs, err := filepath.Abs(homeDir)
	if err != nil {
		return "", err
	}
	if canon, err := filepath.EvalSymlinks(abs); err == nil {
		return canon, nil
	}
	return abs, nil
}

// PlanMigration builds the deterministic, read-only migration plan for the
// v1 task-authority state under homeDir. It writes nothing under the home.
// A home that is already migrated (v2 state present with no v1 sources)
// returns a typed AlreadyMigratedError instead of an empty plan.
func PlanMigration(homeDir string) (*MigrationPlan, error) {
	canon, err := canonicalMigrationHome(homeDir)
	if err != nil {
		return nil, err
	}
	if already, err := migrationAlreadyApplied(canon); err != nil {
		return nil, err
	} else if already {
		digest, _ := receiptSourceDigest(canon)
		return nil, &AlreadyMigratedError{HomeDir: canon, SourceDigest: digest}
	}
	identity, rank, err := home.ReadHomeIdentity(canon)
	if err != nil {
		return nil, fmt.Errorf("resolving home identity: %w", err)
	}
	collector := newSourceCollector(canon)
	if err := collector.collect(); err != nil {
		return nil, err
	}
	plan := &MigrationPlan{
		SchemaVersion:   MigrationPlanSchema,
		Command:         MigrationCommand(),
		HomeDir:         canon,
		HomeIdentity:    identity,
		HomeRank:        string(rank),
		SourceSchema:    V1SourceSchema,
		TargetSchema:    SchemaVersion,
		Sources:         collector.sources,
		Records:         collector.records,
		Aggregates:      collector.aggregates,
		Holds:           collector.holds,
		Interpretations: collector.interpretations,
		Decisions:       collector.decisions,
		Quarantined:     collector.quarantined,
	}
	plan.SourceDigest = migrationSourceDigest(plan.Sources)
	plan.Audits = collector.audits(plan.SourceDigest)
	plan.RecordCount = len(plan.Aggregates) + len(plan.Holds) + len(plan.Interpretations) + len(plan.Decisions)
	if err := validateMigrationPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// migrationAlreadyApplied reports whether the home already carries v2
// task-authority state with no v1 sources: a completion receipt, or v2
// documents without any legacy v1 source.
func migrationAlreadyApplied(canon string) (bool, error) {
	if _, err := os.Stat(migrationReceiptPath(canon)); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	hasV1, err := anyV1SourceVisible(canon)
	if err != nil {
		return false, err
	}
	if hasV1 {
		return false, nil
	}
	hasV2, err := hasVisibleEntries(filepath.Join(canon, filepath.FromSlash(authorityRoot)))
	if err != nil {
		return false, err
	}
	return hasV2, nil
}

// receiptSourceDigest reads the completed migration receipt's source digest,
// returning "" when no receipt exists.
func receiptSourceDigest(canon string) (string, error) {
	data, err := os.ReadFile(migrationReceiptPath(canon))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var receipt MigrationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return "", err
	}
	return receipt.SourceDigest, nil
}

// WriteMigrationPlan writes the reviewed plan to path with private 0600
// permissions. An identical existing plan is accepted without rewrite;
// differing content at the same path conflicts.
func WriteMigrationPlan(path string, plan *MigrationPlan) error {
	if err := validateMigrationPlan(plan); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("task-authority migration plan conflict at %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeMigrationFile(path, data)
}

// ReadMigrationPlan reads and validates one reviewed migration plan file.
func ReadMigrationPlan(path string) (*MigrationPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan MigrationPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if err := validateMigrationPlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// validateMigrationPlan checks the plan shape: identity, schemas, digest,
// valid converted targets with exactly one current generation per task,
// planned audit intents, and a consistent record count.
func validateMigrationPlan(plan *MigrationPlan) error {
	if plan == nil {
		return errors.New("task-authority migration plan is required")
	}
	if plan.SchemaVersion != MigrationPlanSchema {
		return fmt.Errorf("invalid task-authority migration plan schema %q", plan.SchemaVersion)
	}
	if plan.Command == "" || !strings.Contains(plan.Command, "munsu migrate task-authority apply") {
		return fmt.Errorf("task-authority migration plan missing apply command")
	}
	if plan.HomeDir == "" || plan.HomeIdentity == "" {
		return fmt.Errorf("task-authority migration plan missing home identity")
	}
	if plan.SourceSchema != V1SourceSchema {
		return fmt.Errorf("invalid task-authority migration source schema %q", plan.SourceSchema)
	}
	if plan.TargetSchema != SchemaVersion {
		return fmt.Errorf("invalid task-authority migration target schema %q", plan.TargetSchema)
	}
	if !validDigest(plan.SourceDigest) {
		return fmt.Errorf("invalid task-authority migration source digest %q", plan.SourceDigest)
	}
	if migrationSourceDigest(plan.Sources) != plan.SourceDigest {
		return fmt.Errorf("task-authority migration plan source digest does not match its source inventory")
	}
	seenSource := map[string]bool{}
	for _, source := range plan.Sources {
		if source.Path == "" || !validDigest(source.SHA256) {
			return fmt.Errorf("task-authority migration plan has invalid source entry %+v", source)
		}
		if seenSource[source.Path] {
			return fmt.Errorf("task-authority migration plan repeats source path %s", source.Path)
		}
		seenSource[source.Path] = true
	}
	seenAgg := map[string]bool{}
	current := map[string]int{}
	for _, agg := range plan.Aggregates {
		if _, err := EncodeAggregate(agg); err != nil {
			return fmt.Errorf("plan aggregate %s/%d invalid: %w", agg.TaskID, agg.Generation, err)
		}
		key := agg.TaskID + "\x00" + agg.Generation.String()
		if seenAgg[key] {
			return fmt.Errorf("plan repeats aggregate %s", key)
		}
		seenAgg[key] = true
		if agg.Current {
			current[agg.TaskID]++
		}
	}
	for taskID, count := range current {
		if count != 1 {
			return fmt.Errorf("plan task %s has %d current generations", taskID, count)
		}
	}
	for _, agg := range plan.Aggregates {
		if current[agg.TaskID] == 0 {
			return fmt.Errorf("plan task %s has no current generation", agg.TaskID)
		}
	}
	for _, hold := range plan.Holds {
		if _, err := EncodeHold(hold); err != nil {
			return fmt.Errorf("plan hold %q invalid: %w", hold.ID, err)
		}
	}
	for _, rec := range plan.Interpretations {
		if _, err := EncodeInterpretation(rec); err != nil {
			return fmt.Errorf("plan interpretation %q invalid: %w", rec.ID, err)
		}
	}
	for _, dec := range plan.Decisions {
		if _, err := EncodeDecision(dec); err != nil {
			return fmt.Errorf("plan decision %q invalid: %w", dec.Key, err)
		}
	}
	seenAudit := map[string]bool{}
	for _, audit := range plan.Audits {
		if audit.OperationID == "" || strings.ContainsAny(audit.OperationID, `/\\`) {
			return fmt.Errorf("plan audit has unsafe operation id %q", audit.OperationID)
		}
		if seenAudit[audit.OperationID] {
			return fmt.Errorf("plan repeats audit operation id %s", audit.OperationID)
		}
		seenAudit[audit.OperationID] = true
		switch audit.Kind {
		case taskauthority.AuditLifecycle:
			if audit.TaskID == "" || audit.Generation == 0 || !audit.After.Valid() {
				return fmt.Errorf("plan lifecycle audit %s is incomplete", audit.OperationID)
			}
		case taskauthority.AuditDispatch:
		default:
			return fmt.Errorf("plan audit %s has unknown kind %q", audit.OperationID, audit.Kind)
		}
	}
	if plan.RecordCount != len(plan.Aggregates)+len(plan.Holds)+len(plan.Interpretations)+len(plan.Decisions) {
		return fmt.Errorf("plan record count %d does not match %d targets", plan.RecordCount, len(plan.Aggregates)+len(plan.Holds)+len(plan.Interpretations)+len(plan.Decisions))
	}
	seenQuarantine := map[string]bool{}
	for _, q := range plan.Quarantined {
		if q.SchemaVersion != MigrationPlanSchema || q.SourcePath == "" || q.Reason == "" {
			return fmt.Errorf("plan quarantine record is incomplete: %+v", q)
		}
		key := q.Kind + "\x00" + q.SourcePath
		if seenQuarantine[key] {
			return fmt.Errorf("plan repeats quarantine %s", key)
		}
		seenQuarantine[key] = true
	}
	return nil
}

// --- source collection ------------------------------------------------------

// sourceCollector walks the three recognized v1 source locations under one
// home and derives the ordered source inventory, converted v2 targets, and
// quarantine outcomes. Collection is strictly read-only.
type sourceCollector struct {
	homeDir         string
	sources         []SourceFile
	records         []SourceRecord
	aggregates      []taskauthority.Aggregate
	holds           []taskauthority.DispatchHold
	interpretations []taskauthority.DispatchInterpretation
	decisions       []taskauthority.DispatchDecision
	quarantined     []QuarantineRecord
	leaseBindings   map[string]bool
}

func newSourceCollector(homeDir string) *sourceCollector {
	return &sourceCollector{
		homeDir:         homeDir,
		sources:         []SourceFile{},
		records:         []SourceRecord{},
		aggregates:      []taskauthority.Aggregate{},
		holds:           []taskauthority.DispatchHold{},
		interpretations: []taskauthority.DispatchInterpretation{},
		decisions:       []taskauthority.DispatchDecision{},
		quarantined:     []QuarantineRecord{},
		leaseBindings:   map[string]bool{},
	}
}

func (c *sourceCollector) collect() error {
	if err := c.collectAggregates(); err != nil {
		return err
	}
	if err := c.collectLeases(); err != nil {
		return err
	}
	if err := c.collectDispatch(); err != nil {
		return err
	}
	sort.Slice(c.sources, func(i, j int) bool { return sourceFileKey(c.sources[i]) < sourceFileKey(c.sources[j]) })
	sort.Slice(c.records, func(i, j int) bool { return sourceRecordKey(c.records[i]) < sourceRecordKey(c.records[j]) })
	sort.Slice(c.aggregates, func(i, j int) bool {
		if c.aggregates[i].TaskID != c.aggregates[j].TaskID {
			return c.aggregates[i].TaskID < c.aggregates[j].TaskID
		}
		return c.aggregates[i].Generation < c.aggregates[j].Generation
	})
	sort.Slice(c.holds, func(i, j int) bool { return c.holds[i].ID < c.holds[j].ID })
	sort.Slice(c.interpretations, func(i, j int) bool { return c.interpretations[i].ID < c.interpretations[j].ID })
	sort.Slice(c.decisions, func(i, j int) bool { return c.decisions[i].Key < c.decisions[j].Key })
	sort.Slice(c.quarantined, func(i, j int) bool { return quarantineKey(c.quarantined[i]) < quarantineKey(c.quarantined[j]) })
	return nil
}

func (c *sourceCollector) addSource(rel, schema string, data []byte) {
	c.sources = append(c.sources, SourceFile{Path: rel, Schema: schema, SHA256: DigestHex(data)})
}

func (c *sourceCollector) quarantine(record QuarantineRecord) {
	record.SchemaVersion = MigrationPlanSchema
	c.quarantined = append(c.quarantined, record)
}

// collectAggregates walks the v1 aggregate store — the output of the legacy
// task-aggregates migration — one task directory at a time. The current
// pointer is authoritative: the pointer-named generation converts to v2 as
// current even when its stale current flag is false (home.ReopenTask leaves
// exactly that shape). Any contradiction quarantines the whole task.
func (c *sourceCollector) collectAggregates() error {
	root := filepath.Join(c.homeDir, filepath.FromSlash(v1AggregatesRelPath))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			c.quarantine(QuarantineRecord{Kind: "aggregate", SourcePath: v1AggregatesRelPath + "/" + name, Reason: "unrecognized or non-regular aggregate entry"})
			continue
		}
		if err := c.collectAggregateTask(name); err != nil {
			return err
		}
	}
	return nil
}

type v1AggDoc struct {
	rel  string
	data []byte
	gen  taskauthority.Generation
	agg  home.TaskAggregate
}

// collectAggregateTask converts one v1 task directory. Any inconsistency —
// corrupt or identity-mismatched documents, duplicate generations, a missing
// or divergent current pointer, conflicting current claims, or an
// unconvertible generation — quarantines the whole task directory with exact
// evidence, never partially.
func (c *sourceCollector) collectAggregateTask(taskID string) error {
	taskRel := v1AggregatesRelPath + "/" + taskID
	taskDir := filepath.Join(c.homeDir, filepath.FromSlash(taskRel))
	quarantine := func(reason string, evidence ...string) {
		c.quarantine(QuarantineRecord{Kind: "aggregate", SourcePath: taskRel, TaskID: taskID, Reason: reason, Evidence: evidence})
	}
	if err := validateTaskID(taskID); err != nil {
		quarantine("invalid task directory", taskRel)
		return nil
	}
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		return err
	}
	var pointerRaw string
	var docs []v1AggDoc
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := taskRel + "/" + name
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			quarantine("unrecognized or non-regular aggregate entry", rel)
			return nil
		}
		switch {
		case name == currentFileName:
			data, err := os.ReadFile(filepath.Join(taskDir, name))
			if err != nil {
				return err
			}
			c.addSource(rel, "aggregate_pointer", data)
			pointerRaw = strings.TrimSpace(string(data))
		case strings.HasSuffix(name, documentExt):
			data, err := os.ReadFile(filepath.Join(taskDir, name))
			if err != nil {
				return err
			}
			c.addSource(rel, "aggregate", data)
			docs = append(docs, v1AggDoc{rel: rel, data: data})
		default:
			quarantine("unrecognized or non-regular aggregate entry", rel)
			return nil
		}
	}
	if len(docs) == 0 {
		quarantine("empty task directory", taskRel)
		return nil
	}

	// Decode every document and validate its identity against path and task.
	parsed := make([]v1AggDoc, 0, len(docs))
	seenGen := map[uint64]bool{}
	for i := range docs {
		doc := &docs[i]
		fileGen := strings.TrimSuffix(filepath.Base(doc.rel), documentExt)
		gen, err := taskauthority.ParseGeneration(fileGen)
		if err != nil {
			quarantine("non-canonical generation filename", doc.rel, err.Error())
			return nil
		}
		var agg home.TaskAggregate
		if err := json.Unmarshal(doc.data, &agg); err != nil {
			quarantine("corrupt aggregate document", doc.rel, err.Error())
			return nil
		}
		if agg.SchemaVersion != V1SchemaVersion {
			quarantine("unsupported aggregate schema", doc.rel, agg.SchemaVersion)
			return nil
		}
		if agg.TaskID != taskID {
			quarantine("aggregate identity mismatch", doc.rel, "document task_id "+agg.TaskID)
			return nil
		}
		docGen, err := taskauthority.ParseGeneration(agg.Generation)
		if err != nil || docGen != gen {
			quarantine("aggregate generation identity mismatch", doc.rel, "filename "+fileGen, "document generation "+agg.Generation)
			return nil
		}
		if seenGen[uint64(gen)] {
			quarantine("duplicate generation documents", doc.rel, "generation "+gen.String())
			return nil
		}
		seenGen[uint64(gen)] = true
		doc.gen = gen
		doc.agg = agg
		parsed = append(parsed, *doc)
	}

	// Pointer authority rules.
	pointerGen, pointerPresent := taskauthority.Generation(0), false
	if pointerRaw != "" {
		gen, err := taskauthority.ParseGeneration(pointerRaw)
		if err != nil {
			quarantine("corrupt current pointer", taskRel+"/"+currentFileName, pointerRaw)
			return nil
		}
		pointerGen, pointerPresent = gen, true
	}
	var pointerDoc *v1AggDoc
	if pointerPresent {
		for i := range parsed {
			if parsed[i].gen == pointerGen {
				pointerDoc = &parsed[i]
				break
			}
		}
		if pointerDoc == nil {
			quarantine("current pointer divergence", taskRel+"/"+currentFileName, "pointer names generation "+pointerGen.String())
			return nil
		}
		for i := range parsed {
			if parsed[i].agg.Current && parsed[i].gen != pointerGen {
				quarantine("conflicting current generation claims", parsed[i].rel, "pointer names generation "+pointerGen.String())
				return nil
			}
		}
	} else {
		for i := range parsed {
			if parsed[i].agg.Current {
				quarantine("current flag without pointer", parsed[i].rel)
				return nil
			}
		}
		quarantine("no current generation", taskRel, "v2 requires exactly one current generation per task")
		return nil
	}

	// Convert every generation; pointer-named is current.
	for i := range parsed {
		doc := &parsed[i]
		converted, err := convertV1Aggregate(&doc.agg, doc.gen == pointerGen)
		if err != nil {
			quarantine("aggregate cannot be converted safely", doc.rel, err.Error())
			return nil
		}
		c.aggregates = append(c.aggregates, converted)
		c.records = append(c.records, SourceRecord{Kind: "aggregate", SourcePath: doc.rel, TaskID: taskID, Generation: doc.agg.Generation, State: doc.agg.State, Current: doc.gen == pointerGen})
		if converted.Worktree != nil {
			c.leaseBindings[leaseBindingKey(taskID, converted.Generation, converted.Worktree.LeaseID, converted.Worktree.FenceToken)] = true
		}
	}
	return nil
}

// convertV1Aggregate maps one legacy aggregate into a v2 aggregate with
// Revision one, preserving generation/current lifecycle meaning, phase, and
// bindings. Projection and audit-source evidence lists are rebuildable
// projections and are intentionally not carried into v2.
func convertV1Aggregate(v1 *home.TaskAggregate, current bool) (taskauthority.Aggregate, error) {
	gen, err := taskauthority.ParseGeneration(v1.Generation)
	if err != nil {
		return taskauthority.Aggregate{}, err
	}
	phase, err := v1PhaseToV2(v1.State)
	if err != nil {
		return taskauthority.Aggregate{}, err
	}
	if strings.TrimSpace(v1.Owner) == "" {
		return taskauthority.Aggregate{}, errors.New("aggregate missing owner")
	}
	agg := taskauthority.Aggregate{
		SchemaVersion: taskauthority.TaskAuthoritySchema,
		TaskID:        v1.TaskID,
		Generation:    gen,
		Revision:      taskauthority.FirstRevision,
		Current:       current,
		Definition: taskauthority.TaskDefinition{
			Owner:        v1.Owner,
			Description:  v1.Definition,
			Kind:         v1.Kind,
			Project:      v1.Project,
			ParentTaskID: v1.ParentTaskID,
		},
		Phase:                        phase,
		PhaseDetail:                  v1.StateDetail,
		DispatchInterpretationID:     v1.DispatchInterpretationID,
		DispatchInterpretationDigest: v1.DispatchInterpretationDigest,
	}
	if v1.Endpoint != nil {
		if v1.Endpoint.TaskGeneration != v1.Generation {
			return taskauthority.Aggregate{}, fmt.Errorf("endpoint binding targets generation %s, not %s", v1.Endpoint.TaskGeneration, v1.Generation)
		}
		agg.Endpoint = &taskauthority.EndpointBinding{
			Backend:      v1.Endpoint.Backend,
			Handle:       v1.Endpoint.Handle,
			LeaseID:      v1.Endpoint.LeaseID,
			FenceToken:   v1.Endpoint.FenceToken,
			SessionOwner: v1.Endpoint.SessionOwner,
			WorkspaceID:  v1.Endpoint.WorkspaceID,
			TabID:        v1.Endpoint.TabID,
			BoundAtUnix:  v1.Endpoint.BoundAtUnix,
		}
	}
	if v1.Worktree != nil {
		if v1.Worktree.TaskGeneration != v1.Generation {
			return taskauthority.Aggregate{}, fmt.Errorf("worktree binding targets generation %s, not %s", v1.Worktree.TaskGeneration, v1.Generation)
		}
		agg.Worktree = &taskauthority.WorktreeBinding{
			RepositoryIdentity: v1.Worktree.RepositoryIdentity,
			Path:               v1.Worktree.Path,
			GitDir:             v1.Worktree.GitDir,
			CommonDir:          v1.Worktree.CommonDir,
			Head:               v1.Worktree.Head,
			LeaseID:            v1.Worktree.LeaseID,
			FenceToken:         v1.Worktree.FenceToken,
			BoundAtUnix:        v1.Worktree.BoundAtUnix,
		}
	}
	// The v2 schema layer validates the converted document and fails closed
	// on any shape the store could not serve.
	if _, err := EncodeAggregate(agg); err != nil {
		return taskauthority.Aggregate{}, err
	}
	return agg, nil
}

// v1PhaseToV2 maps legacy aggregate states to v2 phases. States with no v2
// phase (projection-only status values) are quarantined, never inferred.
func v1PhaseToV2(state string) (taskauthority.Phase, error) {
	switch strings.TrimSpace(state) {
	case "", "queued":
		return taskauthority.PhaseQueued, nil
	case "blocked":
		return taskauthority.PhaseBlocked, nil
	case "working", "in-flight":
		return taskauthority.PhaseWorking, nil
	case "done":
		return taskauthority.PhaseDone, nil
	case "resolved":
		return taskauthority.PhaseResolved, nil
	case "retired":
		return taskauthority.PhaseRetired, nil
	default:
		return "", fmt.Errorf("unsupported v1 state %q has no v2 phase", state)
	}
}

func leaseBindingKey(taskID string, generation taskauthority.Generation, leaseID, fenceToken string) string {
	return taskID + "\x00" + generation.String() + "\x00" + leaseID + "\x00" + fenceToken
}

// collectLeases walks the v1 worktree-lease markers. Every lease must match a
// converted aggregate's worktree binding exactly; v2 embeds the binding in
// the aggregate, so a lease without its aggregate is quarantined.
func (c *sourceCollector) collectLeases() error {
	root := filepath.Join(c.homeDir, filepath.FromSlash(v1WorktreeLeasesRelPath))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		rel, _ := filepath.Rel(c.homeDir, path)
		rel = filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(name, documentExt) {
			c.quarantine(QuarantineRecord{Kind: "worktree_lease", SourcePath: rel, Reason: "unrecognized or non-regular lease entry"})
			return nil
		}
		parts := strings.Split(rel, "/")
		if len(parts) != 6 || strings.Join(parts[:3], "/") != v1WorktreeLeasesRelPath {
			c.quarantine(QuarantineRecord{Kind: "worktree_lease", SourcePath: rel, Reason: "unrecognized lease path"})
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		c.addSource(rel, "worktree_lease", data)
		var marker struct {
			TaskID         string `json:"task_id"`
			TaskGeneration string `json:"task_generation"`
			LeaseID        string `json:"lease_id"`
			FenceToken     string `json:"fence_token"`
		}
		if err := json.Unmarshal(data, &marker); err != nil {
			c.quarantine(QuarantineRecord{Kind: "worktree_lease", SourcePath: rel, Reason: "corrupt lease marker", Evidence: []string{err.Error()}})
			return nil
		}
		gen, err := taskauthority.ParseGeneration(marker.TaskGeneration)
		if err != nil {
			c.quarantine(QuarantineRecord{Kind: "worktree_lease", SourcePath: rel, TaskID: marker.TaskID, Generation: marker.TaskGeneration, Reason: "invalid lease generation", Evidence: []string{err.Error()}})
			return nil
		}
		if parts[3] != marker.TaskID || parts[4] != marker.TaskGeneration || strings.TrimSuffix(parts[5], documentExt) != marker.LeaseID {
			c.quarantine(QuarantineRecord{Kind: "worktree_lease", SourcePath: rel, TaskID: marker.TaskID, Generation: marker.TaskGeneration, Reason: "lease path identity mismatch"})
			return nil
		}
		if !c.leaseBindings[leaseBindingKey(marker.TaskID, gen, marker.LeaseID, marker.FenceToken)] {
			c.quarantine(QuarantineRecord{Kind: "worktree_lease", SourcePath: rel, TaskID: marker.TaskID, Generation: marker.TaskGeneration, Reason: "lease has no matching aggregate worktree binding"})
			return nil
		}
		c.records = append(c.records, SourceRecord{Kind: "worktree_lease", SourcePath: rel, TaskID: marker.TaskID, Generation: marker.TaskGeneration, ID: marker.LeaseID})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// collectDispatch walks the v1 dispatch-control store. Empty standard family
// directories are silently archived; unrecognized entries and records that
// cannot be validated as v2 are quarantined.
func (c *sourceCollector) collectDispatch() error {
	root := filepath.Join(c.homeDir, filepath.FromSlash(v1DispatchControlRelPath))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			c.quarantine(QuarantineRecord{Kind: "unrecognized", SourcePath: v1DispatchControlRelPath + "/" + name, Reason: "unrecognized or non-regular dispatch-control entry"})
			continue
		}
		switch name {
		case "holds", "interpretations", "decisions":
			if err := c.collectDispatchFamily(name); err != nil {
				return err
			}
		default:
			c.quarantine(QuarantineRecord{Kind: "unrecognized", SourcePath: v1DispatchControlRelPath + "/" + name, Reason: "unrecognized dispatch-control directory"})
		}
	}
	return nil
}

func dispatchFamilyKind(family string) string {
	switch family {
	case "holds":
		return "dispatch_hold"
	case "interpretations":
		return "dispatch_interpretation"
	case "decisions":
		return "dispatch_decision"
	}
	return "unrecognized"
}

func (c *sourceCollector) collectDispatchFamily(family string) error {
	subRel := v1DispatchControlRelPath + "/" + family
	dir := filepath.Join(c.homeDir, filepath.FromSlash(subRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	kind := dispatchFamilyKind(family)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := subRel + "/" + name
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(name, documentExt) {
			c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "unrecognized or non-regular dispatch entry"})
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		c.addSource(rel, kind, data)
		rawID := strings.TrimSuffix(name, documentExt)
		switch family {
		case "holds":
			var rec home.DispatchHold
			if err := json.Unmarshal(data, &rec); err != nil {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "corrupt dispatch hold", Evidence: []string{err.Error()}})
				continue
			}
			if rec.SchemaVersion != V1DispatchSchema {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "unsupported dispatch hold schema", Evidence: []string{rec.SchemaVersion}})
				continue
			}
			if rec.ID != rawID {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "dispatch hold identity mismatch", Evidence: []string{"filename " + rawID, "document id " + rec.ID}})
				continue
			}
			hold := taskauthority.DispatchHold{
				SchemaVersion: taskauthority.TaskAuthoritySchema,
				ID:            rec.ID,
				Scope: taskauthority.DispatchHoldScope{
					ProjectIDs:  normalizeStrings(rec.Scope.ProjectIDs),
					TaskIDs:     normalizeStrings(rec.Scope.TaskIDs),
					Generations: normalizeStrings(rec.Scope.Generations),
					ParentIDs:   normalizeStrings(rec.Scope.ParentIDs),
				},
				Actions:    convertDispatchActions(rec.Actions),
				Reason:     rec.Reason,
				CreatedAt:  rec.CreatedAt,
				ReleasedAt: rec.ReleasedAt,
			}
			if _, err := EncodeHold(hold); err != nil {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "dispatch hold cannot be converted safely", Evidence: []string{err.Error()}})
				continue
			}
			c.holds = append(c.holds, hold)
			c.records = append(c.records, SourceRecord{Kind: kind, SourcePath: rel, ID: rec.ID})
		case "interpretations":
			var rec home.DispatchInterpretation
			if err := json.Unmarshal(data, &rec); err != nil {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "corrupt dispatch interpretation", Evidence: []string{err.Error()}})
				continue
			}
			if rec.SchemaVersion != V1DispatchSchema {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "unsupported dispatch interpretation schema", Evidence: []string{rec.SchemaVersion}})
				continue
			}
			if rec.ID != rawID {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "dispatch interpretation identity mismatch", Evidence: []string{"filename " + rawID, "document id " + rec.ID}})
				continue
			}
			converted := taskauthority.DispatchInterpretation{
				SchemaVersion:            taskauthority.TaskAuthoritySchema,
				ID:                       rec.ID,
				RequestedOrder:           normalizeStrings(rec.RequestedOrder),
				ComputedReadiness:        convertReadiness(rec.ComputedReadiness),
				SelectedTasks:            normalizeStrings(rec.SelectedTasks),
				Evidence:                 convertDispatchEvidence(rec.Evidence),
				DependencySnapshotDigest: rec.DependencySnapshotDigest,
				ParentInterpretationID:   rec.ParentInterpretationID,
				Outcome:                  rec.Outcome,
				DecisionKey:              rec.DecisionKey,
				CreatedAt:                rec.CreatedAt,
			}
			if _, err := EncodeInterpretation(converted); err != nil {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "dispatch interpretation cannot be converted safely", Evidence: []string{err.Error()}})
				continue
			}
			c.interpretations = append(c.interpretations, converted)
			c.records = append(c.records, SourceRecord{Kind: kind, SourcePath: rel, ID: rec.ID})
		case "decisions":
			var rec home.DispatchDecision
			if err := json.Unmarshal(data, &rec); err != nil {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "corrupt dispatch decision", Evidence: []string{err.Error()}})
				continue
			}
			if rec.SchemaVersion != V1DispatchSchema {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "unsupported dispatch decision schema", Evidence: []string{rec.SchemaVersion}})
				continue
			}
			if rec.Key != rawID {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "dispatch decision identity mismatch", Evidence: []string{"filename " + rawID, "document key " + rec.Key}})
				continue
			}
			converted := taskauthority.DispatchDecision{
				SchemaVersion:    taskauthority.TaskAuthoritySchema,
				Key:              rec.Key,
				InterpretationID: rec.InterpretationID,
				Reason:           rec.Reason,
				CreatedAt:        rec.CreatedAt,
				ResolvedAt:       rec.ResolvedAt,
				Answer:           rec.Answer,
			}
			if _, err := EncodeDecision(converted); err != nil {
				c.quarantine(QuarantineRecord{Kind: kind, SourcePath: rel, Reason: "dispatch decision cannot be converted safely", Evidence: []string{err.Error()}})
				continue
			}
			c.decisions = append(c.decisions, converted)
			c.records = append(c.records, SourceRecord{Kind: kind, SourcePath: rel, ID: rec.Key})
		}
	}
	return nil
}

func convertDispatchActions(actions []home.DispatchAction) []taskauthority.DispatchAction {
	out := make([]taskauthority.DispatchAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, taskauthority.DispatchAction(action))
	}
	return out
}

// normalizeStrings returns nil for an empty slice so v2 documents encode
// without omitted empty fields and round-trip through the store exactly.
func normalizeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return in
}

func convertReadiness(readiness []home.DispatchReadiness) []taskauthority.DispatchReadiness {
	if len(readiness) == 0 {
		return nil
	}
	out := make([]taskauthority.DispatchReadiness, 0, len(readiness))
	for _, r := range readiness {
		out = append(out, taskauthority.DispatchReadiness{TaskID: r.TaskID, Generation: r.Generation, Ready: r.Ready, BlockingReasons: r.BlockingReasons})
	}
	return out
}

func convertDispatchEvidence(evidence []home.DispatchEvidence) []taskauthority.DispatchEvidence {
	if len(evidence) == 0 {
		return nil
	}
	out := make([]taskauthority.DispatchEvidence, 0, len(evidence))
	for _, e := range evidence {
		out = append(out, taskauthority.DispatchEvidence{Source: e.Source, Path: e.Path, Field: e.Field, Value: e.Value})
	}
	return out
}

// audits derives the deterministic initial typed audit intents from the
// converted targets and the source digest.
func (c *sourceCollector) audits(sourceDigest string) []PlannedAudit {
	var out []PlannedAudit
	for _, agg := range c.aggregates {
		out = append(out, PlannedAudit{
			OperationID: migrationOpID(sourceDigest, "lifecycle", agg.TaskID, agg.Generation),
			Kind:        taskauthority.AuditLifecycle,
			TaskID:      agg.TaskID,
			Generation:  agg.Generation,
			Reason:      "migrated from v1 task aggregate",
			After:       agg.Phase,
		})
	}
	if len(c.holds)+len(c.interpretations)+len(c.decisions) > 0 {
		out = append(out, PlannedAudit{
			OperationID: migrationOpID(sourceDigest, "dispatch", "", 0),
			Kind:        taskauthority.AuditDispatch,
			Reason:      fmt.Sprintf("migrated %d v1 dispatch record(s)", len(c.holds)+len(c.interpretations)+len(c.decisions)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperationID < out[j].OperationID })
	return out
}

// migrationOpID derives a deterministic, filesystem-safe audit operation id
// from the full source identity and digest, never from task text alone.
func migrationOpID(sourceDigest, kind, taskID string, generation taskauthority.Generation) string {
	sum := sha256.Sum256([]byte(MigrationPlanSchema + "\x00" + sourceDigest + "\x00" + kind + "\x00" + taskID + "\x00" + generation.String()))
	return "migrate-" + hex.EncodeToString(sum[:])
}

// migrationSourceDigest is the deterministic digest of the sorted source
// inventory: canonical JSON of {path, schema, sha256} entries.
func migrationSourceDigest(sources []SourceFile) string {
	data, _ := json.Marshal(sources)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sourceFileKey(f SourceFile) string { return f.Path + "\x00" + f.Schema }
func sourceRecordKey(r SourceRecord) string {
	return r.Kind + "\x00" + r.SourcePath
}
func quarantineKey(q QuarantineRecord) string {
	return q.Kind + "\x00" + q.SourcePath
}
