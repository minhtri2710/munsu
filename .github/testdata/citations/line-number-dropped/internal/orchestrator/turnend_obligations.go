package orchestrator

// ObligationKind names a turn-end obligation.
type ObligationKind string

const (
	// ReportRelay: a material status change must be relayed.
	ReportRelay ObligationKind = "report-relay"
)

// DefaultObligation is the kind a fresh task starts with.
var DefaultObligation = ReportRelay
