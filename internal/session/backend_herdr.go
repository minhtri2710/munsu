package session

import (
	"fmt"
	"os/exec"
	"strings"
)

// HerdrBackend implements Backend using the herdr CLI.
// Experimental: herdr is a terminal workspace manager.
type HerdrBackend struct{}

func (h *HerdrBackend) herdr(args ...string) (string, error) {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr: not found on PATH: %w", err)
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("herdr %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (h *HerdrBackend) NewWindow(session, name string) (string, error) {
	return h.herdr("tab", "create", "--workspace", session, "--label", name)
}

func (h *HerdrBackend) SendKeys(windowID, text string) error {
	if _, err := h.herdr("pane", "send-text", windowID, text); err != nil {
		return err
	}
	_, err := h.herdr("pane", "send-keys", windowID, "Enter")
	return err
}

func (h *HerdrBackend) Capture(windowID string, lines int) (string, error) {
	return h.herdr("pane", "read", windowID, "--source", "recent", "--lines", fmt.Sprintf("%d", lines))
}

func (h *HerdrBackend) Alive(windowID string) bool {
	_, err := h.herdr("pane", "get", windowID)
	return err == nil
}

func (h *HerdrBackend) Teardown(windowID string) error {
	_, err := h.herdr("pane", "close", windowID)
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}
