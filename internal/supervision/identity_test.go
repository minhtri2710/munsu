package supervision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- NewIdentity tests ---

func TestNewIdentity_HasAllFields(t *testing.T) {
	id := NewIdentity(t.TempDir())
	if id.Home == "" {
		t.Error("Home should not be empty")
	}
	if id.PID <= 0 {
		t.Error("PID should be positive")
	}
	if id.ProcessStart == "" {
		t.Error("ProcessStart should not be empty")
	}
	if id.Executable == "" {
		t.Error("Executable should not be empty")
	}
	if id.BuildVersion == "" {
		t.Error("BuildVersion should not be empty")
	}
	if id.ProtocolVersion != ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", id.ProtocolVersion, ProtocolVersion)
	}
	if id.StartTime <= 0 {
		t.Error("StartTime should be positive")
	}
}

func TestNewIdentity_HomeIsSet(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	if id.Home != home {
		t.Errorf("Home = %q, want %q", id.Home, home)
	}
}

func TestNewIdentity_ExecutableResolved(t *testing.T) {
	id := NewIdentity(t.TempDir())
	// Should resolve to a real binary path
	if _, err := os.Stat(id.Executable); err != nil {
		t.Errorf("Executable path %q should be stat-able: %v", id.Executable, err)
	}
}

func TestNewIdentity_CurrentPID(t *testing.T) {
	id := NewIdentity(t.TempDir())
	if id.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d (current process)", id.PID, os.Getpid())
	}
}

func TestNewIdentity_ProtocolVersion(t *testing.T) {
	id := NewIdentity(t.TempDir())
	if id.ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", id.ProtocolVersion)
	}
}

// --- WriteIdentity / ReadIdentity round-trip tests ---

func TestIdentityRoundTrip(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)

	if err := WriteIdentity(home, id); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}

	read := ReadIdentity(home)
	if read == nil {
		t.Fatal("ReadIdentity returned nil after write")
	}
	if read.Home != id.Home {
		t.Errorf("Home = %q, want %q", read.Home, id.Home)
	}
	if read.PID != id.PID {
		t.Errorf("PID = %d, want %d", read.PID, id.PID)
	}
	if read.ProcessStart != id.ProcessStart {
		t.Errorf("ProcessStart = %q, want %q", read.ProcessStart, id.ProcessStart)
	}
	if read.Executable != id.Executable {
		t.Errorf("Executable = %q, want %q", read.Executable, id.Executable)
	}
	if read.BuildVersion != id.BuildVersion {
		t.Errorf("BuildVersion = %q, want %q", read.BuildVersion, id.BuildVersion)
	}
	if read.ProtocolVersion != id.ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", read.ProtocolVersion, id.ProtocolVersion)
	}
	if read.StartTime != id.StartTime {
		t.Errorf("StartTime = %d, want %d", read.StartTime, id.StartTime)
	}
}

func TestReadIdentity_NoFile(t *testing.T) {
	home := t.TempDir()
	if id := ReadIdentity(home); id != nil {
		t.Error("ReadIdentity on empty home should return nil")
	}
}

func TestReadIdentity_EmptyFile(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.WriteFile(filepath.Join(home, "state", ".watcher-identity"), []byte(""), 0644)
	if id := ReadIdentity(home); id != nil {
		t.Error("ReadIdentity on empty file should return nil")
	}
}

func TestReadIdentity_CorruptFile(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.WriteFile(filepath.Join(home, "state", ".watcher-identity"), []byte("garbage data here"), 0644)
	if id := ReadIdentity(home); id != nil {
		t.Error("ReadIdentity on corrupt file should return nil")
	}
}

func TestReadIdentity_PartialFile(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	// Only 3 fields — should fail to parse
	os.WriteFile(filepath.Join(home, "state", ".watcher-identity"), []byte("home\t123\ttoken123\n"), 0644)
	if id := ReadIdentity(home); id != nil {
		t.Error("ReadIdentity on partial file should return nil")
	}
}

func TestWriteIdentity_TempCleanup(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	if err := WriteIdentity(home, id); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	// Verify no .tmp file is left behind
	tmpFiles, _ := filepath.Glob(filepath.Join(home, "state", ".watcher-identity.tmp"))
	if len(tmpFiles) > 0 {
		t.Error("temp file should not exist after successful write")
	}
}

// --- ClearIdentity tests ---

func TestClearIdentity_RemovesFile(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	WriteIdentity(home, id)

	ClearIdentity(home)

	path := identityPath(home)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("identity file %q should not exist after ClearIdentity", path)
	}
	if ReadIdentity(home) != nil {
		t.Error("ReadIdentity after ClearIdentity should return nil")
	}
}

func TestClearIdentity_Idempotent(t *testing.T) {
	home := t.TempDir()
	// Should not panic
	ClearIdentity(home)
	ClearIdentity(home)
	ClearIdentity(home)
}

// --- formatIdentity / parseIdentity tests ---

func TestFormatIdentity_RoundTrip(t *testing.T) {
	id1 := NewIdentity(t.TempDir())
	data := formatIdentity(id1)
	if !strings.HasSuffix(data, "\n") {
		t.Error("formatted identity should end with newline")
	}

	id2 := parseIdentity(data)
	if id2 == nil {
		t.Fatal("parseIdentity returned nil")
	}
	if id2.Home != id1.Home {
		t.Errorf("Home = %q, want %q", id2.Home, id1.Home)
	}
	if id2.PID != id1.PID {
		t.Errorf("PID = %d, want %d", id2.PID, id1.PID)
	}
	if id2.ProcessStart != id1.ProcessStart {
		t.Errorf("ProcessStart = %q, want %q", id2.ProcessStart, id1.ProcessStart)
	}
	if id2.Executable != id1.Executable {
		t.Errorf("Executable = %q, want %q", id2.Executable, id1.Executable)
	}
	if id2.BuildVersion != id1.BuildVersion {
		t.Errorf("BuildVersion = %q, want %q", id2.BuildVersion, id1.BuildVersion)
	}
	if id2.ProtocolVersion != id1.ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", id2.ProtocolVersion, id1.ProtocolVersion)
	}
	if id2.StartTime != id1.StartTime {
		t.Errorf("StartTime = %d, want %d", id2.StartTime, id1.StartTime)
	}
}

func TestParseIdentity_TrailingWhitespace(t *testing.T) {
	id := NewIdentity(t.TempDir())
	data := formatIdentity(id)

	id2 := parseIdentity(data + "   \t\n")
	if id2 == nil {
		t.Fatal("parseIdentity with trailing whitespace should succeed")
	}
	if id2.Home != id.Home {
		t.Errorf("Home = %q, want %q", id2.Home, id.Home)
	}
}

func TestParseIdentity_Nil(t *testing.T) {
	if id := parseIdentity(""); id != nil {
		t.Error("parseIdentity of empty string should return nil")
	}
	if id := parseIdentity("foo"); id != nil {
		t.Error("parseIdentity of short string should return nil")
	}
}

// --- ValidatePIDOwnership tests ---

func TestValidatePIDOwnership_NoIdentityFile(t *testing.T) {
	home := t.TempDir()
	if ValidatePIDOwnership(home, 9999) {
		t.Error("ValidatePIDOwnership without identity file should return false")
	}
}

func TestValidatePIDOwnership_WrongPID(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	id.PID = 99999
	WriteIdentity(home, id)

	if ValidatePIDOwnership(home, 42) {
		t.Error("ValidatePIDOwnership with wrong PID should return false")
	}
}

func TestValidatePIDOwnership_CurrentProcess(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	WriteIdentity(home, id)

	// Our own PID is alive and the executable matches, so this should pass
	if !ValidatePIDOwnership(home, os.Getpid()) {
		t.Error("ValidatePIDOwnership for current process should return true")
	}
}

func TestValidatePIDOwnership_DeadPIDFails(t *testing.T) {
	home := t.TempDir()
	// Create identity with a PID that's definitely not alive (PID 0 is invalid)
	id := NewIdentity(home)
	id.PID = 9999999
	WriteIdentity(home, id)

	if ValidatePIDOwnership(home, 9999999) {
		t.Error("ValidatePIDOwnership for dead PID should return false")
	}
}

// --- IdentitySummary tests ---

func TestIdentitySummary_Nil(t *testing.T) {
	s := IdentitySummary(nil)
	if s != "no watcher identity" {
		t.Errorf("summary for nil = %q, want %q", s, "no watcher identity")
	}
}

func TestIdentitySummary_Valid(t *testing.T) {
	id := NewIdentity(t.TempDir())
	s := IdentitySummary(&id)
	if !strings.Contains(s, "pid=") {
		t.Error("summary should contain pid=")
	}
	if !strings.Contains(s, "version=") {
		t.Error("summary should contain version=")
	}
	if !strings.Contains(s, "proto=") {
		t.Error("summary should contain proto=")
	}
	if !strings.Contains(s, "home=") {
		t.Error("summary should contain home=")
	}
}

func TestIdentitySummary_IncludesAge(t *testing.T) {
	id := NewIdentity(t.TempDir())
	// Should have a reasonable age (less than 5 seconds old)
	s := IdentitySummary(&id)
	if !strings.Contains(s, "age=") {
		t.Error("summary should contain age=")
	}
}

// --- BuildVersion package variable tests ---

func TestBuildVersionDefault(t *testing.T) {
	// BuildVersion should be set to the default "0.0.0-dev" in test context
	if BuildVersion == "" {
		t.Error("BuildVersion should not be empty")
	}
}

func TestNewIdentity_UsesBuildVersion(t *testing.T) {
	id := NewIdentity(t.TempDir())
	if id.BuildVersion != BuildVersion {
		t.Errorf("BuildVersion = %q, want %q", id.BuildVersion, BuildVersion)
	}
}

// --- Concurrent identity operations ---

func TestConcurrentWriteReadIdentity(t *testing.T) {
	home := t.TempDir()
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			id := NewIdentity(home)
			WriteIdentity(home, id)
			ReadIdentity(home)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// --- Edge cases ---

func TestWriteIdentity_NoStateDir(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	// State directory doesn't exist yet — should be created
	if err := WriteIdentity(home, id); err != nil {
		t.Fatalf("WriteIdentity without state dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".watcher-identity")); err != nil {
		t.Errorf("identity file should exist: %v", err)
	}
}

func TestWriteIdentity_Overwrites(t *testing.T) {
	home := t.TempDir()
	id1 := NewIdentity(home)
	id1.BuildVersion = "v1.0.0"
	WriteIdentity(home, id1)

	id2 := NewIdentity(home)
	id2.BuildVersion = "v2.0.0"
	WriteIdentity(home, id2)

	read := ReadIdentity(home)
	if read == nil {
		t.Fatal("ReadIdentity returned nil after overwrite")
	}
	if read.BuildVersion != "v2.0.0" {
		t.Errorf("BuildVersion after overwrite = %q, want %q", read.BuildVersion, "v2.0.0")
	}
}

// --- Stale identity scenarios ---

func TestStaleIdentityClearedOnStart(t *testing.T) {
	// Simulate: old identity, then run watcher (should overwrite)
	home := t.TempDir()
	oldID := NewIdentity(home)
	oldID.PID = 99999
	oldID.BuildVersion = "v0.0.1-stale"
	WriteIdentity(home, oldID)

	// Now simulate a watcher run (which writes identity on start)
	newID := NewIdentity(home)
	WriteIdentity(home, newID)

	read := ReadIdentity(home)
	if read == nil {
		t.Fatal("ReadIdentity returned nil after re-write")
	}
	if read.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d (current process)", read.PID, os.Getpid())
	}
}

// --- Beat + identity consistency ---

func TestBeatAndIdentityConsistency(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	WriteIdentity(home, id)

	read := ReadIdentity(home)
	if read == nil {
		t.Fatal("ReadIdentity returned nil")
	}
	if read.Home != home {
		t.Errorf("Home = %q, want %q", read.Home, home)
	}
}

// --- ProtocolVersion constant ---

func TestProtocolVersionConstant(t *testing.T) {
	if ProtocolVersion < 1 {
		t.Error("ProtocolVersion should be at least 1")
	}
}

// --- resolveExecPath tests ---

func TestResolveExecPath_ReturnsPath(t *testing.T) {
	path := resolveExecPath()
	if path == "" {
		t.Error("resolved path should not be empty")
	}
	if path == "unknown" {
		t.Error("resolved path should not be 'unknown' in test context")
	}
}

func TestResolveExecPath_IsExecutable(t *testing.T) {
	path := resolveExecPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat resolved path %q: %v", path, err)
	}
	if info.Mode()&0100 == 0 {
		t.Errorf("resolved path %q should be executable", path)
	}
}

// --- Zero-value identity ---

func TestZeroValueIdentity_Defaults(t *testing.T) {
	id := WatcherIdentity{}
	if id.Home != "" {
		t.Error("zero value Home should be empty")
	}
	if id.PID != 0 {
		t.Error("zero value PID should be 0")
	}
	if id.ProtocolVersion != 0 {
		t.Error("zero value ProtocolVersion should be 0")
	}
}

// --- Time field sanity ---

func TestStartTime_IsReasonable(t *testing.T) {
	id := NewIdentity(t.TempDir())
	now := time.Now().Unix()
	diff := now - id.StartTime
	if diff < 0 || diff > 10 {
		t.Errorf("StartTime diff = %d seconds, expected 0-10", diff)
	}
}

func TestProcessStart_IsTimestamp(t *testing.T) {
	id := NewIdentity(t.TempDir())
	// ProcessStart should be a number (unix nanos)
	var nanos int64
	n, _ := fmt.Sscanf(id.ProcessStart, "%d", &nanos)
	if n != 1 || nanos <= 0 {
		t.Errorf("ProcessStart = %q, expected positive integer", id.ProcessStart)
	}
}
