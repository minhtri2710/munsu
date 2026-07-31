package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

var taskAggregateMigrationCrashAfter string

func canonicalTaskAggregateHome(homeDir string) (string, error) {
	abs, err := filepath.Abs(homeDir)
	if err != nil {
		return "", err
	}
	if canon, err := filepath.EvalSymlinks(abs); err == nil {
		return canon, nil
	}
	return abs, nil
}

func WriteTaskAggregateMigrationPlan(path string, plan *TaskAggregateMigrationPlan) error {
	if err := validateTaskAggregatePlan(plan); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(data) {
			return fmt.Errorf("task aggregate migration plan conflict at %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func ReadTaskAggregateMigrationPlan(path string) (*TaskAggregateMigrationPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan TaskAggregateMigrationPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if err := validateTaskAggregatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func PlanTaskAggregateMigration(homeDir string) (*TaskAggregateMigrationPlan, error) {
	canonHome, err := canonicalTaskAggregateHome(homeDir)
	if err != nil {
		return nil, err
	}
	identity, _, err := ReadHomeIdentity(canonHome)
	if err != nil {
		identity = filepath.Base(canonHome)
	}
	candidates, quarantined, sources, err := collectTaskAggregateCandidates(canonHome)
	if err != nil {
		return nil, err
	}
	existingAggregates, err := ListTaskAggregates(canonHome)
	if err != nil {
		return nil, err
	}
	markImplicitCurrentGenerations(candidates)
	attachProjectionGenerations(candidates)
	byTaskGen := map[string][]TaskAggregateCandidate{}
	for _, c := range candidates {
		key := c.TaskID + "\x00" + c.Generation
		byTaskGen[key] = append(byTaskGen[key], c)
	}
	keys := make([]string, 0, len(byTaskGen))
	for k := range byTaskGen {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	currentByTask := map[string][]string{}
	allTaskIDs := map[string]bool{}
	plan := &TaskAggregateMigrationPlan{SchemaVersion: "munsu.task-aggregate-migration/v1", HomeDir: canonHome, HomeIdentity: identity, SourceDigest: taskAggregateSourceDigest(sources), Quarantined: quarantined}
	for _, existing := range existingAggregates {
		plan.Aggregates = append(plan.Aggregates, existing)
		allTaskIDs[existing.TaskID] = true
		if existing.Current {
			currentByTask[existing.TaskID] = append(currentByTask[existing.TaskID], existing.Generation)
		}
	}
	for _, k := range keys {
		agg, quarantine := ResolveTaskAggregate(byTaskGen[k])
		if quarantine != nil {
			plan.Quarantined = append(plan.Quarantined, *quarantine)
			continue
		}
		allTaskIDs[agg.TaskID] = true
		if agg.Current {
			currentByTask[agg.TaskID] = append(currentByTask[agg.TaskID], agg.Generation)
		}
		plan.Aggregates = upsertAggregate(plan.Aggregates, *agg)
	}
	allTaskIDs = map[string]bool{}
	currentByTask = map[string][]string{}
	for _, agg := range plan.Aggregates {
		allTaskIDs[agg.TaskID] = true
		if agg.Current {
			currentByTask[agg.TaskID] = append(currentByTask[agg.TaskID], agg.Generation)
		}
	}
	for taskID := range allTaskIDs {
		generations := currentByTask[taskID]
		sort.Strings(generations)
		if len(generations) != 1 {
			reason := "missing current generation"
			if len(generations) > 1 {
				reason = "conflicting current generations"
			}
			plan.Quarantined = append(plan.Quarantined, currentGenerationQuarantine(taskID, reason, byTaskGen))
			for i := range plan.Aggregates {
				if plan.Aggregates[i].TaskID == taskID {
					plan.Aggregates[i].Current = false
				}
			}
		}
	}
	sort.Slice(plan.Aggregates, func(i, j int) bool { return aggregateKey(plan.Aggregates[i]) < aggregateKey(plan.Aggregates[j]) })
	sort.Slice(plan.Quarantined, func(i, j int) bool { return quarantineKey(plan.Quarantined[i]) < quarantineKey(plan.Quarantined[j]) })
	plan.RecordCount = len(plan.Aggregates)
	return plan, nil
}

func ApplyTaskAggregateMigration(plan *TaskAggregateMigrationPlan) (*TaskAggregateMigrationReceipt, error) {
	if plan == nil {
		return nil, fmt.Errorf("task aggregate migration plan is required")
	}
	if err := validateTaskAggregatePlan(plan); err != nil {
		return nil, err
	}
	receiptPath := filepath.Join(plan.HomeDir, taskAggregateMigrationDir, "receipt.json")
	if receipt, err := readTaskAggregateReceipt(receiptPath); err == nil {
		if receipt.HomeIdentity != plan.HomeIdentity || receipt.SourceDigest != plan.SourceDigest || receipt.RecordCount != plan.RecordCount {
			return nil, fmt.Errorf("task aggregate migration receipt does not match plan")
		}
		if err := verifyTaskAggregateInstall(plan); err != nil {
			return nil, err
		}
		return receipt, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	current, err := PlanTaskAggregateMigration(plan.HomeDir)
	if err != nil {
		return nil, err
	}
	canonHome, err := canonicalTaskAggregateHome(plan.HomeDir)
	if err != nil {
		return nil, err
	}
	if canonHome != plan.HomeDir {
		return nil, fmt.Errorf("home identity changed: plan %s current %s", plan.HomeDir, canonHome)
	}
	if current.HomeIdentity != plan.HomeIdentity {
		return nil, fmt.Errorf("home identity changed: plan %s current %s", plan.HomeIdentity, current.HomeIdentity)
	}
	if current.SourceDigest != plan.SourceDigest {
		return nil, fmt.Errorf("source digest changed: plan %s current %s", plan.SourceDigest, current.SourceDigest)
	}
	if !reflect.DeepEqual(current.Aggregates, plan.Aggregates) || !reflect.DeepEqual(current.Quarantined, plan.Quarantined) {
		return nil, fmt.Errorf("task aggregate migration plan no longer matches current source")
	}

	stageRoot := filepath.Join(plan.HomeDir, taskAggregateMigrationDir, "stage", plan.SourceDigest)
	if err := os.RemoveAll(stageRoot); err != nil {
		return nil, err
	}
	for _, agg := range plan.Aggregates {
		if err := writeJSONFile(filepath.Join(stageRoot, taskAggregateRelPath(agg.TaskID, agg.Generation)), agg); err != nil {
			return nil, err
		}
		if agg.Current {
			if err := writeTextFile(filepath.Join(stageRoot, taskAggregateDir, agg.TaskID, taskCurrentFile), agg.Generation+"\n"); err != nil {
				return nil, err
			}
		}
	}
	for _, q := range plan.Quarantined {
		if err := writeJSONFile(filepath.Join(stageRoot, taskAggregateQuarantine, quarantineFileName(q)), q); err != nil {
			return nil, err
		}
	}
	manifest, err := taskAggregateManifest(plan)
	if err != nil {
		return nil, err
	}
	if err := writeJSONFile(filepath.Join(stageRoot, taskAggregateMigrationDir, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	if err := verifyTaskAggregateStage(stageRoot, plan); err != nil {
		return nil, err
	}
	if err := installTaskAggregateStage(stageRoot, plan.HomeDir); err != nil {
		return nil, err
	}
	if err := verifyTaskAggregateInstall(plan); err != nil {
		return nil, err
	}
	if taskAggregateMigrationCrashAfter == "install" {
		return nil, fmt.Errorf("injected task aggregate migration crash after install")
	}
	receipt := &TaskAggregateMigrationReceipt{HomeDir: plan.HomeDir, HomeIdentity: plan.HomeIdentity, SourceDigest: plan.SourceDigest, RecordCount: plan.RecordCount, CompletedAt: time.Now().Unix()}
	if err := writeJSONFile(receiptPath, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

type sourceDigestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func collectTaskAggregateCandidates(homeDir string) ([]TaskAggregateCandidate, []TaskAggregateQuarantineRecord, []sourceDigestEntry, error) {
	var candidates []TaskAggregateCandidate
	var quarantined []TaskAggregateQuarantineRecord
	var sources []sourceDigestEntry
	if err := collectTaskAggregateMeta(homeDir, filepath.Join(homeDir, "state"), "state", &candidates, &quarantined, &sources); err != nil {
		return nil, nil, nil, err
	}
	captainsRoot := filepath.Join(homeDir, "captains")
	captains, err := os.ReadDir(captainsRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, err
	}
	for _, entry := range captains {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		captainState := filepath.Join(captainsRoot, entry.Name(), "state")
		relBase := filepath.ToSlash(filepath.Join("captains", entry.Name(), "state"))
		if err := collectTaskAggregateMeta(homeDir, captainState, relBase, &candidates, &quarantined, &sources); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := collectTaskAggregateBacklog(homeDir, &candidates, &quarantined, &sources); err != nil {
		return nil, nil, nil, err
	}
	if err := collectTaskAggregateAuditFiles(homeDir, &candidates, &sources); err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidateKey(candidates[i]) < candidateKey(candidates[j]) })
	sort.Slice(quarantined, func(i, j int) bool { return quarantineKey(quarantined[i]) < quarantineKey(quarantined[j]) })
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return candidates, quarantined, sources, nil
}

func upsertAggregate(aggregates []TaskAggregate, candidate TaskAggregate) []TaskAggregate {
	for i, agg := range aggregates {
		if agg.TaskID == candidate.TaskID && agg.Generation == candidate.Generation {
			aggregates[i] = candidate
			return aggregates
		}
	}
	return append(aggregates, candidate)
}

func markImplicitCurrentGenerations(candidates []TaskAggregateCandidate) {
	byTask := map[string][]int{}
	currentCount := map[string]int{}
	for i, c := range candidates {
		if c.Projection || c.Generation == "" {
			continue
		}
		byTask[c.TaskID] = append(byTask[c.TaskID], i)
		if c.Current {
			currentCount[c.TaskID]++
		}
	}
	for taskID, idxs := range byTask {
		if currentCount[taskID] != 0 {
			continue
		}
		latest := -1
		for _, idx := range idxs {
			if candidates[idx].Inactive {
				continue
			}
			if latest < 0 || generationLess(candidates[latest].Generation, candidates[idx].Generation) {
				latest = idx
			}
		}
		if latest >= 0 {
			candidates[latest].Current = true
		}
	}
}

func attachProjectionGenerations(candidates []TaskAggregateCandidate) {
	current := map[string]string{}
	for _, c := range candidates {
		if c.Projection || c.Generation == "" || !c.Current {
			continue
		}
		if prior := current[c.TaskID]; prior == "" || generationLess(prior, c.Generation) {
			current[c.TaskID] = c.Generation
		}
	}
	for i := range candidates {
		if !candidates[i].Projection || candidates[i].Generation != "" {
			continue
		}
		if gen := current[candidates[i].TaskID]; gen != "" {
			candidates[i].Generation = gen
			candidates[i].Current = true
		} else {
			candidates[i].Generation = "1"
		}
	}
}

func generationLess(a, b string) bool {
	ai, aerr := strconv.ParseUint(a, 10, 64)
	bi, berr := strconv.ParseUint(b, 10, 64)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}

func collectTaskAggregateMeta(homeDir, stateDir, relBase string, candidates *[]TaskAggregateCandidate, quarantined *[]TaskAggregateQuarantineRecord, sources *[]sourceDigestEntry) error {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(stateDir, entry.Name())
		rel := filepath.ToSlash(filepath.Join(relBase, entry.Name()))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		*sources = append(*sources, sourceDigestEntry{Path: rel, SHA256: digestHex(data)})
		fileID := strings.TrimSuffix(entry.Name(), ".meta")
		meta, err := parseTaskAggregateMeta(fileID, data)
		id := fileID
		if meta != nil && strings.TrimSpace(meta["task_id"]) != "" {
			id = strings.TrimSpace(meta["task_id"])
		}
		if err != nil {
			*quarantined = append(*quarantined, TaskAggregateQuarantineRecord{SchemaVersion: taskAggregateSchema, TaskID: id, Reason: "corrupt source", Evidence: []TaskAggregateEvidence{{Kind: "meta", Path: rel, Field: "parse", Value: err.Error()}}})
			continue
		}
		if err := validateTaskID(id); err != nil {
			*quarantined = append(*quarantined, TaskAggregateQuarantineRecord{SchemaVersion: taskAggregateSchema, TaskID: fileID, Reason: "corrupt source", Evidence: []TaskAggregateEvidence{{Kind: "meta", Path: rel, Field: "task_id", Value: err.Error()}}})
			continue
		}
		generation := strings.TrimSpace(meta["generation"])
		if generation == "" {
			generation = "1"
		}
		owner := strings.TrimSpace(meta["owner"])
		if owner == "" {
			owner = fallbackTaskOwner(homeDir, relBase)
		}
		current := isTrue(meta["current"]) || isTrue(meta["active"])
		inactive := isExplicitFalse(meta["current"]) || isExplicitFalse(meta["active"])
		state, detail := stateFromMeta(meta)
		*candidates = append(*candidates, TaskAggregateCandidate{
			TaskID:           id,
			Generation:       generation,
			Owner:            owner,
			Definition:       strings.TrimSpace(meta["description"]),
			State:            state,
			StateDetail:      detail,
			Project:          pickProject(meta),
			Kind:             meta["kind"],
			Current:          current,
			Inactive:         inactive,
			Source:           TaskAggregateSource{Kind: "meta", Path: rel, Field: "owner"},
			OwnerSource:      TaskAggregateSource{Kind: "meta", Path: rel, Field: "owner"},
			DefinitionSource: TaskAggregateSource{Kind: "meta", Path: rel, Field: "description"},
			StateSource:      TaskAggregateSource{Kind: "meta", Path: rel, Field: "state"},
		})
	}
	return nil
}

func collectTaskAggregateAuditFiles(homeDir string, candidates *[]TaskAggregateCandidate, sources *[]sourceDigestEntry) error {
	roots := []string{filepath.Join(homeDir, "state"), filepath.Join(homeDir, "data")}
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".task-aggregate") {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			isStatus := strings.HasSuffix(name, ".status")
			isBrief := name == "brief.md"
			if !isStatus && !isBrief {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(homeDir, path)
			rel = filepath.ToSlash(rel)
			*sources = append(*sources, sourceDigestEntry{Path: rel, SHA256: digestHex(data)})
			if isStatus {
				id := strings.TrimSuffix(name, ".status")
				state, detail := lastStatusState(string(data))
				if state != "" {
					*candidates = append(*candidates, TaskAggregateCandidate{TaskID: id, State: state, StateDetail: detail, Projection: true, Source: TaskAggregateSource{Kind: "status", Path: rel, Field: "state"}, StateSource: TaskAggregateSource{Kind: "status", Path: rel, Field: "state"}})
				}
			}
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func collectTaskAggregateBacklog(homeDir string, candidates *[]TaskAggregateCandidate, quarantined *[]TaskAggregateQuarantineRecord, sources *[]sourceDigestEntry) error {
	paths := []string{filepath.Join(homeDir, "data", "md"), filepath.Join(homeDir, "data", "backlog.md")}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		rel, _ := filepath.Rel(homeDir, path)
		rel = filepath.ToSlash(rel)
		*sources = append(*sources, sourceDigestEntry{Path: rel, SHA256: digestHex(data)})
		items, bad := parseBacklogProjection(data, rel)
		*quarantined = append(*quarantined, bad...)
		for _, item := range items {
			*candidates = append(*candidates, TaskAggregateCandidate{
				TaskID:           item.ID,
				Definition:       item.Description,
				State:            item.State,
				Current:          item.State != "done",
				Projection:       true,
				Source:           TaskAggregateSource{Kind: "backlog", Path: rel, Field: "description"},
				DefinitionSource: TaskAggregateSource{Kind: "backlog", Path: rel, Field: "description"},
				StateSource:      TaskAggregateSource{Kind: "backlog", Path: rel, Field: "state"},
			})
		}
	}
	return nil
}

type backlogProjectionItem struct {
	ID          string
	Description string
	State       string
}

func parseBacklogProjection(data []byte, rel string) ([]backlogProjectionItem, []TaskAggregateQuarantineRecord) {
	var items []backlogProjectionItem
	var bad []TaskAggregateQuarantineRecord
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "- ") {
			continue
		}
		if !strings.HasPrefix(line, "- [") || len(line) < 5 {
			bad = append(bad, corruptBacklog(rel, lineNo+1, line, "invalid backlog marker"))
			continue
		}
		marker := line[3:4]
		rest := strings.TrimSpace(line[5:])
		sep := " - "
		idx := strings.Index(rest, sep)
		if idx < 0 {
			sep = ": "
			idx = strings.Index(rest, sep)
		}
		if idx < 0 {
			bad = append(bad, corruptBacklog(rel, lineNo+1, line, "missing backlog separator"))
			continue
		}
		id := strings.TrimSpace(rest[:idx])
		if err := validateTaskID(id); err != nil {
			bad = append(bad, corruptBacklog(rel, lineNo+1, line, err.Error()))
			continue
		}
		desc := strings.TrimSpace(rest[idx+len(sep):])
		if metaStart := strings.Index(desc, " (repo:"); metaStart >= 0 {
			desc = strings.TrimSpace(desc[:metaStart])
		}
		if metaStart := strings.Index(desc, " [kind="); metaStart >= 0 {
			desc = strings.TrimSpace(desc[:metaStart])
		}
		state := "queued"
		switch marker {
		case "-":
			state = "working"
		case "!":
			state = "blocked"
		case "x":
			state = "done"
		case " ":
			state = "queued"
		default:
			bad = append(bad, corruptBacklog(rel, lineNo+1, line, "unknown backlog marker"))
			continue
		}
		items = append(items, backlogProjectionItem{ID: id, Description: desc, State: state})
	}
	return items, bad
}

func parseTaskAggregateMeta(id string, data []byte) (map[string]string, error) {
	meta := map[string]string{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid meta line for %s: %q", id, line)
		}
		meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if gen := meta["generation"]; gen != "" {
		if err := validateTaskGeneration(gen); err != nil {
			return nil, err
		}
	}
	return meta, nil
}

func stateFromMeta(meta map[string]string) (string, string) {
	state := strings.TrimSpace(meta["state"])
	if state == "" {
		state = strings.TrimSpace(meta["delivery_state"])
	}
	return state, strings.TrimSpace(meta["state_detail"])
}

func lastStatusState(data string) (string, string) {
	lines := strings.Split(strings.TrimSpace(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		msg, _ := ParseStatusKey(line)
		verb, detail, found := strings.Cut(msg, ":")
		verb = strings.TrimSpace(verb)
		if found && IsValidStatusState(verb) && verb != "resolved" {
			return verb, strings.TrimSpace(detail)
		}
	}
	return "", ""
}

func corruptBacklog(path string, line int, value, reason string) TaskAggregateQuarantineRecord {
	return TaskAggregateQuarantineRecord{SchemaVersion: taskAggregateSchema, TaskID: fmt.Sprintf("%s:%d", path, line), Reason: "corrupt source", Evidence: []TaskAggregateEvidence{{Kind: "backlog", Path: path, Field: fmt.Sprintf("line:%d", line), Value: reason + ": " + value}}}
}

func currentGenerationQuarantine(taskID, reason string, byTaskGen map[string][]TaskAggregateCandidate) TaskAggregateQuarantineRecord {
	seen := map[TaskAggregateEvidence]bool{}
	prefix := taskID + "\x00"
	for key, candidates := range byTaskGen {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, candidate := range candidates {
			if candidate.Projection {
				continue
			}
			seen[evidence(TaskAggregateSource{Kind: candidate.Source.Kind, Path: candidate.Source.Path, Field: "generation"}, candidate.Generation)] = true
			if candidate.Current {
				seen[evidence(TaskAggregateSource{Kind: candidate.Source.Kind, Path: candidate.Source.Path, Field: "current"}, "true")] = true
			}
			if candidate.Inactive {
				seen[evidence(TaskAggregateSource{Kind: candidate.Source.Kind, Path: candidate.Source.Path, Field: "current"}, "false")] = true
			}
		}
	}
	return TaskAggregateQuarantineRecord{SchemaVersion: taskAggregateSchema, TaskID: taskID, Reason: reason, Evidence: sortedEvidence(seen)}
}

func fallbackTaskOwner(homeDir, relBase string) string {
	parts := strings.Split(filepath.ToSlash(relBase), "/")
	if len(parts) >= 2 && parts[0] == "captains" && parts[1] != "" {
		return "captain:" + parts[1]
	}
	if identity, rank, err := ReadHomeIdentity(homeDir); err == nil && rank == RankCaptain {
		return "captain:" + identity
	} else if err == nil && identity != "" {
		return identity
	}
	return filepath.Base(homeDir)
}

func taskAggregateSourceDigest(entries []sourceDigestEntry) string {
	data, _ := json.Marshal(entries)
	return digestHex(data)
}

func installTaskAggregateStage(stageRoot, homeDir string) error {
	stageAuthority := filepath.Join(stageRoot, taskAuthorityDir)
	targetAuthority := filepath.Join(homeDir, taskAuthorityDir)
	if _, err := os.Stat(stageAuthority); os.IsNotExist(err) {
		stageAuthority = filepath.Join(stageRoot, "state", ".task-authority")
	} else if err != nil {
		return err
	}
	tmpDir := targetAuthority + ".installing"
	oldDir := targetAuthority + ".old"
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	if err := copyDir(stageAuthority, tmpDir); err != nil {
		return err
	}
	if err := os.RemoveAll(oldDir); err != nil {
		return err
	}
	if _, err := os.Stat(targetAuthority); err == nil {
		if err := os.Rename(targetAuthority, oldDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmpDir, targetAuthority); err != nil {
		if _, statErr := os.Stat(oldDir); statErr == nil {
			_ = os.Rename(oldDir, targetAuthority)
		}
		return err
	}
	return os.RemoveAll(oldDir)
}

func copyDir(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeTextFile(dest, string(data))
	})
}

type taskAggregateInstallManifest struct {
	Files []sourceDigestEntry `json:"files"`
}

func taskAggregateManifest(plan *TaskAggregateMigrationPlan) (taskAggregateInstallManifest, error) {
	var files []sourceDigestEntry
	seen := map[string]bool{}
	addFile := func(path, digest string) error {
		if seen[path] {
			return fmt.Errorf("duplicate task aggregate manifest path %s", path)
		}
		seen[path] = true
		files = append(files, sourceDigestEntry{Path: path, SHA256: digest})
		return nil
	}
	for _, agg := range plan.Aggregates {
		data, _ := json.MarshalIndent(agg, "", "  ")
		if err := addFile(filepath.ToSlash(taskAggregateRelPath(agg.TaskID, agg.Generation)), digestHex(append(data, '\n'))); err != nil {
			return taskAggregateInstallManifest{}, err
		}
		if agg.Current {
			if err := addFile(filepath.ToSlash(filepath.Join(taskAggregateDir, agg.TaskID, taskCurrentFile)), digestHex([]byte(agg.Generation+"\n"))); err != nil {
				return taskAggregateInstallManifest{}, err
			}
		}
	}
	for _, q := range plan.Quarantined {
		data, _ := json.MarshalIndent(q, "", "  ")
		if err := addFile(filepath.ToSlash(filepath.Join(taskAggregateQuarantine, quarantineFileName(q))), digestHex(append(data, '\n'))); err != nil {
			return taskAggregateInstallManifest{}, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return taskAggregateInstallManifest{Files: files}, nil
}

func verifyTaskAggregateStage(stageRoot string, plan *TaskAggregateMigrationPlan) error {
	manifest, err := taskAggregateManifest(plan)
	if err != nil {
		return err
	}
	return verifyTaskAggregateManifest(stageRoot, manifest)
}

func verifyTaskAggregateManifest(root string, manifest taskAggregateInstallManifest) error {
	seen := map[string]string{}
	for _, file := range manifest.Files {
		seen[file.Path] = file.SHA256
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return err
		}
		if digestHex(data) != file.SHA256 {
			return fmt.Errorf("task aggregate manifest digest mismatch: %s", file.Path)
		}
	}
	for _, top := range []string{taskAggregateDir, taskAggregateQuarantine} {
		rootTop := filepath.Join(root, top)
		if err := filepath.WalkDir(rootTop, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if _, ok := seen[rel]; !ok {
				return fmt.Errorf("unexpected task aggregate file: %s", rel)
			}
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func verifyTaskAggregateInstall(plan *TaskAggregateMigrationPlan) error {
	manifest, err := taskAggregateManifest(plan)
	if err != nil {
		return err
	}
	if err := verifyTaskAggregateManifest(plan.HomeDir, manifest); err != nil {
		return err
	}
	for _, agg := range plan.Aggregates {
		installed, err := ReadTaskAggregate(plan.HomeDir, agg.TaskID, agg.Generation)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(*installed, agg) {
			return fmt.Errorf("installed task aggregate mismatch: %s/%s", agg.TaskID, agg.Generation)
		}
		if agg.Current {
			current, ok, err := ReadCurrentTaskAggregate(plan.HomeDir, agg.TaskID)
			if err != nil || !ok || current.Generation != agg.Generation {
				return fmt.Errorf("current task generation mismatch for %s", agg.TaskID)
			}
		}
	}
	for _, q := range plan.Quarantined {
		path := filepath.Join(plan.HomeDir, taskAggregateQuarantine, quarantineFileName(q))
		if _, err := os.Stat(path); err != nil {
			return err
		}
	}
	return nil
}

func readTaskAggregateReceipt(path string) (*TaskAggregateMigrationReceipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var receipt TaskAggregateMigrationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}
