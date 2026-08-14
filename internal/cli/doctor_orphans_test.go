package cli

import "testing"

func TestDoctorRegistersOrphanFlag(t *testing.T) {
	flag := newDoctorCmd().Flags().Lookup("orphans")
	if flag == nil {
		t.Fatal("doctor must expose --orphans")
	}
	if flag.Value.String() != "false" {
		t.Fatalf("the orphan scan must be opt-in, got default %q", flag.Value.String())
	}
}
