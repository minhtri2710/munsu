package fleet

import (
	"crypto/rand"
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

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

const taskHandoffDirName = ".task-handoff"

// Handoff transfers queued tasks through a durable, resumable transaction
// between two v1 task-authority homes (ADR-0007 §10): the source task is read
// through the source Authority, the complete Task Generation is committed at
// the destination through the destination Authority's receive operation (one
// Store transaction, durable receipt), and only then is source ownership
// retired. Post-receive projection copies (backlog/.meta/.status/brief) are
// reconciled after the authoritative transfer; a projection failure returns a
// typed partial result and never rolls back or re-transfers ownership.
func Handoff(parentHome, captainHome string, itemKeys []string) error {
	return durableTaskHandoff(parentHome, captainHome, itemKeys)
}

// SetHandoffCrashHookForTest installs a crash-boundary hook for subprocess tests.
// Production callers should never set this hook.
func SetHandoffCrashHookForTest(hook func(string)) func() {
	previous := handoffCrashHook
	if hook == nil {
		handoffCrashHook = func(string) {}
	} else {
		handoffCrashHook = hook
	}
	return func() { handoffCrashHook = previous }
}

// SetHandoffProjectionFailHookForTest installs a hook that fails one
// destination-side projection copy during roll-forward, so tests can prove a
// post-receive projection failure returns a typed partial result without
// changing ownership truth. Production callers should never set this hook.
func SetHandoffProjectionFailHookForTest(hook func(target string) error) func() {
	previous := handoffProjectionFailHook
	if hook == nil {
		handoffProjectionFailHook = func(string) error { return nil }
	} else {
		handoffProjectionFailHook = hook
	}
	return func() { handoffProjectionFailHook = previous }
}

// HandoffPartialError is the typed outcome of a handoff whose authoritative
// ownership transfer committed (destination received, source retired) but
// whose post-receive projection copies could not complete. Ownership truth is
// never rolled back and never re-transferred; the pending journal converges on
// the next recovery, and the destination can reconcile its projections from
// canonical state (Task 6.2 criterion 3).
type HandoffPartialError struct {
	Transferred   []string
	ProjectionErr error
}

func (e *HandoffPartialError) Error() string {
	return fmt.Sprintf("handoff transferred %s but projection copies failed: %v", strings.Join(e.Transferred, ", "), e.ProjectionErr)
}

func (e *HandoffPartialError) Unwrap() error { return e.ProjectionErr }

// RecoverTaskHandoffs makes public task reads safe when a handoff was interrupted.
func RecoverTaskHandoffs(homeDir string) error {
	canonical, err := canonicalHandoffHome(homeDir)
	if err != nil {
		return err
	}
	parent, hasParent, err := configuredParentHome(canonical)
	if err != nil {
		return err
	}
	if !hasParent {
		unlock, err := acquireHandoffLocksWithPending(canonical, canonical)
		if err != nil {
			return err
		}
		defer unlock()
		return recoverIncompleteTaskHandoffs(canonical)
	}
	unlock, err := acquireHandoffLocksWithPending(parent, canonical)
	if err != nil {
		return err
	}
	defer unlock()
	if err := recoverIncompleteTaskHandoffs(canonical); err != nil {
		return err
	}
	return recoverIncompleteTaskHandoffs(parent)
}

func configuredParentHome(homeDir string) (string, bool, error) {
	path := filepath.Join(config.ConfigDir(homeDir), "parent-home")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading parent-home configuration: %w", err)
	}
	parent, err := config.Get(homeDir, "parent-home")
	if err != nil {
		return "", false, fmt.Errorf("reading parent-home configuration: %w", err)
	}
	if strings.TrimSpace(parent) == "" {
		return "", false, fmt.Errorf("malformed parent-home configuration")
	}
	canonical, err := canonicalHandoffHome(parent)
	if err != nil {
		return "", false, fmt.Errorf("canonicalizing parent-home: %w", err)
	}
	return canonical, true, nil
}

type handoffFile struct {
	Target string `json:"target"`
	Stage  string `json:"stage,omitempty"`
	Exists bool   `json:"exists"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type taskHandoffJournal struct {
	Version           int           `json:"version"`
	ID                string        `json:"id"`
	Phase             string        `json:"phase"`
	SourceHome        string        `json:"source_home"`
	DestinationHome   string        `json:"destination_home"`
	SourceBacklogPre  handoffFile   `json:"source_backlog_pre"`
	SourceBacklogPost handoffFile   `json:"source_backlog_post"`
	DestBacklogPre    handoffFile   `json:"destination_backlog_pre"`
	DestBacklogPost   handoffFile   `json:"destination_backlog_post"`
	Tasks             []handoffTask `json:"tasks"`
}

type handoffTask struct {
	ID                     string                        `json:"id"`
	Generation             string                        `json:"generation"`
	Intent                 taskauthority.TransferIntent  `json:"intent"`
	Payload                taskauthority.TransferPayload `json:"payload"`
	SourceAuthority        []handoffFile                 `json:"source_authority"`
	SourceProjections      []handoffFile                 `json:"source_projections"`
	DestinationProjections []handoffFile                 `json:"destination_projections"`
}

var handoffCrashHook = func(string) {}

var handoffProjectionFailHook = func(string) error { return nil }

func durableTaskHandoff(parentHome, captainHome string, itemKeys []string) error {
	source, err := canonicalHandoffHome(parentHome)
	if err != nil {
		return err
	}
	destination, err := canonicalHandoffHome(captainHome)
	if err != nil {
		return err
	}
	if source == destination {
		return fmt.Errorf("refusing handoff: destination is parent home itself")
	}
	captainID, err := ValidateProvenance(destination)
	if err != nil {
		return fmt.Errorf("refusing handoff to unmarked home %s: %w", destination, err)
	}
	if !isTasksAxiBackend(source) {
		return fmt.Errorf("backlog backend is not set to tasks-axi — handoff requires tasks-axi")
	}

	unlock, err := acquireHandoffLocksWithPending(source, destination)
	if err != nil {
		return err
	}
	defer unlock()
	if err := recoverIncompleteTaskHandoffs(source); err != nil {
		return err
	}
	// Supervision gate: handoff fails closed when the watcher lease of either
	// home is degraded (Task 4.3, ADR-0007 §8). Durable Dispatch Holds are
	// evaluated by the fleet holds gate below.
	if err := CheckSupervisionForDispatch(source, mhome.DispatchActionHandoff); err != nil {
		return err
	}
	if err := CheckSupervisionForDispatch(destination, mhome.DispatchActionHandoff); err != nil {
		return err
	}
	sourceStore, err := taskauthorityfs.NewStore(source)
	if err != nil {
		return err
	}
	destinationStore, err := taskauthorityfs.NewStore(destination)
	if err != nil {
		return err
	}
	// Task 6.2 cutover: the saga operates only on current v1 homes. A home that
	// still carries legacy v1 task-authority records fails closed with the typed
	// migration-required error surfaced by the CLI; there is no silent v1
	// fallback (authoritative mutation compatibility may not persist).
	for _, store := range []*taskauthorityfs.Store{sourceStore, destinationStore} {
		if _, err := store.View(); err != nil {
			home := source
			if store == destinationStore {
				home = destination
			}
			return fmt.Errorf("handoff requires task-authority v1 state at %s: %w", home, err)
		}
	}
	sourceAuth := taskauthority.New(sourceStore)
	destinationAuth := taskauthority.New(destinationStore)
	actor := handoffActor(source)

	// Fleet-owned holds gate: every applicable durable Dispatch Hold on either
	// home blocks the transfer before any staging (the matching rule lives in
	// taskauthority.DispatchHold.Matches; only the orchestration stays here).
	for _, home := range []string{source, destination} {
		auth := sourceAuth
		if home == destination {
			auth = destinationAuth
		}
		if err := checkHandoffHolds(auth, "", "", "", ""); err != nil {
			return err
		}
	}

	path, err := captainLookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}
	sourceBacklog := filepath.Join(source, "data", "backlog.md")
	destinationBacklog := filepath.Join(destination, "data", "backlog.md")
	keys, dependencies, err := resolveHandoffKeysAndDependencies(source, sourceAuth, sourceBacklog, path, itemKeys)
	if err != nil {
		return err
	}
	autonomy := taskauthority.DispatchAutonomyManual
	if agg, err := sourceAuth.Get(keys[0]); err != nil {
		return err
	} else if agg.Definition.Project != "" {
		if snapshot, err := config.LoadResolvedSnapshot(source, agg.Definition.Project, config.BoundaryOverrides{}); err == nil {
			autonomy = taskauthority.DispatchAutonomy(snapshot.Config().DispatchAutonomy)
		}
	}
	result, err := sourceAuth.InterpretDispatch(taskauthority.InterpretDispatchRequest{
		OperationID:    mustHandoffOperationID("handoff-interpret"),
		Actor:          actor,
		RequestedOrder: keys,
		Dependencies:   dependencies,
		Autonomy:       autonomy,
	})
	if err != nil {
		return fmt.Errorf("handoff dispatch interpretation: %w", err)
	}
	if result.Record.Outcome == taskauthority.DispatchInterpretationDecisionRequired {
		return fmt.Errorf("handoff dispatch interpretation %s requires a decision", result.Record.ID)
	}
	keys = result.SelectedTasks
	owner := "captain:" + captainID
	view, err := sourceStore.View()
	if err != nil {
		return err
	}
	for _, taskID := range keys {
		agg, err := sourceAuth.Get(taskID)
		if err != nil {
			return err
		}
		project := agg.Definition.Project
		parentID := agg.Definition.ParentTaskID
		if err := checkHandoffHolds(sourceAuth, taskID, project, agg.Generation.String(), parentID); err != nil {
			return err
		}
		if err := checkHandoffHolds(destinationAuth, taskID, project, agg.Generation.String(), parentID); err != nil {
			return err
		}
		if parentID != "" {
			if err := checkHandoffHolds(sourceAuth, parentID, project, "", parentID); err != nil {
				return err
			}
		}
	}
	journal, dir, err := prepareHandoff(source, destination, owner, keys, sourceBacklog, destinationBacklog, result.Record, view, sourceAuth, destinationAuth)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()

	handoffCrashHook("journal")
	if err := stageHandoff(journal, dir, path, keys); err != nil {
		return err
	}
	handoffCrashHook("staging")
	journal.Phase = "prepared"
	if err := writeHandoffJournal(dir, journal); err != nil {
		return err
	}
	handoffCrashHook("prepared")
	if err := revalidateHandoff(journal); err != nil {
		return err
	}
	journal.Phase = "commit-decided"
	if err := writeHandoffJournal(dir, journal); err != nil {
		return err
	}
	cleanup = false
	handoffCrashHook("commit-decided")
	if err := rollForwardHandoff(journal, dir); err != nil {
		return err
	}
	for _, task := range journal.Tasks {
		fmt.Printf("handed-off %s\n", task.ID)
	}
	return nil
}

// handoffAuthority constructs the concrete taskauthorityfs Store and composed
// Authority for one home. Construction is side-effect free; the first
// canonical read fails closed on legacy v1 state.
func handoffAuthority(homeDir string) (*taskauthorityfs.Store, *taskauthority.Authority, error) {
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		return nil, nil, err
	}
	return store, taskauthority.New(store), nil
}

// handoffActor derives the mutation actor for the transfer from the source
// home identity, falling back to the general orchestrator.
func handoffActor(homeDir string) taskauthority.Actor {
	if identity, rank, err := mhome.ReadHomeIdentity(homeDir); err == nil && identity != "" {
		return taskauthority.Actor{ID: identity, Rank: string(rank)}
	}
	return taskauthority.Actor{ID: "general", Rank: "general"}
}

// checkHandoffHolds is the fleet-owned dispatch-hold gate for the handoff
// action: it lists the home's durable holds through the Authority and matches
// them against the task/project/generation/parent. The matching rule lives in
// taskauthority.DispatchHold.Matches; only the orchestration stays in fleet.
func checkHandoffHolds(auth *taskauthority.Authority, taskID, project, generation, parentID string) error {
	holds, err := auth.ListHolds()
	if err != nil {
		return err
	}
	for _, hold := range holds {
		if hold.Matches(taskauthority.DispatchActionHandoff, taskID, project, generation, parentID) {
			return domain.NewError(domain.ErrorConflict,
				fmt.Sprintf("dispatch is held: %s (%s)", hold.ID, hold.Reason),
				domain.RetryNever, taskauthority.ErrDispatchHeld)
		}
	}
	return nil
}

// handoffCandidateOwners returns every canonical v1 home that currently owns
// the task: the source home plus every captain under its captains/ tree.
// Ownership is read from the canonical stores, so a home owns the task when
// its current aggregate exists. A home that cannot serve a canonical view
// (legacy v1 records or corrupt state) fails closed: its ownership is
// unknowable without migration. Cross-home resolution and candidate-owner
// collection remain fleet-owned (Task 6.2 criterion 4).
func handoffCandidateOwners(source, taskID string) ([]string, error) {
	var owners []string
	collect := func(home string) error {
		store, err := taskauthorityfs.NewStore(home)
		if err != nil {
			return err
		}
		view, err := store.View()
		if err != nil {
			return fmt.Errorf("resolving handoff task %s: home %s cannot serve canonical authority state: %w", taskID, home, err)
		}
		if _, ok := view.Current(taskID); ok {
			owners = append(owners, home)
		}
		return nil
	}
	if err := collect(source); err != nil {
		return nil, err
	}
	captainsRoot := filepath.Join(source, "captains")
	entries, err := os.ReadDir(captainsRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := collect(filepath.Join(captainsRoot, entry.Name())); err != nil {
			return nil, err
		}
	}
	return owners, nil
}

// resolveHandoffTaskID resolves one requested key against canonical v2
// ownership across the source home and its captains, collecting all candidate
// owners (criterion 4). Multiple canonical owners make the key ambiguous and
// the CLI surfaces correction commands preserving the requested destination.
func resolveHandoffTaskID(source, key string) (string, error) {
	if err := validateHandoffTaskID(key); err != nil {
		return "", err
	}
	owners, err := handoffCandidateOwners(source, key)
	if err != nil {
		return "", err
	}
	if len(owners) > 1 {
		return "", &mhome.AmbiguousTaskIDError{Requested: key, Matches: owners}
	}
	return key, nil
}

func validateHandoffTaskID(taskID string) error {
	if taskID == "" || taskID == "." || taskID == ".." || filepath.Base(taskID) != taskID || strings.ContainsAny(taskID, `/\\`) {
		return fmt.Errorf("invalid handoff task id %q", taskID)
	}
	return nil
}

func canonicalHandoffHome(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving handoff home %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("canonicalizing handoff home %s: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

func acquireSingleHandoffLock(home string) (func(), error) {
	return acquireExclusiveLock(filepath.Join(home, "state", taskHandoffDirName+".lock"))
}

func acquireHandoffLocks(source, destination string) (func(), error) {
	return acquireHandoffHomeLocks([]string{source, destination})
}

func acquireHandoffLocksWithPending(source, destination string) (func(), error) {
	homes := []string{source, destination}
	root := filepath.Join(source, "state", taskHandoffDirName)
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, fmt.Errorf("invalid handoff journal entry %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "journal.json"))
		if err != nil {
			return nil, err
		}
		var journal taskHandoffJournal
		if err := json.Unmarshal(data, &journal); err != nil {
			return nil, fmt.Errorf("corrupt handoff journal: %w", err)
		}
		canonical, err := canonicalHandoffHome(journal.DestinationHome)
		if err != nil || canonical != journal.DestinationHome {
			return nil, fmt.Errorf("invalid pending handoff destination")
		}
		homes = append(homes, canonical)
	}
	return acquireHandoffHomeLocks(homes)
}

func acquireHandoffHomeLocks(homes []string) (func(), error) {
	unique := make(map[string]struct{}, len(homes))
	paths := make([]string, 0, len(homes))
	for _, home := range homes {
		canonical, err := canonicalHandoffHome(home)
		if err != nil {
			return nil, err
		}
		if _, ok := unique[canonical]; ok {
			continue
		}
		unique[canonical] = struct{}{}
		paths = append(paths, filepath.Join(canonical, "state", taskHandoffDirName+".lock"))
	}
	sort.Strings(paths)
	var releases []func()
	for _, path := range paths {
		release, err := acquireExclusiveLock(path)
		if err != nil {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
			return nil, fmt.Errorf("acquiring handoff lock %s: %w", path, err)
		}
		releases = append(releases, release)
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, nil
}

func resolveHandoffKeysAndDependencies(source string, sourceAuth *taskauthority.Authority, backlog, path string, requested []string) ([]string, []taskauthority.DispatchDependency, error) {
	seen := make(map[string]bool, len(requested))
	keys := make([]string, 0, len(requested))
	dependencies := make([]taskauthority.DispatchDependency, 0, len(requested))
	for _, key := range requested {
		resolved, err := resolveHandoffTaskID(source, key)
		if err != nil {
			return nil, nil, fmt.Errorf("handoff: resolving task %s: %w", key, err)
		}
		if seen[resolved] {
			return nil, nil, fmt.Errorf("handoff: duplicate task %s", resolved)
		}
		seen[resolved] = true
		out, err := execCommand(path, "show", resolved, "--file", backlog).CombinedOutput()
		if err != nil {
			return nil, nil, fmt.Errorf("handoff: key %s not found in source backlog: %w: %s", resolved, err, strings.TrimSpace(string(out)))
		}
		state := extractTaskStateFromShow(string(out))
		if state == "" {
			return nil, nil, fmt.Errorf("handoff: key %s has no parseable state — only queued items may be handed off", resolved)
		}
		if state != "queued" {
			return nil, nil, fmt.Errorf("handoff: key %s has state %q, only queued items may be handed off", resolved, state)
		}
		keys = append(keys, resolved)
		dependencies = append(dependencies, taskauthority.DispatchDependency{TaskID: resolved, DependsOn: parseHandoffDependencies(string(out)), State: state})
	}
	return keys, dependencies, nil
}

// resolveHandoffKeys resolves the requested keys without building dependency
// edges. It is retained for callers that only need the resolved key set.
func resolveHandoffKeys(source, backlog, path string, requested []string) ([]string, error) {
	keys, _, err := resolveHandoffKeysAndDependencies(source, nil, backlog, path, requested)
	return keys, err
}

func parseHandoffDependencies(output string) []string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "blocked-by:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "blocked-by:"))
			if value == "" {
				return nil
			}
			parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })
			return parts
		}
	}
	return nil
}

// prepareHandoff builds the durable journal for one transfer: one validated
// TransferIntent and complete Task Generation payload per task, the source
// authority inventory (v2 aggregate, current pointer, and the transferred
// dispatch records), the source projection inventory, and the destination
// projection absence preflight. A destination that already owns the task
// (same or newer Generation) quarantines the transfer with a typed conflict
// and never overwrites destination truth (ADR-0007 §10).
func prepareHandoff(source, destination, owner string, keys []string, sourceBacklog, destinationBacklog string, interpretation taskauthority.DispatchInterpretation, sourceView taskauthority.View, sourceAuth, destinationAuth *taskauthority.Authority) (*taskHandoffJournal, string, error) {
	id, err := newTaskHandoffID()
	if err != nil {
		return nil, "", err
	}
	journal := &taskHandoffJournal{
		Version:         3,
		ID:              id,
		Phase:           "preparing",
		SourceHome:      source,
		DestinationHome: destination,
	}
	journal.SourceBacklogPre, err = inventoryHandoffFile(source, sourceBacklog)
	if err != nil {
		return nil, "", err
	}
	journal.DestBacklogPre, err = inventoryHandoffFile(destination, destinationBacklog)
	if err != nil {
		return nil, "", err
	}
	if !journal.DestBacklogPre.Exists {
		journal.DestBacklogPre = handoffFile{Target: "data/backlog.md", Mode: 0644}
	}
	for _, taskID := range keys {
		// A destination that already owns the task (same or newer Generation)
		// quarantines the transfer: the saga fails closed with a typed conflict
		// and never overwrites destination truth (ADR-0007 §10). The receive
		// operation re-fences the same rule inside the Store transaction.
		if existing, err := destinationAuth.Get(taskID); err == nil {
			return nil, "", handoffDestinationConflict(taskID, existing.Generation)
		} else if !errors.Is(err, taskauthority.ErrNotFound) {
			return nil, "", fmt.Errorf("handoff: reading destination ownership for %s: %w", taskID, err)
		}
		agg, err := sourceAuth.Get(taskID)
		if err != nil {
			return nil, "", fmt.Errorf("handoff: reading source task %s: %w", taskID, err)
		}
		generation := agg.Generation
		request := taskauthority.TransferRequest{
			SourceHome:      source,
			DestinationHome: destination,
			TaskID:          taskID,
			Generation:      generation,
		}
		digest, err := request.Digest()
		if err != nil {
			return nil, "", fmt.Errorf("handoff: computing transfer intent for %s: %w", taskID, err)
		}
		intent := taskauthority.TransferIntent{
			SourceHome:             source,
			DestinationHome:        destination,
			TaskID:                 taskID,
			Generation:             generation,
			RequestDigest:          digest,
			SourceOperationID:      fmt.Sprintf("handoff-source-%s-%s", taskID, generation),
			DestinationOperationID: fmt.Sprintf("handoff-dest-%s-%s", taskID, generation),
		}
		if err := intent.Validate(); err != nil {
			return nil, "", fmt.Errorf("handoff: invalid transfer intent for %s: %w", taskID, err)
		}
		destinationAgg := agg
		destinationAgg.Definition.Owner = owner
		destinationAgg.DispatchInterpretationID = interpretation.ID
		destinationAgg.DispatchInterpretationDigest = interpretation.DependencySnapshotDigest
		payload := buildTransferPayload(sourceView, destinationAgg, interpretation)
		task := handoffTask{ID: taskID, Generation: generation.String(), Intent: intent, Payload: payload}
		for _, rel := range taskAggregateAuthorityRelPaths(taskID, generation) {
			file, err := inventoryHandoffFile(source, filepath.Join(source, rel))
			if err != nil {
				return nil, "", err
			}
			task.SourceAuthority = append(task.SourceAuthority, file)
		}
		for _, rel := range taskHandoffDispatchRecordRelPaths(interpretation) {
			file, err := inventoryHandoffFile(source, filepath.Join(source, rel))
			if err != nil {
				return nil, "", err
			}
			if file.Exists {
				task.SourceAuthority = append(task.SourceAuthority, file)
			}
		}
		for _, rel := range taskHandoffProjectionRelPaths(taskID) {
			file, err := inventoryHandoffFile(source, filepath.Join(source, rel))
			if err != nil {
				return nil, "", err
			}
			task.SourceProjections = append(task.SourceProjections, file)
			if file.Exists {
				if _, err := os.Stat(filepath.Join(destination, rel)); err == nil {
					return nil, "", fmt.Errorf("handoff: destination projection already exists: %s", rel)
				} else if !os.IsNotExist(err) {
					return nil, "", err
				}
			}
			task.DestinationProjections = append(task.DestinationProjections, handoffFile{Target: rel, Mode: 0600})
		}
		journal.Tasks = append(journal.Tasks, task)
	}
	dir := filepath.Join(source, "state", taskHandoffDirName, id)
	if err := os.MkdirAll(filepath.Join(dir, "stage"), 0700); err != nil {
		return nil, "", err
	}
	if err := writeHandoffJournal(dir, journal); err != nil {
		return nil, "", err
	}
	return journal, dir, nil
}

// buildTransferPayload assembles the complete Task Generation payload for one
// transferred task from the source canonical view: the aggregate (with its
// bindings and dispatch interpretation binding), the dispatch records of the
// interpretation, and the generation's typed audit history.
func buildTransferPayload(sourceView taskauthority.View, agg taskauthority.Aggregate, interpretation taskauthority.DispatchInterpretation) taskauthority.TransferPayload {
	payload := taskauthority.TransferPayload{
		Aggregate:       agg,
		Interpretations: []taskauthority.DispatchInterpretation{interpretation},
	}
	if interpretation.DecisionKey != "" {
		for _, dec := range sourceView.Decisions {
			if dec.Key == interpretation.DecisionKey {
				payload.Decisions = append(payload.Decisions, dec)
				break
			}
		}
		for _, hold := range sourceView.Holds {
			if hold.ID == interpretation.DecisionKey+"-hold" {
				payload.Holds = append(payload.Holds, hold)
				break
			}
		}
	}
	for _, ev := range sourceView.Audit {
		if ev.TaskID == agg.TaskID && ev.Generation == agg.Generation {
			payload.History = append(payload.History, ev)
		}
	}
	return payload
}

// taskAggregateAuthorityRelPaths returns the current v1 canonical aggregate
// document and current pointer for one task generation.
func taskAggregateAuthorityRelPaths(taskID string, generation taskauthority.Generation) []string {
	return []string{
		filepath.Join("state", ".task-authority", "v1", "aggregates", taskID, generation.String()+".json"),
		filepath.Join("state", ".task-authority", "v1", "aggregates", taskID, "current"),
	}
}

// taskHandoffDispatchRecordRelPaths returns the v2 dispatch record documents
// of one transferred interpretation (interpretation, and the decision and
// matching hold when the outcome is decision-required). Filenames use the
// taskauthorityfs hex-encoded identities.
func taskHandoffDispatchRecordRelPaths(interpretation taskauthority.DispatchInterpretation) []string {
	paths := []string{}
	if rel, err := taskauthorityfs.InterpretationRelPath(interpretation.ID); err == nil {
		paths = append(paths, filepath.FromSlash(rel))
	}
	if interpretation.DecisionKey != "" {
		if rel, err := taskauthorityfs.DecisionRelPath(interpretation.DecisionKey); err == nil {
			paths = append(paths, filepath.FromSlash(rel))
		}
		if rel, err := taskauthorityfs.HoldRelPath(interpretation.DecisionKey + "-hold"); err == nil {
			paths = append(paths, filepath.FromSlash(rel))
		}
	}
	return paths
}

func newTaskHandoffID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generating handoff transaction ID: %w", err)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), buffer), nil
}

func newHandoffOperationID(prefix string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generating handoff operation ID: %w", err)
	}
	return fmt.Sprintf("%s-%d-%x", prefix, time.Now().UnixNano(), buffer), nil
}

func mustHandoffOperationID(prefix string) string {
	id, err := newHandoffOperationID(prefix)
	if err != nil {
		panic(err)
	}
	return id
}

func inventoryHandoffFile(home, path string) (handoffFile, error) {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return handoffFile{}, err
	}
	file := handoffFile{Target: filepath.ToSlash(rel), Mode: 0600}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return file, nil
	}
	if err != nil {
		return file, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return file, err
	}
	file.Exists = true
	file.Mode = uint32(info.Mode().Perm())
	file.Size = int64(len(data))
	file.SHA256 = digestHandoff(data)
	return file, nil
}

func digestHandoff(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeHandoffJournal(dir string, journal *taskHandoffJournal) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return durableHandoffWrite(filepath.Join(dir, "journal.json"), append(data, '\n'), 0600)
}

func stageHandoff(journal *taskHandoffJournal, dir, tasksAxi string, keys []string) error {
	stageRoot := filepath.Join(dir, "stage")
	if err := stageExistingHandoffFile(&journal.SourceBacklogPre, stageRoot, filepath.Join(journal.SourceHome, filepath.FromSlash(journal.SourceBacklogPre.Target)), "source-backlog-pre"); err != nil {
		return err
	}
	if journal.DestBacklogPre.Exists {
		if err := stageExistingHandoffFile(&journal.DestBacklogPre, stageRoot, filepath.Join(journal.DestinationHome, filepath.FromSlash(journal.DestBacklogPre.Target)), "destination-backlog-pre"); err != nil {
			return err
		}
	}
	for i := range journal.Tasks {
		for k := range journal.Tasks[i].SourceProjections {
			file := &journal.Tasks[i].SourceProjections[k]
			if err := stageExistingHandoffFile(file, stageRoot, filepath.Join(journal.SourceHome, filepath.FromSlash(file.Target)), fmt.Sprintf("task-%d-source-projection-%d", i, k)); err != nil {
				return err
			}
		}
	}

	sourcePost := filepath.Join(stageRoot, "source-backlog-post")
	destinationPost := filepath.Join(stageRoot, "destination-backlog-post")
	if journal.SourceBacklogPre.Exists {
		if err := copyHandoffFile(filepath.Join(journal.SourceHome, filepath.FromSlash(journal.SourceBacklogPre.Target)), sourcePost, 0644); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("handoff source backlog does not exist")
	}
	if journal.DestBacklogPre.Exists {
		if err := copyHandoffFile(filepath.Join(journal.DestinationHome, filepath.FromSlash(journal.DestBacklogPre.Target)), destinationPost, uint32(journal.DestBacklogPre.Mode)); err != nil {
			return err
		}
	} else if err := durableHandoffWrite(destinationPost, []byte("# Backlog\n\n"), 0644); err != nil {
		return err
	}
	args := append([]string{"mv"}, keys...)
	args = append(args, "--to", destinationPost, "--file", sourcePost)
	cmd := execCommand(tasksAxi, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tasks-axi mv failed during handoff staging: %w", err)
	}
	journal.SourceBacklogPost = stagedHandoffFile("source-backlog-post", sourcePost, journal.SourceBacklogPre.Target, journal.SourceBacklogPre.Mode)
	journal.DestBacklogPost = stagedHandoffFile("destination-backlog-post", destinationPost, journal.DestBacklogPre.Target, journal.DestBacklogPre.Mode)

	for i := range journal.Tasks {
		task := &journal.Tasks[i]
		for k := range task.DestinationProjections {
			if !task.SourceProjections[k].Exists {
				continue
			}
			data, err := os.ReadFile(filepath.Join(journal.SourceHome, filepath.FromSlash(task.SourceProjections[k].Target)))
			if err != nil {
				return err
			}
			task.DestinationProjections[k] = stagedHandoffBytes(fmt.Sprintf("task-%d-destination-projection-%d", i, k), data, task.DestinationProjections[k].Target, uint32(task.SourceProjections[k].Mode))
			if err := durableHandoffWrite(filepath.Join(dir, filepath.FromSlash(task.DestinationProjections[k].Stage)), data, os.FileMode(task.DestinationProjections[k].Mode)); err != nil {
				return err
			}
		}
	}
	return writeHandoffJournal(dir, journal)
}

func stageExistingHandoffFile(file *handoffFile, root, source, name string) error {
	if !file.Exists {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	file.Stage = filepath.ToSlash(filepath.Join("stage", name))
	return durableHandoffWrite(filepath.Join(filepath.Dir(root), file.Stage), data, os.FileMode(file.Mode))
}

func copyHandoffFile(source, destination string, mode uint32) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return durableHandoffWrite(destination, data, os.FileMode(mode))
}

func stagedHandoffFile(name, source, target string, mode uint32) handoffFile {
	data, _ := os.ReadFile(source)
	return stagedHandoffBytes(name, data, target, mode)
}

func stagedHandoffBytes(name string, data []byte, target string, mode uint32) handoffFile {
	return handoffFile{Target: target, Stage: filepath.ToSlash(filepath.Join("stage", name)), Exists: true, Mode: mode, Size: int64(len(data)), SHA256: digestHandoff(data)}
}

func revalidateHandoff(journal *taskHandoffJournal) error {
	if err := verifyHandoffFile(journal.SourceHome, journal.SourceBacklogPre); err != nil {
		return fmt.Errorf("handoff source backlog changed: %w", err)
	}
	if journal.DestBacklogPre.Exists {
		if err := verifyHandoffFile(journal.DestinationHome, journal.DestBacklogPre); err != nil {
			return fmt.Errorf("handoff destination backlog changed: %w", err)
		}
	} else if _, err := os.Stat(filepath.Join(journal.DestinationHome, filepath.FromSlash(journal.DestBacklogPre.Target))); err == nil {
		return fmt.Errorf("handoff destination backlog appeared")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, task := range journal.Tasks {
		for _, file := range append(append([]handoffFile(nil), task.SourceAuthority...), task.SourceProjections...) {
			if file.Exists {
				if err := verifyHandoffFile(journal.SourceHome, file); err != nil {
					return fmt.Errorf("handoff source changed: %w", err)
				}
			}
		}
		for _, file := range task.DestinationProjections {
			if _, err := os.Stat(filepath.Join(journal.DestinationHome, filepath.FromSlash(file.Target))); err == nil {
				return fmt.Errorf("handoff destination appeared: %s", file.Target)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// rollForwardHandoff commits the authoritative transfer and then the
// post-receive projection copies. Each task's complete Task Generation is
// committed at the destination through the destination Authority's receive
// operation — one Store transaction with the destination Operation ID and the
// durable request-digest receipt — BEFORE any source authority mutation
// (Task 6.1 ordering invariant preserved: a crash before the destination
// receipt leaves source ownership current, and recovery re-commits the same
// receipt idempotently). Only after every destination receipt is durable is
// source ownership retired. A failure in the post-receive projection copies
// (backlog/.meta/.status/brief) returns a typed partial result: ownership
// truth is never rolled back and never re-transferred, and the pending
// journal converges on the next recovery.
func rollForwardHandoff(journal *taskHandoffJournal, dir string) error {
	_, destinationAuth, err := handoffAuthority(journal.DestinationHome)
	if err != nil {
		return err
	}
	actor := handoffActor(journal.SourceHome)
	for i, task := range journal.Tasks {
		if err := task.Intent.Validate(); err != nil {
			return fmt.Errorf("invalid handoff intent for %s: %w", task.ID, err)
		}
		if _, err := destinationAuth.ReceiveTransfer(taskauthority.ReceiveTransferRequest{
			Actor:   actor,
			Intent:  task.Intent,
			Payload: task.Payload,
		}); err != nil {
			return err
		}
		if i == 0 && len(journal.Tasks) > 1 {
			handoffCrashHook("between-authority")
		}
	}
	handoffCrashHook("destination-receipt")
	for _, task := range journal.Tasks {
		if err := retireSourceAuthority(journal.SourceHome, task); err != nil {
			return err
		}
	}
	handoffCrashHook("source-authority")

	// Post-receive projection copies: any failure here returns a typed partial
	// result; the authoritative transfer above is never rolled back or
	// re-transferred.
	var projectionErr error
	if err := installHandoffPost(journal.SourceHome, journal.SourceBacklogPost, journal.SourceBacklogPre, dir); err != nil {
		projectionErr = err
	}
	if projectionErr == nil {
		if err := handoffProjectionFailHook(journal.DestBacklogPost.Target); err != nil {
			projectionErr = err
		} else if err := installHandoffPost(journal.DestinationHome, journal.DestBacklogPost, journal.DestBacklogPre, dir); err != nil {
			projectionErr = err
		}
	}
	if projectionErr == nil {
		for _, task := range journal.Tasks {
			for _, file := range task.DestinationProjections {
				if !file.Exists {
					continue
				}
				if err := handoffProjectionFailHook(file.Target); err != nil {
					projectionErr = err
					break
				}
				if err := installHandoffFile(journal.DestinationHome, file, dir); err != nil {
					projectionErr = err
					break
				}
			}
			if projectionErr != nil {
				break
			}
			for _, file := range task.SourceProjections {
				if err := removeHandoffFile(journal.SourceHome, file); err != nil {
					projectionErr = err
					break
				}
			}
		}
	}
	handoffCrashHook("artifacts")
	if projectionErr != nil {
		transferred := make([]string, 0, len(journal.Tasks))
		for _, task := range journal.Tasks {
			transferred = append(transferred, task.ID)
		}
		return &HandoffPartialError{Transferred: transferred, ProjectionErr: projectionErr}
	}
	if err := verifyFinalHandoff(journal); err != nil {
		return err
	}
	journal.Phase = "completed"
	if err := writeHandoffJournal(dir, journal); err != nil {
		return err
	}
	handoffCrashHook("completed")
	return os.RemoveAll(dir)
}

// retireSourceAuthority retires the source's ownership of one transferred
// task: the transferred dispatch record documents are removed individually
// (idempotent), and the task's v2 aggregate directory is removed as a whole
// after verifying every inventoried document still matches. The canonical
// store fails closed on a partial task directory (a current pointer without a
// matching document), so ownership retirement must be all-or-nothing per
// task; an already-removed directory is already retired.
func retireSourceAuthority(home string, task handoffTask) error {
	var taskDir string
	for _, file := range task.SourceAuthority {
		rel := filepath.FromSlash(file.Target)
		if !strings.Contains(rel, string(filepath.Separator)+"aggregates"+string(filepath.Separator)) {
			if err := removeHandoffFile(home, file); err != nil {
				return err
			}
			continue
		}
		dir := filepath.Dir(rel)
		if taskDir != "" && dir != taskDir {
			return fmt.Errorf("handoff authority files span directories")
		}
		taskDir = dir
		path := filepath.Join(home, rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue // already retired
		} else if err != nil {
			return err
		}
		if file.Exists {
			if err := verifyHandoffFile(home, file); err != nil {
				return fmt.Errorf("refusing to retire changed source authority: %w", err)
			}
		}
	}
	if taskDir != "" {
		if err := os.RemoveAll(filepath.Join(home, taskDir)); err != nil {
			return err
		}
	}
	return nil
}

func verifyHandoffFile(home string, file handoffFile) error {
	path := filepath.Join(home, filepath.FromSlash(file.Target))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if digestHandoff(data) != file.SHA256 || int64(len(data)) != file.Size || uint32(info.Mode().Perm()) != file.Mode {
		return fmt.Errorf("handoff file changed: %s", file.Target)
	}
	return nil
}

func removeHandoffFile(home string, file handoffFile) error {
	if !file.Exists {
		return nil
	}
	path := filepath.Join(home, filepath.FromSlash(file.Target))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if digestHandoff(data) != file.SHA256 {
		return fmt.Errorf("refusing to remove changed handoff file: %s", file.Target)
	}
	return os.Remove(path)
}

func installHandoffPost(home string, file, preimage handoffFile, dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.Stage)))
	if err != nil {
		return err
	}
	if digestHandoff(data) != file.SHA256 {
		return fmt.Errorf("corrupt handoff stage: %s", file.Stage)
	}
	path := filepath.Join(home, filepath.FromSlash(file.Target))
	if existing, err := os.ReadFile(path); err == nil {
		if digestHandoff(existing) == file.SHA256 {
			return nil
		}
		if !preimage.Exists || digestHandoff(existing) != preimage.SHA256 {
			return fmt.Errorf("conflicting handoff destination: %s", file.Target)
		}
		return durableHandoffWrite(path, data, os.FileMode(file.Mode))
	} else if !os.IsNotExist(err) {
		return err
	}
	return durableHandoffWrite(path, data, os.FileMode(file.Mode))
}

func installHandoffFile(home string, file handoffFile, dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.Stage)))
	if err != nil {
		return err
	}
	if digestHandoff(data) != file.SHA256 {
		return fmt.Errorf("corrupt handoff stage: %s", file.Stage)
	}
	path := filepath.Join(home, filepath.FromSlash(file.Target))
	if existing, err := os.ReadFile(path); err == nil {
		if digestHandoff(existing) != file.SHA256 {
			return fmt.Errorf("conflicting handoff destination: %s", file.Target)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return durableHandoffWrite(path, data, os.FileMode(file.Mode))
}

func verifyFinalHandoff(journal *taskHandoffJournal) error {
	if err := verifyHandoffFile(journal.SourceHome, journal.SourceBacklogPost); err != nil {
		return err
	}
	if err := verifyHandoffFile(journal.DestinationHome, journal.DestBacklogPost); err != nil {
		return err
	}
	_, sourceAuth, err := handoffAuthority(journal.SourceHome)
	if err != nil {
		return err
	}
	_, destinationAuth, err := handoffAuthority(journal.DestinationHome)
	if err != nil {
		return err
	}
	for _, task := range journal.Tasks {
		if _, err := destinationAuth.Get(task.ID); err != nil {
			return fmt.Errorf("destination missing %s", task.ID)
		}
		if _, err := sourceAuth.Get(task.ID); !errors.Is(err, taskauthority.ErrNotFound) {
			return fmt.Errorf("source still owns %s", task.ID)
		}
		for _, file := range task.DestinationProjections {
			if file.Exists {
				if err := verifyHandoffFile(journal.DestinationHome, file); err != nil {
					return err
				}
			}
		}
		for _, file := range append(append([]handoffFile(nil), task.SourceAuthority...), task.SourceProjections...) {
			if _, err := os.Stat(filepath.Join(journal.SourceHome, filepath.FromSlash(file.Target))); err == nil {
				return fmt.Errorf("source artifact remains: %s", file.Target)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func recoverIncompleteTaskHandoffs(home string) error {
	root := filepath.Join(home, "state", taskHandoffDirName)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("invalid handoff journal entry %s", entry.Name())
		}
		dir := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "journal.json"))
		if err != nil {
			return err
		}
		var journal taskHandoffJournal
		if err := json.Unmarshal(data, &journal); err != nil {
			return fmt.Errorf("corrupt handoff journal: %w", err)
		}
		if journal.Version != 3 || journal.ID != entry.Name() || journal.SourceHome != home || journal.DestinationHome == home {
			return fmt.Errorf("invalid handoff journal")
		}
		canonicalDestination, err := canonicalHandoffHome(journal.DestinationHome)
		if err != nil || canonicalDestination != journal.DestinationHome {
			return fmt.Errorf("invalid handoff journal destination")
		}
		if err := validateHandoffPaths(&journal); err != nil {
			return err
		}
		switch journal.Phase {
		case "preparing", "prepared":
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		case "commit-decided":
			if err := rollForwardHandoff(&journal, dir); err != nil {
				return err
			}
		case "completed":
			if err := verifyFinalHandoff(&journal); err != nil {
				return err
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown handoff journal phase %q", journal.Phase)
		}
	}
	return nil
}

func validateHandoffPaths(journal *taskHandoffJournal) error {
	for _, file := range append([]handoffFile{journal.SourceBacklogPre, journal.SourceBacklogPost, journal.DestBacklogPre, journal.DestBacklogPost}, handoffJournalFiles(journal)...) {
		for _, path := range []string{file.Target, file.Stage} {
			if path == "" {
				continue
			}
			clean := filepath.Clean(filepath.FromSlash(path))
			if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("handoff journal path escapes root: %s", path)
			}
		}
	}
	return nil
}

func handoffJournalFiles(journal *taskHandoffJournal) []handoffFile {
	var files []handoffFile
	for _, task := range journal.Tasks {
		files = append(files, task.SourceAuthority...)
		files = append(files, task.SourceProjections...)
		files = append(files, task.DestinationProjections...)
	}
	return files
}

func durableHandoffWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".handoff-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// handoffDestinationConflict is the typed conflict returned when the
// destination already owns the task: the transfer quarantines and never
// overwrites destination truth (ADR-0007 §10).
func handoffDestinationConflict(taskID string, generation taskauthority.Generation) error {
	return domain.NewError(domain.ErrorConflict,
		fmt.Sprintf("handoff: destination already has current authority for %s generation %s", taskID, generation),
		domain.RetryNever, nil)
}
