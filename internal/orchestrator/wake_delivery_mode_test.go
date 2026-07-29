package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWakeDeliveryMode(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        WakeDeliveryMode
		wantErr     bool
	}{
		{name: "unset", want: WakeDeliveryNative},
		{name: "native", value: "native", want: WakeDeliveryNative},
		{name: "herdr", value: "herdr", want: WakeDeliveryHerdr},
		{name: "manual", value: "manual", want: WakeDeliveryManual},
		{name: "invalid", value: "other", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.value != "" {
				if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "config", "wake-delivery-mode"), []byte(tc.value), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ResolveWakeDeliveryMode(home)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("mode=%q err=%v", got, err)
			}
		})
	}
}
