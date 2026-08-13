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
		"  exit " + itoa(waitExit) + "\n" +
		"fi\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
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
	return NewHerdrEventSource("test-s"), tmp
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
		src := NewHerdrEventSource("test-s")
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
		src := NewHerdrEventSource("test-s")
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
		src := NewHerdrEventSource("test-s")
		_, err := src.Wait(context.Background(), EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
		if !errors.Is(err, ErrEventUnsupported) {
			t.Errorf("err = %v, want ErrEventUnsupported", err)
		}
		_ = oldPath
	})
}

func TestHerdrEventSource_Wait_Timeout(t *testing.T) {
	// The fake exits non-zero on agent wait; the adapter must classify a
	// context deadline as the normal bounded-wait outcome.
	logPath := filepath.Join(t.TempDir(), "args.log")
	src, _ := eventSourceWithFake(t, `{"error":{"code":"timeout","message":"wait timed out"}}`, 1, logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := src.Wait(ctx, EndpointRef{Backend: "herdr", Handle: "w:p"}, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context deadline exceeded", err)
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
	src := NewHerdrEventSource("test-s")
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
