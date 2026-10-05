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
