package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/harness"
)

// PiAdapter implements harness adapter for Pi. It generates the Pi extension
// TypeScript file that hooks into Pi's extension API for session-start,
// wake claim/follow-up (via agent_settled), turn-end guard, pre-tool checks, and scope gate.
type PiAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// piExtensionSource is the TypeScript extension source. BINPATH is substituted
// at install time with the munsu binary path (JSON-encoded string), and
// WRITETOOLS with the Pi native file-write tool names (JSON array literal).
//
// Uses agent_settled (not agent_end) for wake claim/follow-up because Pi may
// still auto-retry, auto-compact and retry, or continue with queued follow-up
// messages after agent_end. agent_settled is the correct event for status
// integrations that need to know Pi will not continue running automatically.
const piExtensionSource = `// munsu-integrate v1 -- do not edit this section
// Pi integration extension for munsu — auto-generated.
// Run "munsu integrate install --harness pi" to regenerate.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const MUNSU_BIN: string = BINPATH;
const MUNSU_WRITE_TOOLS: string[] = WRITETOOLS;
const SESSION_CONSUMER = "munsu:wake";

// Strict contract parser — rejects malformed, missing fields, wrong schema/kind/status.
function parseContract<T>(raw: string, expectKind: string): { ok: true; data: T } | { ok: false; reason: string } {
  if (!raw || !raw.trim()) return { ok: false, reason: "empty stdout" };
  let parsed: any;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { ok: false, reason: "malformed JSON" };
  }
  if (typeof parsed !== "object" || parsed === null) return { ok: false, reason: "not a JSON object" };
  if (parsed.schema_version !== "munsu.orchestration/v2") return { ok: false, reason: "wrong schema_version: " + String(parsed.schema_version) };
  if (parsed.kind !== expectKind) return { ok: false, reason: "wrong kind: " + String(parsed.kind) + " (expected " + expectKind + ")" };
  if (parsed.status !== "success") return { ok: false, reason: "status is not success: " + String(parsed.status) };
  if (!parsed.data || typeof parsed.data !== "object") return { ok: false, reason: "missing or non-object data" };
  return { ok: true, data: parsed.data as T };
}

// Strict safety check parser — falls closed on any failure.
function parseSafetyCheck(raw: string): { ok: true; gate_refused: boolean; block: boolean; reason?: string } | { ok: false; reason: string } {
  if (!raw || !raw.trim()) return { ok: false, reason: "empty stdout" };
  let parsed: any;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { ok: false, reason: "malformed JSON" };
  }
  if (typeof parsed !== "object" || parsed === null) return { ok: false, reason: "not a JSON object" };
  // Only accept exact envelope: schema_version, kind, status, data.
  if (parsed.schema_version !== "munsu.orchestration/v2") return { ok: false, reason: "wrong schema_version: " + String(parsed.schema_version) };
  if (parsed.kind !== "integrate.safety-check") return { ok: false, reason: "wrong kind: " + String(parsed.kind) };
  if (parsed.status !== "success") return { ok: false, reason: "status is not success: " + String(parsed.status) };
  const data = parsed.data;
  if (!data || typeof data !== "object") return { ok: false, reason: "missing or non-object data" };
  if (typeof data.gate_refused !== "boolean") return { ok: false, reason: "missing or non-boolean gate_refused" };
  if (typeof data.block !== "boolean") return { ok: false, reason: "missing or non-boolean block" };
  return { ok: true, gate_refused: data.gate_refused, block: data.block, reason: data.reason };
}

export default function (pi: ExtensionAPI) {
  // In-memory state for the pending wake claim.
  // Survives reload via pi.appendEntry, reset only on ack or claim expiry.
  let pendingWake: {
    leaseId: string;
    eventIds: string[];
    key: string;
    leaseExpiry: number;
    deliveryState: string;
  } | null = null;
  let claimInFlight = false;

  // 1. Session-start: exactly once per native Pi session (including /reload).
  //    Invokes 'munsu session-start --output json' after safety check passes.
  pi.on("session_start", async (_event, ctx) => {
    // Restore pending wake state from persistent entry.
    // Scan NEWEST-TO-OLDEST, honor the newest entry per lease/generation.
    // An acknowledged tombstone (deliveryState === "acknowledged") or
    // expired newest entry prevents an older pending entry from being resurrected.
    const entries = ctx.sessionManager.getEntries();
    const wakeEntries: Array<{ index: number; data: any }> = [];
    for (let i = 0; i < entries.length; i++) {
      const entry = entries[i];
      if (entry.type === "custom" && (entry as any).customType === "munsu-pending-wake") {
        wakeEntries.push({ index: i, data: (entry as any).data });
      }
      if (entry.type === "custom" && (entry as any).customType === "munsu-session-start") {
        return; // Already dispatched for this session.
      }
    }
    // Process newest-to-oldest
    const now = Date.now();
    let resolvedLeases = new Set<string>();
    for (let i = wakeEntries.length - 1; i >= 0; i--) {
      const we = wakeEntries[i];
      const d = we.data;
      if (!d || !d.leaseId) continue;
      const leaseId = d.leaseId;
      // If we already processed a newer entry for this lease, skip.
      if (resolvedLeases.has(leaseId)) continue;
      resolvedLeases.add(leaseId);
      // Tombstone check: acknowledged means no resurrection.
      if (d.deliveryState === "acknowledged") continue;
      // Expiry check: expired newest entry prevents resurrection.
      if (d.leaseExpiry && d.leaseExpiry <= now) continue;
      // Valid pending entry
      if (d.key && d.leaseExpiry > now) {
        pendingWake = {
          leaseId: d.leaseId,
          eventIds: d.eventIds || [],
          key: d.key,
          leaseExpiry: d.leaseExpiry,
          deliveryState: d.deliveryState || "pending",
        };
      }
      break; // Newest relevant entry found
    }

    // Check scope/gate safety before session-start (fail closed).
    const safetyResult = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--output", "json",
    ]);
    if (safetyResult.code !== 0) { ctx.ui.notify("munsu: safety check failed (exit code " + safetyResult.code + ")", "error"); return; }
    const safety = parseSafetyCheck(safetyResult.stdout);
    if (!safety.ok) {
      ctx.ui.notify("munsu: safety check failed (" + safety.reason + "). Cannot start session.", "warning");
      return;
    }
    if (safety.gate_refused) {
      ctx.ui.notify("munsu: scope/gate refuses action in this directory. Use a non-gated primary checkout.", "warning");
      return;
    }

    // Invoke munsu session-start --output json after safety succeeds.
    const sessionResult = await pi.exec(MUNSU_BIN, ["session-start", "--output", "json"]);
    if (sessionResult.code !== 0) { ctx.ui.notify("munsu: session-start failed (exit code " + sessionResult.code + ")", "error"); return; }
    const sessionParsed = parseContract<{ message?: string; state?: string }>(sessionResult.stdout, "session.start");
    if (!sessionParsed.ok) {
      ctx.ui.notify("munsu: session-start failed (" + sessionParsed.reason + ")", "error");
      return;
    }
    // Display useful result
    const displayText = sessionParsed.data.message || sessionParsed.data.state || "session started";
    ctx.ui.notify("munsu: " + displayText, "info");

    // Append exactly-once entry only after successful consumption.
    pi.appendEntry("munsu-session-start", { timestamp: Date.now() });

    // Set widget with structured session-start output lines.
    const lines = sessionResult.stdout.trim().split("\n").filter((l: string) => l.trim());
    ctx.ui.setWidget("munsu-session", lines);
  });

  // 2. agent_settled -> wake claim -> followUp (one per claimed wake, no loop).
  //    agent_settled fires after all automatic retry, compaction, and queued
  //    follow-up messages are exhausted. agent_end is NOT sufficient for
  //    status integration because Pi may still continue running.
  pi.on("agent_settled", async (_event, ctx) => {
    if (!ctx.isIdle() || pendingWake || claimInFlight) return;
    const modeResult = await pi.exec(MUNSU_BIN, ["config", "get", "wake-delivery-mode", "--output", "json"]);
    if (modeResult.code === 0) {
      const mode = parseContract<{ message: string }>(modeResult.stdout, "message");
      if (!mode.ok || mode.data.message !== "native") return;
    }
    claimInFlight = true;
    try {

    // Check scope/gate safety before claiming (fail closed).
    const safetyCheck = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--output", "json",
    ]);
    if (safetyCheck.code !== 0) return;
    const safety = parseSafetyCheck(safetyCheck.stdout);
    if (!safety.ok || safety.gate_refused) return;

    const result = await pi.exec(MUNSU_BIN, [
      "wake", "claim", "--consumer", SESSION_CONSUMER,
      "--lease-captains", "120", "--limit", "1",
      "--output", "json",
    ]);
    if (result.code !== 0) return;

    const parsed = parseContract<{
      claim_id: string;
      wake_id?: string;
      key?: string;
      lease_expires?: number;
      summary?: string;
    }>(result.stdout, "wake.claim");
    if (!parsed.ok) return;  // Parse failure: preserve claim/pending state, do not silently discard

    const d = parsed.data;
    const claimId = d.claim_id;
    if (typeof claimId !== "string" || !claimId.trim()) return;  // Non-empty string claim_id required

    // Require numeric finite lease_expires; do not silently default a malformed value.
    if (typeof d.lease_expires !== "number" || !isFinite(d.lease_expires)) return;
    const leaseExpiry = d.lease_expires * 1000;

    const wakeId = d.wake_id || "";
    const eventIds = wakeId ? wakeId.split(",").filter((id: string) => id.trim()) : [];
    if (eventIds.length === 0) return;
    const key = d.key || eventIds[0];
    const summary = d.summary || "wake";

    pendingWake = { leaseId: claimId, eventIds, key, leaseExpiry, deliveryState: "pending" };

    // Persist pending wake before sendUserMessage for reload durability.
    pi.appendEntry("munsu-pending-wake", {
      leaseId: claimId,
      eventIds: eventIds,
      key: key,
      leaseExpiry: leaseExpiry,
      deliveryState: "pending",
    });

    pi.sendUserMessage(
      "Wake: " + summary + "\n\nAfter checking the wake, call munsu_wake_resolve with key " + key + " and a non-empty summary. Do not use munsu report or print a slash command.",
      { deliverAs: "followUp" },
    );
    } finally {
      claimInFlight = false;
    }
  });

  async function resolvePendingWake(text: string, ctx: any): Promise<boolean> {
    if (!pendingWake || !text) return false;
    const match = text.match(/(?:^|\n)\s*\/munsu:wake\s+resolved\s+\[key=([^\]]+)\]\s*:\s*(\S[^\n]*)/);
    if (!match || match[1] !== pendingWake.key || !match[2].trim()) return false;
    if (pendingWake.eventIds.length !== 1) return false;
    const ackArgs = ["wake", "resolve", "--claim-id", pendingWake.leaseId, "--event-id", pendingWake.eventIds[0], "--summary", match[2].trim(), "--output", "json"];
    const ackResult = await pi.exec(MUNSU_BIN, ackArgs);
    if (ackResult.code !== 0) return false;
    const ackParsed = parseContract<{ claim_id: string; state: string }>(ackResult.stdout, "wake.resolve");
    if (!ackParsed.ok || ackParsed.data.claim_id !== pendingWake.leaseId) return false;
    pi.appendEntry("munsu-pending-wake", {
      leaseId: pendingWake.leaseId,
      eventIds: pendingWake.eventIds,
      key: pendingWake.key,
      leaseExpiry: 0,
      deliveryState: "acknowledged",
    });
    ctx.ui.notify("Wake " + pendingWake.key + " resolved: " + match[2].trim(), "info");
    pendingWake = null;
    return true;
  }

  pi.registerTool({
    name: "munsu_wake_resolve",
    label: "Resolve munsu wake",
    description: "Resolve the currently pending munsu wake after checking its payload. Use the exact key from the wake prompt.",
    promptSnippet: "Resolve the current durable munsu wake by exact key.",
    promptGuidelines: ["Use munsu_wake_resolve for Wake prompts; do not print slash commands or use munsu report to resolve a wake."],
    parameters: {
      type: "object",
      properties: {
        key: { type: "string", description: "Exact key shown in the Wake prompt" },
        summary: { type: "string", description: "Non-empty resolution summary" },
      },
      required: ["key", "summary"],
      additionalProperties: false,
    } as any,
    async execute(_toolCallId: string, params: { key: string; summary: string }, _signal: any, _onUpdate: any, ctx: any) {
      if (!pendingWake) return { content: [{ type: "text", text: "No pending wake." }], details: {}, isError: true };
      if (params.key !== pendingWake.key || !params.summary.trim()) {
        return { content: [{ type: "text", text: "Wake key mismatch or empty summary." }], details: {}, isError: true };
      }
      const marker = "/munsu:wake resolved [key=" + params.key + "]: " + params.summary.trim();
      const resolved = await resolvePendingWake(marker, ctx);
      return {
        content: [{ type: "text", text: resolved ? "Wake acknowledged." : "Wake acknowledgment failed; lease preserved." }],
        details: { key: params.key, resolved },
        isError: !resolved,
      };
    },
  });

  // 3. Turn-end resolves model-produced wake markers, then performs only
  //    non-delivery guard/UI work. agent_settled is the sole claim producer.
  pi.on("turn_end", async (event, ctx) => {
    const message = event && event.message;
    if (message && message.role === "assistant") {
      const text = Array.isArray(message.content)
        ? message.content.filter((part: any) => part && part.type === "text").map((part: any) => part.text || "").join("\n")
        : "";
      if (await resolvePendingWake(text, ctx)) return;
    }
    if (!ctx.isIdle() || pendingWake) return;

    // Check scope/gate safety before guard (fail closed).
    const safetyCheck = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--output", "json",
    ]);
    if (safetyCheck.code !== 0) return;
    const safety = parseSafetyCheck(safetyCheck.stdout);

    // 3a. MUNSU_ROLE nudge: remind captain/soldier to report material outcomes.
    //     NEVER auto-fabricate a status line from chat content.
    const munsuRole = process.env.MUNSU_ROLE || "";
    if (munsuRole === "captain" || munsuRole === "soldier") {
      ctx.ui.notify(
        "munsu: " + (munsuRole === "captain" ? "Captain" : "Soldier") +
        " — report material outcomes via: munsu report <state> \"<msg>\" [--key <slug>]",
        "info"
      );
    }

    await pi.exec(MUNSU_BIN, ["guard", "--output", "json"]);
  });

  // 4. Pre-tool safety checks — block unsafe operations. Fail closed on any error.
  pi.on("tool_call", async (event: any, ctx: any) => {
    // Native file-write tools never go through the shell, so a bash-only
    // check leaves the most common write path unguarded. They carry their
    // target as a path argument instead of a command string.
    const toolName: string = String(event.toolName ?? "");
    const isBash = toolName === "bash";
    const isWrite = MUNSU_WRITE_TOOLS.indexOf(toolName) !== -1;
    if (!isBash && !isWrite) return;

    const checkArgs: string[] = ["integrate", "safety-check", ctx.cwd || "."];
    if (isBash) {
      const cmd: string = event.input?.command ?? "";
      if (typeof cmd !== "string" || cmd === "") return;
      checkArgs.push("--command", cmd);
    } else {
      const input: any = event.input ?? {};
      const filePath = input.file_path ?? input.filePath ?? input.notebook_path ?? input.path ?? "";
      // No path in the payload means there is nothing location-based to
      // check; leave the call alone rather than refusing an unknown shape.
      if (typeof filePath !== "string" || filePath === "") return;
      checkArgs.push("--file-path", filePath);
    }
    checkArgs.push("--output", "json");

    // Use safety-check command for all tool safety decisions.
    const safetyResult = await pi.exec(MUNSU_BIN, checkArgs);
    if (safetyResult.code !== 0) {
      return { block: true, reason: "Command blocked: safety check failed (exit " + safetyResult.code + ")" };
    }
    const safety = parseSafetyCheck(safetyResult.stdout);
    if (!safety.ok) {
      // Fail closed: cannot verify safety, block the command.
      return { block: true, reason: "Command blocked: safety check failed (" + safety.reason + ")" };
    }
    // The Pi contract reports the command verdict and the scope/gate verdict
    // in separate fields. The other five harnesses receive them already merged
    // (effectiveBlock in runSafetyCheck), so checking only "block" here would
    // silently drop every scope/gate refusal — including the primary-checkout
    // refusal — on Pi alone.
    if (safety.block || safety.gate_refused) {
      return { block: true, reason: safety.reason || "Command blocked by munsu safety policy." };
    }
  });

  // 5. /munsu:wake resolved command — lease-based ack with key syntax.
  pi.registerCommand("munsu:wake", {
    description: "Manage munsu wakes from within Pi. Usage: /munsu:wake resolved [key=<slug>]: <summary>",
    handler: async (args: string, ctx: any) => {
      const trimmed = args.trim();
      if (!trimmed.startsWith("resolved")) {
        ctx.ui.notify("Usage: /munsu:wake resolved [key=<slug>]: <summary>", "warning");
        return;
      }
      if (!pendingWake) {
        ctx.ui.notify("No pending wake to resolve", "warning");
        return;
      }

      // Extract key from [key=<slug>] in the args.
      const keyMatch = trimmed.match(/\[key=([^\]]+)\]/);
      const providedKey = keyMatch ? keyMatch[1] : pendingWake.key;

      // Verify key matches pending wake key.
      if (providedKey !== pendingWake.key) {
        ctx.ui.notify("Key mismatch: provided '" + providedKey + "' does not match pending key '" + pendingWake.key + "'", "warning");
        return; // Fail without ack.
      }

      // Extract summary after "resolved" and optional [key=...].
      // Required format: "resolved [key=<slug>]: <summary>"
      // The colon separator is required.
      let rest = trimmed.replace(/^resolved\s*/, "").replace(/\[key=[^\]]+\]\s*/, "").trim();
      if (!rest.startsWith(":")) {
        ctx.ui.notify("Syntax error: missing ':' after key. Usage: resolved [key=<slug>]: <summary>", "warning");
        return;
      }
      let summary = rest.slice(1).trim();
      if (!summary) {
        ctx.ui.notify("Syntax error: non-empty summary required after ':'. Usage: resolved [key=<slug>]: <summary>", "warning");
        return;
      }

      if (pendingWake.eventIds.length !== 1) {
        ctx.ui.notify("Wake resolution requires exactly one event", "error");
        return;
      }
      const ackArgs = ["wake", "resolve", "--claim-id", pendingWake.leaseId, "--event-id", pendingWake.eventIds[0], "--summary", summary, "--output", "json"];
      const ackResult = await pi.exec(MUNSU_BIN, ackArgs);

      // Reject nonzero code before parsing.
      if (ackResult.code !== 0) {
        ctx.ui.notify("Failed to ack wake (exit " + ackResult.code + ")", "error");
        return;
      }

      const ackParsed = parseContract<{ claim_id: string; state: string }>(ackResult.stdout, "wake.resolve");
      if (ackParsed.ok) {
        ctx.ui.notify("Wake " + pendingWake.key + " resolved: " + summary, "info");

        // Append tombstone entry to mark the wake as acknowledged.
        pi.appendEntry("munsu-pending-wake", {
          leaseId: pendingWake.leaseId,
          eventIds: pendingWake.eventIds,
          key: pendingWake.key,
          leaseExpiry: 0,
          deliveryState: "acknowledged",
        });

        pendingWake = null;
      } else {
        const errMsg = ackResult.stderr || ackParsed.reason || "unknown error";
        ctx.ui.notify("Failed to ack wake: " + errMsg, "error");
        // Keep pendingWake on failure so user can retry.
      }
    },
  });
}
`

// PiExtensionTemplate returns the TypeScript extension source for the Pi adapter,
// with the JSON-encoded munsu binary path substituted.
func PiExtensionTemplate(munsuBinPath string) string {
	// JSON-serialize the path to safely encode spaces, quotes, backslashes.
	source := strings.ReplaceAll(piExtensionSource, "WRITETOOLS", writeToolJSArray(piWriteToolNames))
	data, err := json.Marshal(munsuBinPath)
	if err != nil {
		// Fallback: use %q which is safe for strings without control chars.
		encoded := fmt.Sprintf("%q", munsuBinPath)
		return strings.ReplaceAll(source, "BINPATH", encoded)
	}
	return strings.ReplaceAll(source, "BINPATH", string(data))
}

// GenerateManifest creates the integration manifest for the Pi adapter,
// including a SHA-256 content digest and proper scope.
func GenerateManifest(harnessName string, scope string, caps []Capability, contentDigest string) Manifest {
	capStrs := make([]string, len(caps))
	for i, c := range caps {
		capStrs[i] = string(c)
	}
	return Manifest{
		SchemaVersion: "munsu.integrate/v1",
		Harness:       harnessName,
		Version:       "1.0.0",
		Scope:         scope,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		Capabilities:  capStrs,
		ContentDigest: contentDigest,
	}
}

// InstallPiExtension generates and installs the Pi extension file.
// Returns the target path, whether it was actually written, and the content digest.
func (a *PiAdapter) InstallPiExtension() (targetPath string, written bool, digest string, err error) {
	// Check Pi capability before any filesystem mutation.
	if err := CheckPiCapability(""); err != nil {
		return "", false, "", err
	}

	var extDir string
	if a.Scope == "user" {
		extDir = UserExtensionsDir()
	} else {
		extDir = ProjectExtensionsDir(a.Cwd)
	}
	if extDir == "" {
		return "", false, "", fmt.Errorf("cannot determine extension directory for scope %q", a.Scope)
	}

	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return "", false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	targetPath = filepath.Join(extDir, harness.CanonicalPiIntegrationName)

	for _, name := range harness.PiIntegrationAliasNames() {
		aliasPath := filepath.Join(extDir, name)
		if _, statErr := os.Stat(aliasPath); statErr == nil {
			if !FileContainsOwnershipMarker(aliasPath) {
				return "", false, "", fmt.Errorf("compatibility alias %s exists and is not owned by munsu; move or remove it before retrying", aliasPath)
			}
		} else if !os.IsNotExist(statErr) {
			return "", false, "", fmt.Errorf("checking compatibility alias %s: %w", aliasPath, statErr)
		}
	}

	// Check existing file: if it exists and is not owned, return conflict.
	if existing, statErr := os.Stat(targetPath); statErr == nil && existing.Size() > 0 {
		if !FileContainsOwnershipMarker(targetPath) {
			return "", false, "", fmt.Errorf("conflict: %s exists and is not owned by munsu. Back up manually or remove it first. Use --dry-run to preview", targetPath)
		}
	}

	content := PiExtensionTemplate(munsuBin)
	digest = PiExtensionContentDigest(munsuBin)

	if a.DryRun {
		return targetPath, false, digest, nil
	}

	if err := writeAtomic(targetPath, content, 0644); err != nil {
		return "", false, "", fmt.Errorf("writing extension: %w", err)
	}

	for _, name := range harness.PiIntegrationAliasNames() {
		aliasPath := filepath.Join(extDir, name)
		if err := os.Remove(aliasPath); err != nil && !os.IsNotExist(err) {
			return "", false, "", fmt.Errorf("removing owned compatibility alias %s: %w", aliasPath, err)
		}
	}

	return targetPath, true, digest, nil
}
