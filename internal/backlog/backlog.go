package backlog

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	execCommand = exec.Command
	lookPath    = exec.LookPath
)

var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Run dispatches to tasks-axi if compatible and not forced to manual, or falls back to manual markdown.
func Run(homeDir, verb string, args []string) error {
	if isManual(homeDir) {
		return manualRun(homeDir, verb, args)
	}
	if tasksAxiAvailable() {
		return runTasksAxi(verb, args)
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

// manualRun handles backlog operations using a local markdown file.
func manualRun(homeDir, verb string, args []string) error {
	backlogPath := filepath.Join(homeDir, "data", "backlog.md")

	switch verb {
	case "add":
		return addItem(backlogPath, args)
	case "list":
		return listItems(backlogPath, args)
	case "show":
		return showItem(backlogPath, args)
	case "start":
		return transitionItem(backlogPath, args, "-")
	case "done":
		return transitionItem(backlogPath, args, "x")
	case "block":
		return transitionItem(backlogPath, args, "!")
	case "ready", "unblock":
		return transitionItem(backlogPath, args, " ")
	default:
		return fmt.Errorf("backlog: unknown verb %q (supported: add, list, show, start, done, block, ready, unblock)", verb)
	}
}

// --- data model ---

// backlogItem represents a single backlog item.
type backlogItem struct {
	id    string
	desc  string
	state string // " ", "x", "-", "!"
	kind  string
	repo  string
}

// stateDisplay maps internal state chars to display markers.
func stateDisplay(s string) string {
	switch s {
	case " ":
		return "[ ]"
	case "-":
		return "[-]"
	case "!":
		return "[!]"
	case "x":
		return "[x]"
	default:
		return "[ ]"
	}
}

// stateMap maps state verbs to their marker char.
var stateMap = map[string]string{
	"queued":    " ",
	"in-flight": "-",
	"blocked":   "!",
	"done":      "x",
}

// --- parse ---

// parseBacklog reads a backlog.md and returns items grouped by section.
// Format:
//
//	# Backlog
//
//	## 2024-01-01
//	- [ ] id: description
//	- [-] id: description
//	- [!] id: description
//	- [x] id: description
func parseBacklog(path string) ([]backlogItem, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening backlog: %w", err)
	}
	defer f.Close()

	itemRe := regexp.MustCompile(`^\s*-\s+\[( |x|-|!)\]\s+(\S+):\s*(.*)$`)
	metaRe := regexp.MustCompile(`^(.*)\s+\[kind=(\S+)\s+repo=(\S+)\]$`)
	var items []backlogItem
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		m := itemRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		desc := m[3]
		kind := ""
		repo := ""
		if metaM := metaRe.FindStringSubmatch(desc); metaM != nil {
			desc = metaM[1]
			kind = metaM[2]
			repo = metaM[3]
		}
		items = append(items, backlogItem{
			state: m[1],
			id:    m[2],
			desc:  desc,
			kind:  kind,
			repo:  repo,
		})
	}
	return items, scanner.Err()
}

// --- render ---

// renderBacklog writes items to the backlog file, preserving the header/section structure.
func renderBacklog(path string, items []backlogItem) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	sort.Slice(items, func(i, j int) bool {
		// Items queued first, then in-flight, then blocked, then done
		order := map[string]int{" ": 0, "-": 1, "!": 2, "x": 3}
		oi := order[items[i].state]
		oj := order[items[j].state]
		if oi != oj {
			return oi < oj
		}
		return items[i].id < items[j].id
	})

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("writing backlog: %w", err)
	}
	defer f.Close()

	fmt.Fprintln(f, "# Backlog")
	fmt.Fprintln(f)

	today := time.Now().Format("2006-01-02")
	fmt.Fprintf(f, "## %s\n", today)
	for _, item := range items {
		line := fmt.Sprintf("- %s %s: %s", stateDisplay(item.state), item.id, item.desc)
		if item.kind != "" || item.repo != "" {
			var parts []string
			if item.kind != "" {
				parts = append(parts, "kind="+item.kind)
			}
			if item.repo != "" {
				parts = append(parts, "repo="+item.repo)
			}
			line += " [" + strings.Join(parts, " ") + "]"
		}
		fmt.Fprintln(f, line)
	}

	return f.Close()
}

// --- ensureBacklog creates the backlog file with a header if it doesn't exist. ---
func ensureBacklog(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("creating data directory: %w", err)
		}
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("creating backlog: %w", err)
		}
		defer f.Close()
		fmt.Fprintln(f, "# Backlog")
		fmt.Fprintln(f)
		today := time.Now().Format("2006-01-02")
		fmt.Fprintf(f, "## %s\n", today)
		return f.Close()
	}
	return nil
}

// --- verb handlers ---

func addItem(path string, args []string) error {
	return addItemOpts(path, args, "", "", false)
}

func addItemOpts(path string, args []string, kind string, repo string, start bool) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: backlog add <id> <description>")
	}
	id := args[0]
	desc := strings.Join(args[1:], " ")

	if err := ensureBacklog(path); err != nil {
		return err
	}

	items, err := parseBacklog(path)
	if err != nil {
		return err
	}

	// Check for duplicate
	for _, item := range items {
		if item.id == id {
			return fmt.Errorf("backlog: item %q already exists", id)
		}
	}

	initialState := " "
	if start {
		initialState = "-"
	}

	items = append(items, backlogItem{id: id, desc: desc, state: initialState, kind: kind, repo: repo})
	if err := renderBacklog(path, items); err != nil {
		return err
	}
	fmt.Printf("added: %s\n", id)
	return nil
}

// AddItem adds a backlog item with optional metadata, using the manual (home-scoped) backend.
// This is the public entry point for the backlog add command with --kind/--repo/--start.
func AddItem(homeDir, id, desc, kind, repo string, start bool) error {
	path := filepath.Join(homeDir, "data", "backlog.md")
	return addItemOpts(path, []string{id, desc}, kind, repo, start)
}

// RunManual runs a backlog verb using the manual backend (always home-scoped).
func RunManual(homeDir, verb string, args []string) error {
	return manualRun(homeDir, verb, args)
}



func listItems(path string, args []string) error {
	items, err := parseBacklog(path)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		fmt.Println("backlog is empty")
		return nil
	}

	// Filter by state if requested
	filterState := ""
	if len(args) > 0 {
		if s, ok := stateMap[args[0]]; ok {
			filterState = s
		} else {
			return fmt.Errorf("backlog: unknown state filter %q (supported: queued, in-flight, blocked, done)", args[0])
		}
	}

	for _, item := range items {
		if filterState != "" && item.state != filterState {
			continue
		}
		line := fmt.Sprintf("- %s %s: %s", stateDisplay(item.state), item.id, item.desc)
		if item.kind != "" || item.repo != "" {
			var parts []string
			if item.kind != "" {
				parts = append(parts, "kind="+item.kind)
			}
			if item.repo != "" {
				parts = append(parts, "repo="+item.repo)
			}
			line += " [" + strings.Join(parts, " ") + "]"
		}
		fmt.Println(line)
	}
	return nil
}

func showItem(path string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: backlog show <id>")
	}
	id := args[0]

	items, err := parseBacklog(path)
	if err != nil {
		return err
	}

	for _, item := range items {
		if item.id == id {
			stateName := "queued"
			switch item.state {
			case "-":
				stateName = "in-flight"
			case "!":
				stateName = "blocked"
			case "x":
				stateName = "done"
			}
			fmt.Printf("id:     %s\n", item.id)
			fmt.Printf("desc:   %s\n", item.desc)
			fmt.Printf("state:  %s %s\n", stateDisplay(item.state), stateName)
			if item.kind != "" {
				fmt.Printf("kind:   %s\n", item.kind)
			}
			if item.repo != "" {
				fmt.Printf("repo:   %s\n", item.repo)
			}
			return nil
		}
	}
	return fmt.Errorf("backlog: item %q not found", id)
}

func transitionItem(path string, args []string, newState string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: backlog <verb> <id>")
	}
	id := args[0]

	items, err := parseBacklog(path)
	if err != nil {
		return err
	}

	found := false
	for i, item := range items {
		if item.id == id {
			items[i].state = newState
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("backlog: item %q not found", id)
	}

	if err := renderBacklog(path, items); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", stateDisplay(newState), id)
	return nil
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

// runTasksAxi runs tasks-axi with the given verb and args.
func runTasksAxi(verb string, args []string) error {
	path, err := lookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}

	cliArgs := []string{verb}
	cliArgs = append(cliArgs, args...)

	cmd := execCommand(path, cliArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
