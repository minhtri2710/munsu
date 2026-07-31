package fleet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

const taskHandoffDirName = ".task-handoff"

// Handoff transfers queued tasks through a durable, resumable transaction.
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
	ID                     string              `json:"id"`
	Generation             string              `json:"generation"`
	SourceAggregate        mhome.TaskAggregate `json:"source_aggregate"`
	DestinationAggregate   mhome.TaskAggregate `json:"destination_aggregate"`
	SourceAuthority        []handoffFile       `json:"source_authority"`
	SourceProjections      []handoffFile       `json:"source_projections"`
	DestinationAuthority   []handoffFile       `json:"destination_authority"`
	DestinationProjections []handoffFile       `json:"destination_projections"`
}

var handoffCrashHook = func(string) {}

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

	path, err := captainLookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}
	sourceBacklog := filepath.Join(source, "data", "backlog.md")
	destinationBacklog := filepath.Join(destination, "data", "backlog.md")
	keys, err := resolveHandoffKeys(source, sourceBacklog, path, itemKeys)
	if err != nil {
		return err
	}
	journal, dir, err := prepareHandoff(source, destination, "captain:"+captainID, keys, sourceBacklog, destinationBacklog)
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

func resolveHandoffKeys(source, backlog, path string, requested []string) ([]string, error) {
	seen := make(map[string]bool, len(requested))
	keys := make([]string, 0, len(requested))
	for _, key := range requested {
		resolved, err := mhome.ResolveCurrentTaskID(source, key)
		if err != nil {
			return nil, fmt.Errorf("handoff: resolving task %s: %w", key, err)
		}
		if seen[resolved] {
			return nil, fmt.Errorf("handoff: duplicate task %s", resolved)
		}
		seen[resolved] = true
		out, err := execCommand(path, "show", resolved, "--file", backlog).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("handoff: key %s not found in source backlog: %w: %s", resolved, err, strings.TrimSpace(string(out)))
		}
		state := extractTaskStateFromShow(string(out))
		if state == "" {
			return nil, fmt.Errorf("handoff: key %s has no parseable state — only queued items may be handed off", resolved)
		}
		if state != "queued" {
			return nil, fmt.Errorf("handoff: key %s has state %q, only queued items may be handed off", resolved, state)
		}
		keys = append(keys, resolved)
	}
	return keys, nil
}

func prepareHandoff(source, destination, owner string, keys []string, sourceBacklog, destinationBacklog string) (*taskHandoffJournal, string, error) {
	id, err := newTaskHandoffID()
	if err != nil {
		return nil, "", err
	}
	journal := &taskHandoffJournal{
		Version:         1,
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
		agg, err := mhome.PreflightTaskTransfer(source, destination, taskID)
		if err != nil {
			return nil, "", fmt.Errorf("handoff: preflight task transfer %s: %w", taskID, err)
		}
		destinationAgg := *agg
		destinationAgg.Owner = owner
		task := handoffTask{ID: taskID, Generation: agg.Generation, SourceAggregate: *agg, DestinationAggregate: destinationAgg}
		for _, rel := range taskAggregateAuthorityRelPaths(taskID, agg.Generation) {
			file, err := inventoryHandoffFile(source, filepath.Join(source, rel))
			if err != nil {
				return nil, "", err
			}
			task.SourceAuthority = append(task.SourceAuthority, file)
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
		}
		if existing, ok, err := mhome.ReadCurrentTaskAggregate(destination, taskID); err != nil {
			return nil, "", err
		} else if ok {
			return nil, "", fmt.Errorf("handoff: destination already has current authority for %s generation %s", taskID, existing.Generation)
		}
		for _, rel := range taskAggregateAuthorityRelPaths(taskID, agg.Generation) {
			if _, err := os.Stat(filepath.Join(destination, rel)); err == nil {
				return nil, "", fmt.Errorf("handoff: destination authority already exists: %s", rel)
			} else if !os.IsNotExist(err) {
				return nil, "", err
			}
			task.DestinationAuthority = append(task.DestinationAuthority, handoffFile{Target: rel, Mode: 0600})
		}
		for _, rel := range taskHandoffProjectionRelPaths(taskID) {
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

func newTaskHandoffID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generating handoff transaction ID: %w", err)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), buffer), nil
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

func taskAggregateAuthorityRelPaths(taskID, generation string) []string {
	return []string{
		filepath.Join("state", ".task-authority", "aggregates", taskID, generation+".json"),
		filepath.Join("state", ".task-authority", "aggregates", taskID, "current"),
	}
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
		for k := range journal.Tasks[i].SourceAuthority {
			file := &journal.Tasks[i].SourceAuthority[k]
			if err := stageExistingHandoffFile(file, stageRoot, filepath.Join(journal.SourceHome, filepath.FromSlash(file.Target)), fmt.Sprintf("task-%d-source-authority-%d", i, k)); err != nil {
				return err
			}
		}
		for k := range journal.Tasks[i].SourceProjections {
			file := &journal.Tasks[i].SourceProjections[k]
			if err := stageExistingHandoffFile(file, stageRoot, filepath.Join(journal.SourceHome, filepath.FromSlash(file.Target)), fmt.Sprintf("task-%d-source-projection-%d", i, k)); err != nil {
				return err
			}
		}
	}

	sourcePost := filepath.Join(stageRoot, "source-backlog-post")
	destinationPost := filepath.Join(stageRoot, "destination-backlog-post")
	if err := copyHandoffFile(filepath.Join(journal.SourceHome, filepath.FromSlash(journal.SourceBacklogPre.Target)), sourcePost, 0644); err != nil {
		return err
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
		aggregate, err := json.MarshalIndent(task.DestinationAggregate, "", "  ")
		if err != nil {
			return err
		}
		aggregate = append(aggregate, '\n')
		task.DestinationAuthority[0] = stagedHandoffBytes(fmt.Sprintf("task-%d-destination-aggregate", i), aggregate, task.DestinationAuthority[0].Target, 0600)
		if err := durableHandoffWrite(filepath.Join(dir, filepath.FromSlash(task.DestinationAuthority[0].Stage)), aggregate, 0600); err != nil {
			return err
		}
		current := []byte(task.Generation + "\n")
		task.DestinationAuthority[1] = stagedHandoffBytes(fmt.Sprintf("task-%d-destination-current", i), current, task.DestinationAuthority[1].Target, 0600)
		if err := durableHandoffWrite(filepath.Join(dir, filepath.FromSlash(task.DestinationAuthority[1].Stage)), current, 0600); err != nil {
			return err
		}
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
		for _, file := range append(task.SourceAuthority, task.SourceProjections...) {
			if err := verifyHandoffFile(journal.SourceHome, file); err != nil {
				return fmt.Errorf("handoff source changed: %w", err)
			}
		}
		for _, file := range append(task.DestinationAuthority, task.DestinationProjections...) {
			if _, err := os.Stat(filepath.Join(journal.DestinationHome, filepath.FromSlash(file.Target))); err == nil {
				return fmt.Errorf("handoff destination appeared: %s", file.Target)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func rollForwardHandoff(journal *taskHandoffJournal, dir string) error {
	for _, task := range journal.Tasks {
		for _, file := range task.SourceAuthority {
			if err := removeHandoffFile(journal.SourceHome, file); err != nil {
				return err
			}
		}
	}
	handoffCrashHook("source-authority")
	if err := installHandoffPost(journal.SourceHome, journal.SourceBacklogPost, journal.SourceBacklogPre, dir); err != nil {
		return err
	}
	if err := installHandoffPost(journal.DestinationHome, journal.DestBacklogPost, journal.DestBacklogPre, dir); err != nil {
		return err
	}
	for _, task := range journal.Tasks {
		for _, file := range task.DestinationProjections {
			if file.Exists {
				if err := installHandoffFile(journal.DestinationHome, file, dir); err != nil {
					return err
				}
			}
		}
		for _, file := range task.SourceProjections {
			if err := removeHandoffFile(journal.SourceHome, file); err != nil {
				return err
			}
		}
	}
	handoffCrashHook("artifacts")
	for i, task := range journal.Tasks {
		for _, file := range task.DestinationAuthority {
			if err := installHandoffFile(journal.DestinationHome, file, dir); err != nil {
				return err
			}
		}
		if i == 0 && len(journal.Tasks) > 1 {
			handoffCrashHook("between-authority")
		}
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
	for _, task := range journal.Tasks {
		for _, file := range task.DestinationAuthority {
			if err := verifyHandoffFile(journal.DestinationHome, file); err != nil {
				return err
			}
		}
		for _, file := range task.DestinationProjections {
			if file.Exists {
				if err := verifyHandoffFile(journal.DestinationHome, file); err != nil {
					return err
				}
			}
		}
		if _, ok, err := mhome.ReadCurrentTaskAggregate(journal.SourceHome, task.ID); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("source still owns %s", task.ID)
		}
		for _, file := range append(task.SourceAuthority, task.SourceProjections...) {
			if _, err := os.Stat(filepath.Join(journal.SourceHome, filepath.FromSlash(file.Target))); err == nil {
				return fmt.Errorf("source artifact remains: %s", file.Target)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if _, ok, err := mhome.ReadCurrentTaskAggregate(journal.DestinationHome, task.ID); err != nil || !ok {
			return fmt.Errorf("destination missing %s", task.ID)
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
		if journal.Version != 1 || journal.ID != entry.Name() || journal.SourceHome != home || journal.DestinationHome == home {
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
		files = append(files, task.DestinationAuthority...)
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
