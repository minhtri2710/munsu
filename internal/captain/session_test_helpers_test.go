package captain

import (
	"github.com/minhtri2710/munsu/internal/session"
)

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
func setTestBackend(be session.Backend) func() {
	orig := backendForTask
	backendForTask = func(string, map[string]string) (session.Backend, string, error) { return be, "test", nil }
	return func() { backendForTask = orig }
}
