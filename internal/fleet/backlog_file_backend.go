package fleet

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// FileBackend implements Backend using a local markdown file.
// The file format is:
//
//	# Backlog
//
//	## YYYY-MM-DD
//	- [ ] id: description
//	- [-] id: description [kind=foo repo=bar]
//	- [!] id: description
//	- [x] id: description
type FileBackend struct {
	path string
}

// compile once
var (
	// itemRe matches both legacy ("id: description") and tasks-axi dash ("id - description") formats.
	itemRe = regexp.MustCompile(`^\s*-\s+\[( |x|-|!)\]\s+(\S+)(?:\s*-\s+|:\s+)(.*)$`)
	// legacyMetaRe matches [kind=x repo=y] suffix.
	legacyMetaRe = regexp.MustCompile(`^(.*)\s+\[kind=(\S+)\s+repo=(\S+)\]$`)
	// dashMetaRe matches (repo: x) (kind: y) and optional (since ...) suffix (tasks-axi format).
	dashMetaRe = regexp.MustCompile(`^(.*)\(repo:\s+(\S+)\)\s*\(kind:\s+(\S+)\)(?:\s*\(since[^)]*\))?$`)
)

// NewFileBackend creates a FileBackend writing to the given path.
func NewFileBackend(path string) *FileBackend {
	return &FileBackend{path: path}
}

// Add implements Backend.Add.
func (fb *FileBackend) Add(id, description, kind, repo string, _ bool) error {
	if err := fb.ensureBacklog(); err != nil {
		return err
	}

	items, err := fb.parse()
	if err != nil {
		return err
	}

	// Check for duplicate
	for _, item := range items {
		if item.ID == id {
			return fmt.Errorf("backlog: item %q already exists", id)
		}
	}

	items = append(items, Item{ID: id, Description: description, State: StateQueued, Kind: kind, Repo: repo})
	if err := fb.render(items); err != nil {
		return err
	}
	fmt.Printf("added: %s\n", id)
	return nil
}

// List implements Backend.List.
func (fb *FileBackend) List(filter TaskState) ([]Item, error) {
	items, err := fb.parse()
	if err != nil {
		return nil, err
	}

	// When filter is zero (StateQueued), return all items (no filtering).
	// When a non-default state is provided, narrow to matching items.
	// Callers requesting only queued items should post-filter.
	if filter == StateQueued {
		return items, nil
	}

	var result []Item
	for _, item := range items {
		if item.State == filter {
			result = append(result, item)
		}
	}
	return result, nil
}

// Show implements Backend.Show.
func (fb *FileBackend) Show(id string) (Item, bool) {
	items, err := fb.parse()
	if err != nil {
		return Item{}, false
	}
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

// UpdateState implements Backend.UpdateState.
func (fb *FileBackend) UpdateState(id string, state TaskState) error {
	items, err := fb.parse()
	if err != nil {
		return err
	}

	found := false
	for i, item := range items {
		if item.ID == id {
			if !item.State.CanTransitionTo(state) {
				return fmt.Errorf("backlog: cannot transition from %s to %s", item.State, state)
			}
			items[i].State = state
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("backlog: item %q not found", id)
	}

	return fb.render(items)
}

// Reopen transitions a terminal backlog item back to queued.
func (fb *FileBackend) Reopen(id string) error {
	items, err := fb.parse()
	if err != nil {
		return err
	}
	for i, item := range items {
		if item.ID != id {
			continue
		}
		if item.State != StateDone {
			return fmt.Errorf("backlog: reopen requires done item %q", id)
		}
		items[i].State = StateQueued
		return fb.render(items)
	}
	return fmt.Errorf("backlog: item %q not found", id)
}

// Retry transitions a failed, blocked, or done backlog item back to queued so
// a superseded generation can be re-dispatched. Live queued/in-flight items are
// not touched by this verb (they have no failed generation to retry).
func (fb *FileBackend) Retry(id string) error {
	items, err := fb.parse()
	if err != nil {
		return err
	}
	for i, item := range items {
		if item.ID != id {
			continue
		}
		switch item.State {
		case StateInFlight, StateBlocked, StateDone:
			items[i].State = StateQueued
			return fb.render(items)
		case StateQueued:
			return fmt.Errorf("backlog: retry requires a failed/blocked/done item %q, item is queued", id)
		default:
			return fmt.Errorf("backlog: retry requires a failed/blocked/done item %q", id)
		}
	}
	return fmt.Errorf("backlog: item %q not found", id)
}

// --- internal helpers ---

// parse reads backlog items from the file.
func (fb *FileBackend) parse() ([]Item, error) {
	f, err := os.Open(fb.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening backlog: %w", err)
	}
	defer f.Close()

	var items []Item
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
		if metaM := legacyMetaRe.FindStringSubmatch(desc); metaM != nil {
			desc = metaM[1]
			kind = metaM[2]
			repo = metaM[3]
		} else if metaM := dashMetaRe.FindStringSubmatch(desc); metaM != nil {
			desc = strings.TrimSpace(metaM[1])
			repo = metaM[2]
			kind = metaM[3]
		}

		state, _ := ParseState(m[1])
		items = append(items, Item{
			State:       state,
			ID:          m[2],
			Description: desc,
			Kind:        kind,
			Repo:        repo,
		})
	}
	return items, scanner.Err()
}

// render writes items to the backlog file.
func (fb *FileBackend) render(items []Item) error {
	if err := os.MkdirAll(filepath.Dir(fb.path), 0755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	sort.Slice(items, func(i, j int) bool {
		order := map[TaskState]int{StateQueued: 0, StateInFlight: 1, StateBlocked: 2, StateDone: 3}
		oi := order[items[i].State]
		oj := order[items[j].State]
		if oi != oj {
			return oi < oj
		}
		return items[i].ID < items[j].ID
	})

	f, err := os.Create(fb.path)
	if err != nil {
		return fmt.Errorf("writing backlog: %w", err)
	}
	defer f.Close()

	fmt.Fprintln(f, "# Backlog")
	fmt.Fprintln(f)

	today := time.Now().Format("2006-01-02")
	fmt.Fprintf(f, "## %s\n", today)
	for _, item := range items {
		line := formatItem(item)
		fmt.Fprintln(f, line)
	}

	return f.Close()
}

// ensureBacklog creates the backlog file with a header if it doesn't exist.
func (fb *FileBackend) ensureBacklog() error {
	if _, err := os.Stat(fb.path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(fb.path), 0755); err != nil {
			return fmt.Errorf("creating data directory: %w", err)
		}
		f, err := os.Create(fb.path)
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
