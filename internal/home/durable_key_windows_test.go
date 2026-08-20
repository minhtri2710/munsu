//go:build windows

package home

import "testing"

func TestWindowsDurableKeyEscapes(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"task-1", "task-1"},
		{"captain:test", "captain%3Atest"},
		{"captain:domain-alpha", "captain%3Adomain-alpha"},
		{"captain:my cap", "captain%3Amy%20cap"},
		{"cap:t.", "cap%3At%2E"},
		{"a%3Ab", "a%253Ab"},
	}
	for _, c := range cases {
		got, err := DurableKey(c.id)
		if err != nil {
			t.Errorf("DurableKey(%q): %v", c.id, err)
			continue
		}
		if got != c.want {
			t.Errorf("DurableKey(%q) = %q, want %q", c.id, got, c.want)
		}
		back, err := ReverseDurableKey(got)
		if err != nil {
			t.Errorf("ReverseDurableKey(%q): %v", got, err)
			continue
		}
		if back != c.id {
			t.Errorf("round trip %q -> %q -> %q", c.id, got, back)
		}
	}
}

func TestWindowsDurableKeyIsInjective(t *testing.T) {
	// Different logical keys must never map to the same stem.
	ids := []string{"captain:foo", "captain_foo", "captain%3Afoo", "a:b", "ab", "a%2Ab"}
	seen := map[string]string{}
	for _, id := range ids {
		stem, err := DurableKey(id)
		if err != nil {
			t.Fatalf("DurableKey(%q): %v", id, err)
		}
		if other, dup := seen[stem]; dup {
			t.Errorf("collision: %q and %q both map to %q", other, id, stem)
		}
		seen[stem] = id
	}
}

func TestWindowsDurableKeyRejectsReservedNames(t *testing.T) {
	for _, id := range []string{"CON", "con", "prn", "AUX", "NUL", "COM1", "com9", "LPT1", "lpt8"} {
		if _, err := DurableKey(id); err == nil {
			t.Errorf("DurableKey(%q) succeeded, want reserved-name error", id)
		}
	}
	// Captain keys are never reserved device names even with a reserved suffix.
	if _, err := DurableKey("captain:con"); err != nil {
		t.Errorf("DurableKey(captain:con) errored: %v", err)
	}
}

func TestWindowsReverseRejectsMalformed(t *testing.T) {
	for _, stem := range []string{"%", "a%2", "a%GGb", "a:b"} {
		if _, err := ReverseDurableKey(stem); err == nil {
			t.Errorf("ReverseDurableKey(%q) succeeded, want error", stem)
		}
	}
}

func TestWindowsReverseRejectsNonCanonical(t *testing.T) {
	for _, stem := range []string{"a%2b", "%41", "%2E%2E", "CON"} {
		if _, err := ReverseDurableKey(stem); err == nil {
			t.Errorf("ReverseDurableKey(%q) succeeded, want error", stem)
		}
	}
}

func TestWindowsReservedName(t *testing.T) {
	for _, s := range []string{"CON", "con", "PRN", "Aux", "NUL", "COM1", "com9", "LPT1", "lpt8"} {
		if !WindowsReservedName(s) {
			t.Errorf("WindowsReservedName(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"captain:con", "NOTRESERVED", "COM", "COM0", "LPT10", "cons", "CON.txt"} {
		if WindowsReservedName(s) {
			t.Errorf("WindowsReservedName(%q) = true, want false", s)
		}
	}
}
