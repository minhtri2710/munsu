package captain

type ProbeResult struct {
	PaneAlive  bool
	AgentAlive bool
}

type ProbeEndpoint interface {
	Probe(home string, meta map[string]string) (ProbeResult, error)
}

type RecoverCapabilities struct {
	Integration IntegrationPort
	Launch      LaunchEndpoint
	Probe       ProbeEndpoint
	Nudge       NudgeEndpoint
}
