package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetAndGet(t *testing.T) {
	tmp := t.TempDir()

	// Set a value
	if err := Set(tmp, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}

	// Read it back
	val, err := Get(tmp, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "tmux" {
		t.Errorf("Get() = %q, want %q", val, "tmux")
	}

	// File should exist with the value
	data, err := os.ReadFile(filepath.Join(ConfigDir(tmp), "backend"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tmux\n" {
		t.Errorf("file content = %q, want %q", string(data), "tmux\n")
	}
}

func TestGetNotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := Get(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key, got nil")
	}
}

func TestConfigRejectsUnknownKeysBeforeFilesystemAccess(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"../../escaped", "base.json"} {
		t.Run(key, func(t *testing.T) {
			if err := Set(home, key, "pwned"); err == nil {
				t.Errorf("Set(%q) succeeded; want unknown-key refusal", key)
			}
			if _, err := Get(home, key); err == nil {
				t.Errorf("Get(%q) succeeded; want unknown-key refusal", key)
			}
		})
	}

	outside := filepath.Join(ConfigDir(home), "../../escaped")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("unknown-key write escaped config directory at %s: %v", outside, err)
	}
	if _, err := os.Stat(filepath.Join(ConfigDir(home), "base.json")); !os.IsNotExist(err) {
		t.Fatalf("unknown-key write created base.json: %v", err)
	}
	if _, err := os.Stat(ConfigDir(home)); !os.IsNotExist(err) {
		t.Fatalf("unknown-key access created config directory: %v", err)
	}
}

func TestSetOverwrites(t *testing.T) {
	tmp := t.TempDir()

	if err := Set(tmp, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}
	if err := Set(tmp, "backend", "docker"); err != nil {
		t.Fatal(err)
	}

	val, err := Get(tmp, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "docker" {
		t.Errorf("Get() = %q, want %q", val, "docker")
	}
}

func TestKnownKeys(t *testing.T) {
	known := KnownKeys
	expected := []string{"backend", "parent-home", "soldier-harness", "captain-harness", "model", "model-allowlist", "wake-delivery-mode", "general-pane", "afk-digest-window", "seat-silent-after", "seat-repeat-nudge", "seat-repeat-end", "install-root"}
	if len(known) != len(expected) {
		t.Errorf("KnownKeys length = %d, want %d", len(known), len(expected))
	}
	for i, key := range expected {
		if i < len(known) && known[i] != key {
			t.Errorf("KnownKeys[%d] = %q, want %q", i, known[i], key)
		}
	}
}

func TestSeatLivenessConfig(t *testing.T) {
	rows := []struct {
		name    string
		sets    [][2]string
		want    SeatLiveness
		wantErr string
	}{
		{"unset-keys-take-the-starting-values", nil, SeatLiveness{SilentAfter: 5 * time.Minute, RepeatNudge: 5, RepeatEnd: 8}, ""},
		{"set-keys-override", [][2]string{{"seat-silent-after", "90s"}, {"seat-repeat-end", "12"}, {"seat-repeat-nudge", "3"}}, SeatLiveness{SilentAfter: 90 * time.Second, RepeatNudge: 3, RepeatEnd: 12}, ""},
		{"silence-must-be-a-positive-duration", [][2]string{{"seat-silent-after", "0s"}}, SeatLiveness{}, `config seat-silent-after "0s": want a positive duration such as 5m`},
		{"silence-must-parse", [][2]string{{"seat-silent-after", "soon"}}, SeatLiveness{}, `config seat-silent-after "soon": want a positive duration such as 5m`},
		{"nudge-must-be-at-least-two", [][2]string{{"seat-repeat-nudge", "1"}}, SeatLiveness{}, `config seat-repeat-nudge "1": want an integer of at least 2`},
		{"end-must-follow-the-default-nudge", [][2]string{{"seat-repeat-end", "5"}}, SeatLiveness{}, `config seat-repeat-end 5: want an integer greater than seat-repeat-nudge 5`},
		{"nudge-must-precede-the-set-end", [][2]string{{"seat-repeat-end", "9"}, {"seat-repeat-nudge", "9"}}, SeatLiveness{}, `config seat-repeat-end 9: want an integer greater than seat-repeat-nudge 9`},
		{"deleted-key-is-unknown", [][2]string{{"afk-max-defer", "5m"}}, SeatLiveness{}, `unknown config key "afk-max-defer"`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			h := t.TempDir()
			for _, kv := range row.sets {
				if err := Set(h, kv[0], kv[1]); err != nil {
					if row.wantErr == "" || !strings.Contains(err.Error(), row.wantErr) {
						t.Fatalf("Set %s %q error = %v, want %q", kv[0], kv[1], err, row.wantErr)
					}
					return
				}
			}
			if row.wantErr != "" {
				t.Fatalf("Set accepted %v, want error containing %q", row.sets, row.wantErr)
			}
			got, err := ReadSeatLiveness(h)
			if err != nil || got != row.want {
				t.Fatalf("ReadSeatLiveness = %+v, %v, want %+v", got, err, row.want)
			}
		})
	}
}

func TestReadSeatLivenessRejectsAHandEditedValue(t *testing.T) {
	h := t.TempDir()
	if err := os.MkdirAll(ConfigDir(h), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ConfigDir(h), "seat-repeat-nudge"), []byte("many\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSeatLiveness(h); err == nil || !strings.Contains(err.Error(), `config seat-repeat-nudge "many": want an integer of at least 2`) {
		t.Fatalf("ReadSeatLiveness error = %v, want the key and its valid range", err)
	}
	// A bad file stays repairable one key at a time.
	if err := Set(h, "seat-repeat-end", "9"); err != nil {
		t.Fatalf("Set seat-repeat-end beside a bad nudge: %v", err)
	}
	if err := Set(h, "seat-repeat-nudge", "4"); err != nil {
		t.Fatalf("Set seat-repeat-nudge: %v", err)
	}
	if _, err := ReadSeatLiveness(h); err != nil {
		t.Fatalf("ReadSeatLiveness after repair: %v", err)
	}
}
