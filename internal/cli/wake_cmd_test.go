package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestWakeClaimOutputsWakeContent(t *testing.T) {
	const (
		kind    = "signal"
		key     = "task-17"
		payload = "failed: CI is red"
	)

	for _, output := range []string{OutputTOON, OutputJSON} {
		t.Run(output, func(t *testing.T) {
			homeDir := t.TempDir()
			if _, err := home.Init(homeDir); err != nil {
				t.Fatal(err)
			}
			if err := home.EnqueueWake(homeDir, kind, key, payload); err != nil {
				t.Fatal(err)
			}

			out, err := runRoot(t, "wake", "claim", "--consumer", "general-pilot", "--home", homeDir, "--output", output)
			if err != nil {
				t.Fatalf("wake claim: %v\n%s", err, out)
			}

			if output == OutputJSON {
				var response struct {
					Data struct {
						Wakes []struct {
							WakeID  string `json:"wake_id"`
							Kind    string `json:"kind"`
							Key     string `json:"key"`
							Payload string `json:"payload"`
						} `json:"wakes"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(out), &response); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, out)
				}
				if len(response.Data.Wakes) != 1 || response.Data.Wakes[0].WakeID == "" || response.Data.Wakes[0].Kind != kind || response.Data.Wakes[0].Key != key || response.Data.Wakes[0].Payload != payload {
					t.Fatalf("claimed wake content = %+v, want one wake ID and kind %q, key %q, payload %q\n%s", response.Data.Wakes, kind, key, payload, out)
				}
				return
			}

			for _, content := range []string{kind, key, payload} {
				if !strings.Contains(out, content) {
					t.Errorf("wake claim output does not include %q:\n%s", content, out)
				}
			}
		})
	}
}
