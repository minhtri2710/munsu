# munsu — Remaining Architecture Remediation Tasks

> Derived from `docs/architecture-audit.md` (Items 3–5).  
> Items 1 & 2 already shipped: homeDir parameterisation + `internal/backlog` extraction.

---

## Item 3 — Secondmate: atomic backlog handoff + safe config push

**Priority**: High  
**Type**: New capability  
**Firstmate reference**: `bin/fm-backlog-handoff.sh`, `bin/fm-config-inherit-lib.sh`

### Context

`secondmate.Handoff` (lines 148–175 of `internal/secondmate/secondmate.go`)
currently **copies** meta/status files from parent home to secondmate home but never
removes the originals. A task handed off therefore exists in two places simultaneously —
the parent backlog still shows it as active.

`secondmate.ConfigPush` (lines 178–200) copies a hardcoded list of config files but:
1. Does **not** verify the target worktree gitignores the copied file (`git check-ignore`).
2. Does **not** mirror deletions — files removed from parent config remain in secondmate.
3. Hardcodes the inheritable list instead of honouring `MUNSU_INHERITABLE_CONFIG` env.
4. Produces no structured report log.

### Affected files

| File | Change needed |
|---|---|
| `internal/secondmate/secondmate.go` | Rewrite `Handoff` + `ConfigPush` |
| `internal/secondmate/secondmate_test.go` | New — add table tests |

### Implementation steps

#### 3a. Atomic backlog handoff (`Handoff`)

1. **Copy** meta + status files to secondmate home (existing behaviour — keep).
2. **Remove** originals from parent `state/` after successful copy — making the move atomic.
3. If any copy fails, abort before removing originals (fail-closed).
4. Print a structured line per item: `handed-off <id>` or `warning: <id> — <reason>`.

New signature (homeDir already parameterised by Item 1):
```go
func Handoff(parentHome, secondmateHome string, itemKeys []string) error
```

#### 3b. Safe config push (`ConfigPush`)

1. **Build inheritable list** from `MUNSU_INHERITABLE_CONFIG` env (colon-separated names);
   fall back to the hardcoded default list if env is absent.
2. **Copy present files** from `parentHome/config/<name>` → `secondmateHome/config/<name>`.
3. **Mirror deletions**: for each file in `secondmateHome/config/` that is in the
   inheritable list but absent from `parentHome/config/`, remove it.
4. **Gitignore check**: after each write, run `git check-ignore -q <dst>` in the
   secondmate home. If the file is **not** gitignored, print:
   `WARNING: <name> is tracked in secondmate git — add it to .gitignore`.
5. **Report log**: append one line per action to `secondmateHome/state/config-push.log`
   with timestamp, action (`pushed`/`deleted`/`skipped`), and file name.

```go
func ConfigPush(parentHome, secondmateHome string) error
```

### Acceptance criteria

- [ ] `Handoff` removes source files after successful copy.
- [ ] `Handoff` does not remove any source if a copy step fails.
- [ ] `ConfigPush` respects `MUNSU_INHERITABLE_CONFIG` env override.
- [ ] `ConfigPush` deletes files from secondmate config that are absent in parent.
- [ ] `ConfigPush` warns (stdout) when a written file is not gitignored.
- [ ] `ConfigPush` appends to `state/config-push.log`.
- [ ] `go test ./internal/secondmate/...` passes with new table tests covering all branches.
- [ ] `go build ./... && go vet ./...` clean.

---

## Item 4 — Session backend config resolution + hometag namespace isolation

**Priority**: Medium  
**Type**: New capability  
**Firstmate reference**: `bin/fm-backend-hometag-lib.sh`, `docs/configuration.md`

### Context

`session.Default()` (line 43 of `internal/session/session.go`) auto-detects the
backend from environment markers but **ignores** the config file `config/backend`
under munsu home. The firstmate resolution chain is:

```
config/backend file  >  runtime env marker  >  tmux (fallback)
```

There is also no hometag isolation. When two munsu homes share the same tmux server,
their sessions collide because window names are generated without a home-scoped prefix.
Firstmate uses `fm_backend_hometag()` (a short hash of `FM_HOME`) as a prefix for all
window names so two fleet instances never share names.

Currently:
- `cmux`, `zellij`, `orca` backends return `ErrNotImplemented` for every method.
- No `internal/hometag` package exists.

### Affected files

| File | Change needed |
|---|---|
| `internal/session/session.go` | Add `Resolve(homeDir string) Backend` |
| `internal/hometag/hometag.go` | **New package** — derive tag from home path |
| `internal/session/tmux.go` | Prefix window names with hometag |
| `internal/session/backend_cmux.go` | Improve stub error message |
| `internal/session/backend_zellij.go` | Improve stub error message |
| `internal/session/backend_orca.go` | Improve stub error message |
| `internal/cli/root.go` | Replace `session.Default()` with `session.Resolve(homeDir)` |

### Implementation steps

#### 4a. `internal/hometag` package (new)

Create `internal/hometag/hometag.go`:

```go
// Package hometag derives a short, stable tag from a munsu home path.
// The tag is used to namespace session window names so multiple
// fleet instances on the same machine never collide.
//
// Matches firstmate's fm_backend_hometag():
//   printf '%s' "$FM_HOME" | sha256sum | cut -c1-6
package hometag

import (
    "crypto/sha256"
    "fmt"
)

// Tag returns a 6-character lowercase hex prefix for homeDir.
func Tag(homeDir string) string {
    h := sha256.Sum256([]byte(homeDir))
    return fmt.Sprintf("%x", h[:3]) // 3 bytes → 6 hex chars
}
```

Test (`internal/hometag/hometag_test.go`):
- Same input → same tag (deterministic).
- Different inputs → different tags.
- Tag length is always exactly 6 characters.

#### 4b. `session.Resolve(homeDir string) Backend`

Add to `session.go`:

```go
// Resolve returns the configured backend for homeDir.
// Resolution order:
//  1. homeDir/config/backend file (first whitespace-delimited word)
//  2. Runtime env markers (HERDR_ENV > ZELLIJ_SESSION_NAME > CMUX_SOCKET > ORCA_RUNTIME)
//  3. tmux (fallback)
func Resolve(homeDir string) Backend {
    if name := readConfigBackend(homeDir); name != "" {
        return Select(name)
    }
    return Default()
}

func readConfigBackend(homeDir string) string {
    data, err := os.ReadFile(filepath.Join(homeDir, "config", "backend"))
    if err != nil {
        return ""
    }
    fields := strings.Fields(strings.TrimSpace(string(data)))
    if len(fields) == 0 {
        return ""
    }
    return fields[0]
}
```

#### 4c. Hometag-prefixed window names in tmux backend

Modify `TmuxBackend` in `tmux.go` to carry a tag field set at construction time:

```go
type TmuxBackend struct {
    Tag string // e.g. "a3f7c1" — empty = no prefix
}

func (t *TmuxBackend) windowName(name string) string {
    if t.Tag == "" {
        return "fm-" + name
    }
    return t.Tag + "-fm-" + name
}
```

`session.Resolve` passes `hometag.Tag(homeDir)` when constructing a `TmuxBackend`.
`session.Default()` is unchanged (uses empty tag) to preserve existing behaviour.

Update `newSpawnCmd` in `root.go`:
```go
bk := session.Resolve(homeDir)  // was: session.Default()
```

#### 4d. Stub backend error messages

Replace bare `ErrNotImplemented` in `cmux`, `zellij`, `orca` stubs:
```go
// cmux example:
func (c *CmuxBackend) NewWindow(session, name string) (string, error) {
    return "", fmt.Errorf("cmux backend not yet implemented — see docs/port-mapping.md")
}
```

### Acceptance criteria

- [ ] `internal/hometag` package exists with `Tag(string) string`.
- [ ] `hometag.Tag` is deterministic; always returns a 6-character hex string.
- [ ] `session.Resolve(homeDir)` reads `config/backend` before falling back to env detection.
- [ ] `munsu spawn` uses `session.Resolve(homeDir)` (not `session.Default()`).
- [ ] Tmux window names include the hometag prefix when homeDir is non-empty.
- [ ] Two different homeDirs produce different tags (verified in test).
- [ ] Stub backends return descriptive errors referencing `docs/port-mapping.md`.
- [ ] `go test ./internal/hometag/... ./internal/session/...` passes.
- [ ] `go build ./... && go vet ./...` clean.

---

## Item 5 — Backlog manual fallback parser

**Priority**: Low  
**Type**: New capability  
**Firstmate reference**: firstmate manual backlog mode (`config/backlog-backend = manual`)

### Context

`internal/backlog/backlog.go` (created in Item 2) returns this error when `tasks-axi`
is unavailable:

```
backlog: tasks-axi not available and fallback backlog.md editing not yet implemented
```

Firstmate supports a `manual` backend where `$FM_HOME/data/backlog.md` is a plain
markdown file edited directly. When `config/backlog-backend` is `manual` **or**
`tasks-axi` is absent, munsu should read/write `$MUNSU_HOME/data/backlog.md` itself.

### Backlog.md format

```markdown
## Backlog

- [ ] task-abc — Fix login bug (queued)
- [-] task-ghi — Research caching (in-flight)
- [!] task-xyz — Blocked on upstream (blocked)
- [x] task-def — Add dark mode (done)
```

State markers:

| Symbol | State |
|---|---|
| `[ ]` | queued |
| `[-]` | in-flight |
| `[!]` | blocked |
| `[x]` | done |

### Affected files

| File | Change needed |
|---|---|
| `internal/backlog/backlog.go` | Add `ManualRun`, parser, renderer, verb handlers |
| `internal/backlog/backlog_test.go` | Table tests for all verb handlers |

### Implementation steps

#### 5a. Detect manual mode

In the top-level `Run(verb string, args []string, homeDir string) error`:

```go
func Run(verb string, args []string, homeDir string) error {
    if isManual(homeDir) || !Available() {
        return runManual(verb, args, homeDir)
    }
    return runTasksAxi(verb, args)
}

func isManual(homeDir string) bool {
    data, err := os.ReadFile(filepath.Join(homeDir, "config", "backlog-backend"))
    if err != nil {
        return false
    }
    return strings.TrimSpace(string(data)) == "manual"
}
```

#### 5b. File path

```go
func backlogPath(homeDir string) string {
    return filepath.Join(homeDir, "data", "backlog.md")
}
```

#### 5c. Data model + parser

```go
type Item struct {
    ID    string // e.g. "task-abc"
    Title string // e.g. "Fix login bug"
    State string // queued | in-flight | blocked | done
}

// stateFromMarker maps "[ ]" → "queued", etc.
var stateFromMarker = map[string]string{
    "[ ]": "queued",
    "[-]": "in-flight",
    "[!]": "blocked",
    "[x]": "done",
}

// parseBacklog reads and parses backlog.md.
// Lines not matching the format are preserved as-is in a raw lines slice.
func parseBacklog(homeDir string) ([]Item, error)

// renderBacklog writes items back to backlog.md, preserving the header.
func renderBacklog(homeDir string, items []Item) error
```

Parse line format: `- <marker> <id> — <title> (<state>)`  
(The trailing `(<state>)` is informational; canonical state comes from the marker.)

#### 5d. Verb handlers in `runManual`

| Verb | Args | Behaviour |
|---|---|---|
| `add` | `<id> <title>` | Append item with state `queued` |
| `list` | — | Print all items: `<state>\t<id>\t<title>` |
| `show` | `<id>` | Print single item or `not found` |
| `start` | `<id>` | Set state → `in-flight` |
| `done` | `<id>` | Set state → `done` |
| `block` | `<id>` | Set state → `blocked` |
| `ready` | `<id>` | Set state → `queued` |

All mutation verbs: parse → update in memory → render.  
Unknown verb → `fmt.Errorf("backlog manual: unknown verb %q", verb)`.

#### 5e. Create backlog.md if absent

On first `add`, if `data/backlog.md` does not exist, create it with the header:
```markdown
## Backlog

```

### Acceptance criteria

- [ ] When `config/backlog-backend` is `manual`, all verbs use the local file.
- [ ] When `tasks-axi` is absent, falls back to manual automatically.
- [ ] `munsu backlog add task-foo "My new task"` appends the item and prints confirmation.
- [ ] `munsu backlog list` prints all items with state column.
- [ ] `munsu backlog done task-foo` updates marker to `[x]` in the file.
- [ ] `munsu backlog start task-foo` updates marker to `[-]` in the file.
- [ ] Creating from an absent `backlog.md` auto-creates the file with the correct header.
- [ ] Unknown verbs return a clear error (not a panic).
- [ ] `go test ./internal/backlog/...` covers all verb handlers and parse/render round-trips.
- [ ] `go build ./... && go vet ./...` clean.

---

## Summary

| Item | Area | Type | Priority | Firstmate ref |
|---|---|---|---|---|
| 3 | `internal/secondmate` | New capability | **High** | `fm-backlog-handoff.sh`, `fm-config-inherit-lib.sh` |
| 4 | `internal/session` + `internal/hometag` | New capability | **Medium** | `fm-backend-hometag-lib.sh` |
| 5 | `internal/backlog` | New capability | **Low** | Manual backlog mode |

All items must pass `go build ./...`, `go vet ./...`, and `go test ./...`.
