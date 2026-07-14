package session

import "fmt"

// CmuxBackend implements Backend using the cmux CLI.
// EXPERIMENTAL: not yet implemented.
type CmuxBackend struct{}

func (c *CmuxBackend) NewWindow(session, name string) (string, error) {
	return "", fmt.Errorf("cmux backend not yet implemented — see docs/port-mapping.md")
}

func (c *CmuxBackend) SendKeys(windowID, text string) error {
	return fmt.Errorf("cmux backend not yet implemented — see docs/port-mapping.md")
}

func (c *CmuxBackend) Capture(windowID string, lines int) (string, error) {
	return "", fmt.Errorf("cmux backend not yet implemented — see docs/port-mapping.md")
}

func (c *CmuxBackend) Alive(windowID string) bool {
	return false
}

func (c *CmuxBackend) Teardown(windowID string) error {
	return fmt.Errorf("cmux backend not yet implemented — see docs/port-mapping.md")
}
