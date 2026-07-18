package integrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PiAdapter implements harness adapter for Pi. It generates the Pi extension
// TypeScript file that hooks into Pi's extension API for session-start nudge,
// agent_settled (followUp), turn-end guard, pre-tool checks, and scope gate.
type PiAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

const piExtensionSource = `// munsu-integrate -- do not edit this section
// Pi integration extension for munsu - auto-generated.
// Run "munsu integrate install --harness pi" to regenerate.

import type { ExtensionAPI, ToolCallEvent } from "@earendil-works/pi-coding-agent";

const MUNSU_BIN: string = "BINPATH";

export default function (pi: ExtensionAPI) {
  let sessionStarted = false;
  let pendingWakeKey: string | null = null;

  // 1. Session-start nudge - exactly once per session.
  pi.on("session_start", async (_event, ctx) => {
    if (sessionStarted) return;
    sessionStarted = true;

    const proc = Bun.spawn([MUNSU_BIN, "session-start"], {
      stdout: "pipe",
      stderr: "pipe",
    });
    const output = await new Response(proc.stdout).text();
    ctx.ui.setWidget("munsu-session", output.trim().split("\n"));
  });

  // 2. agent_settled -> wake check -> followUp.
  pi.on("agent_settled", async (_event, ctx) => {
    if (!ctx.isIdle()) return;

    const drainProc = Bun.spawn([MUNSU_BIN, "wake-drain"], {
      stdout: "pipe",
      stderr: "pipe",
    });
    const drainOut = await new Response(drainProc.stdout).text();
    const drainLines = drainOut.trim().split("\n").filter((l) => l.trim());

    for (const line of drainLines) {
      const parts = line.trim().split(/\s+/, 2);
      if (parts.length < 1) continue;
      const wakeKey = parts[0];
      const summary = parts.length > 1 ? parts.slice(1).join(" ") : "wake";

      pendingWakeKey = wakeKey;

      pi.sendUserMessage(
        "Wake: " + summary + "\n\nRespond with:\n" +
        "- /munsu:backlog show to inspect current work\n" +
        "- /munsu:wake resolved [key=" + wakeKey + "] <summary> to close this wake",
        { deliverAs: "followUp" },
      );

      break;
    }
  });

  // 3. Turn-end guard.
  pi.on("turn_end", async (_event, ctx) => {
    const guardProc = Bun.spawn([MUNSU_BIN, "guard"], {
      stdout: "pipe",
      stderr: "pipe",
    });
    const guardOut = await new Response(guardProc.stdout).text();
    const guardText = guardOut.trim();

    if (guardText && ctx.isIdle() && pendingWakeKey === null) {
      const reDrain = Bun.spawn([MUNSU_BIN, "wake-drain"], {
        stdout: "pipe",
        stderr: "pipe",
      });
      const reOut = await new Response(reDrain.stdout).text();
      const reLines = reOut.trim().split("\n").filter((l) => l.trim());
      if (reLines.length > 0) {
        const wakeKey = reLines[0].trim().split(/\s+/, 1)[0];
        pendingWakeKey = wakeKey;
        pi.sendUserMessage(
          "Actionable fleet state remains.\n\nRespond with " +
          "/munsu:wake resolved [key=" + wakeKey + "] <summary> to close.",
          { deliverAs: "followUp" },
        );
      }
    }
  });

  // 4. Pre-tool safety checks.
  pi.on("tool_call", async (event: ToolCallEvent, ctx) => {
    if (event.toolName === "bash" && typeof event.input.command === "string") {
      const cmd = event.input.command;

      if (/munsu\s+watch\s+(arm|ensure)/i.test(cmd)) {
        return { block: true, reason: "Use 'munsu guard' or 'munsu watch run' to inspect; watcher lifecycle is managed automatically." };
      }

      if (/cd\s+.*\.no-mistakes/.test(cmd) && !/guard|doctor/.test(cmd)) {
        return { block: true, reason: "No-mistakes managed directories are not regular projects." };
      }
    }
  });

  // 5. Resolved wake key command.
  pi.registerCommand("munsu:wake", {
    description: "Manage munsu wakes from within Pi. Usage: /munsu:wake resolved [key=...] <summary>",
    handler: async (args, ctx) => {
      const trimmed = args.trim();
      if (trimmed.startsWith("resolved")) {
        const keyMatch = trimmed.match(/\[key=([^\]]+)\]/);
        const key = keyMatch ? keyMatch[1] : pendingWakeKey;

        if (key) {
          const ackProc = Bun.spawn([MUNSU_BIN, "wake", "ack", key], {
            stdout: "pipe",
            stderr: "pipe",
          });
          await new Response(ackProc.stdout).text();
          pendingWakeKey = null;
          ctx.ui.notify("Wake " + key + " resolved", "info");
        }
      }
    },
  });
}
`

// PiExtensionTemplate returns the TypeScript extension source for the Pi adapter,
// with the munsu binary path substituted.
func PiExtensionTemplate(munsuBinPath string) string {
	return stringsReplaceAll(piExtensionSource, "BINPATH", munsuBinPath)
}

// stringsReplaceAll is strings.ReplaceAll (aliased for readability in gen code).
func stringsReplaceAll(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

// InstallPiExtension generates and installs the Pi extension file.
// Returns the target path and whether it was actually written.
func (a *PiAdapter) InstallPiExtension() (string, bool, error) {
	var extDir string
	if a.Scope == "user" {
		extDir = UserExtensionsDir()
	} else {
		extDir = ProjectExtensionsDir(a.Cwd)
	}
	if extDir == "" {
		return "", false, fmt.Errorf("cannot determine extension directory for scope %q", a.Scope)
	}

	munsuBin := a.resolveMunsuPath()
	if munsuBin == "" {
		return "", false, fmt.Errorf("cannot resolve munsu binary path: not on PATH and /proc/self/exe unavailable")
	}

	targetPath := filepath.Join(extDir, "munsu-pi-integration.ts")

	if _, err := os.Stat(targetPath); err == nil {
		if !FileContainsOwnershipMarker(targetPath) {
			if !a.DryRun {
				backupPath := targetPath + ".bak." + time.Now().Format("20060102-150405")
				data, err := os.ReadFile(targetPath)
				if err != nil {
					return "", false, fmt.Errorf("reading existing extension for backup: %w", err)
				}
				if err := writeAtomic(backupPath, string(data), 0644); err != nil {
					return "", false, fmt.Errorf("backup existing extension: %w", err)
				}
			}
		}
	}

	content := PiExtensionTemplate(munsuBin)

	if a.DryRun {
		return targetPath, false, nil
	}

	if err := writeAtomic(targetPath, content, 0644); err != nil {
		return "", false, fmt.Errorf("writing extension: %w", err)
	}

	return targetPath, true, nil
}

// resolveMunsuPath finds the munsu binary.
func (a *PiAdapter) resolveMunsuPath() string {
	if data, err := os.ReadFile("/proc/self/exe"); err == nil {
		return string(data)
	}

	path := os.Getenv("PATH")
	dirs := filepath.SplitList(path)
	for _, dir := range dirs {
		candidate := filepath.Join(dir, "munsu")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}

	return ""
}

// ResetWakeKey is a no-op — state is in the TypeScript extension.
func (a *PiAdapter) ResetWakeKey() {}

// GenerateManifest creates the integration manifest for the Pi adapter.
func GenerateManifest(homeDir string, harnessName string, caps []Capability) Manifest {
	capStrs := make([]string, len(caps))
	for i, c := range caps {
		capStrs[i] = string(c)
	}
	return Manifest{
		SchemaVersion: "munsu.integrate/v1",
		Harness:       harnessName,
		Version:       "1.0.0",
		Scope:         "user",
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		Capabilities:  capStrs,
	}
}
