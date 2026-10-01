//go:build darwin

package fleet

import (
	"slices"
	"testing"
)

func TestGuardBurnDownProcArgsRejectsTruncatedRaw(t *testing.T) {
	_, _, err := parseProcArgs([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("parseProcArgs accepted truncated raw data")
	}
	t.Logf("parseProcArgs truncated-raw refusal: %v", err)
}

func TestGuardBurnDownProcArgsRejectsMissingExecutable(t *testing.T) {
	_, _, err := parseProcArgs([]byte{1, 0, 0, 0, 0})
	if err == nil {
		t.Fatal("parseProcArgs accepted missing executable")
	}
	t.Logf("parseProcArgs missing-executable refusal: %v", err)
}

func TestProcArgsParsesExecutableAndEnvironment(t *testing.T) {
	raw := append([]byte{1, 0, 0, 0}, "/bin/sh\x00\x00\x00sh\x00MUNSU_ROLE=soldier\x00HOME=/h\x00"...)
	executable, environment, err := parseProcArgs(raw)
	if err != nil {
		t.Fatalf("parseProcArgs: %v", err)
	}
	if executable != "/bin/sh" {
		t.Fatalf("executable = %q, want /bin/sh", executable)
	}
	if want := []string{"MUNSU_ROLE=soldier", "HOME=/h"}; !slices.Equal(environment, want) {
		t.Fatalf("environment = %q, want %q", environment, want)
	}
}
