//go:build linux

package orchestrator

import "testing"

func TestGuardBurnDownLinuxProcessStatRejectsMalformedStat(t *testing.T) {
	if _, err := parseLinuxProcessStat([]byte("1 (munsu")); err == nil {
		t.Fatal("parseLinuxProcessStat accepted malformed stat")
	}
}

func TestGuardBurnDownLinuxProcessStatRejectsTruncatedStat(t *testing.T) {
	if _, err := parseLinuxProcessStat([]byte("1 (munsu) S 2 3 4")); err == nil {
		t.Fatal("parseLinuxProcessStat accepted truncated stat")
	}
}
