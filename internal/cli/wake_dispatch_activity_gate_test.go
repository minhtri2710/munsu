package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// herdrDispatchFixture is one isolated end-to-end fixture for the shipped wake
// dispatch path: an isolated munsu home configured for herdr wake delivery,
// a queued wake, and a fake herdr CLI on PATH that answers `pane get`,
// `agent get`, `api schema` and `agent prompt` while appending every
// invocation to a transcript. Nothing is stubbed inside munsu: the fixture
// drives dispatchHerdrWake, which composes the real probeAdapter,
// busyAdapter, submitAdapter, HerdrBackend, fleet busy authority and durable
// wake queue.
type herdrDispatchFixture struct {
	home       string
	logPath    string
	promptPath string
}

// newHerdrDispatchFixture installs the fake herdr, configures the home for
// herdr delivery and enqueues exactly one wake with the given payload.
// agentStatus is returned verbatim by `herdr agent get` as agent_status; the
// sentinel "AGENT_NOT_FOUND" makes it answer the structured agent_not_found
// error instead. paneAbsent makes `herdr pane get` report pane_not_found.
func newHerdrDispatchFixture(t *testing.T, agentStatus, payload string, paneAbsent bool) *herdrDispatchFixture {
	t.Helper()

	fakeDir := t.TempDir()
	logPath := filepath.Join(fakeDir, "herdr-invocations.log")
	promptPath := filepath.Join(fakeDir, "delivered-prompt.txt")
	statusPath := filepath.Join(fakeDir, "agent-status")
	if err := os.WriteFile(statusPath, []byte(agentStatus), 0o644); err != nil {
		t.Fatalf("writing agent status: %v", err)
	}
	paneState := "present"
	if paneAbsent {
		paneState = "absent"
	}

	script := fmt.Sprintf(`#!/bin/sh
LOG=%[1]q
STATUS_FILE=%[2]q
PROMPT_FILE=%[3]q
PANE_STATE=%[4]q
if [ "$1" = "--session" ]; then shift 2; fi
echo "herdr $1 $2" >> "$LOG"
case "$1 $2" in
  "api schema")
    echo "protocol: 20"
    exit 0 ;;
  "pane get")
    if [ "$PANE_STATE" = "absent" ]; then
      echo "pane_not_found" >&2
      exit 1
    fi
    echo "{\"result\":{\"pane_id\":\"$3\"}}"
    exit 0 ;;
  "agent get")
    status=$(cat "$STATUS_FILE")
    if [ "$status" = "AGENT_NOT_FOUND" ]; then
      echo '{"error":{"code":"agent_not_found"}}'
      exit 1
    fi
    printf '{"result":{"agent":{"agent_status":"%%s"}}}\n' "$status"
    exit 0 ;;
  "agent prompt")
    shift 3
    printf '%%s' "$*" > "$PROMPT_FILE"
    echo '{"result":{"type":"prompt_submitted","agent":{"agent_status":"working"}}}'
    exit 0 ;;
esac
echo "unsupported fake herdr command: $*" >&2
exit 1
`, logPath, statusPath, promptPath, paneState)

	testutil.WriteFakeExecutable(t, filepath.Join(fakeDir, "herdr"), script)
	testutil.PrependPath(t, fakeDir)

	// The shipped runtime target for a herdr general pane comes from the
	// herdr runtime environment, not from a test-only seam.
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SESSION", "default")
	t.Setenv("HERDR_PANE_ID", "%general")

	home := testutil.TempHome(t)
	if err := config.Set(home, "wake-delivery-mode", "herdr"); err != nil {
		t.Fatalf("configuring wake delivery mode: %v", err)
	}
	if err := orchestrator.EnqueueWake(home, "signal", "task-1", payload); err != nil {
		t.Fatalf("enqueueing wake: %v", err)
	}
	if got := countQueuedWakes(home); got != 1 {
		t.Fatalf("queued wakes before dispatch = %d, want 1", got)
	}

	return &herdrDispatchFixture{home: home, logPath: logPath, promptPath: promptPath}
}

// transcript returns the fake herdr invocation transcript.
func (f *herdrDispatchFixture) transcript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("reading herdr transcript: %v", err)
	}
	return string(data)
}

// deliveredPrompt returns the prompt text the fake herdr actually received,
// or "" when no prompt was ever delivered to the pane.
func (f *herdrDispatchFixture) deliveredPrompt(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.promptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("reading delivered prompt: %v", err)
	}
	return string(data)
}

func countLines(transcript, line string) int {
	n := 0
	for _, l := range strings.Split(transcript, "\n") {
		if strings.TrimSpace(l) == line {
			n++
		}
	}
	return n
}

// TestDispatchHerdrWake_HoldsWakeUnlessActivityReadsIdle drives the shipped
// dispatch path against a fake herdr agent in each activity state and asserts
// the end-user-visible effect: whether a wake prompt is injected into the
// agent pane, and whether the durable wake stays queued for a later cycle.
func TestDispatchHerdrWake_HoldsWakeUnlessActivityReadsIdle(t *testing.T) {
	cases := []struct {
		name        string
		agentStatus string
		paneAbsent  bool
		why         string
	}{
		{name: "busy_working", agentStatus: "Working", why: "activity reads busy: hold, do not inject a competing turn"},
		{name: "busy_lowercase", agentStatus: "busy", why: "activity reads busy: hold"},
		{name: "blocked", agentStatus: "Blocked", why: "activity reads blocked: hold, awaiting input"},
		{name: "unknown_status", agentStatus: "compacting", why: "unrecognized status is unknown, never treated as idle"},
		{name: "no_agent_status", agentStatus: "", why: "empty status is unknown, never treated as idle"},
		{name: "agent_not_found", agentStatus: "AGENT_NOT_FOUND", why: "no recognized agent: activity cannot enrich, no dispatch"},
		{name: "idle_but_pane_absent", agentStatus: "idle", paneAbsent: true, why: "idle activity never concludes liveness: absent pane still refuses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHerdrDispatchFixture(t, tc.agentStatus, "soldier task-1 reported done", tc.paneAbsent)

			if err := dispatchHerdrWake(f.home); err != nil {
				t.Fatalf("dispatchHerdrWake: %v", err)
			}

			transcript := f.transcript(t)
			t.Logf("agent_status=%q pane_absent=%v (%s)\nherdr transcript:\n%s", tc.agentStatus, tc.paneAbsent, tc.why, transcript)

			if prompt := f.deliveredPrompt(t); prompt != "" {
				t.Fatalf("wake prompt was injected into a non-idle endpoint:\n%s", prompt)
			}
			if got := countLines(transcript, "herdr agent prompt"); got != 0 {
				t.Fatalf("herdr agent prompt invoked %d times, want 0", got)
			}
			if got := countQueuedWakes(f.home); got != 1 {
				t.Fatalf("queued wakes after refused dispatch = %d, want 1 (wake must survive for a later cycle)", got)
			}
			if !tc.paneAbsent {
				// One observation, one `agent get`: liveness and the activity
				// hint come from the same single probe.
				if got := countLines(transcript, "herdr agent get"); got != 1 {
					t.Fatalf("herdr agent get invoked %d times for one observation, want 1: %s", got, transcript)
				}
			}
		})
	}
}

// TestDispatchHerdrWake_DeliversWakeWhenActivityReadsIdle asserts the positive
// half of the gate: an idle agent receives the wake prompt, with the exact
// claim/event identity and resolve instruction, and the wake leaves the queue.
// The fake reports an unnormalized " Idle " to match what herdr really returns.
func TestDispatchHerdrWake_DeliversWakeWhenActivityReadsIdle(t *testing.T) {
	f := newHerdrDispatchFixture(t, " Idle ", "soldier task-1 reported done", false)

	if err := dispatchHerdrWake(f.home); err != nil {
		t.Fatalf("dispatchHerdrWake: %v", err)
	}

	transcript := f.transcript(t)
	prompt := f.deliveredPrompt(t)
	t.Logf("agent_status=%q\nherdr transcript:\n%s\nprompt delivered to the agent pane:\n%s", " Idle ", transcript, prompt)

	if got := countLines(transcript, "herdr agent prompt"); got != 1 {
		t.Fatalf("herdr agent prompt invoked %d times, want exactly 1: %s", got, transcript)
	}
	if prompt == "" {
		t.Fatal("idle endpoint received no wake prompt")
	}
	for _, want := range []string{
		"[mu-system:wake]",
		"claim_id:",
		"event_id:",
		"soldier task-1 reported done",
		"munsu wake resolve --claim-id",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("delivered prompt missing %q:\n%s", want, prompt)
		}
	}
	if got := countQueuedWakes(f.home); got != 0 {
		t.Fatalf("queued wakes after delivery = %d, want 0 (the wake was claimed and delivered)", got)
	}
}
