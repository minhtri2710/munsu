package backend

import (
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// CmuxBackend unit tests
// ---------------------------------------------------------------------------

// hasCmux reports whether cmux is available on PATH.
func hasCmux() bool {
	_, err := cmuxBin()
	return err == nil
}

func TestCmuxBin_Found(t *testing.T) {
	if !hasCmux() {
		t.Skip("cmux not on PATH")
	}
	path, err := cmuxBin()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("cmuxBin() returned empty path")
	}
	if !strings.Contains(path, "cmux") {
		t.Errorf("cmuxBin() = %q, expected path containing 'cmux'", path)
	}
}

func TestCmuxBin_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	_, err := cmuxBin()
	if err == nil {
		t.Fatal("expected error when cmux is not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelect_CmuxFailsClosedWhenAbsent(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	bk, err := Select("cmux")
	if err == nil {
		t.Fatalf("Select('cmux') must fail CLOSED when cmux is absent from PATH, got %T", bk)
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelect_CmuxWhenRequestedBinaryPresent(t *testing.T) {
	fakeBin := fakeExecutables(t, "cmux")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	bk, err := Select("cmux")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*CmuxBackend); !ok {
		t.Errorf("Select('cmux') returned %T, want *CmuxBackend", bk)
	}

	// Verify it's a fresh instance (not sharing state)
	bk2, _ := Select("cmux")
	if bk == bk2 {
		t.Error("Select('cmux') returned the same instance")
	}
}

func TestParseCmuxWindow(t *testing.T) {
	tests := []struct {
		handle          string
		wantWorkspaceID string
		wantSurfaceID   string
	}{
		{"workspace:1|surface:1", "workspace:1", "surface:1"},
		{"ws_abc|surf_def", "ws_abc", "surf_def"},
		{"|surface:1", "", "surface:1"},
		{"bare", "", "bare"},
		{"", "", ""},
	}
	for _, tt := range tests {
		gotWS, gotSurf := ParseCmuxWindow(tt.handle)
		if gotWS != tt.wantWorkspaceID || gotSurf != tt.wantSurfaceID {
			t.Errorf("ParseCmuxWindow(%q) = (%q, %q), want (%q, %q)",
				tt.handle, gotWS, gotSurf, tt.wantWorkspaceID, tt.wantSurfaceID)
		}
	}
}

func TestCmuxBackend_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	c := newCmuxBackend()

	t.Run("NewWindow", func(t *testing.T) {
		_, err := c.NewWindow("s", "n")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("SendKeys", func(t *testing.T) {
		err := c.SendKeys("w|s", "text")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Capture", func(t *testing.T) {
		_, err := c.Capture("w:s", 10)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "capture not supported") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Alive", func(t *testing.T) {
		if c.Alive("w:s") {
			t.Error("Alive returned true when cmux is not on PATH")
		}
	})

	t.Run("Teardown", func(t *testing.T) {
		err := c.Teardown("w|s")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})
}

func TestCmuxBackend_Alive_InvalidHandle(t *testing.T) {
	c := newCmuxBackend()
	if c.Alive("") {
		t.Error("Alive returned true for empty window handle")
	}
}

func TestCmuxBackend_SendKeys_InvalidHandle(t *testing.T) {
	c := newCmuxBackend()
	err := c.SendKeys("", "text")
	if err == nil || !strings.Contains(err.Error(), "invalid cmux window handle") {
		t.Errorf("expected invalid handle error, got: %v", err)
	}
}

func TestCmuxBackend_Teardown_InvalidHandle(t *testing.T) {
	c := newCmuxBackend()
	err := c.Teardown("")
	if err == nil || !strings.Contains(err.Error(), "invalid cmux window handle") {
		t.Errorf("expected invalid handle error, got: %v", err)
	}
}

// TestCmuxBackend_Capture_AlwaysErrors confirms capture is explicitly unsupported.
func TestCmuxBackend_Capture_AlwaysErrors(t *testing.T) {
	c := newCmuxBackend()
	_, err := c.Capture("w:s", 10)
	if err == nil {
		t.Fatal("Capture should always return an error")
	}
	if !strings.Contains(err.Error(), "capture not supported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSelect_CmuxUnknownBackend(t *testing.T) {
	_, err := Select("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !strings.Contains(err.Error(), "unknown session backend") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestCmuxBackend_SelectRoundTrip verifies that Select("cmux") returns a backend
// that can be type-asserted and used without panic when the requested binary
// is present on PATH.
func TestCmuxBackend_SelectRoundTrip(t *testing.T) {
	fakeBin := fakeExecutables(t, "cmux")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	bk, err := Select("cmux")
	if err != nil {
		t.Fatal(err)
	}
	cmux, ok := bk.(*CmuxBackend)
	if !ok {
		t.Fatalf("Select('cmux') returned %T, want *CmuxBackend", bk)
	}
	if cmux == nil {
		t.Fatal("Select('cmux') returned nil")
	}
}
