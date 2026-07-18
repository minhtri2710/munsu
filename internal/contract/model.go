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
	TaskID              string `json:"task_id"`
	Status              string `json:"status"`
	Description         string `json:"description,omitempty"`
	Branch              string `json:"branch,omitempty"`
	PaneAlive           *bool  `json:"pane_alive,omitempty"`
	NoMistakesStep      string `json:"no_mistakes_step,omitempty"`
	StatusLines         int    `json:"status_lines,omitempty"`
	StatusLogSuperseded bool   `json:"status_log_superseded,omitempty"`
}

// FleetSnapshotV2 is the version-two fleet state with cheap aggregate counts.
type FleetSnapshotV2 struct {
	Scope           string            `json:"scope"`
	Count           int               `json:"count"`
	Total           int               `json:"total"`
	Crewmates       []Crewmate        `json:"crewmates"`
	Secondmates     []SecondmateEntry `json:"secondmates,omitempty"`
	UnresolvedHolds int               `json:"unresolved_holds,omitempty"`
}

// Crewmate is the minimal row in a fleet snapshot.
type Crewmate struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Branch string `json:"branch,omitempty"`
}

// SecondmateEntry represents one secondmate in the fleet snapshot.
type SecondmateEntry struct {
	ID     string `json:"id"`
	Scope  string `json:"scope,omitempty"`
	Status string `json:"status"`
}

// GuardViolation is a single violation with supporting evidence.
type GuardViolation struct {
	Condition string   `json:"condition"`
	Evidence  []string `json:"evidence"`
}

// Guard reports the current guard evaluation.
type Guard struct {
	State      string           `json:"state"` // healthy | unhealthy | indeterminate
	Violations []GuardViolation `json:"violations,omitempty"`
	Conditions []string         `json:"conditions,omitempty"` // kept for backward compat
}

// WatchLeaseInfo carries identity and heartbeat metadata for a watcher lease.
type WatchLeaseInfo struct {
	Identity    string `json:"identity,omitempty"`
	LeaseID     string `json:"lease_id,omitempty"`
	Heartbeat   string `json:"heartbeat,omitempty"`
	HeartbeatOK bool   `json:"heartbeat_ok,omitempty"`
}

// WatchEnsure reports the idempotent watcher-ensure result.
type WatchEnsure struct {
	WatchID  string          `json:"watch_id"`
	State    string          `json:"state"` // started | attached | healthy | failed
	Interval string          `json:"interval,omitempty"`
	Lease    *WatchLeaseInfo `json:"lease,omitempty"`
	Noop     bool            `json:"noop"`
}

// WatchRun reports one watcher execution with terminal observation.
type WatchRun struct {
	WatchID        string `json:"watch_id"`
	State          string `json:"state"` // completed | failed
	WakesScanned   int    `json:"wakes_scanned"`
	WakesEmitted   int    `json:"wakes_emitted"`
	EventsObserved int    `json:"events_observed,omitempty"`
}

// WatchStop reports the result of stopping a watcher.
type WatchStop struct {
	WatchID string `json:"watch_id"`
	PID     int    `json:"pid"`
	State   string `json:"state"` // stopped | already-stopped
}

// WakeClaim records a leased wake claim.
type WakeClaim struct {
	WakeID       string `json:"wake_id"`
	ClaimID      string `json:"claim_id"`
	Owner        string `json:"owner"`
	State        string `json:"state"` // claimed | replayed
	LeaseExpires int64  `json:"lease_expires,omitempty"`
	Reclaimed    int    `json:"reclaimed,omitempty"`
}

// WakeAck records acknowledgement of a claimed wake.
type WakeAck struct {
	WakeID  string `json:"wake_id"`
	ClaimID string `json:"claim_id"`
	State   string `json:"state"`
}

// EventRecord is one typed event log entry.
type EventRecord struct {
	EventID   uint64 `json:"event_id"`
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"`
	Producer  string `json:"producer,omitempty"`
	Key       string `json:"key,omitempty"`
	Payload   string `json:"payload"`
}

// EventAppend is the result of appending a typed event.
type EventAppend struct {
	EventID   uint64 `json:"event_id"`
	Type      string `json:"type"`
	Synthetic bool   `json:"synthetic,omitempty"`
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

// SessionStart is the structured session-start digest.
type SessionStart struct {
	Lock        string `json:"lock"`
	Watcher     string `json:"watcher"`
	BootstrapOK bool   `json:"bootstrap_ok"`
	FleetSyncOK bool   `json:"fleet_sync_ok"`
	Message     string `json:"message"`
}

// TaskEntry is one row in a task list.
type TaskEntry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	Project string `json:"project,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ProjectEntry is one row in a project list.
type ProjectEntry struct {
	Name        string `json:"name"`
	Mode        string `json:"mode,omitempty"`
	Yolo        bool   `json:"yolo,omitempty"`
	Description string `json:"description,omitempty"`
	Added       string `json:"added,omitempty"`
	Directory   string `json:"directory,omitempty"`
}

// SafetyCheckData is the structured safety assessment result.
// Both block and gate_refused MUST always serialize (no omitempty)
// because the TS parseSafetyCheck helper requires typeof boolean checks.
type SafetyCheckData struct {
	Identity       string `json:"identity"`
	GateCapability string `json:"gate_capability"`
	CanonicalPath  string `json:"canonical_path,omitempty"`
	GateRefused    bool   `json:"gate_refused"`
	Block          bool   `json:"block"`
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
}

// DecisionHoldInfo is one row in a decision-hold list.
type DecisionHoldInfo struct {
	DecisionKey string `json:"decision_key"`
	Reason      string `json:"reason,omitempty"`
}
