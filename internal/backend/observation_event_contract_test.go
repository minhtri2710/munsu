// Contract tests for the native observation event seam (BEO-17/P1b).
//
// These tests live in the DEFAULT test lane (no integration build tag) and
// use fake herdr executables to check exact argv/JSON/absence/error behavior.
// The real Herdr lane is supplementary evidence only and never replaces these
// fake contract tests.
package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeFakeHerdrEventWait creates a fake herdr binary that responds to the
// capability schema probe (protocol 17, agent_wait flag) and to
// `agent wait <pane> --timeout <ms>` with the given JSON body on exit 0, or
// a JSON error envelope on exit 1. The args are recorded to the log path.
func writeFakeHerdrEventWait(t *testing.T, dir, schemaJSON, waitJSON string, waitExit int, logPath string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "herdr 0.7.5"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  cat <<'SCHEMA_EOF'` + "\n" +
		schemaJSON + "\n" +
		"SCHEMA_EOF\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$3" = "agent" ] && [ "$4" = "wait" ]; then` + "\n" +
		`  echo "$@" >> "` + logPath + `"` + "\n" +
		`  echo '` + waitJSON + `'` + "\n" +
		"  exit " + strconv.Itoa(waitExit) + "\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeHerdrSchemaReady is a protocol-17 schema (agent_facade + agent_wait
// flags) for the default-lane contract tests.
const fakeHerdrSchemaReady = `{"protocol":17,"schema_version":1,"$schema":"https://json-schema.org/draft/2020-12/schema","schemas":{}}`

func eventSourceWithFake(t *testing.T, waitJSON string, waitExit int, logPath string) (*HerdrEventSource, string) {
	t.Helper()
	tmp := t.TempDir()
	writeFakeHerdrEventWait(t, tmp, fakeHerdrSchemaReady, waitJSON, waitExit, logPath)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)
	return &HerdrEventSource{Session: "test-s"}, tmp
}

// eventSourceWithFakeBlocking creates a fake herdr binary whose agent-wait
// branch blocks until the invoking process is killed (context cancellation
// via exec.CommandContext), then returns a placeholder JSON body.
func eventSourceWithFakeBlocking(t *testing.T, logPath string) (*HerdrEventSource, string) {
	t.Helper()
	tmp := t.TempDir()
	writeFakeHerdrEventWaitBlocking(t, tmp, fakeHerdrSchemaReady, logPath)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)
	return &HerdrEventSource{Session: "test-s"}, tmp
}

// writeFakeHerdrEventWaitBlocking is like writeFakeHerdrEventWait but the
// `agent wait` branch sleeps until killed (models a herdr CLI blocked inside
// its own bounded wait, only interruptible by the caller's context).
//
// The blocking branch uses `exec sleep` so the sleeping process REPLACES the
// shell and keeps the same pid exec.CommandContext knows about. A plain
// `sleep 300` would be a grandchild: CommandContext kills only the direct
// child (the shell), the surviving grandchild keeps the write end of the
// stdout pipe open, and cmd.Output() blocks waiting for an EOF that never
// comes until the sleep elapses. With `exec` there is no grandchild at all,
// so cancellation is deterministic on both macOS and Linux and does not
// depend on process-group kill semantics, which differ between them.
func writeFakeHerdrEventWaitBlocking(t *testing.T, dir, schemaJSON, logPath string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--version" ]; then` + "\n" +
		`  echo "herdr 0.7.5"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "api" ] && [ "$2" = "schema" ] && [ "$3" = "--json" ]; then` + "\n" +
		`  cat <<'SCHEMA_EOF'` + "\n" +
		schemaJSON + "\n" +
		"SCHEMA_EOF\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$3" = "agent" ] && [ "$4" = "wait" ]; then` + "\n" +
		`  echo "$@" >> "` + logPath + `"` + "\n" +
		"  exec sleep 300\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHerdrEventSource_Wait_NormalizedSignal(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `{"agent_status":"working","state_change_seq":42,"pane_id":"w1:p1"}`, 0, logPath)

	ref := EndpointRef{Backend: "herdr", Handle: "w1:p1"}
	sig, err := src.Wait(context.Background(), ref, "")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !sig.Valid() {
		t.Fatalf("signal invalid: %+v", sig)
	}
	if sig.Source != SourceEvent {
		t.Errorf("Source = %v, want event", sig.Source)
	}
	if sig.Activity != ActivityBusy {
		t.Errorf("Activity = %v, want busy (wire status working)", sig.Activity)
	}
	if sig.Cursor != "42" {
		t.Errorf("Cursor = %q, want 42", sig.Cursor)
	}
	if sig.Incarnation != "" {
		t.Errorf("Incarnation = %q, want empty (adapter cannot attest opaque incarnation)", sig.Incarnation)
	}
	if sig.ObservedAt.IsZero() {
		t.Error("ObservedAt must be set")
	}

	// Exact argv: session flag, agent wait subcommand, pane handle, timeout.
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading argv log: %v", err)
	}
	line := strings.TrimSpace(string(args))
	for _, want := range []string{"--session", "test-s", "agent", "wait", "w1:p1", "--timeout"} {
		if !strings.Contains(line, want) {
			t.Errorf("argv %q missing %q", line, want)
		}
	}
}

func TestHerdrEventSource_Wait_IdleAndBlockedNormalization(t *testing.T) {
	cases := []struct {
		wire string
		want Activity
	}{
		{"idle", ActivityIdle},
		{"done", ActivityIdle},
		{"blocked", ActivityBlocked},
		{"working", ActivityBusy},
		{"unknown", ActivityUnknown},
		{"", ActivityUnknown},
		{"garbage", ActivityUnknown},
	}
	for _, tc := range cases {
		wire := `{"agent_status":"` + tc.wire + `","state_change_seq":1}`
		logPath := filepath.Join(t.TempDir(), "args.log")
		src, _ := eventSourceWithFake(t, wire, 0, logPath)
		sig, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
		if err != nil {
			t.Fatalf("Wait(%q): %v", tc.wire, err)
		}
		if sig.Activity != tc.want {
			t.Errorf("wire %q: Activity = %v, want %v", tc.wire, sig.Activity, tc.want)
		}
	}
}

func TestHerdrEventSource_NegotiationGates(t *testing.T) {
	t.Run("absent binary -> ErrEventUnavailable", func(t *testing.T) {
		oldPath := os.Getenv("PATH")
		t.Setenv("PATH", "/nonexistent")
		src := &HerdrEventSource{Session: "test-s"}
		_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
		if !errors.Is(err, ErrEventUnavailable) {
			t.Errorf("err = %v, want ErrEventUnavailable", err)
		}
		_ = oldPath
	})

	t.Run("unsupported protocol -> ErrEventProtocolMismatch", func(t *testing.T) {
		tmp := t.TempDir()
		writeFakeHerdrEventWait(t, tmp, `{"protocol":99,"schema_version":2,"schemas":{}}`, `{}`, 0, filepath.Join(tmp, "a.log"))
		oldPath := os.Getenv("PATH")
		t.Setenv("PATH", tmp+":"+oldPath)
		src := &HerdrEventSource{Session: "test-s"}
		_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
		if !errors.Is(err, ErrEventProtocolMismatch) {
			t.Errorf("err = %v, want ErrEventProtocolMismatch", err)
		}
		_ = oldPath
	})

	t.Run("protocol without agent-wait flag -> ErrEventUnsupported", func(t *testing.T) {
		// Protocol 16 has pane_wait_output but no agent_wait.
		tmp := t.TempDir()
		writeFakeHerdrEventWait(t, tmp, `{"protocol":16,"schema_version":1,"schemas":{}}`, `{}`, 0, filepath.Join(tmp, "a.log"))
		oldPath := os.Getenv("PATH")
		t.Setenv("PATH", tmp+":"+oldPath)
		src := &HerdrEventSource{Session: "test-s"}
		_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
		if !errors.Is(err, ErrEventUnsupported) {
			t.Errorf("err = %v, want ErrEventUnsupported", err)
		}
		_ = oldPath
	})
}

func TestHerdrEventSource_Wait_Timeout(t *testing.T) {
	// The fake exits non-zero with a STRUCTURED timeout error envelope. The
	// adapter must map the herdr CLI's own bounded-wait timeout to
	// context.DeadlineExceeded (the normal bounded-wait outcome, poll
	// fallback) — NOT to a generic reader failure. A deadline-free context
	// keeps this deterministic: the structured-error path is always the one
	// exercised, never the caller-cancellation path.
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `{"error":{"code":"timeout","message":"wait timed out"}}`, 1, logPath)
	_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context deadline exceeded", err)
	}
}

// spawnBackstop bounds a wait on a spawned process by a share of the test
// binary's OWN remaining -timeout budget instead of by a literal.
//
// A literal is what made this file flaky (#574): `2 * time.Second` is not a
// property of the adapter, it is a claim about how fast this machine forks,
// execs a shell and lands its first write. Under `go test ./...` on a loaded
// runner that claim is false while nothing at all is wrong with the code under
// test. Deriving the bound from -timeout gives a loaded runner and an idle one
// the same semantics; the slow one merely takes longer to reach the same
// verdict. Halving what remains leaves room for the rest of the test and still
// fails ahead of the binary's own timeout panic, so the failure keeps its
// message instead of becoming a goroutine dump.
//
// This is a backstop for a genuine hang, never the primary signal: every
// caller also ends on evidence (see awaitFakeBlocking). `go test -timeout 0`
// asks for no deadline at all, so honour that with a nil channel that never
// fires and let the evidence do the work.
func spawnBackstop(t *testing.T) <-chan time.Time {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		return nil
	}
	return time.After(time.Until(deadline) / 2)
}

// awaitFakeBlocking blocks until the fake herdr has recorded its agent-wait
// argv line, which it does immediately before `exec sleep`. Polling this
// marker replaces a fixed sleep: on a loaded runner a fixed sleep can elapse
// while the fake is still starting up, so cancel() would kill the process
// during exec/start-up instead of while it is blocked. That variant still
// passes (ctx.Err() is non-nil either way) but never exercises the
// kill-the-blocked-process path — silent coverage loss, not a visible flake.
//
// The polling itself is unbounded on purpose. What ends this wait is evidence,
// not a clock: either the marker appears, or waitReturned proves the fake can
// no longer reach the blocking branch because the process running it is
// already gone — the same "the child died instead of signalling" signal the
// readiness pipe in internal/orchestrator gets from EOF. Both outcomes are
// exact, so the derived backstop below only ever catches a true hang.
func awaitFakeBlocking(t *testing.T, logPath string, waitReturned <-chan error) {
	t.Helper()
	backstop := spawnBackstop(t)
	poll := time.NewTicker(2 * time.Millisecond)
	defer poll.Stop()
	for {
		if fi, err := os.Stat(logPath); err == nil && fi.Size() > 0 {
			return
		}
		select {
		case err := <-waitReturned:
			t.Fatalf("Wait returned (err = %v) while the fake herdr was still short of its blocking agent-wait branch (no argv recorded at %s): the fake never blocked, so the kill-the-blocked-process path cannot be exercised", err, logPath)
		case <-backstop:
			t.Fatalf("fake herdr never reached its blocking agent-wait branch and Wait is still in flight (no argv recorded at %s)", logPath)
		case <-poll.C:
		}
	}
}

func TestHerdrEventSource_Wait_ContextCancellation(t *testing.T) {
	// Caller cancellation must surface as the caller's own ctx.Err() (the
	// adapter never masks a cancelled bounded wait as anything else).
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFakeBlocking(t, logPath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := src.Wait(ctx, EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
		done <- err
	}()
	// Synchronize on the fake actually reaching its blocking branch, so the
	// cancellation below can only be observed by killing a blocked process.
	awaitFakeBlocking(t, logPath, done)
	// Guard the other half of the same property: Wait must still be in flight
	// when cancel() fires. If it already returned, whatever error arrives says
	// nothing about the cancellation path.
	select {
	case err := <-done:
		t.Fatalf("Wait returned before cancellation (err = %v); the blocked-process kill path was not exercised", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-spawnBackstop(t):
		t.Fatal("Wait did not return after context cancellation")
	}
}

func TestHerdrEventSource_Wait_StructuredNonTimeoutReaderFailure(t *testing.T) {
	// A structured, NON-timeout herdr error (internal_error) must stay a
	// generic reader failure — only the 'timeout' code maps to a context
	// deadline. This pins the narrowness of the timeout mapping.
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `{"error":{"code":"internal_error","message":"boom"}}`, 1, logPath)
	_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
	if !errors.Is(err, ErrEventReaderFailure) {
		t.Errorf("err = %v, want ErrEventReaderFailure", err)
	}
}

func TestHerdrEventSource_Wait_ReaderFailure(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `not json at all`, 1, logPath)
	_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
	if !errors.Is(err, ErrEventReaderFailure) {
		t.Errorf("err = %v, want ErrEventReaderFailure", err)
	}
}

func TestHerdrEventSource_Wait_ProtocolMismatchOnWait(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `{"error":{"code":"protocol_mismatch","message":"expected_protocol: 17"}}`, 1, logPath)
	_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
	if !errors.Is(err, ErrEventProtocolMismatch) {
		t.Errorf("err = %v, want ErrEventProtocolMismatch", err)
	}
}

func TestHerdrEventSource_Wait_PaneAbsentIsReaderFailureNotLifecycle(t *testing.T) {
	// A vanished pane during an event wait must NOT surface as dead: the
	// adapter returns a reader failure and the orchestrator re-probes the
	// exact binding before any policy decision.
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `{"error":{"code":"pane_not_found","message":"pane w:p not found"}}`, 1, logPath)
	_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
	if !errors.Is(err, ErrEventReaderFailure) {
		t.Errorf("err = %v, want ErrEventReaderFailure (never dead)", err)
	}
}

func TestNormalizeActivityHint(t *testing.T) {
	cases := map[string]Activity{
		"busy":    ActivityBusy,
		"working": ActivityBusy,
		"idle":    ActivityIdle,
		"done":    ActivityIdle,
		"blocked": ActivityBlocked,
		"unknown": ActivityUnknown,
		"":        ActivityUnknown,
		"garbage": ActivityUnknown,
		"WORKING": ActivityUnknown,
	}
	for wire, want := range cases {
		if got := NormalizeActivityHint(wire); got != want {
			t.Errorf("NormalizeActivityHint(%q) = %v, want %v", wire, got, want)
		}
	}
}

func TestObservationSignal_Valid(t *testing.T) {
	valid := ObservationSignal{
		Endpoint: EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: ActivityBusy,
		Source:   SourceEvent,
	}
	if !valid.Valid() {
		t.Error("valid signal must pass Valid()")
	}
	noHandle := valid
	noHandle.Endpoint.Handle = ""
	if noHandle.Valid() {
		t.Error("signal without handle must fail Valid()")
	}
	badSource := valid
	badSource.Source = SourceInvalid
	if badSource.Valid() {
		t.Error("signal with invalid source must fail Valid()")
	}
}

func TestObservationSourceEventEnum(t *testing.T) {
	if SourceEvent.String() != "event" {
		t.Errorf("SourceEvent.String() = %q, want event", SourceEvent.String())
	}
	if !SourceEvent.Valid() {
		t.Error("SourceEvent must be valid")
	}
	if SourceInvalid.Valid() {
		t.Error("SourceInvalid must not be valid")
	}
	// Event source must never qualify as Absent(): only probe/derived sources
	// are absence-capable, and freshness authorization is Fleet-owned.
	eventObs := EndpointObservation{
		Lifecycle:      LifecycleDead,
		Responsiveness: Responsive,
		Freshness:      FreshnessCurrent,
		Activity:       ActivityUnknown,
		Source:         SourceEvent,
	}
	if eventObs.Absent() {
		t.Error("an event-derived observation must never be Absent()")
	}
}

func TestHerdrEventSource_After(t *testing.T) {
	// Cursor ordering is adapter-owned: the orchestrator calls After and never
	// parses or compares cursors itself.
	src := &HerdrEventSource{Session: "test-s"}
	cases := []struct {
		sig, after EventCursor
		want       bool
	}{
		{"2", "1", true},
		{"1", "1", false}, // duplicate
		{"1", "2", false}, // out-of-order
		{"10", "9", true}, // numeric ordering beyond lexical
		{"", "5", true},   // unknown cursor accepted (adapter no-position)
		{"5", "", true},   // no prior position
		{"", "", true},
		{"a2", "a1", true}, // lexical fallback for non-numeric
		{"a1", "a2", false},
	}
	for _, tc := range cases {
		if got := src.After(tc.sig, tc.after); got != tc.want {
			t.Errorf("After(%q, %q) = %v, want %v", tc.sig, tc.after, got, tc.want)
		}
	}
}
