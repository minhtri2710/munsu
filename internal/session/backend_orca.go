package session

import "fmt"

// OrcaBackend implements Backend using the Orca CLI.
// EXPERIMENTAL: not yet implemented.
type OrcaBackend struct{}

func (o *OrcaBackend) NewWindow(session, name string) (string, error) {
	return "", fmt.Errorf("orca backend not yet implemented — see docs/port-mapping.md")
}

func (o *OrcaBackend) SendKeys(windowID, text string) error {
	return fmt.Errorf("orca backend not yet implemented — see docs/port-mapping.md")
}

func (o *OrcaBackend) Capture(windowID string, lines int) (string, error) {
	return "", fmt.Errorf("orca backend not yet implemented — see docs/port-mapping.md")
}

func (o *OrcaBackend) Alive(windowID string) bool {
	return false
}

func (o *OrcaBackend) Teardown(windowID string) error {
	return fmt.Errorf("orca backend not yet implemented — see docs/port-mapping.md")
}
