package fleet

type CaptainProbeResult struct {
	PaneAlive      bool
	AgentAlive     bool
	ReadyForPrompt bool
	AgentStatus    string
}

type ProbeEndpoint interface {
	Probe(home string, meta map[string]string) (CaptainProbeResult, error)
}

type RecoverCapabilities struct {
	Continuity  CaptainContinuityPort
	Watcher     CaptainWatcherPort
	Integration IntegrationPort
	Launch      LaunchEndpoint
	Probe       ProbeEndpoint
	Nudge       NudgeEndpoint
}
