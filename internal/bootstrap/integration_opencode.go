// Package integrate manages opt-in harness integration.
//
// OpenCode adapter: generates .opencode/plugins/*.js plugin files anchored to
// the munsu binary path, mirroring the munsu OpenCode plugin contract.
//
// OpenCode plugin model: JS files in .opencode/plugins/*.js. Each exports an
// async function that returns hook/event handlers. The plugin runtime is the
// wake mechanism (persistent TUI; not headless `opencode run`).
//
// PreToolUse deny mechanism: the checker writes the reason to stderr and exits 2.
// The plugin reads `result.stderr.trim()` and throws `new Error(...)` which
// blocks the bash command (verified against OpenCode 1.17.15).
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// opencodePluginsDir returns the path to .opencode/plugins/ for the given scope.
func opencodePluginsDir(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home: %w", err)
		}
		return filepath.Join(home, ".opencode", "plugins"), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		return filepath.Join(canonical, ".opencode", "plugins"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// opencodePluginFileNames lists the 4 munsu-owned OpenCode plugin file names.
var opencodePluginFileNames = []string{
	"munsu-pretool-check.js",
	"munsu-sessionstart-nudge.js",
	"munsu-turnend-guard.js",
	"munsu-watch-arm.js",
}

// opencodePluginCommand wraps a munsu binary path in JSON-quoted form for
// embedding in a JS string literal.
func opencodePluginCommand(munsuBin string) string {
	// JSON-marshal the path to handle spaces/special chars, then strip quotes
	// for JS string embedding.
	return strings.ReplaceAll(fmt.Sprintf("%q", munsuBin), `"`, `\"`)
}

// opencodePluginContent generates the JS plugin content for the given plugin name,
// embedding the absolute munsu binary path.
func opencodePluginContent(munsuBin, pluginName string) string {
	binJSON := opencodePluginCommand(munsuBin)

	switch pluginName {
	case "munsu-pretool-check.js":
		return opencodePretoolCheckPlugin(binJSON)
	case "munsu-sessionstart-nudge.js":
		return opencodeSessionstartNudgePlugin(binJSON)
	case "munsu-turnend-guard.js":
		return opencodeTurnendGuardPlugin(binJSON)
	case "munsu-watch-arm.js":
		return opencodeWatchArmPlugin(binJSON)
	default:
		return ""
	}
}

// opencodePretoolCheckPlugin generates the pre-tool check plugin JS.
//
// Mirrors the munsu pretool-check plugin contract:
// - `tool.execute.before` handler checks input?.tool !== "bash"
// - Gets command from output?.args?.command (OpenCode uses --command arg, not stdin JSON)
// - Spawns checker with ["--command", command]
// - If exit code === 2, throws new Error(stderr.trim()) to block the bash command
func opencodePretoolCheckPlugin(munsuBin string) string {
	return fmt.Sprintf(`import { spawn } from "node:child_process";
import { realpathSync } from "node:fs";
import { resolve } from "node:path";

const MUNSU_BIN = "%s";
const MUNSU_WRITE_TOOLS = %s;

function runProcess(command, args) {
  return new Promise((resolvePromise) => {
    const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += chunk.toString(); });
    child.stderr.on("data", (chunk) => { stderr += chunk.toString(); });
    child.on("error", () => resolvePromise({ code: 0, stdout: "", stderr: "" }));
    child.on("close", (code) => resolvePromise({ code: code ?? 0, stdout, stderr }));
  });
}

async function resolveRoot(anchor) {
  if (!anchor) return "";
  const result = await runProcess("git", ["-C", anchor, "rev-parse", "--show-toplevel"]);
  const root = result.stdout.trim();
  if (result.code === 0 && root) return root;
  try { return realpathSync(anchor); } catch { return resolve(anchor); }
}

export const MunsuPretoolCheck = async ({ directory, worktree }) => {
  const root = worktree
    ? (() => { try { return realpathSync(worktree); } catch { return resolve(worktree); } })()
    : await resolveRoot(directory);

  return {
    "tool.execute.before": async (input, output) => {
      if (!root) return;
      const tool = input?.tool;
      // Native file-write tools never go through the shell, so a bash-only
      // check leaves the most common write path unguarded. They carry their
      // target as a path argument instead of a command string.
      const isBash = tool === "bash";
      const isWrite = MUNSU_WRITE_TOOLS.indexOf(tool) !== -1;
      if (!isBash && !isWrite) return;

      const args = ["integrate", "safety-check", "--harness", "opencode"];
      if (isBash) {
        const command = output?.args?.command;
        if (!command || typeof command !== "string") return;
        args.push("--command", command);
      } else {
        const a = output?.args ?? {};
        const filePath = a.filePath ?? a.file_path ?? a.notebookPath ?? a.path ?? "";
        // No path in the payload means there is nothing location-based to
        // check; leave the call alone rather than refusing an unknown shape.
        if (!filePath || typeof filePath !== "string") return;
        args.push("--file-path", filePath);
      }

      const result = await runProcess(MUNSU_BIN, args);
      if (result.code !== 2) return;

      const reason = result.stderr.trim() || "denied by munsu PreToolUse seatbelt";
      throw new Error(reason);
    },
  };
};
`, munsuBin, writeToolJSArray(opencodeWriteToolNames))
}

// opencodeSessionstartNudgePlugin generates the session-start nudge plugin JS.
//
// Mirrors the munsu sessionstart-nudge plugin contract:
// - `event` handler checks event.type === "session.created"
// - exactly-once per session via durable file + in-memory guard
// - spawns munsu integrate sessionstart-nudge
// - records success-only after promptAsync delivers
func opencodeSessionstartNudgePlugin(munsuBin string) string {
	return fmt.Sprintf(`import { spawn } from "node:child_process";
import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const MUNSU_BIN = "%s";

// Durable file path: next to this plugin file in .opencode/plugins/
const DURABLE_FILE = (() => {
  try {
    const dir = dirname(fileURLToPath(import.meta.url));
    return join(dir, ".munsu-nudged");
  } catch { return ""; }
})();

function loadNudgedSessions() {
  if (!DURABLE_FILE) return new Set();
  try {
    if (!existsSync(DURABLE_FILE)) return new Set();
    const data = readFileSync(DURABLE_FILE, "utf-8").trim();
    if (!data) return new Set();
    return new Set(JSON.parse(data));
  } catch { return new Set(); }
}

function saveNudgedSession(sessionID) {
  if (!DURABLE_FILE) return;
  try {
    const sessions = loadNudgedSessions();
    sessions.add(sessionID);
    mkdirSync(dirname(DURABLE_FILE), { recursive: true });
    writeFileSync(DURABLE_FILE, JSON.stringify([...sessions]), "utf-8");
  } catch {}
}

function runProcess(command, args) {
  return new Promise((resolveResult) => {
    const child = spawn(command, args, { stdio: ["ignore", "pipe", "ignore"] });
    let stdout = "";
    child.stdout.on("data", (chunk) => { stdout += chunk.toString(); });
    child.on("error", () => resolveResult({ code: 0, stdout: "" }));
    child.on("close", (code) => resolveResult({ code: code ?? 0, stdout }));
  });
}

export const MunsuSessionstartNudge = async ({ client, directory, worktree }) => {
  return {
    event: async ({ event }) => {
      if (event.type !== "session.created") return;
      const sessionID = event.properties?.info?.id ?? event.properties?.sessionID;
      if (!sessionID) return;

      // Check durable marker (survives plugin reload).
      const durable = loadNudgedSessions();
      if (durable.has(sessionID)) return;

      // In-memory guard: prevents concurrent duplicate processing.
      const inMemory = new Set();
      if (inMemory.has(sessionID)) return;
      inMemory.add(sessionID);

      const result = await runProcess(MUNSU_BIN, ["integrate", "sessionstart-nudge"]);
      const nudge = result.code === 0 ? result.stdout.trim() : "";
      if (!nudge) {
        // Nudge command failed or returned empty. Remove guard so retry is possible.
        inMemory.delete(sessionID);
        return;
      }

      try {
        await client.session.promptAsync({
          path: { id: sessionID },
          body: { parts: [{ type: "text", text: nudge }] },
        });
      } catch {
        // promptAsync failed. Remove guard so retry is possible.
        inMemory.delete(sessionID);
        return;
      }

      // Success: record durable exactly-once marker and clean up in-memory guard.
      saveNudgedSession(sessionID);
      inMemory.delete(sessionID);
    },
  };
};
`, munsuBin)
}

// opencodeTurnendGuardPlugin generates the turn-end guard plugin JS.
//
// Mirrors the munsu turnend-guard plugin contract:
// - `event` handler checks event.type === "session.idle"
// - Spawns munsu guard --harness opencode with stdin stdinJSON
// - If exit code 2, sends promptAsync with blocking reason
// - Defers to watch-arm coordinator if armed
func opencodeTurnendGuardPlugin(munsuBin string) string {
	return fmt.Sprintf(`import { spawn } from "node:child_process";

const MUNSU_BIN = "%s";
const COORDINATOR_KEY = "__munsuOpenCodeWatchArm";
let skipNextIdle = false;

function runProcess(command, args, input = "") {
  return new Promise((resolve) => {
    const child = spawn(command, args, { stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => { stdout += chunk.toString(); });
    child.stderr.on("data", (chunk) => { stderr += chunk.toString(); });
    child.on("error", () => resolve({ code: 0, stdout: "", stderr: "" }));
    child.on("close", (code) => resolve({ code: code ?? 0, stdout, stderr }));
    child.stdin.end(input);
  });
}

function runGuard() {
  return runProcess(MUNSU_BIN, ["guard", "--harness", "opencode"], '{"stop_hook_active":false}');
}

async function letWatchArmRun(sessionID, client) {
  const coordinator = globalThis[COORDINATOR_KEY];
  if (!coordinator?.ensureArmed) return false;
  const status = await coordinator.ensureArmed(sessionID, client);
  return status === "armed" || status === "wake" || status === "failed";
}

export const MunsuTurnendGuard = async ({ client, directory, worktree }) => {
  return {
    event: async ({ event }) => {
      if (event.type !== "session.idle") return;

      if (skipNextIdle) {
        skipNextIdle = false;
        return;
      }

      const sessionID = event.properties?.sessionID;
      if (!sessionID) return;

      if (await letWatchArmRun(sessionID, client)) return;

      const result = await runGuard();
      if (result.code !== 2) return;

      try {
        await client.session.promptAsync({
          path: { id: sessionID },
          body: {
            parts: [{
              type: "text",
              text: "TURN WOULD END BLIND - supervision is off.\\n\\n" + result.stderr,
            }],
          },
        });
        skipNextIdle = true;
      } catch {
        skipNextIdle = false;
      }
    },
  };
};
`, munsuBin)
}

// opencodeWatchArmPlugin generates the watcher arm plugin JS.
//
// Mirrors the munsu watch-arm plugin contract:
// - Registers a global coordinator for cross-plugin coordination
// - On session.idle, spawns munsu watch ensure to arm the watcher
// - Reports arm status via the coordinator for the turnend-guard to check
// - On wake/failure, sends promptAsync with actionable output
func opencodeWatchArmPlugin(munsuBin string) string {
	return fmt.Sprintf(`import { spawn } from "node:child_process";

const MUNSU_BIN = "%s";
const COORDINATOR_KEY = "__munsuOpenCodeWatchArm";
const ARM_READY_TIMEOUT_MS = 12000;

let child = null;
let armStatus = "idle";
let waiters = new Set();

function setArmStatus(status) {
  armStatus = status;
  for (const resolve of waiters) resolve(status);
  waiters.clear();
}

function readyStatus() {
  if (armStatus === "armed" || armStatus === "wake" || armStatus === "failed" || armStatus === "external") return armStatus;
  return "";
}

function waitForArmReady() {
  const ready = readyStatus();
  if (ready) return Promise.resolve(ready);
  return new Promise((resolve) => {
    let timer = null;
    const waiter = (status) => {
      if (timer) clearTimeout(timer);
      waiters.delete(waiter);
      resolve(status);
    };
    timer = setTimeout(() => {
      waiters.delete(waiter);
      resolve("timeout");
    }, ARM_READY_TIMEOUT_MS);
    waiters.add(waiter);
  });
}

function firstWakeOrFailure(stdout, stderr, code) {
  const combined = stdout + "\\n" + stderr;
  const wakeLine = combined.split(/\\r?\\n/).find((line) => /^(signal:|stale:|check:|heartbeat($|:))/.test(line));
  if (wakeLine) return wakeLine;
  if (/^watcher: healthy/m.test(combined)) return "";
  const failed = combined.split(/\\r?\\n/).find((line) => /^watcher: FAILED/.test(line));
  if (failed) return failed;
  if (code && code !== 0) return "watcher: FAILED - munsu watch ensure exited " + String(code) + (combined.trim() ? "\\n" + combined.trim() : "");
  return "";
}

function observeArmOutput(stdout, stderr) {
  const combined = stdout + "\\n" + stderr;
  if (combined.split(/\\r?\\n/).some((line) => /^watcher: started\\b/.test(line))) {
    setArmStatus("armed");
    return;
  }
  if (combined.split(/\\r?\\n/).some((line) => /^watcher: healthy\\b/.test(line))) {
    setArmStatus("external");
    return;
  }
  if (combined.split(/\\r?\\n/).some((line) => /^watcher: FAILED/.test(line))) {
    setArmStatus("failed");
  }
}

function spawnArm(sessionID, client) {
  setArmStatus("starting");
  child = spawn(MUNSU_BIN, ["watch", "ensure"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (chunk) => {
    stdout += chunk.toString();
    observeArmOutput(stdout, stderr);
  });
  child.stderr.on("data", (chunk) => {
    stderr += chunk.toString();
    observeArmOutput(stdout, stderr);
  });
  child.on("close", async (code) => {
    child = null;
    const reason = firstWakeOrFailure(stdout, stderr, code);
    if (reason) setArmStatus(reason.startsWith("watcher: FAILED") ? "failed" : "wake");
    else if (!readyStatus()) setArmStatus("idle");
    if (!reason) return;
    try {
      await client.session.promptAsync({
        path: { id: sessionID },
        body: {
          parts: [{
            type: "text",
            text: "WATCHER FIRED - claim queued wakes with 'munsu wake claim', handle the reported wake, and continue normal supervision.\\n\\n" + reason,
          }],
        },
      });
    } catch {}
  });
  child.on("error", async (error) => {
    child = null;
    setArmStatus("failed");
    try {
      await client.session.promptAsync({
        path: { id: sessionID },
        body: {
          parts: [{
            type: "text",
            text: "WATCHER FIRED - claim queued wakes with 'munsu wake claim', handle the reported wake, and continue normal supervision.\\n\\nwatcher: FAILED - OpenCode arm child failed: " + String(error.message),
          }],
        },
      });
    } catch {}
  });
}

async function ensureArm(sessionID, client) {
  if (!sessionID) return "skipped";
  if (child) return waitForArmReady();
  spawnArm(sessionID, client);
  return waitForArmReady();
}

export const MunsuWatchArm = async ({ client, directory, worktree }) => {
  globalThis[COORDINATOR_KEY] = {
    ensureArmed: (sessionID, activeClient) => ensureArm(sessionID, activeClient ?? client),
  };

  return {
    event: async ({ event }) => {
      if (event.type !== "session.idle") return;
      const sessionID = event.properties?.sessionID;
      if (!sessionID) return;
      void ensureArm(sessionID, client);
    },
  };
};
`, munsuBin)
}

// OpencodePluginsHasOwnedHooks checks whether the plugin files in pluginsDir
// contain munsu-owned content anchored to the given munsu binary path.
// Uses structural ownership detection (JS files can't carry a reliable
// first-line marker).
func OpencodePluginsHasOwnedHooks(pluginsDir, munsuBin string) (bool, string, error) {
	var missing []string

	for _, name := range opencodePluginFileNames {
		path := filepath.Join(pluginsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			missing = append(missing, name+" (not found)")
			continue
		}
		content := string(data)

		expectedContent := opencodePluginContent(munsuBin, name)
		if expectedContent == "" {
			missing = append(missing, name+" (cannot generate expected content)")
			continue
		}

		// Structural ownership: check that the file contains the munsu binary
		// path and the expected exported function name.
		if !strings.Contains(content, munsuBin) {
			missing = append(missing, name+" (missing munsu binary path)")
			continue
		}

		// Check for the expected export function name.
		funcName := expectedFuncName(name)
		if funcName != "" && !strings.Contains(content, funcName) {
			missing = append(missing, name+" (missing expected export function)")
			continue
		}

		// The pre-tool plugin must also carry the native file-write tool
		// table. Without this check an installation that predates it reports
		// as healthy, is never repaired, and leaves the write path unguarded
		// on machines already running munsu.
		if name == "munsu-pretool-check.js" && !strings.Contains(content, "MUNSU_WRITE_TOOLS") {
			missing = append(missing, name+" (missing native file-write tool coverage)")
			continue
		}
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("missing or mismatched munsu-owned plugins: %s", strings.Join(missing, ", ")), nil
	}
	return true, "all munsu-owned plugins present", nil
}

// expectedFuncName returns the expected export function name for a plugin file.
func expectedFuncName(pluginName string) string {
	switch pluginName {
	case "munsu-pretool-check.js":
		return "MunsuPretoolCheck"
	case "munsu-sessionstart-nudge.js":
		return "MunsuSessionstartNudge"
	case "munsu-turnend-guard.js":
		return "MunsuTurnendGuard"
	case "munsu-watch-arm.js":
		return "MunsuWatchArm"
	default:
		return ""
	}
}

// OpencodePluginsAllTargetPaths returns all 4 plugin file paths for the given scope+cwd.
func OpencodePluginsAllTargetPaths(scope Scope, cwd string) ([]string, error) {
	dir, err := opencodePluginsDir(scope, cwd)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range opencodePluginFileNames {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

// OpencodeAdapter implements plugin generation and installation for the OpenCode harness.
type OpencodeAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// InstallOpencodePlugins generates and installs all 4 .opencode/plugins/*.js files.
// Returns the target paths, whether anything was actually written, and the combined content digest.
func (a *OpencodeAdapter) InstallOpencodePlugins() (targetPaths []string, written bool, combinedDigest string, err error) {
	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return nil, false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	scope := Scope(a.Scope)
	dir, err := opencodePluginsDir(scope, a.Cwd)
	if err != nil {
		return nil, false, "", fmt.Errorf("cannot determine opencode plugins dir: %w", err)
	}

	var allTargets []string
	var allContents []string
	anyWritten := false

	for _, name := range opencodePluginFileNames {
		targetPath := filepath.Join(dir, name)
		allTargets = append(allTargets, targetPath)

		content := opencodePluginContent(munsuBin, name)
		if content == "" {
			return nil, false, "", fmt.Errorf("cannot generate content for plugin %s", name)
		}
		allContents = append(allContents, content)

		if a.DryRun {
			continue
		}

		if err := writeAtomic(targetPath, content, 0644); err != nil {
			return nil, false, "", fmt.Errorf("writing opencode plugin %s: %w", name, err)
		}
		anyWritten = true
	}

	// Compute combined digest of all 4 files
	combined := strings.Join(allContents, "\x00")
	sum := sha256.Sum256([]byte(combined))
	combinedDigest = hex.EncodeToString(sum[:])

	return allTargets, anyWritten, combinedDigest, nil
}

// generateOpencodeManifest creates the integration manifest for the OpenCode adapter.
func generateOpencodeManifest(harnessName string, scope string, caps []Capability, combinedDigest string, targetPaths []string) Manifest {
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
		TargetPaths:   targetPaths,
		Capabilities:  capStrs,
		ContentDigest: combinedDigest,
	}
}
