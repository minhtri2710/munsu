// Package contract defines the JSON-compatible data model for the versioned
// orchestration contract. It intentionally has no runtime behavior; Phase 1
// will use these types at the output boundary.
package cli

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
	Scope           string          `json:"scope"`
	Count           int             `json:"count"`
	Total           int             `json:"total"`
	Soldiers        []Soldier       `json:"soldiers"`
	Captains        []CaptainEntry  `json:"captains,omitempty"`
	CaptainGuidance CaptainGuidance `json:"captain_guidance"`
	UnresolvedHolds int             `json:"unresolved_holds,omitempty"`
}

// CaptainGuidance is return-channel action note for renderers and bearings
// (munsu captain_guidance analog).
type CaptainGuidance struct {
	Note              string `json:"note"`
	Watch             string `json:"watch,omitempty"`
	Send              string `json:"send,omitempty"`
	ReturnChannelNote string `json:"return_channel_note,omitempty"`
}

// DefaultCaptainGuidance returns the static General bearings note for captains.
func DefaultCaptainGuidance() CaptainGuidance {
	return CaptainGuidance{
		Note:              "For kind=captain, bearings selects validated structured state from that registered home; parent events and bounded terminal evidence are fallback-only supplements and never current-state authority.",
		Watch:             "read status/doc return channel; do not routinely munsu peek a captain for answers",
		Send:              "munsu send captain:<id> '<request>'",
		ReturnChannelNote: "Captain answers come back through parent state/captain:<id>.status (and optional docs) after a marked munsu send request.",
	}
}

// Soldier is the minimal row in a fleet snapshot.
type Soldier struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Branch string `json:"branch,omitempty"`
}

// CaptainEntry represents one captain in the fleet snapshot.
// Parent status is untrusted supplemental evidence (return channel);
// Current/home fields are derived from the registered Captain home when readable.
// When parent last_status disagrees with structured home, Contradiction is set and
// Provenance remains structured-home (home is authoritative).
type CaptainEntry struct {
	ID                  string              `json:"id"`
	Home                string              `json:"home,omitempty"`
	Scope               string              `json:"scope,omitempty"`
	Status              string              `json:"status"` // endpoint liveness: seeded|alive|dead|unknown
	CurrentState        string              `json:"current_state,omitempty"`
	CurrentReason       string              `json:"current_reason,omitempty"`
	Valid               *bool               `json:"valid,omitempty"`
	LastParentStatus    string              `json:"last_parent_status,omitempty"`
	ActiveChildren      []CaptainChildBrief `json:"active_children,omitempty"`
	DecisionsOpen       []CaptainDecision   `json:"decisions_open,omitempty"`
	Holds               []CaptainHold       `json:"holds,omitempty"`
	Queued              []CaptainQueued     `json:"queued,omitempty"`
	Landed              []CaptainLanded     `json:"landed,omitempty"`
	Omitted             []CaptainOmitted    `json:"omitted,omitempty"`
	Counts              *CaptainHomeCounts  `json:"counts,omitempty"`
	Provenance          string              `json:"provenance,omitempty"`        // structured-home | parent-status-only | unavailable
	Freshness           string              `json:"freshness,omitempty"`         // fresh | historical-event | unknown
	ParentEventRole     string              `json:"parent_event_role,omitempty"` // historical-only | fallback-only-not-current | none
	Contradiction       bool                `json:"contradiction,omitempty"`
	ContradictionReason string              `json:"contradiction_reason,omitempty"`
}

// CaptainChildBrief is a bounded child row under a Captain home.
type CaptainChildBrief struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Doing  string `json:"doing,omitempty"`
}

// CaptainDecision is one open decision under a Captain home.
type CaptainDecision struct {
	ID      string `json:"id"`
	Key     string `json:"key,omitempty"`
	Verb    string `json:"verb,omitempty"`
	Summary string `json:"summary,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Source  string `json:"source,omitempty"`
}

// CaptainHold is one external hold under a Captain home.
type CaptainHold struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	BlockedBy string `json:"blocked_by,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Source    string `json:"source,omitempty"`
}

// CaptainQueued is one queued Task Authority record under a Captain home.
type CaptainQueued struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Repo  string `json:"repo,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// CaptainLanded is one recently completed Task Authority record under a Captain home.
type CaptainLanded struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	PRURL string `json:"pr_url,omitempty"`
}

// CaptainOmitted discloses a truncated home-summary surface.
type CaptainOmitted struct {
	Surface string `json:"surface"`
	Count   int    `json:"count"`
}

// CaptainHomeCounts aggregates Captain-home workload surfaces.
type CaptainHomeCounts struct {
	ActiveChildren int `json:"active_children"`
	DecisionsOpen  int `json:"decisions_open"`
	Holds          int `json:"holds"`
	Queued         int `json:"queued"`
	Landed         int `json:"landed"`
	Endpoints      int `json:"endpoints"`
	InFlight       int `json:"in_flight"`
	Blocked        int `json:"blocked"`
	Done           int `json:"done"`
}

// GuardViolation is a single violation with supporting evidence.
type GuardViolation struct {
	Code      string   `json:"code,omitempty"` // stable machine-readable condition code
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

// DrainCycle records one General drain cycle: actionable wakes claimed,
// routine count, fleet peek, and guidance.
type DrainCycle struct {
	ClaimID      string   `json:"claim_id"`
	Consumer     string   `json:"consumer"`
	Actionable   []string `json:"actionable,omitempty"`
	RoutineCount int      `json:"routine_count"`
	Reclaimed    int      `json:"reclaimed,omitempty"`
	InFlight     int      `json:"in_flight,omitempty"`
	Dead         int      `json:"dead,omitempty"`
	Guidance     []string `json:"guidance,omitempty"`
	State        string   `json:"state"` // clean | actionable
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
	Message          string           `json:"message"`
	Noop             bool             `json:"noop"`
	Injection        *ReportInjection `json:"injection,omitempty"`
	EnqueueTimestamp int64            `json:"enqueue_timestamp,omitempty"`
	ReceiptTimestamp int64            `json:"receipt_timestamp,omitempty"`
	WatcherIdentity  string           `json:"watcher_identity,omitempty"`
}

// InboxReceiveResult carries the loaded envelope payload from inbox receive.
type InboxReceiveResult struct {
	MessageID      string `json:"message_id"`
	SenderIdentity string `json:"sender_identity"`
	Payload        string `json:"payload"`
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
	Lock            string                   `json:"lock"`
	Watcher         string                   `json:"watcher"`
	BootstrapOK     bool                     `json:"bootstrap_ok"`
	FleetSyncOK     bool                     `json:"fleet_sync_ok"`
	RuntimeIdentity *RuntimeIdentityContract `json:"runtime_identity,omitempty"`
	Message         string                   `json:"message"`
}

// RuntimeIdentityContract is the complete runtime identity and skew payload.
type RuntimeIdentityContract struct {
	ProtocolVersion   int                          `json:"protocol_version"`
	RunningExecutable ExecutableIdentityContract   `json:"running_executable"`
	PATHExecutable    ExecutableIdentityContract   `json:"path_executable"`
	Build             BuildProvenanceContract      `json:"build"`
	SourceCheckouts   []SourceCheckoutContract     `json:"source_checkouts,omitempty"`
	Watcher           *WatcherRuntimeContract      `json:"watcher,omitempty"`
	Captains          []CaptainRuntimeContract     `json:"captains,omitempty"`
	Integrations      []IntegrationRuntimeContract `json:"integrations,omitempty"`
	Skew              []RuntimeSkewContract        `json:"skew,omitempty"`
}

type ExecutableIdentityContract struct {
	Path   string `json:"path,omitempty"`
	Digest string `json:"digest,omitempty"`
	Error  string `json:"error,omitempty"`
}

type BuildProvenanceContract struct {
	CLIVersion    string `json:"cli_version,omitempty"`
	ModulePath    string `json:"module_path,omitempty"`
	ModuleVersion string `json:"module_version,omitempty"`
	VCSRevision   string `json:"vcs_revision,omitempty"`
	VCSTime       string `json:"vcs_time,omitempty"`
	VCSModified   bool   `json:"vcs_modified"`
	Available     bool   `json:"available"`
}

type SourceCheckoutContract struct {
	Path     string `json:"path"`
	Revision string `json:"revision,omitempty"`
	Dirty    bool   `json:"dirty"`
	Error    string `json:"error,omitempty"`
}

type WatcherRuntimeContract struct {
	Component        string `json:"component"`
	Home             string `json:"home,omitempty"`
	Executable       string `json:"executable,omitempty"`
	ExecutableDigest string `json:"executable_digest,omitempty"`
	BuildVersion     string `json:"build_version,omitempty"`
	ProtocolVersion  int    `json:"protocol_version,omitempty"`
	CommitSHA        string `json:"commit_sha,omitempty"`
	Running          bool   `json:"running"`
}

type CaptainRuntimeContract struct {
	ID             string                  `json:"id"`
	Home           string                  `json:"home,omitempty"`
	SourceCheckout *SourceCheckoutContract `json:"source_checkout,omitempty"`
	Watcher        *WatcherRuntimeContract `json:"watcher,omitempty"`
}

type IntegrationRuntimeContract struct {
	Harness        string `json:"harness"`
	Scope          string `json:"scope"`
	State          string `json:"state"`
	Version        string `json:"version,omitempty"`
	ManifestPath   string `json:"manifest_path,omitempty"`
	ManifestSchema string `json:"manifest_schema,omitempty"`
	ContentDigest  string `json:"content_digest,omitempty"`
	Drifted        bool   `json:"drifted"`
	Message        string `json:"message,omitempty"`
	Remediation    string `json:"remediation,omitempty"`
}

// RuntimeSkewContract is one typed skew finding in contract output.
type RuntimeSkewContract struct {
	Classification string `json:"classification"`
	Component      string `json:"component"`
	Detail         string `json:"detail,omitempty"`
	Remediation    string `json:"remediation"`
}

// TaskEntry is one row in a task list.
type TaskEntry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	Project string `json:"project,omitempty"`
	Status  string `json:"status,omitempty"`
}

// TaskProjectionRow is one task's .meta/.status projection reconciliation
// outcome. Generation and Revision pin the canonical record the projections
// were derived from: reconciliation never changes them.
type TaskProjectionRow struct {
	TaskID     string `json:"task_id"`
	Generation string `json:"generation"`
	Revision   uint64 `json:"revision"`
	Meta       string `json:"meta"`
	Status     string `json:"status"`
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

// ReportInjection records the outcome of one material report injection attempt.
type ReportInjection struct {
	Outcome string `json:"outcome"` // injected | unsafe | afk | endpoint-dead | backend-failed
	Verdict string `json:"verdict,omitempty"`
	Target  string `json:"target,omitempty"`
	EventID uint64 `json:"event_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

// WatchStatus reports the bounded watcher health and status.
// Never enters foreground daemon mode — pure stateless read of beat/identity/wake state.
type WatchStatus struct {
	WatchID     string          `json:"watch_id"`
	State       string          `json:"state"` // healthy | stale | absent
	BeatAge     string          `json:"beat_age"`
	PID         int             `json:"pid"`
	Identity    string          `json:"identity,omitempty"`
	QueuedWakes int             `json:"queued_wakes"`
	MaterialAge string          `json:"material_age,omitempty"` // age of oldest material wake
	GuardState  string          `json:"guard_state"`            // healthy | unhealthy | indeterminate
	Lease       *WatchLeaseInfo `json:"lease,omitempty"`
	Diagnostics []string        `json:"diagnostics,omitempty"`
}

// DecisionHoldInfo is one row in a decision-hold list.
type DecisionHoldInfo struct {
	DecisionKey string `json:"decision_key"`
	Reason      string `json:"reason,omitempty"`
}
