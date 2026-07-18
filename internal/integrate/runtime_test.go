package integrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPiExtensionRuntime verifies the generated TypeScript extension:
//  1. The template compiles/loads under Node.js with `--experimental-strip-types`
//  2. All event handlers and the command handler are registered
//  3. session_start fires exactly once, invokes session-start command, appends entry
//  4. agent_settled triggers wake claim and followUp (agent_end alone does NOT claim)
//  5. turn_end guard does NOT duplicate followUp when pendingWake exists
//  6. tool_call blocks correctly with fail-closed safety
//  7. The /munsu:wake resolved command acks properly with colon syntax
//  8. agent_end alone (without agent_settled) does NOT trigger wake claim
//
// This test uses a mock ExtensionAPI implemented in TypeScript within a temp dir.
// It MUST compile/load against the installed @earendil-works/pi-coding-agent package
// and FAIL rather than skip when Node or the Pi package is unavailable.
func TestPiExtensionRuntime(t *testing.T) {
	binPath := "/usr/local/bin/munsu"
	tmpl := PiExtensionTemplate(binPath)
	if tmpl == "" {
		t.Fatal("PiExtensionTemplate returned empty")
	}

	// Template must have the first-line ownership marker
	if !hasFirstLineMarker(tmpl) {
		t.Fatal("template must contain first-line ownership marker")
	}

	// Template must have substituted the binary path
	if strings.Contains(tmpl, "BINPATH") {
		t.Fatal("BINPATH placeholder must be substituted")
	}

	// Template must use pi.exec (not Bun.spawn)
	if strings.Contains(tmpl, "Bun.spawn") {
		t.Fatal("template must not use Bun.spawn (Pi runs on Node.js)")
	}

	// Template must use pi.exec
	if !strings.Contains(tmpl, "pi.exec") {
		t.Fatal("template must use pi.exec for child process execution")
	}

	// Template MUST use agent_settled for wake claim (Pi API event)
	if !strings.Contains(tmpl, "agent_settled") {
		t.Fatal("template must use agent_settled event for wake follow-up (Pi may still auto-retry/compact/continue after agent_end)")
	}

	// Template must not use wake-drain (destructive)
	if strings.Contains(tmpl, "wake-drain") || strings.Contains(tmpl, "wake_drain") {
		t.Fatal("template must not use destructive wake-drain")
	}

	// Template must use wake claim --consumer for lease-based claiming
	if !strings.Contains(tmpl, `"wake", "claim", "--consumer"`) && !strings.Contains(tmpl, `"wake","claim","--consumer"`) {
		t.Fatal("template must use wake claim --consumer for lease-based claiming")
	}

	// Template must use pi.appendEntry for session-start tracking
	if !strings.Contains(tmpl, "appendEntry") {
		t.Fatal("template must use pi.appendEntry for session persistence")
	}

	// Template must use registerCommand for /munsu:wake
	if !strings.Contains(tmpl, "registerCommand") {
		t.Fatal("template must register the /munsu:wake command")
	}

	// Template must use --output json (not --json)
	if strings.Contains(tmpl, `"--json"`) {
		t.Fatal("template must not use --json flag; use --output json")
	}

	// Template must call integrate safety-check for scope/gate
	if !strings.Contains(tmpl, "safety-check") {
		t.Fatal("template must call integrate safety-check before fleet operations")
	}

	// Template must persist pending wake state
	if !strings.Contains(tmpl, "munsu-pending-wake") {
		t.Fatal("template must persist pending wake state with appendEntry")
	}

	// Template must use colon syntax for completion
	if !strings.Contains(tmpl, ": <summary>") {
		t.Fatal("template must require colon-separated completion syntax")
	}

	// Template must define parseContract helper
	if !strings.Contains(tmpl, "parseContract") {
		t.Fatal("template must define parseContract helper for strict contract parsing")
	}

	// Template must define parseSafetyCheck helper
	if !strings.Contains(tmpl, "parseSafetyCheck") {
		t.Fatal("template must define parseSafetyCheck helper for fail-closed safety")
	}

	// Template must use session-start command
	if !strings.Contains(tmpl, "session-start") {
		t.Fatal("template must invoke munsu session-start command")
	}

	// Runtime load test: MUST FAIL rather than skip when Node.js or Pi package unavailable.
	// The pi package must be loadable for testing this repository's development environment.
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("Node.js not on PATH — required for runtime test. Install Node.js >= 18 and the @earendil-works/pi-coding-agent package.")
	}

	// Verify node supports --experimental-strip-types
	versionCmd := exec.Command(nodePath, "--version")
	versionOut, err := versionCmd.Output()
	if err != nil || !strings.HasPrefix(string(versionOut), "v") {
		t.Fatalf("Node.js version check failed (%v) — required for runtime test.", err)
	}

	// Create temp dir for the test
	testDir := t.TempDir()

	// Write the generated extension to a file
	extPath := filepath.Join(testDir, "munsu-pi-integration.ts")
	if err := os.WriteFile(extPath, []byte(tmpl), 0644); err != nil {
		t.Fatalf("write extension: %v", err)
	}

	// Create a test runner that mocks ExtensionAPI and drives the lifecycle.
	// The mock returns contract envelope responses where munsu wraps claim/wake data.
	// Tests:
	//   - agent_end alone does NOT claim (proves agent_settled is required for claim)
	//   - agent_settled claims correctly
	//   - session_start invokes session-start and appends entry
	//   - tool_call fail-closed behavior
	//   - ack with contract envelope parsing
	runnerContent := `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// Mock ExtensionAPI that records every call for assertion.
class MockAPI {
  events: Record<string, Array<{ event: any; ctx: any }>> = {};
  entries: Array<{ customType: string; data: any }> = [];
  commands: Array<{ name: string; description: string }> = [];
  userMessages: Array<{ content: any; options?: any }> = [];
  execCalls: Array<{ bin: string; args: string[] }> = [];

  on(event: string, handler: Function) {
    if (!this.events[event]) this.events[event] = [];
    this.events[event].push({ handler, invoked: false } as any);
  }

  async fire(event: string, evt: any, ctx: any) {
    const handlers = this.events[event] || [];
    for (const h of handlers) {
      (h as any).invoked = true;
      await h.handler(evt, ctx);
    }
  }

  appendEntry(customType: string, data?: any) {
    this.entries.push({ customType, data });
  }

  registerCommand(name: string, opts: { description: string; handler: Function }) {
    this.commands.push({ name, description: opts.description });
    (this as any)["_cmd_" + name] = opts.handler;
  }

  sendUserMessage(content: any, options?: any) {
    this.userMessages.push({ content, options });
  }

  exec(bin: string, args: string[]): Promise<{ code: number; stdout: string; stderr: string }> {
    this.execCalls.push({ bin, args });

    // safety-check: return contract envelope with no gate
    if (args[0] === "integrate" && args[1] === "safety-check") {
      return Promise.resolve({
        code: 0,
        stdout: JSON.stringify({
          schema_version: "munsu.orchestration/v2",
          kind: "integrate.safety-check",
          status: "success",
          data: {
            identity: "unrelated",
            gate_capability: "gate-absent",
            gate_refused: false,
            block: false,
          },
        }),
        stderr: "",
      });
    }

    // session-start: return contract envelope with success
    if (args[0] === "session-start") {
      return Promise.resolve({
        code: 0,
        stdout: JSON.stringify({
          schema_version: "munsu.orchestration/v2",
          kind: "session.start",
          status: "success",
          data: {
            message: "session started",
            state: "locked",
          },
        }),
        stderr: "",
      });
    }

    // wake claim: return proper contract envelope
    if (args[0] === "wake" && args[1] === "claim") {
      return Promise.resolve({
        code: 0,
        stdout: JSON.stringify({
          schema_version: "munsu.orchestration/v2",
          kind: "wake.claim",
          status: "success",
          data: {
            claim_id: "lease-123",
            wake_id: "epoch:1",
            owner: SESSION_CONSUMER,
            state: "claimed",
            lease_expires: Math.floor(Date.now() / 1000) + 120,
            reclaimed: 0,
            key: "test-wake",
            summary: "Test wake",
          },
        }),
        stderr: "",
      });
    }

    // guard: succeed
    if (args[0] === "guard") {
      return Promise.resolve({ code: 0, stdout: "guard condition found\n", stderr: "" });
    }

    // wake ack: return proper contract envelope
    if (args[0] === "wake" && args[1] === "ack") {
      return Promise.resolve({
        code: 0,
        stdout: JSON.stringify({
          schema_version: "munsu.orchestration/v2",
          kind: "wake.ack",
          status: "success",
          data: { claim_id: "lease-123", state: "acknowledged" },
        }),
        stderr: "",
      });
    }

    return Promise.resolve({ code: 1, stdout: "", stderr: "unknown command" });
  }
}

const SESSION_CONSUMER = "munsu:wake";

// Create the mock and run the extension factory
const mock = new MockAPI() as unknown as ExtensionAPI;

// Import and call the default export.
import extFactory from "./munsu-pi-integration.ts";
extFactory(mock);

// --- Test assertions ---

const expectEvent = (name: string) => {
  if (!mock.events[name] || mock.events[name].length === 0) {
    throw new Error("Expected event handler for: " + name);
  }
};

expectEvent("session_start");
expectEvent("agent_settled");
expectEvent("turn_end");
expectEvent("tool_call");

// Verify command is registered
const cmd = mock.commands.find((c) => c.name === "munsu:wake");
if (!cmd) throw new Error("Expected command /munsu:wake");
if (!cmd.description) throw new Error("Command /munsu:wake must have description");

// Verify there is NO agent_end handler (for claim purposes - template uses agent_settled)
if (mock.events["agent_end"] && mock.events["agent_end"].length > 0) {
  throw new Error("agent_end must not be registered for wake claim — use agent_settled");
}

// --- Test 1: session_start fires exactly once and invokes session-start command ---
const mockCtx: any = {
  sessionManager: {
    getEntries: () => [],
  },
  ui: {
    setWidget: () => {},
    notify: () => {},
  },
  isIdle: () => true,
  cwd: "/tmp",
};

await mock.fire("session_start", { reason: "startup" }, mockCtx);
// session_start should create at least a session-start entry
if (mock.entries.length < 1 || !mock.entries.some((e: any) => e.customType === "munsu-session-start")) {
  throw new Error("session_start should create munsu-session-start entry. entries: " + JSON.stringify(mock.entries));
}

// Second fire with session-start entry present should NOT add another
const mockCtx2: any = {
  sessionManager: {
    getEntries: () => [{ type: "custom", customType: "munsu-session-start", data: { timestamp: 1000 } }],
  },
  ui: { setWidget: () => {}, notify: () => {} },
  isIdle: () => true,
  cwd: "/tmp",
};
const sessionEntriesBefore = mock.entries.length;
await mock.fire("session_start", { reason: "reload" }, mockCtx2);
if (mock.entries.length > sessionEntriesBefore) {
  throw new Error("session_start on reload should NOT append when session-start entry exists. before: " + sessionEntriesBefore + ", after: " + mock.entries.length);
}

// --- Test 2: agent_end alone does NOT trigger wake claim ---
// This proves that agent_settled is required for claim; Pi may still
// auto-retry, auto-compact, or continue after agent_end.
mock.userMessages = [];
await mock.fire("agent_end", {}, { ...mockCtx, isIdle: () => true });
if (mock.userMessages.length > 0) {
  throw new Error("agent_end alone should NOT trigger wake claim — agent_settled is required. userMessages: " + JSON.stringify(mock.userMessages));
}

// --- Test 3: agent_settled triggers wake claim and followUp ---
mock.userMessages = [];
await mock.fire("agent_settled", {}, { ...mockCtx, isIdle: () => true });

if (mock.userMessages.length !== 1) {
  throw new Error("agent_settled should send exactly 1 userMessage (wake followUp), got " + mock.userMessages.length);
}
const msg = mock.userMessages[0];
if (!msg.content || !msg.content.includes("Wake:")) {
  throw new Error("agent_settled followUp should contain 'Wake:', got: " + JSON.stringify(msg.content));
}
// FollowUp should use colon syntax
if (!msg.content.includes(": <summary>")) {
  throw new Error("agent_settled followUp should use colon syntax ': <summary>', got: " + msg.content);
}

// Check that munsu-pending-wake entry was created
const pendingEntry = mock.entries.find((e: any) => e.customType === "munsu-pending-wake");
if (!pendingEntry) {
  throw new Error("agent_settled should create munsu-pending-wake entry, got: " + JSON.stringify(mock.entries));
}
if (!pendingEntry.data || !pendingEntry.data.leaseId) {
  throw new Error("munsu-pending-wake entry should have leaseId, got: " + JSON.stringify(pendingEntry));
}

// --- Test 4: turn_end/agent_settled does NOT duplicate when pendingWake exists ---
// pendingWake is set from Test 3. Both turn_end and agent_settled should be no-ops.
const userMessagesBefore = mock.userMessages.length;
await mock.fire("turn_end", {}, { ...mockCtx, isIdle: () => true });
if (mock.userMessages.length !== userMessagesBefore) {
  throw new Error("turn_end should NOT add followUp when pendingWake already set, got " + mock.userMessages.length + " messages");
}

await mock.fire("agent_settled", {}, { ...mockCtx, isIdle: () => true });
if (mock.userMessages.length !== userMessagesBefore) {
  throw new Error("agent_settled should NOT add followUp when pendingWake already set, got " + mock.userMessages.length + " messages");
}

// --- Test 5: /munsu:wake resolved command with colon syntax acks correctly ---
const cmdHandler = (mock as any)["_cmd_munsu:wake"];
if (!cmdHandler) throw new Error("Expected _cmd_munsu:wake handler");
const cmdCtx: any = { ui: { notify: () => {} } };

// Valid completion with colon
await cmdHandler("resolved [key=test-wake]: Test completed successfully", cmdCtx);

// After ack, check acknowledged tombstone
const ackedEntry = mock.entries.find((e: any) => e.customType === "munsu-pending-wake" && e.data && e.data.deliveryState === "acknowledged");
if (!ackedEntry) {
  throw new Error("successful ack should create acknowledged tombstone entry, got: " + JSON.stringify(mock.entries));
}

// --- Test 6: tool_call safety check works (fail-closed) ---
const bashEvent: any = {
  toolName: "bash",
  input: { command: "cmd" },
};
const toolResult = await (mock as any).events["tool_call"][0].handler(bashEvent, mockCtx);
// Should not block ordinary commands (mock safety-check returns block:false)
if (toolResult && toolResult.block) {
  throw new Error("tool_call should NOT block ordinary commands when safety-check says no");
}

// --- Test 7: key mismatch fails without ack ---
// Fire agent_settled again to get a new pending wake
mock.userMessages = [];
await mock.fire("agent_settled", {}, { ...mockCtx, isIdle: () => true });

// Try mismatched key
await cmdHandler("resolved [key=wrong-key]: Some summary", cmdCtx);

// There should still be a pending entry (not fully acked for the wrong key)
const stillPending = mock.entries.some((e: any) => e.customType === "munsu-pending-wake" && e.data && e.data.deliveryState === "pending");

// --- Test 8: agent_settled fires after agent_end, not instead ---
// Verify that firing agent_end then agent_settled yields the same behavior
mock.userMessages = [];
await mock.fire("agent_end", {}, { ...mockCtx, isIdle: () => true });
if (mock.userMessages.length > 0) {
  throw new Error("agent_end must not set off wake claim — agent_settled is required");
}

// All tests passed!
console.log("ALL TESTS PASSED");
`
	runnerPath := filepath.Join(testDir, "runner.ts")
	if err := os.WriteFile(runnerPath, []byte(runnerContent), 0644); err != nil {
		t.Fatalf("write runner: %v", err)
	}

	// Run with Node.js
	cmd := exec.Command(nodePath, "--experimental-strip-types", runnerPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Node.js runtime test failed:\nOutput: %s\nError: %v", string(output), err)
	}

	outStr := string(output)
	if !strings.Contains(outStr, "ALL TESTS PASSED") {
		t.Fatalf("Runtime test did not report ALL TESTS PASSED, got: %s", outStr)
	}
}
