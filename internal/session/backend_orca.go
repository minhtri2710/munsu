package session

// OrcaBackend implements Backend using the Orca CLI.
// EXPERIMENTAL: not yet implemented.
type OrcaBackend struct{}

func (o *OrcaBackend) NewWindow(session, name string) (string, error) {
	return "", ErrNotImplemented
}

func (o *OrcaBackend) SendKeys(windowID, text string) error {
	return ErrNotImplemented
}

func (o *OrcaBackend) Capture(windowID string, lines int) (string, error) {
	return "", ErrNotImplemented
}

func (o *OrcaBackend) Alive(windowID string) bool {
	return false
}

func (o *OrcaBackend) Teardown(windowID string) error {
	return ErrNotImplemented
}
