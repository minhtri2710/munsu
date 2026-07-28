package fleet

type testLaunchEndpoint struct{}

func (testLaunchEndpoint) Launch(string, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{Backend: "test", Window: "test-window"}, nil
}

func (testLaunchEndpoint) Cleanup(string, LaunchResult) error { return nil }

type testProbeEndpoint struct {
	result  CaptainProbeResult
	results []CaptainProbeResult
	err     error
	calls   int
}

func (p *testProbeEndpoint) Probe(string, map[string]string) (CaptainProbeResult, error) {
	result := p.result
	if p.calls < len(p.results) {
		result = p.results[p.calls]
	}
	p.calls++
	return result, p.err
}

type testNudgeEndpoint struct {
	result NudgeResult
	err    error
	calls  int
}

func (n *testNudgeEndpoint) Nudge(string, map[string]string, string) (NudgeResult, error) {
	n.calls++
	return n.result, n.err
}

type testRetireEndpoint struct {
	err   error
	calls int
}

func (r *testRetireEndpoint) Retire(string, map[string]string) error { r.calls++; return r.err }
