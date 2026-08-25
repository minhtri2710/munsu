//go:build darwin

package orchestrator

import "testing"

func TestGuardBurnDownDarwinProcessArgsRejectsTruncatedRaw(t *testing.T) {
	_, err := parseDarwinProcessArgs([]byte{1, 2, 3, 4})
	if err == nil {
		t.Fatal("parseDarwinProcessArgs accepted truncated raw data")
	}
	t.Logf("parseDarwinProcessArgs truncated-raw refusal: %v", err)
}

func TestGuardBurnDownDarwinProcessArgsRejectsMissingExecutable(t *testing.T) {
	raw := []byte{1, 2, 3, 4, 0}
	_, err := parseDarwinProcessArgs(raw)
	if err == nil {
		t.Fatal("parseDarwinProcessArgs accepted missing executable")
	}
	t.Logf("parseDarwinProcessArgs missing-executable refusal: %v", err)
}
