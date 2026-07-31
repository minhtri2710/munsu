package fleet

// Backend defines the interface for backlog storage backends.
// Adding a new backend (e.g. SQLite, GitHub Issues) means implementing
// this interface without changing any callers.
type Backend interface {
	// Add creates a new backlog item. The start argument is retained for
	// compatibility but is ignored; additions always begin queued.
	Add(id, description, kind, repo string, start bool) error

	// List returns all items. When filter is StateQueued with empty marker
	// (zero value), returns all items unfiltered. Otherwise filters by state.
	List(filter TaskState) ([]Item, error)

	// Show returns the item with the given ID. The bool indicates whether
	// the item was found.
	Show(id string) (Item, bool)

	// UpdateState transitions an item to a new state. Returns an error if
	// the state transition is invalid (e.g. done -> queued).
	UpdateState(id string, state TaskState) error
}

// TasksAxiBackend implements Backend by delegating to the tasks-axi CLI.
// HomeDir scopes operations to <home>/data/md; an empty value uses cwd configuration.
type TasksAxiBackend struct {
	HomeDir string
}

// Add delegates to tasks-axi add.
func (b *TasksAxiBackend) Add(id, description, kind, repo string, start bool) error {
	args := buildTasksAxiAddArgs(id, description, kind, repo, start)
	return runTasksAxiForHome(b.HomeDir, "add", args)
}

// List delegates to tasks-axi list.
func (b *TasksAxiBackend) List(filter TaskState) ([]Item, error) {
	args := []string{}
	if filter != StateQueued || filter.Marker() != " " {
		// Only pass filter for non-default states
		args = append(args, filter.String())
	}
	if err := runTasksAxiForHome(b.HomeDir, "list", args); err != nil {
		return nil, err
	}
	return nil, nil // tasks-axi writes to stdout directly; we return no structured data
}

// Show delegates to tasks-axi show.
func (b *TasksAxiBackend) Show(id string) (Item, bool) {
	if err := runTasksAxiForHome(b.HomeDir, "show", []string{id}); err != nil {
		return Item{}, false
	}
	return Item{}, true // tasks-axi writes to stdout directly
}

// UpdateState delegates to tasks-axi start/done/block/ready.
func (b *TasksAxiBackend) UpdateState(id string, state TaskState) error {
	verb, err := stateToVerb(state)
	if err != nil {
		return err
	}
	return runTasksAxiForHome(b.HomeDir, verb, []string{id})
}

// stateToVerb maps a TaskState to the corresponding tasks-axi verb.
func stateToVerb(s TaskState) (string, error) {
	switch s {
	case StateInFlight:
		return "start", nil
	case StateDone:
		return "done", nil
	case StateBlocked:
		return "block", nil
	case StateQueued:
		return "ready", nil
	default:
		return "", nil
	}
}
