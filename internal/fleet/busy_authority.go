package fleet

// BusyReading is Fleet's single answer to "is this endpoint busy / idle /
// unknown / blocked / dead" (ADR-0021 Decision 1). It is derived from the
// typed axes already carried on a Fleet-authorized EndpointStatus, primarily
// the Activity axis, and is a dispatch/attention input only — never Task
// lifecycle truth (that lives in taskauthority).
//
// P1a invariants preserved verbatim: unknown != idle != dead; blocked is a
// distinct diagnostic attention state, never folded into idle or busy and
// never a lifecycle conclusion; only a Fleet-authorized Absent() overrides a
// busy reading; event-derived observations (SourceEvent) contribute busy/idle
// hints only and can never yield the dead answer.
type BusyReading uint8

const (
	BusyReadingInvalid BusyReading = iota
	// BusyReadingHeld: the endpoint is busy; hold, do not dispatch a competing turn.
	BusyReadingHeld
	BusyReadingIdle
	// BusyReadingUnknown is its own answer and is never treated as idle.
	BusyReadingUnknown
	// BusyReadingBlocked is a diagnostic attention state, not a lifecycle conclusion.
	BusyReadingBlocked
	// BusyReadingDead is concluded only from a Fleet-authorized Absent() observation.
	BusyReadingDead
)

func (r BusyReading) String() string {
	switch r {
	case BusyReadingHeld:
		return "held"
	case BusyReadingIdle:
		return "idle"
	case BusyReadingUnknown:
		return "unknown"
	case BusyReadingBlocked:
		return "blocked"
	case BusyReadingDead:
		return "dead"
	default:
		return "invalid"
	}
}

func (r BusyReading) Valid() bool {
	return r >= BusyReadingHeld && r <= BusyReadingDead
}

// ReadBusy derives the busy reading of one exact bound endpoint from a
// Fleet-authorized observation. It consumes the observation; it does not
// authorize liveness itself (authorizeAbsence/authorizeLive do that upstream).
//
// Only endpoint death — a Fleet-authorized Absent() — overrides a busy
// reading. An adapter probe alone is never Absent(), and a SourceEvent
// observation is excluded by Absent()'s source check, so neither can reach
// the dead answer here. An invalid or unset Activity maps to unknown, never idle.
func ReadBusy(obs EndpointStatus) BusyReading {
	if obs.Absent() {
		return BusyReadingDead
	}
	switch obs.Activity {
	case ActivityBusy:
		return BusyReadingHeld
	case ActivityIdle:
		return BusyReadingIdle
	case ActivityBlocked:
		return BusyReadingBlocked
	case ActivityUnknown:
		return BusyReadingUnknown
	default:
		return BusyReadingUnknown
	}
}
