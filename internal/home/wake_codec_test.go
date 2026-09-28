package home

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWakePayloadRoundTripsThroughEveryDrainPath(t *testing.T) {
	hazards := []struct {
		name    string
		payload string
	}{
		{name: "tab", payload: "before\tafter"},
		{name: "line feed", payload: "before\nafter"},
		{name: "carriage return", payload: "before\rafter"},
		{name: "escape characters", payload: `before\\n\\t\\r\\after`},
		{name: "over scanner limit", payload: strings.Repeat("x", 64*1024+1)},
	}
	paths := []struct {
		name  string
		drain func(string) (string, error)
	}{
		{name: "all", drain: drainWakeDirect},
		{name: "kind", drain: drainWakeOfKind},
		{name: "excluding kind", drain: drainWakeExcludingKind},
		{name: "claim", drain: drainWakeClaim},
		{name: "reclaim", drain: drainWakeReclaim},
	}

	for _, hazard := range hazards {
		for _, path := range paths {
			t.Run(hazard.name+"/"+path.name, func(t *testing.T) {
				home := t.TempDir()
				if err := EnqueueWake(home, "signal", "key", hazard.payload); err != nil {
					t.Fatal(err)
				}
				read, err := readWakeQueue(home)
				if err != nil {
					t.Fatal(err)
				}
				if len(read) != 1 || read[0].Payload != hazard.payload {
					t.Fatalf("read payload = %q, want exact %q", read[0].Payload, hazard.payload)
				}
				got, err := path.drain(home)
				if err != nil {
					t.Fatal(err)
				}
				if got != hazard.payload {
					t.Fatalf("payload = %q, want exact %q", got, hazard.payload)
				}
			})
		}
	}
}

func drainWakeDirect(home string) (string, error) {
	records, err := DrainWakes(home)
	if err != nil {
		return "", err
	}
	if len(records) != 1 {
		return "", gotWakeCountError(len(records))
	}
	return records[0].Payload, nil
}

func drainWakeOfKind(home string) (string, error) {
	records, err := DrainWakesOfKind(home, "signal")
	if err != nil {
		return "", err
	}
	if len(records) != 1 {
		return "", gotWakeCountError(len(records))
	}
	return records[0].Payload, nil
}

func drainWakeExcludingKind(home string) (string, error) {
	records, err := DrainWakesExcludingKind(home, ProcessEventWakeKind)
	if err != nil {
		return "", err
	}
	if len(records) != 1 {
		return "", gotWakeCountError(len(records))
	}
	return records[0].Payload, nil
}

func drainWakeClaim(home string) (string, error) {
	result, err := ClaimWakes(home, "consumer", 60, 1)
	if err != nil {
		return "", err
	}
	if len(result.Wakes) != 1 {
		return "", gotWakeCountError(len(result.Wakes))
	}
	return result.Wakes[0].Payload, nil
}

func drainWakeReclaim(home string) (string, error) {
	at := time.Unix(1000, 0)
	result, err := claimWakesAt(home, "consumer", 0, 1, func() time.Time { return at })
	if err != nil {
		return "", err
	}
	if len(result.Wakes) != 1 {
		return "", gotWakeCountError(len(result.Wakes))
	}
	if _, err := reclaimExpiredLeasesAt(home, func() time.Time { return at.Add(time.Second) }); err != nil {
		return "", err
	}
	return drainWakeDirect(home)
}

func gotWakeCountError(got int) error {
	return fmt.Errorf("got %d wake records, want one", got)
}

func TestEnqueueWakeRejectsSeparatorInIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		key  string
	}{
		{name: "tab in kind", kind: "sig\tnal", key: "key"},
		{name: "line feed in kind", kind: "sig\nnal", key: "key"},
		{name: "carriage return in kind", kind: "sig\rnal", key: "key"},
		{name: "tab in key", kind: "signal", key: "ke\ty"},
		{name: "line feed in key", kind: "signal", key: "ke\ny"},
		{name: "carriage return in key", kind: "signal", key: "ke\ry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := EnqueueWake(home, tc.kind, tc.key, "payload"); err == nil {
				t.Fatal("EnqueueWake accepted a separator in an identifier")
			}
			if _, err := os.Stat(WakeQueuePath(home)); !os.IsNotExist(err) {
				t.Fatalf("refused enqueue touched queue: %v", err)
			}
		})
	}
}
