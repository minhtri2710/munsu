package session

// CmuxBackend implements Backend using the cmux CLI.
// EXPERIMENTAL: not yet implemented.
type CmuxBackend struct{}

func (c *CmuxBackend) NewWindow(session, name string) (string, error) {
	return "", ErrNotImplemented
}

func (c *CmuxBackend) SendKeys(windowID, text string) error {
	return ErrNotImplemented
}

func (c *CmuxBackend) Capture(windowID string, lines int) (string, error) {
	return "", ErrNotImplemented
}

func (c *CmuxBackend) Alive(windowID string) bool {
	return false
}

func (c *CmuxBackend) Teardown(windowID string) error {
	return ErrNotImplemented
}
