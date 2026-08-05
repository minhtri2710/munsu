package backend

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

func TestSelect_OrcaFailsClosedWhenAbsent(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	bk, err := Select("orca")
	if err == nil {
		t.Fatalf("Select('orca') must fail CLOSED when orca is absent from PATH, got %T", bk)
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelect_OrcaWhenRequestedBinaryPresent(t *testing.T) {
	fakeBin := fakeExecutables(t, "orca")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

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
		handle          string
		wantContainerID string
		wantTerminalID  string
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
// that can be type-asserted and used without panic when the requested binary
// is present on PATH.
func TestOrcaBackend_SelectRoundTrip(t *testing.T) {
	fakeBin := fakeExecutables(t, "orca")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

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

// TestOrcaBackend_NoAutoDetect verifies that auto-detection is gone: an empty
// requested identity FAILS CLOSED even when orca and tmux binaries are on PATH.
func TestOrcaBackend_NoAutoDetect(t *testing.T) {
	// Set up a fake PATH with an orca binary (and tmux).
	fakeBin := fakeExecutables(t, "orca", "tmux")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")

	homeDir := t.TempDir()
	if _, _, err := Resolve(homeDir, ""); err == nil {
		t.Fatal("Resolve('') must fail closed — no env/PATH auto-selection (orca never auto-detects)")
	}
}

// TestOrcaBackend_SelectOnly verifies that orca is selectable ONLY by explicit
// name, failing closed when the requested binary is absent.
func TestOrcaBackend_SelectOnly(t *testing.T) {
	// Select must fail CLOSED when orca is absent.
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")
	if _, err := Select("orca"); err == nil {
		t.Fatal("Select('orca') must fail closed when orca is absent from PATH")
	}

	// Select succeeds when a fake orca binary is on PATH.
	fakeBin := fakeExecutables(t, "orca")
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)
	bk, err := Select("orca")
	if err != nil {
		t.Fatalf("Select('orca') failed: %v", err)
	}
	if _, ok := bk.(*OrcaBackend); !ok {
		t.Fatalf("Select('orca') returned %T, want *OrcaBackend", bk)
	}

	// Empty identity never resolves — even with orca on PATH.
	homeDir := t.TempDir()
	if _, _, err := Resolve(homeDir, ""); err == nil {
		t.Error("Resolve('') must fail closed — no implicit orca selection")
	}
}
