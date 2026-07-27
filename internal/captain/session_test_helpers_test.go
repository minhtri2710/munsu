package captain

import "github.com/minhtri2710/munsu/internal/session"

type fakeSubmitPromptBackend struct {
	alive, acknowledged bool
	lastText            string
}

func (f *fakeSubmitPromptBackend) Alive(string) bool { return f.alive }
func (f *fakeSubmitPromptBackend) NewWindow(string, string) (string, error) {
	return "test-window", nil
}
func (f *fakeSubmitPromptBackend) SendKeys(string, string) error       { return nil }
func (f *fakeSubmitPromptBackend) Capture(string, int) (string, error) { return "", nil }
func (f *fakeSubmitPromptBackend) Teardown(string) error               { return nil }
func (f *fakeSubmitPromptBackend) AgentPrompt(_ string, text string) session.PromptResult {
	f.lastText = text
	if f.acknowledged {
		return session.PromptResult{Status: session.PromptSubmitted}
	}
	return session.PromptResult{Status: session.PromptStalled}
}

type testLaunchEndpoint struct{}

func (testLaunchEndpoint) Launch(home string, req LaunchRequest) (LaunchResult, error) {
	bk, name, err := newSessionBackend(home)
	if err != nil {
		return LaunchResult{}, err
	}
	window, err := bk.NewWindow(req.ContainerLabel, req.WindowName)
	if err != nil {
		return LaunchResult{}, err
	}
	if err := bk.SendKeys(window, req.Command); err != nil {
		_ = bk.Teardown(window)
		return LaunchResult{}, err
	}
	meta := map[string]string{}
	if extras, ok := bk.(session.BackendMetaExtras); ok {
		for k, v := range extras.MetaExtras() {
			meta[k] = v
		}
	}
	return LaunchResult{Backend: name, Window: window, Meta: meta}, nil
}
func (testLaunchEndpoint) Cleanup(home string, result LaunchResult) error {
	bk, _, err := newSessionBackend(home)
	if err != nil {
		return err
	}
	return bk.Teardown(result.Window)
}

func setTestBackend(be session.Backend) func() {
	orig := backendForTask
	backendForTask = func(string, map[string]string) (session.Backend, string, error) { return be, "test", nil }
	return func() { backendForTask = orig }
}

type testProbeEndpoint struct {
	result  ProbeResult
	results []ProbeResult
	err     error
	calls   int
}

func (p *testProbeEndpoint) Probe(string, map[string]string) (ProbeResult, error) {
	result := p.result
	if p.calls < len(p.results) {
		result = p.results[p.calls]
	}
	p.calls++
	return result, p.err
}
