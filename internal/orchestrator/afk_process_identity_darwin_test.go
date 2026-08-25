//go:build darwin

package orchestrator

import "testing"

func TestGuardBurnDownDarwinProcessArgsRejectsTruncatedRaw(t *testing.T) {
	if _, err := parseDarwinProcessArgs([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("parseDarwinProcessArgs accepted truncated raw data")
	}
}

func TestGuardBurnDownDarwinProcessArgsRejectsMissingExecutable(t *testing.T) {
	raw := []byte{1, 2, 3, 4, 0}
	if _, err := parseDarwinProcessArgs(raw); err == nil {
		t.Fatal("parseDarwinProcessArgs accepted missing executable")
	}
}
