package orchestrator

import (
	mhome "github.com/minhtri2710/munsu/internal/home"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWakeQueueLines writes tab-separated wake queue entries for testing.
func writeWakeQueueLines(t *testing.T, homeDir string, lines []string) {
	t.Helper()
	path := mhome.WakeQueuePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestClaimWakesLogsLatencyObservation(t *testing.T) {
	home := t.TempDir()
	writeWakeQueueLines(t, home, []string{"1700000000\t1\tsignal\tkey1\tpayload1"})
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	result, claimErr := ClaimWakes(home, "test-consumer", 60, 1)
	_ = writer.Close()
	os.Stderr = previous
	if claimErr != nil {
		t.Fatalf("ClaimWakes: %v", claimErr)
	}
	if len(result.Wakes) != 1 {
		t.Fatalf("claimed %d wakes, want 1", len(result.Wakes))
	}
	output, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "wake-to-claim=") {
		t.Fatalf("claim observation output %q omits wake-to-claim latency", output)
	}
}
