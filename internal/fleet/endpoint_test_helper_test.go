package fleet

// endpointStatusFromState converts a coarse observation state into the typed
// orthogonal EndpointObservation used by test fixtures (BEO-16/P1a). It lets
// tests express a probe result concisely while the software under test reads
// the orthogonal axes.
func endpointStatusFromState(s EndpointObservationState) EndpointStatus {
	switch s {
	case EndpointAlive:
		return EndpointStatus{Lifecycle: LifecycleAlive, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceProbe}
	case EndpointStarting:
		return EndpointStatus{Lifecycle: LifecycleStarting, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceProbe}
	case EndpointDead:
		return EndpointStatus{Lifecycle: LifecycleDead, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceProbe}
	case EndpointUnresponsive:
		return EndpointStatus{Lifecycle: LifecycleUnknown, Responsiveness: Unresponsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe}
	case EndpointStaleIdentity:
		return EndpointStatus{Lifecycle: LifecycleUnknown, Responsiveness: Responsive, Freshness: FreshnessStale, Activity: ActivityUnknown, Source: SourceProbe}
	case EndpointUnresolved:
		return EndpointStatus{Lifecycle: LifecycleUnknown, Responsiveness: ResponsivenessUnknown, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceDerived}
	default:
		return EndpointStatus{Lifecycle: LifecycleUnknown, Responsiveness: Responsive, Freshness: FreshnessUnknown, Activity: ActivityUnknown, Source: SourceProbe}
	}
}
