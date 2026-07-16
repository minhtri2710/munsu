// Package contract defines the JSON-compatible data model for the versioned
// orchestration contract. It intentionally has no runtime behavior; Phase 1
// will use these types at the output boundary.
package contract

const (
	// SchemaVersion is the stable schema identifier shared by TOON and JSON.
	SchemaVersion = "munsu.orchestration/v2"
	OutputTOON    = "toon"
	OutputJSON    = "json"
)

// Response is the common envelope for successful contract responses.
type Response[T any] struct {
	SchemaVersion string   `json:"schema_version"`
	Kind          string   `json:"kind"`
	Status        string   `json:"status"`
	Data          T        `json:"data"`
	Help          []string `json:"help,omitempty"`
}

// ErrorResponse is the common envelope for operation and usage failures.
type ErrorResponse struct {
	SchemaVersion string        `json:"schema_version"`
	Kind          string        `json:"kind"`
	Status        string        `json:"status"`
	Error         ErrorEnvelope `json:"error"`
}

// ErrorEnvelope supplies stable, actionable error details on stdout.
type ErrorEnvelope struct {
	ErrorCode string `json:"error_code"`
	Retryable bool   `json:"retryable"`
	Action    string `json:"action"`
	Message   string `json:"message"`
}

// Capabilities describes the contract surface available from this executable.
type Capabilities struct {
	ContractVersion string   `json:"contract_version"`
	Commands        []string `json:"commands"`
	OutputFormats   []string `json:"output_formats"`
}

// TaskObserve is the compact state of one task in the current project.
type TaskObserve struct {
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	Description    string `json:"description,omitempty"`
	Branch         string `json:"branch,omitempty"`
	PaneAlive      bool   `json:"pane_alive"`
	NoMistakesStep string `json:"no_mistakes_step,omitempty"`
}

// FleetSnapshotV2 is the version-two fleet state with cheap aggregate counts.
type FleetSnapshotV2 struct {
	Scope     string     `json:"scope"`
	Count     int        `json:"count"`
	Total     int        `json:"total"`
	Crewmates []Crewmate `json:"crewmates"`
}

// Crewmate is the minimal row in a fleet snapshot.
type Crewmate struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Branch string `json:"branch,omitempty"`
}

// Guard reports the current guard evaluation.
type Guard struct {
	State      string   `json:"state"`
	Conditions []string `json:"conditions"`
}

// WatchEnsure reports the idempotent watcher-ensure result.
type WatchEnsure struct {
	WatchID  string `json:"watch_id"`
	State    string `json:"state"`
	Interval string `json:"interval"`
	Noop     bool   `json:"noop"`
}

// WatchRun reports one watcher execution.
type WatchRun struct {
	WatchID      string `json:"watch_id"`
	State        string `json:"state"`
	WakesScanned int    `json:"wakes_scanned"`
	WakesEmitted int    `json:"wakes_emitted"`
}

// WakeClaim records a leased wake claim.
type WakeClaim struct {
	WakeID  string `json:"wake_id"`
	ClaimID string `json:"claim_id"`
	Owner   string `json:"owner"`
	State   string `json:"state"`
}

// WakeAck records acknowledgement of a claimed wake.
type WakeAck struct {
	WakeID  string `json:"wake_id"`
	ClaimID string `json:"claim_id"`
	State   string `json:"state"`
}

// BackendCapabilities describes one session backend's supported operations.
type BackendCapabilities struct {
	Backend  string   `json:"backend"`
	Features []string `json:"features"`
}

// SpawnReceipt is the compact, successful spawn result.
type SpawnReceipt struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
	Worktree  string `json:"worktree"`
	Branch    string `json:"branch"`
	State     string `json:"state"`
}

// MessageResult represents success and idempotent no-op acknowledgements.
type MessageResult struct {
	Message string `json:"message"`
	Noop    bool   `json:"noop"`
}

// EmptyResult makes an empty collection definitive.
type EmptyResult struct {
	Count   int    `json:"count"`
	Context string `json:"context"`
}

// TruncatedResult preserves a preview and an explicit retrieval escape hatch.
type TruncatedResult struct {
	Preview     string `json:"preview"`
	TotalChars  int    `json:"total_chars"`
	Truncated   bool   `json:"truncated"`
	FullCommand string `json:"full_command"`
}
