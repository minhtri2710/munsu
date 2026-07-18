package integrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PiAdapter implements harness adapter for Pi. It generates the Pi extension
// TypeScript file that hooks into Pi's extension API for session-start nudge,
// wake claim/followUp, turn-end guard, pre-tool checks, and scope gate.
type PiAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// piExtensionSource is the TypeScript extension source. BINPATH is substituted
// at install time with the munsu binary path (JSON-encoded string).
const piExtensionSource = `// munsu-integrate v1 -- do not edit this section
// Pi integration extension for munsu — auto-generated.
// Run "munsu integrate install --harness pi" to regenerate.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const MUNSU_BIN: string = BINPATH;
const SESSION_CONSUMER = "munsu:wake";

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

  // 1. Session-start: exactly once per native Pi session (including /reload).
  pi.on("session_start", async (_event, ctx) => {
    // Restore pending wake state from persistent entry.
    for (const entry of ctx.sessionManager.getEntries()) {
      if (entry.type === "custom" && (entry as any).customType === "munsu-pending-wake") {
        const data = (entry as any).data;
        if (data && data.leaseId && data.key) {
          const now = Date.now();
          if (data.leaseExpiry && data.leaseExpiry > now) {
            pendingWake = {
              leaseId: data.leaseId,
              eventIds: data.eventIds || [],
              key: data.key,
              leaseExpiry: data.leaseExpiry,
              deliveryState: data.deliveryState || "pending",
            };
          }
          // If lease expired, clear it silently.
        }
        break;
      }
      if (entry.type === "custom" && (entry as any).customType === "munsu-session-start") {
        return; // Already dispatched for this session.
      }
    }

    // Check scope/gate safety before session-start (fail closed).
    const safetyResult = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--output", "json",
    ]);
    if (safetyResult.code === 0 && safetyResult.stdout.trim()) {
      try {
        const envData = JSON.parse(safetyResult.stdout);
        const scData = envData.data || envData;
        if (scData.gate_refused) {
          ctx.ui.notify("munsu: scope/gate refuses action in this directory. Use a non-gated primary checkout.", "warning");
          return;
        }
      } catch (_e) { /* best-effort */ }
    }

    pi.appendEntry("munsu-session-start", { timestamp: Date.now() });
    const lines = safetyResult.stdout.trim().split("\n").filter((l: string) => l.trim());
    ctx.ui.setWidget("munsu-session", lines);
  });

  // 2. agent_end -> wake claim -> followUp (one per claimed wake, no loop).
  pi.on("agent_end", async (_event, ctx) => {
    if (!ctx.isIdle() || pendingWake) return;

    // Check scope/gate safety before claiming.
    const safetyCheck = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--output", "json",
    ]);
    if (safetyCheck.code === 0 && safetyCheck.stdout.trim()) {
      try {
        const envData = JSON.parse(safetyCheck.stdout);
        const scData = envData.data || envData;
        if (scData.gate_refused) return;
      } catch (_e) { /* best-effort */ }
    }

    const result = await pi.exec(MUNSU_BIN, [
      "wake", "claim", "--consumer", SESSION_CONSUMER,
      "--lease-seconds", "120", "--limit", "1",
      "--output", "json",
    ]);
    if (result.code !== 0 || !result.stdout.trim()) return;

    try {
      const envelope = JSON.parse(result.stdout);
      // Parse the munsu contract envelope: {schema_version, kind, status, data: {claim_id, wake_id, ...}}
      const data = envelope.data || envelope;
      const claimId = data.claim_id ?? "";
      const wakeId = data.wake_id ?? "";
      if (!claimId) return;

      // Parse event IDs from wake_id (comma-separated epoch:seq pairs).
      const eventIds = wakeId ? wakeId.split(",").filter((id: string) => id.trim()) : [];
      // Derive a human-readable key from the wake data or use the claim ID.
      const key = data.key ?? eventIds[0] ?? claimId;
      const leaseExpiry = data.lease_expires ? (data.lease_expires * 1000) : (Date.now() + 120000);
      const summary = data.summary ?? "wake";
      const keyAttr = key !== claimId ? " [key=" + key + "]" : "";

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
        "Wake: " + summary + "\n\nRespond with:\n" +
        "- /munsu:wake resolved" + keyAttr + ": <summary> to close this wake",
        { deliverAs: "followUp" },
      );
    } catch (_e) {
      // JSON parse failure — skip silently.
    }
  });

  // 3. Turn-end guard: only fires when idle, no pending followUp, and
  //    actionable fleet state exists.
  pi.on("turn_end", async (_event, ctx) => {
    if (!ctx.isIdle() || pendingWake) return;

    // Check scope/gate safety before guard.
    const safetyCheck = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--output", "json",
    ]);
    if (safetyCheck.code === 0 && safetyCheck.stdout.trim()) {
      try {
        const envData = JSON.parse(safetyCheck.stdout);
        const scData = envData.data || envData;
        if (scData.gate_refused) return;
      } catch (_e) { /* best-effort */ }
    }

    const guardResult = await pi.exec(MUNSU_BIN, ["guard", "--output", "json"]);
    if (guardResult.code !== 0 || !guardResult.stdout.trim()) return;

    // If guard found issues, try to claim a wake.
    const claimResult = await pi.exec(MUNSU_BIN, [
      "wake", "claim", "--consumer", SESSION_CONSUMER,
      "--lease-seconds", "120", "--limit", "1",
      "--output", "json",
    ]);
    if (claimResult.code !== 0 || !claimResult.stdout.trim()) return;

    try {
      const envelope = JSON.parse(claimResult.stdout);
      const data = envelope.data || envelope;
      const claimId = data.claim_id ?? "";
      if (!claimId) return;

      const wakeId = data.wake_id ?? "";
      const eventIds = wakeId ? wakeId.split(",").filter((id: string) => id.trim()) : [];
      const key = data.key ?? eventIds[0] ?? claimId;
      const leaseExpiry = data.lease_expires ? (data.lease_expires * 1000) : (Date.now() + 120000);
      const keyAttr = key !== claimId ? " [key=" + key + "]" : "";

      pendingWake = { leaseId: claimId, eventIds, key, leaseExpiry, deliveryState: "pending" };

      pi.appendEntry("munsu-pending-wake", {
        leaseId: claimId,
        eventIds: eventIds,
        key: key,
        leaseExpiry: leaseExpiry,
        deliveryState: "pending",
      });

      pi.sendUserMessage(
        "Actionable fleet state remains.\n\n" +
        "Respond with /munsu:wake resolved" + keyAttr + ": <summary> to close.",
        { deliverAs: "followUp" },
      );
    } catch (_e) {
      // JSON parse failure — skip silently.
    }
  });

  // 4. Pre-tool safety checks — block unsafe watcher lifecycle and managed clones.
  pi.on("tool_call", async (event: any, ctx: any) => {
    if (event.toolName !== "bash") return;
    const cmd: string = event.input?.command ?? "";
    if (typeof cmd !== "string") return;

    // Use safety-check command for all tool safety decisions.
    const safetyResult = await pi.exec(MUNSU_BIN, [
      "integrate", "safety-check", ctx.cwd || ".",
      "--command", cmd,
      "--output", "json",
    ]);
    if (safetyResult.code === 0 && safetyResult.stdout.trim()) {
      try {
        const envelope = JSON.parse(safetyResult.stdout);
        const scData = envelope.data || envelope;
        if (scData.block) {
          return { block: true, reason: scData.reason || "Command blocked by munsu safety policy." };
        }
      } catch (_e) { /* best-effort */ }
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

      // Ack the wake with lease ID and event IDs.
      const ackArgs = ["wake", "ack", pendingWake.leaseId, ...pendingWake.eventIds, "--output", "json"];
      const ackResult = await pi.exec(MUNSU_BIN, ackArgs);

      if (ackResult.code === 0) {
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
        const errMsg = ackResult.stderr || "unknown error";
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
	data, err := json.Marshal(munsuBinPath)
	if err != nil {
		// Fallback: use %q which is safe for strings without control chars.
		encoded := fmt.Sprintf("%q", munsuBinPath)
		return strings.ReplaceAll(piExtensionSource, "BINPATH", encoded)
	}
	return strings.ReplaceAll(piExtensionSource, "BINPATH", string(data))
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

	targetPath = filepath.Join(extDir, "munsu-pi-integration.ts")

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

	return targetPath, true, digest, nil
}
