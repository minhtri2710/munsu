package session

import "fmt"

// ZellijBackend implements Backend using the zellij CLI.
// EXPERIMENTAL: not yet implemented.
type ZellijBackend struct{}

func (z *ZellijBackend) NewWindow(session, name string) (string, error) {
	return "", fmt.Errorf("zellij backend not yet implemented — see docs/port-mapping.md")
}

func (z *ZellijBackend) SendKeys(windowID, text string) error {
	return fmt.Errorf("zellij backend not yet implemented — see docs/port-mapping.md")
}

func (z *ZellijBackend) Capture(windowID string, lines int) (string, error) {
	return "", fmt.Errorf("zellij backend not yet implemented — see docs/port-mapping.md")
}

func (z *ZellijBackend) Alive(windowID string) bool {
	return false
}

func (z *ZellijBackend) Teardown(windowID string) error {
	return fmt.Errorf("zellij backend not yet implemented — see docs/port-mapping.md")
}
