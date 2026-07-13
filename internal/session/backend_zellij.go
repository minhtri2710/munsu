package session

// ZellijBackend implements Backend using the zellij CLI.
// EXPERIMENTAL: not yet implemented.
type ZellijBackend struct{}

func (z *ZellijBackend) NewWindow(session, name string) (string, error) {
	return "", ErrNotImplemented
}

func (z *ZellijBackend) SendKeys(windowID, text string) error {
	return ErrNotImplemented
}

func (z *ZellijBackend) Capture(windowID string, lines int) (string, error) {
	return "", ErrNotImplemented
}

func (z *ZellijBackend) Alive(windowID string) bool {
	return false
}

func (z *ZellijBackend) Teardown(windowID string) error {
	return ErrNotImplemented
}
