package backlog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	execCommand = exec.Command
	lookPath    = exec.LookPath
)

var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+`)

// --- Compat public API ---

// Run dispatches to tasks-axi if compatible and not forced to manual, or falls back
// to manual markdown.
// When isDefault is false (non-default --home), always forces manual backend
// to prevent data leaks across homes.
func Run(homeDir string, isDefault bool, verb string, args []string) error {
	if !isDefault {
		return manualRun(homeDir, verb, args)
	}
	if isManual(homeDir) {
		return manualRun(homeDir, verb, args)
	}
	if tasksAxiAvailable() {
		return runTasksAxiForHome(homeDir, verb, args)
	}
	return manualRun(homeDir, verb, args)
}

// isManual checks whether the config/backlog-backend file under homeDir contains "manual".
func isManual(homeDir string) bool {
	if homeDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(homeDir, "config", "backlog-backend"))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "manual"
}

// RunManual runs a backlog verb using the manual backend (always home-scoped).
func RunManual(homeDir, verb string, args []string) error {
	return manualRun(homeDir, verb, args)
}

// manualRun handles backlog operations using the FileBackend.
func manualRun(homeDir, verb string, args []string) error {
	fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
	switch verb {
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: backlog add <id> <description>")
		}
		return fb.Add(args[0], strings.Join(args[1:], " "), "", "", false)
	case "list":
		return listViaFileBackend(fb, args)
	case "show":
		if len(args) < 1 {
			return fmt.Errorf("usage: backlog show <id>")
		}
		return showViaFileBackend(fb, args[0])
	case "start":
		return transitionViaFileBackend(fb, args, StateInFlight)
	case "done":
		return transitionViaFileBackend(fb, args, StateDone)
	case "block":
		return transitionViaFileBackend(fb, args, StateBlocked)
	case "ready", "unblock":
		return transitionViaFileBackend(fb, args, StateQueued)
	default:
		return fmt.Errorf("backlog: unknown verb %q (supported: add, list, show, start, done, block, ready, unblock)", verb)
	}
}

// listViaFileBackend lists items to stdout via the FileBackend.
func listViaFileBackend(fb *FileBackend, args []string) error {
	filter := StateQueued // zero value means no filter
	if len(args) > 0 {
		s, ok := nameToState[args[0]]
		if !ok {
			return fmt.Errorf("backlog: unknown state filter %q (supported: queued, in-flight, blocked, done)", args[0])
		}
		filter = s
	}
	items, err := fb.List(filter)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("backlog is empty")
		return nil
	}
	for _, item := range items {
		fmt.Println(formatItem(item))
	}
	return nil
}

// showViaFileBackend shows item details to stdout via the FileBackend.
func showViaFileBackend(fb *FileBackend, id string) error {
	item, ok := fb.Show(id)
	if !ok {
		return fmt.Errorf("backlog: item %q not found", id)
	}
	fmt.Printf("id:     %s\n", item.ID)
	fmt.Printf("desc:   %s\n", item.Description)
	fmt.Printf("state:  %s %s\n", item.State.Display(), item.State)
	if item.Kind != "" {
		fmt.Printf("kind:   %s\n", item.Kind)
	}
	if item.Repo != "" {
		fmt.Printf("repo:   %s\n", item.Repo)
	}
	return nil
}

// transitionViaFileBackend transitions an item via the FileBackend and prints the result.
func transitionViaFileBackend(fb *FileBackend, args []string, toState TaskState) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: backlog <verb> <id>")
	}
	id := args[0]
	if err := fb.UpdateState(id, toState); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", toState.Display(), id)
	return nil
}

// AddItem adds a backlog item with optional metadata, using the manual (home-scoped) backend.
func AddItem(homeDir, id, desc, kind, repo string, start bool) error {
	fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
	return fb.Add(id, desc, kind, repo, start)
}

// GetItem reads a backlog item by ID from the home-scoped backlog file.
// Returns the item, whether it was found, and any error.
func GetItem(homeDir, id string) (Item, bool, error) {
	fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
	item, ok := fb.Show(id)
	if !ok {
		return Item{}, false, nil
	}
	return item, true, nil
}

// HasDuplicate checks whether the backlog contains multiple items with the same ID.
func HasDuplicate(homeDir, id string) (bool, error) {
	fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
	items, err := fb.List(StateQueued) // unfiltered returns all
	if err != nil {
		return false, err
	}
	count := 0
	for _, item := range items {
		if item.ID == id {
			count++
			if count > 1 {
				return true, nil
			}
		}
	}
	return false, nil
}

// AddItemDispatch adds a backlog item using the unified dispatcher.
// Routes to tasks-axi when available and not forced to manual.
func AddItemDispatch(homeDir, id, desc, kind, repo string, start bool) error {
	if isManual(homeDir) {
		return AddItem(homeDir, id, desc, kind, repo, start)
	}
	if tasksAxiAvailable() {
		args := buildTasksAxiAddArgs(id, desc, kind, repo, start)
		return runTasksAxiForHome(homeDir, "add", args)
	}
	return AddItem(homeDir, id, desc, kind, repo, start)
}

// formatItem formats an Item for display output.
func formatItem(item Item) string {
	line := fmt.Sprintf("- %s %s: %s", item.State.Display(), item.ID, item.Description)
	if item.Kind != "" || item.Repo != "" {
		var parts []string
		if item.Kind != "" {
			parts = append(parts, "kind="+item.Kind)
		}
		if item.Repo != "" {
			parts = append(parts, "repo="+item.Repo)
		}
		line += " [" + strings.Join(parts, " ") + "]"
	}
	return line
}

// --- tasks-axi integration ---

// tasksAxiAvailable checks if tasks-axi >= 0.1.1 is on PATH.
func tasksAxiAvailable() bool {
	path, err := lookPath("tasks-axi")
	if err != nil {
		return false
	}

	cmd := execCommand(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	version := strings.TrimSpace(string(out))
	return isCompatibleVersion(version, "0.1.1")
}

// isCompatibleVersion checks if the installed version is >= minimum.
// Simple semver comparison (major.minor.patch).
func isCompatibleVersion(installed, minimum string) bool {
	installParts := parseVersion(installed)
	minParts := parseVersion(minimum)

	for i := 0; i < 3; i++ {
		if installParts[i] > minParts[i] {
			return true
		}
		if installParts[i] < minParts[i] {
			return false
		}
	}
	return true
}

// parseVersion extracts the first "x.y.z" match and splits it into [major, minor, patch] ints.
func parseVersion(v string) [3]int {
	match := semverRegex.FindString(v)
	if match == "" {
		return [3]int{0, 0, 0}
	}
	parts := strings.SplitN(match, ".", 3)
	var result [3]int
	for i, p := range parts {
		if i < 3 {
			result[i] = atoi(p)
		}
	}
	return result
}

// atoi parses an integer from a string, returning 0 on error.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}

// runTasksAxi runs tasks-axi using its current-directory configuration.
func runTasksAxi(verb string, args []string) error {
	return runTasksAxiForHome("", verb, args)
}

// runTasksAxiForHome scopes tasks-axi to a runtime home's durable backlog.
func runTasksAxiForHome(homeDir, verb string, args []string) error {
	path, err := lookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}

	cliArgs := []string{verb}
	cliArgs = append(cliArgs, args...)
	if homeDir != "" {
		backlogPath, err := filepath.Abs(filepath.Join(homeDir, "data", "backlog.md"))
		if err != nil {
			return fmt.Errorf("resolving backlog path: %w", err)
		}
		cliArgs = append(cliArgs, "--file", backlogPath)
	}

	cmd := execCommand(path, cliArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildTasksAxiAddArgs builds the argument list for tasks-axi add.
func buildTasksAxiAddArgs(id, desc, kind, repo string, start bool) []string {
	args := []string{id, desc}
	if kind != "" {
		args = append(args, "--kind", kind)
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	if start {
		args = append(args, "--start")
	}
	return args
}
