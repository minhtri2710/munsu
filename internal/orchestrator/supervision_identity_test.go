package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	homepkg "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
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
	want := homepkg.Canonical(home)
	if id.Home != want {
		t.Errorf("Home = %q, want %q", id.Home, want)
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
	// Verify no temporary file is left behind.
	tmpFiles, _ := filepath.Glob(filepath.Join(home, "state", ".watcher-identity.tmp-*"))
	if len(tmpFiles) > 0 {
		t.Error("temp file should not exist after successful write")
	}
}

func TestWriteIdentityUsesPrivatePermissions(t *testing.T) {
	home := t.TempDir()
	if err := WriteIdentity(home, NewIdentity(home)); err != nil {
		t.Fatal(err)
	}
	testutil.AssertOwnerPrivate(t, homepkg.WriterIdentityPath(home, "watcher"))
}

// --- ClearIdentity tests ---

func TestClearIdentity_RemovesFile(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	WriteIdentity(home, id)

	ClearIdentity(home)

	path := homepkg.WriterIdentityPath(home, "watcher")
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

func TestValidatePIDOwnership_ProcessStartMismatch(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	id.ProcessStart = "different-generation"
	if err := WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}
	if ValidatePIDOwnership(home, os.Getpid()) {
		t.Fatal("process start mismatch must fail ownership validation")
	}
}

func TestValidatePIDOwnership_ExecutableMismatch(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	id.Executable = filepath.Join(t.TempDir(), "other-munsu")
	if err := WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}
	if ValidatePIDOwnership(home, os.Getpid()) {
		t.Fatal("target executable mismatch must fail ownership validation")
	}
}

func TestClearIdentityIfMatches_PreservesNewGeneration(t *testing.T) {
	home := t.TempDir()
	old := NewIdentity(home)
	current := old
	current.ProcessStart = "new-generation"
	if err := WriteIdentity(home, current); err != nil {
		t.Fatal(err)
	}
	ClearIdentityIfMatches(home, old)
	if got := ReadIdentity(home); got == nil || got.ProcessStart != current.ProcessStart {
		t.Fatalf("new generation identity was cleared: %+v", got)
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

func TestValidatePIDOwnership_HomeMismatch(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	os.MkdirAll(filepath.Join(homeA, "state"), 0755)
	os.MkdirAll(filepath.Join(homeB, "state"), 0755)

	// Identity written for homeA, then planted under homeB's state.
	id := NewIdentity(homeA)
	if err := WriteIdentity(homeB, id); err != nil {
		t.Fatal(err)
	}
	if ValidatePIDOwnership(homeB, os.Getpid()) {
		t.Fatal("identity home must match operated home")
	}
	// Same identity under the home it was minted for still validates.
	if err := WriteIdentity(homeA, id); err != nil {
		t.Fatal(err)
	}
	if !ValidatePIDOwnership(homeA, os.Getpid()) {
		t.Fatal("matching home should validate ownership")
	}
}

func TestValidatePIDOwnership_HomeAliasOK(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	if err := WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}
	// Relative or non-canonical path to the same home must still pass.
	alias := filepath.Join(home, ".", "")
	if !ValidatePIDOwnership(alias, os.Getpid()) {
		t.Fatalf("canonical-equivalent home path should validate: home=%q alias=%q id.Home=%q", home, alias, id.Home)
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
	want := homepkg.Canonical(home)
	if read.Home != want {
		t.Errorf("Home = %q, want %q", read.Home, want)
	}
}

// --- ProtocolVersion constant ---

func TestProtocolVersionConstant(t *testing.T) {
	if ProtocolVersion < 1 {
		t.Error("ProtocolVersion should be at least 1")
	}
}

// --- resolveExecPath tests ---

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

// --- BuildIdentity tests ---

func TestBuildIdentity_ZeroNeverMatches(t *testing.T) {
	zero := BuildIdentity{}
	other := NewBuildIdentity("abc1234")
	if zero.Matches(zero) {
		t.Error("zero BuildIdentity should not match itself")
	}
	if zero.Matches(other) {
		t.Error("zero BuildIdentity should not match a non-zero identity")
	}
	if other.Matches(zero) {
		t.Error("non-zero BuildIdentity should not match a zero identity")
	}
}

func TestBuildIdentity_ExactMatch(t *testing.T) {
	a := NewBuildIdentity("abc1234")
	b := NewBuildIdentity("abc1234")
	if !a.Matches(b) {
		t.Error("identical SHAs should match")
	}
}

func TestBuildIdentity_ShortVsFullShA(t *testing.T) {
	short := NewBuildIdentity("abc1234")
	full := NewBuildIdentity("abc1234def5678abc1234def5678abc1234def5")
	if !short.Matches(full) {
		t.Error("short SHA should match when it's a prefix of full SHA")
	}
	if !full.Matches(short) {
		t.Error("full SHA should match short SHA when short is a prefix")
	}
}

func TestBuildIdentity_DifferentCommits(t *testing.T) {
	a := NewBuildIdentity("abc1234")
	b := NewBuildIdentity("xyz7890")
	if a.Matches(b) {
		t.Error("different SHAs should not match")
	}
}

func TestBuildIdentity_IsZero(t *testing.T) {
	if !NewBuildIdentity("").IsZero() {
		t.Error("BuildIdentity with empty SHA should be zero")
	}
	if NewBuildIdentity("abc1234").IsZero() {
		t.Error("BuildIdentity with SHA should not be zero")
	}
}

func TestBuildIdentity_WhitespaceTrimmed(t *testing.T) {
	a := NewBuildIdentity("  abc1234  ")
	b := NewBuildIdentity("abc1234")
	if !a.Matches(b) {
		t.Error("BuildIdentity should trim whitespace")
	}
}

// --- CommitSHA field on WatcherIdentity ---

func TestNewIdentity_HasCommitSHA(t *testing.T) {
	orig := CommitSHA
	CommitSHA = "testsha1234"
	defer func() { CommitSHA = orig }()

	id := NewIdentity(t.TempDir())
	if id.CommitSHA != "testsha1234" {
		t.Errorf("CommitSHA = %q, want %q", id.CommitSHA, "testsha1234")
	}
}

func TestNewIdentity_EmptyCommitSHA(t *testing.T) {
	id := NewIdentity(t.TempDir())
	// In test context CommitSHA is empty by default
	if id.CommitSHA != "" {
		t.Errorf("CommitSHA should be empty in test context, got %q", id.CommitSHA)
	}
}

func TestCommitSHARoundTrip_ProtocolV2(t *testing.T) {
	home := t.TempDir()
	id := NewIdentity(home)
	id.CommitSHA = "abc1234def5678"

	if err := WriteIdentity(home, id); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}

	read := ReadIdentity(home)
	if read == nil {
		t.Fatal("ReadIdentity returned nil after write")
	}
	if read.CommitSHA != "abc1234def5678" {
		t.Errorf("CommitSHA round-trip = %q, want %q", read.CommitSHA, "abc1234def5678")
	}
}

func TestCommitSHA_BackwardCompatV1(t *testing.T) {
	// Write a protocol v1 identity (7 fields, no CommitSHA) and verify
	// that parseIdentity returns a WatcherIdentity with empty CommitSHA.
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	// Generic JSON identity without an optional commit SHA.
	content := fmt.Sprintf(`{"schema_version":1,"kind":"watcher","pid":12345,"start_token":"startts","executable_path":"/usr/bin/munsu","canonical_home":%q,"build_version":"v1.0.0","protocol_version":1,"started_at":1700000000}`, homepkg.Canonical(home))
	os.WriteFile(filepath.Join(home, "state", ".watcher-identity"), []byte(content), 0600)

	read := ReadIdentity(home)
	if read == nil {
		t.Fatal("ReadIdentity should parse v1 format")
	}
	if read.CommitSHA != "" {
		t.Errorf("CommitSHA should be empty for v1 format, got %q", read.CommitSHA)
	}
	if read.BuildVersion != "v1.0.0" {
		t.Errorf("BuildVersion = %q, want v1.0.0", read.BuildVersion)
	}
}

func TestIdentitySummary_IncludesCommitSHA(t *testing.T) {
	id := NewIdentity(t.TempDir())
	id.CommitSHA = "abc1234"
	s := IdentitySummary(&id)
	if !strings.Contains(s, "commit=abc1234") {
		t.Errorf("summary should include commit SHA, got: %s", s)
	}
}

func TestIdentitySummary_CommitSHAMissing(t *testing.T) {
	id := NewIdentity(t.TempDir())
	id.CommitSHA = ""
	s := IdentitySummary(&id)
	if !strings.Contains(s, "commit=-") {
		t.Errorf("summary should show '-' for missing commit SHA, got: %s", s)
	}
}
