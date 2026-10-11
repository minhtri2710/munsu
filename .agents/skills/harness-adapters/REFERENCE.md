# Harness adapters reference

## Launch templates

| Harness | Model flag | Effort flag | Default model / effort | Extra launch args |
|---|---|---|---|---|
| claude | `--model` | none | none | none |
| codex | `--model` | `--effort` | `gpt-5.2-codex` / `80` | none |
| opencode | `--model` | `--effort` | `gpt-5.2-codex` / `80` | none |
| pi | `--model` | `--thinking` | none | none |
| grok | `--model` | `--reasoning-effort` | none | none |
| agy | `--model` | none | none | `--dangerously-skip-permissions` |

Harness detection checks environment markers, then process ancestry. Dispatch precedence is CLI override, matched dispatch profile, the project's Soldier harness and model, then adapter template defaults. Manage profiles with `munsu config dispatch`.

## Soldier launch

Only pi and claude launch as soldiers; codex, opencode, grok and agy are refused. Captain launch is pi only.

claude soldier argv, measured on claude 2.1.289: `claude [--model M] --permission-mode bypassPermissions --disallowedTools AskUserQuestion --disallowedTools 'Skill(munsu-ops)' -- <prompt>`. Permission prompts are off; on macOS the write fence and the git shim bound the soldier, and on other hosts there is no write fence, only the git shim. The ask-the-user tool and the `munsu-ops` skill are denied; the deny flags are variadic, so each is its own flag and `--` ends the list.

One-time step per project: open claude once in the project repository and answer "Yes, I trust this folder". A soldier worktree inherits its main repository's trust; a nested git repository does not inherit a parent folder's. munsu reads and writes no claude config file. In an untrusted repository claude shows "Quick safety check: Is this a project you created or one you trust?" with "No, exit" as the default, so munsu never answers it: the launch handshake fails on that text.

## Supervision by harness

No supervising session arms the watcher with a shell `&`. Under herdr a claude General is delivered each wake as a prompt (the `munsu-ops` skill owns that rule); every other session except pi claims with `munsu wake claim --consumer <id>` (in pi the extension claims). `munsu watch ensure` returns within the beacon timeout (3s), plus up to 2s to stop a holder on `--restart` or a stalled holder, with the watcher state (`attached`, `healthy` or `failed`); the watcher is a detached daemon that records wakes in the durable queue, and neither command prints `signal:`, `stale:`, `check:` or `heartbeat` lines or notifies on completion. `munsu watch ensure --restart` forces a restart.

| Harness | Mode | Arm and wake rule |
|---|---|---|
| claude | herdr prompt delivery; else queue claim | Run `munsu watch ensure` once; it returns within the beacon timeout. Under herdr the watcher delivers each wake as a prompt: resolve it with the `munsu wake resolve` command it carries. On another backend collect wakes with `munsu wake claim` and handle them. Re-run `munsu watch ensure` if the watcher is not healthy. |
| grok | queue claim | Run `munsu watch ensure` once; it returns within the beacon timeout. Collect wakes with `munsu wake claim` and handle them. Do not invent a wake from an attach status. Interactive TUI sessions are the supported host; headless `grok -p` may not surface wake output. |
| codex | foreground checkpoint | No background mechanism. Run `munsu watch run` as one foreground checkpoint; it runs one poll cycle and returns the wakes scanned and emitted. Drain queued wakes with `munsu wake claim`, handle them, then start the next checkpoint. |
| opencode | TUI plugin background wake | The `.opencode/plugins/munsu-watch-arm.js` plugin arms supervision on `session.idle` and prompts the session when the watcher exits with an actionable reason. If it reports `watcher: healthy ...`, start no other cycle. Use `munsu watch ensure` by hand only as a short recovery probe. The plugin applies in the main primary checkout and a general's own home, and not in headless `opencode run`. |
| pi | extension background wake | The `munsu-pi-integration.ts` extension arms the watcher on session start through `munsu session-start`; do not run `munsu watch ensure` through Pi's bash tool and do not claim wakes manually. On `agent_settled` the extension claims queued wakes and sends a follow-up message; resolve each with the `munsu_wake_resolve` tool (human fallback: `/munsu:wake resolved [key=<slug>]: <summary>`). If the watcher is unhealthy, repair with `munsu watch ensure --restart`. The turn-end guard extension is a backstop, not the normal wake path. |
