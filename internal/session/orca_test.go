package session

import (
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// OrcaBackend unit tests
// ---------------------------------------------------------------------------

// hasOrca reports whether orca is available on PATH.
func hasOrca() bool {
	_, err := orcaBin()
	return err == nil
}

func TestOrcaBin_Found(t *testing.T) {
	if !hasOrca() {
		t.Skip("orca not on PATH")
	}
	path, err := orcaBin()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("orcaBin() returned empty path")
	}
	if !strings.Contains(path, "orca") {
		t.Errorf("orcaBin() = %q, expected path containing 'orca'", path)
	}
}

func TestOrcaBin_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	_, err := orcaBin()
	if err == nil {
		t.Fatal("expected error when orca is not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelect_OrcaAvailable(t *testing.T) {
	bk, err := Select("orca")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*OrcaBackend); !ok {
		t.Errorf("Select('orca') returned %T, want *OrcaBackend", bk)
	}

}

func TestParseOrcaWindow(t *testing.T) {
	tests := []struct {
		handle              string
		wantContainerID     string
		wantTerminalID      string
	}{
		{"container:1|terminal:1", "container:1", "terminal:1"},
		{"ctn_abc|term_def", "ctn_abc", "term_def"},
		{"|terminal:1", "", "terminal:1"},
		{"bare", "", "bare"},
		{"", "", ""},
	}
	for _, tt := range tests {
		gotCtn, gotTerm := ParseOrcaWindow(tt.handle)
		if gotCtn != tt.wantContainerID || gotTerm != tt.wantTerminalID {
			t.Errorf("ParseOrcaWindow(%q) = (%q, %q), want (%q, %q)",
				tt.handle, gotCtn, gotTerm, tt.wantContainerID, tt.wantTerminalID)
		}
	}
}

func TestOrcaBackend_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	o := NewOrcaBackend()

	t.Run("NewWindow", func(t *testing.T) {
		_, err := o.NewWindow("s", "n")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("SendKeys", func(t *testing.T) {
		err := o.SendKeys("c|t", "text")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Capture", func(t *testing.T) {
		_, err := o.Capture("c|t", 10)
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Alive", func(t *testing.T) {
		if o.Alive("c|t") {
			t.Error("Alive returned true when orca is not on PATH")
		}
	})

	t.Run("Teardown", func(t *testing.T) {
		err := o.Teardown("c|t")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})
}

func TestOrcaBackend_Alive_InvalidHandle(t *testing.T) {
	o := NewOrcaBackend()
	if o.Alive("") {
		t.Error("Alive returned true for empty window handle")
	}
}

func TestOrcaBackend_SendKeys_InvalidHandle(t *testing.T) {
	o := NewOrcaBackend()
	err := o.SendKeys("", "text")
	if err == nil || !strings.Contains(err.Error(), "invalid orca window handle") {
		t.Errorf("expected invalid handle error, got: %v", err)
	}
}

func TestOrcaBackend_Teardown_InvalidHandle(t *testing.T) {
	o := NewOrcaBackend()
	err := o.Teardown("")
	if err == nil || !strings.Contains(err.Error(), "invalid orca window handle") {
		t.Errorf("expected invalid handle error, got: %v", err)
	}
}

func TestOrcaBackend_Capture_InvalidHandle(t *testing.T) {
	o := NewOrcaBackend()
	_, err := o.Capture("", 10)
	if err == nil || !strings.Contains(err.Error(), "invalid orca window handle") {
		t.Errorf("expected invalid handle error, got: %v", err)
	}
}

func TestSelect_OrcaUnknownBackend(t *testing.T) {
	_, err := Select("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !strings.Contains(err.Error(), "unknown session backend") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestOrcaBackend_SelectRoundTrip verifies that Select("orca") returns a backend
// that can be type-asserted and used without panic.
func TestOrcaBackend_SelectRoundTrip(t *testing.T) {
	bk, err := Select("orca")
	if err != nil {
		t.Fatal(err)
	}
	orca, ok := bk.(*OrcaBackend)
	if !ok {
		t.Fatalf("Select('orca') returned %T, want *OrcaBackend", bk)
	}
	if orca == nil {
		t.Fatal("Select('orca') returned nil")
	}
}

// TestOrcaBackend_NoAutoDetect verifies that orca never appears in Default().
func TestOrcaBackend_NoAutoDetect(t *testing.T) {
	// Set up a fake PATH with an orca binary.
	fakeBin := t.TempDir()
	if err := os.WriteFile(fakeBin+"/orca", []byte("#!/bin/sh\necho fake"), 0755); err != nil {
		t.Fatal(err)
	}
	// Also add tmux so Default() has something to return.
	if err := os.WriteFile(fakeBin+"/tmux", []byte("#!/bin/sh\necho fake"), 0755); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	// Default() should NOT return orca — even with orca on PATH.
	b := Default()
	if b == nil {
		t.Fatal("Default() returned nil when tmux is on PATH")
	}
	if _, ok := b.(*OrcaBackend); ok {
		t.Error("Default() returned OrcaBackend — orca must never auto-detect")
	}
}

// TestOrcaBackend_SelectOnly verifies that orca is selectable by name but
// not auto-detected by Default().
func TestOrcaBackend_SelectOnly(t *testing.T) {
	// Select must work.
	bk, err := Select("orca")
	if err != nil {
		t.Fatalf("Select('orca') failed: %v", err)
	}
	if _, ok := bk.(*OrcaBackend); !ok {
		t.Fatalf("Select('orca') returned %T, want *OrcaBackend", bk)
	}

	// But must not be returned by Default() — even with an empty PATH
	// where no other backend is available.
	t.Setenv("PATH", "/dev/null")
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")

	b := Default()
	if b != nil {
		t.Error("Default() returned a backend when PATH is empty and no env is set — expected nil")
	}
}
