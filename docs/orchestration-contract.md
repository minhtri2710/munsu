# Versioned orchestration contract

**Status:** Implemented contract surface with versioned models, TOON/JSON output encoder, and paired golden fixtures. This document specifies the structured agent-facing orchestration contract that the following command families already implement; it does not grant or promise behavior for any command/flag beyond what is registered below.

## Purpose and scope

This contract gives agent callers a small, versioned view of orchestration state and mutation receipts. It is grounded in the Agent eXperience Interface (AXI): token-efficient TOON on stdout by default, minimal result schemas, definitive empties, cheap aggregates, contextual next actions, structured errors, no prompts, and idempotent mutations.

The JSON-compatible Go model lives in package `internal/cli` under `internal/cli/contract_model.go` (response/error envelopes, schemas, `SchemaVersion`), the TOON/JSON encoder and output helpers under `internal/cli/contract_output.go`, and the command wiring under `internal/cli/contract_commands.go` / `internal/cli/fleet_contract.go`. Paired golden fixtures live under `internal/cli/contract_fixtures/` (embedded via `internal/cli/contract_fixtures_embed.go`) and are drift-checked by `internal/cli/contract_contract_test.go`. There is no package literally named `internal/contract`; the contract belongs to the `cli` output boundary and composes cleanly with the domain owners it names.

`schema_version` is required in every response and is presently `munsu.orchestration/v2`. `kind` identifies the response schema. Both encodings have identical field names, values, and semantics.

## Scope and single-owner boundaries

A command family has one runtime owner: the package that emits its contract result. A package may call another package's public interface, but must not independently emit or redefine that family's contract result. Every family below is **implemented**: the `internal/cli` output boundary emits its contract result, and the named domain owner holds that family's authority. Earlier packages that owned these domains (`internal/soldierstate`, `internal/task`, `internal/supervision`, `internal/waker`, `internal/session`, `internal/spawn`, `internal/integrate`) have been absorbed into the packages named below; the table names only packages that exist today.

| Contract family | Exact command | Runtime owner (emits contract) | Domain authority | Existing authority reused | Status |
|---|---|---|---|---|---|
| Discovery | `munsu capabilities` | `internal/cli` | root command registration | root command registration | implemented |
| Task observation | `munsu task observe <task-id>` | `internal/cli` | `internal/fleet` soldier state | `internal/taskauthority` canonical record + `internal/fleet` (`ReadWithProbe`) + `internal/home` projection | implemented |
| Fleet state | `munsu fleet snapshot --version 2` | `internal/cli` | `internal/fleet` | `internal/fleet` snapshot (canonical-aware) | implemented |
| Guard | `munsu guard` | `internal/cli` | `internal/orchestrator` | `internal/orchestrator` guard evaluation + `internal/fleet` snapshot | implemented |
| Watch | `munsu watch ensure`, `munsu watch run` | `internal/cli` | `internal/orchestrator` | `internal/orchestrator` watcher lifecycle | implemented |
| Wake | `munsu wake claim --consumer <id>`, `munsu wake resolve --claim-id <lease-id> --event-id <event-id> --summary <text>`, `munsu wake ack <lease-id> <event-id...>` | `internal/cli` | `internal/orchestrator` | `internal/orchestrator` wake records and drain state | implemented |
| Backend discovery | `munsu backend capabilities [--backend <name>]` | `internal/cli` | `internal/backend` | `internal/backend` backend selection | implemented |
| Spawn receipt | `munsu spawn <task-id> ...` | `internal/cli` | `internal/fleet` | `internal/fleet` spawn lifecycle | implemented |
| Integration | `munsu integrate install`, `munsu integrate repair` | `internal/cli` | `internal/cli` | `internal/bootstrap` hook installation (post embed-ops) | implemented |

A later implementation may add an output-boundary package, but it must not move domain ownership or alter the above commands' domain authority. Existing commands retain their behavior until a separately approved implementation phase.

## Invocation, flags, and output channels

Every contract command accepts `--output toon|json`; `toon` is the default and `json` is explicit. `--output` is an enum, not a free-form format selector. Commands that return a collection or expandable record accept `--fields <comma-separated-field-names>`; default fields remain minimal and unsupported fields are usage errors. Detail commands that truncate content accept `--full`.

| Command | Required input | Contract flags | Default response |
|---|---|---|---|
| `munsu` | none | `--output toon|json` | cwd-scoped compact home view |
| `munsu capabilities` | none | `--output toon|json` | capabilities |
| `munsu task observe <task-id>` | task ID | `--fields`, `--full`, `--output` | task observation |
| `munsu fleet snapshot --version 2` | version exactly `2` | `--fields`, `--full`, `--output` | fleet snapshot v2 |
| `munsu guard` | none | `--output` | guard result |
| `munsu watch ensure` | none | `--interval <duration>`, `--output` | ensure receipt |
| `munsu watch run` | none | `--output` | one run receipt |
| `munsu wake claim` | `--consumer <id>` (required) | `--consumer`, `--lease-captains`, `--limit`, `--output` | claim receipt |
| `munsu wake resolve` | `--claim-id <lease-id>`, `--event-id <event-id>`, `--summary <text>` | `--claim-id`, `--event-id`, `--summary`, `--output` | resolve receipt |
| `munsu wake ack <lease-id> <event-id...>` | lease ID + at least one event ID | `--output` | acknowledgement receipt |
| `munsu backend capabilities` | none | `--backend <name>`, `--output` | backend capabilities (structured discovery currently exposes only `tmux` and `herdr`; zellij/cmux/orca remain experimental runtime backends and are not part of this contract surface) |
| `munsu spawn <task-id>` | existing spawn arguments | `--output` in addition to existing flags | spawn receipt |
| `munsu integrate install` | none | `--harness <name>`, `--scope project|user`, `--output` | integration receipt |
| `munsu integrate repair` | none | `--harness <name>`, `--scope project|user`, `--output` | integration receipt |

`--help` remains universally accepted. Each command validates positional arguments, flag names, enum values, and requested fields **before** reading a backend or calling a dependency. Unknown arguments and flags are rejected; a targeted renamed-flag hint is preferred where a replacement exists. Commands never prompt: every required value must arrive as an argument or flag.

stdout contains only the structured result, structured error, and actionable `help` hints. stderr contains only progress, debugging, and diagnostics. Dependency output, stack traces, and progress lines must never contaminate stdout.

The no-arguments home view is content-first rather than a help dump. It must include the resolved executable path (with the home prefix shortened to `~`), the one-line purpose, and compact live state scoped to the current working directory. It may contain a maximum of two context-relevant complete next-command hints.

## Common envelope and schemas

All successful responses have this envelope:

| Field | Type | Required | Meaning |
|---|---|---:|---|
| `schema_version` | string | yes | `munsu.orchestration/v2` |
| `kind` | string | yes | stable response identity |
| `status` | string | yes | `success` |
| `data` | object | yes | one of the schema objects below |
| `help` | string array | no | complete contextual commands, only when useful |

`capabilities`: `contract_version`, `commands[]`, `output_formats[]`.

`task.observe`: default `task_id`, `status`, and cheap state; optional expansion adds `description`, `branch`, `pane_alive`, `no_mistakes_step`. `--fields` can request only documented optional fields. `--full` is only meaningful when a response declares truncation.

`fleet.snapshot`: `scope`, `count`, `total`, and `soldiers[]`. A soldier row defaults to `task_id`, `status`, `branch`. `count` is returned rows and `total` is the definitive matching total; both are precomputed cheaply.

`guard`: `state`, `conditions[]`. `watch.ensure`: `watch_id`, `state`, `interval`, with `noop: true` when already ensured. `watch.run`: `watch_id`, `state`, `wakes_scanned`, `wakes_emitted`. `wake.claim`: `wake_id`, `claim_id`, `owner`, `state`; `wake.ack`: `wake_id`, `claim_id`, `state`. `backend.capabilities`: `backend`, `features[]`. `spawn.receipt`: `task_id`, `session_id`, `worktree`, `branch`, `state`.

A normal acknowledgement has `kind: message` and `data.message`, `data.noop`. A no-op remains `status: success`, sets `noop: true`, and exits zero. A quiet wake (nothing actionable) is likewise a successful definitive empty result, not an error.

An empty result sets `count: 0`, carries contextual text, and represents empty arrays as `[]`; no output is never an empty answer. A truncated result preserves `preview`, `total_chars`, `truncated: true`, and `full_command`, and provides a `--full` hint. Long content is previewed rather than omitted.

## Errors and exits

Errors use this stdout envelope in both formats:

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | string | contract version |
| `kind` | string | always `error` |
| `status` | string | always `error` |
| `error.error_code` | string | stable machine-readable code |
| `error.retryable` | boolean | whether retrying unchanged input can reasonably succeed |
| `error.action` | string | exact next command or correction |
| `error.message` | string | concise, dependency-neutral explanation |

Stable `error_code` values are `invalid_argument`, `unknown_flag`, `unsupported_input`, `not_found`, `invalid_state`, `dependency_unavailable`, `conflict`, and `internal`. New codes are additive only within v2. `action`, not a new exit-code class, carries next-step guidance.

| Exit code | Meaning | Examples |
|---:|---|---|
| 0 | success | data, definitive empty, quiet wake, idempotent no-op |
| 1 | operation, state, or dependency failure | missing task, active claim conflict, unavailable backend |
| 2 | usage or unsupported input | missing required flag, unknown flag, bad enum, unsupported field/version |

The command must reject invalid input before dependencies. It must translate dependency errors without exposing dependency names, raw messages, credentials, paths that should remain private, or stack traces.

## TOON v3.3 conformance and JSON equivalence

TOON fixtures and every future TOON encoder conform strictly to the published TOON Specification v3.3 (Working Draft), sections 1–16. Documents are UTF-8, use LF separators, use two spaces for indentation, never use indentation tabs, and have neither trailing whitespace nor a trailing newline. The v3.3 JSON data model is authoritative: arrays preserve order, object key order is preserved during emission, booleans and null are lowercase, numbers use canonical JSON-compatible formatting, and non-JSON values are normalized before output.

Future encoders must use strict validation: declared array counts and tabular widths match, headers have valid non-negative lengths, delimiters are scoped correctly, duplicate sibling keys are rejected, and invalid indentation, malformed structure, or invalid escapes are rejected. No key folding or path expansion is enabled by this contract.

Strings and quoted keys escape backslash, quotation mark, LF, CR, and tab as `\\`, `\"`, `\n`, `\r`, and `\t`; other U+0000–U+001F controls use `\uXXXX`. Quoted strings must be used for empty strings, leading/trailing whitespace, boolean/null/numeric-looking strings, structural characters, delimiters in their active scope, and values beginning with `-`. Only valid v3.3 escapes are accepted. These rules prevent delimiter, line, and structure injection; untrusted text must not be concatenated into TOON.

TOON is the default because it is compact for agents. JSON is an opt-in transport with the same schema version, keys, values, nullability, ordering intent, and error shape; it must never be a looser or richer schema. The paired fixtures are golden equivalence examples, not a promise that JSON object textual ordering is semantically meaningful to a general JSON decoder.

## Integration concept (opt-in, post embed-ops)

`munsu integrate install|repair` is an opt-in command (post embed-ops #257). Normal commands may never install hooks or plugins. It installs or repairs supported session hooks/adapters for the selected harness and `project` or `user` directory scope.

The installed command resolves portability in this order: use `munsu` only if PATH resolves to the currently executing binary; otherwise use that binary's absolute path. `repair` rechecks and updates a stale path. Repeating `install` with identical content and path is a successful silent no-op. The ambient session context is cwd-scoped and compact: executable, one-line purpose, and only live aggregate/action state that helps the next command; it never injects full fleet or long task content.

Hooks are primary where supported. The installable `munsu-ops` skill is captainary discovery for harnesses without hooks or users who prefer on-demand context. A future consistency check must compare static command guidance generated for the skill with the no-args home-view guidance after removing live state; CI must fail when that check detects drift. No skill, adapter, hook, or generated command is added by this Phase 0 PR.

## Adapter delivery contract (future)

A future adapter declares one delivery mode per action: **blocking stop** (the caller waits for a terminal receipt), **passive follow-up** (the caller receives a receipt and later observes state), or **bounded checkpoint** (the caller waits only to a named, time-bounded checkpoint). A receipt always identifies the selected mode and gives a next command when further observation is useful.

| Harness | Small edge retained by adapter |
|---|---|
| Claude | native session hook configuration is the adapter concern |
| Codex | hooks require the harness hook feature to be enabled |
| OpenCode | ambient context is plugin-mediated rather than a shell hook |
| Pi | harness-specific session adapter controls context injection |
| agy | launch permission semantics remain adapter-local |

The core contract does not encode harness-private arguments, process names, or lifecycle mechanics.

## Compatibility policy

| Change | v2 treatment | Required action |
|---|---|---|
| Add optional response field or capability | compatible | document and add paired fixtures/tests |
| Add command or output format | compatible | advertise via capabilities and fixture it |
| Add stable error code | compatible | document and fixture it |
| Change field meaning, type, requiredness, default, command spelling, flag spelling, or exit meaning | breaking | publish a new schema version |
| Remove a field, command, format, or error code | breaking | publish a new schema version with migration guidance |
| Change TOON escaping/strictness below v3 major compatibility | breaking | publish a new schema version |

## Implementation status / phasing

The versioned model, TOON/JSON encoder, golden fixtures, fixture drift tests, and the command families below have landed under the `internal/cli` output boundary (see the owner table above). Residual phasing that requires future work before the remaining guarantees are exercised end-to-end:

1. **Shipped:** versioned JSON-compatible models, paired TOON/JSON golden fixtures, fixture drift tests, and the registered contract command families (`capabilities`, `task observe`, `fleet snapshot --version 2`, `guard`, `watch`, `wake`, `backend capabilities`, `spawn`, `integrate`).
2. **Remaining:** the skill/home-view consistency check in CI (compare static command guidance generated for the `munsu-ops` skill with the no-args home view) and full adapter delivery modes; these are opt-in and not yet enabled.

Each phase must keep the exit, stdout/stderr, no-prompt, idempotency, and TOON v3.3 guarantees defined here.
