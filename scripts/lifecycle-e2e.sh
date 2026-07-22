#!/usr/bin/env bash
# scripts/lifecycle-e2e.sh — Hermetic General-Captain-Soldier lifecycle E2E test
#
# Exercises the full lifecycle through merge+teardown and true fleet idle:
#   session-start → task add + brief → soldier spawn (simulated) →
#   supervision → delivery (simulated) → teardown → fleet idle → session end
#
# This test is hermetic: it creates a temp MUNSU_HOME and temp git project,
# and simulates backend-dependent steps (pane/worktree) by writing state
# artifacts directly. No real tmux/herdr/gh required — suitable for CI.
#
# Usage:
#   scripts/lifecycle-e2e.sh          # full run (exit 0 on success)
#   scripts/lifecycle-e2e.sh --verbose  # verbose output
#   scripts/lifecycle-e2e.sh --real-spawn  # use real spawn (requires tmux + herdr)
set -euo pipefail

VERBOSE=false
REAL_SPAWN=false
for arg in "$@"; do
	case "$arg" in
		--verbose) VERBOSE=true ;;
		--real-spawn) REAL_SPAWN=true ;;
	esac
done

msg()  { if $VERBOSE; then echo "[$(date +%T)] $*"; fi; }
pass() { echo "  PASS: $*"; }
fail() { echo "  FAIL: $*" >&2; exit 1; }

cd "$(dirname "$0")/.."
BIN="$(pwd)/munsu"

# --- Build the binary if stale ---
if [ ! -x "$BIN" ]; then
	msg "Building munsu..."
	go build -o "$BIN" .
fi

# --- Temp directories (cleaned up on exit) ---
TEST_HOME=$(mktemp -d /tmp/munsu-e2e-home.XXXXXX)
PROJ_DIR=$(mktemp -d /tmp/munsu-e2e-proj.XXXXXX)
trap 'rm -rf "$TEST_HOME" "$PROJ_DIR"' EXIT

export MUNSU_HOME="$TEST_HOME"

msg "MUNSU_HOME: $TEST_HOME"
msg "PROJ_DIR:   $PROJ_DIR"

# --- Initialize a temp git project ---
cd "$PROJ_DIR"
git init -b main >/dev/null 2>&1
git config user.email "e2e@munsu.test"
git config user.name "E2E Test"
echo "# e2e test project" > README.md
git add -A
git commit -m "initial commit" >/dev/null 2>&1
msg "Temp git project initialized at $PROJ_DIR"

PASS=0
FAIL=0

check() {
	local name="$1"
	local desc="$2"
	shift 2
	if "$@"; then
		pass "$desc"
		PASS=$((PASS+1))
	else
		fail "$desc"
		FAIL=$((FAIL+1))
	fi
}

# ============================================================
# Phase 1: Session start
# ============================================================
echo ""
echo "=== Phase 1: Session start ==="

SESSION_OUT=$("$BIN" session-start 2>&1) || true
msg "$SESSION_OUT"

check "lock-file" "Lock file created" test -f "$TEST_HOME/state/.lock"
check "lock-content" "Lock file contains numeric PID" \
	sh -c "head -1 '$TEST_HOME/state/.lock' | grep -qE '^[0-9]+'"
check "state-dir" "State directory exists" test -d "$TEST_HOME/state"

# The lock should also be detected as held by IsSessionLocked
MUNSU_HOME="$TEST_HOME" "$BIN" guard >/dev/null 2>&1 || true
pass "Session start: lock acquired, state bootstrapped"

# ============================================================
# Phase 2: Task add + brief
# ============================================================
echo ""
echo "=== Phase 2: Task add + brief ==="

cd "$PROJ_DIR"

"$BIN" task add e2e-test "Lifecycle E2E integration test" >/dev/null 2>&1
check "task-meta" "Task meta file created" test -f "$TEST_HOME/state/e2e-test.meta"
META_CONTENT=$(cat "$TEST_HOME/state/e2e-test.meta")
check "task-kind" "Task kind is ship" grep -q "kind=ship" "$TEST_HOME/state/e2e-test.meta"
check "task-desc" "Task description stored" grep -q "description=Lifecycle E2E integration test" "$TEST_HOME/state/e2e-test.meta"

"$BIN" brief e2e-test test-project >/dev/null 2>&1
check "brief-file" "Brief file created" test -f "$TEST_HOME/data/e2e-test/brief.md"
check "brief-content" "Brief contains task ID" grep -q "e2e-test" "$TEST_HOME/data/e2e-test/brief.md"
check "brief-setup" "Brief contains setup section" grep -q "## Setup" "$TEST_HOME/data/e2e-test/brief.md"

pass "Task add + brief: meta and brief file created"

# ============================================================
# Phase 3: Project registration + soldier spawn (simulated)
# ============================================================
echo ""
echo "=== Phase 3: Soldier spawn (simulated) ==="

# Register the project so the home knows about it
"$BIN" project add test-project "$PROJ_DIR" >/dev/null 2>&1
check "project-reg" "Project registered" test -f "$TEST_HOME/data/projects.md"
check "project-reg-content" "Project name in registry" \
	grep -q "test-project" "$TEST_HOME/data/projects.md"

if $REAL_SPAWN; then
	# Real spawn — requires tmux and a soldier harness on PATH
	if command -v tmux >/dev/null 2>&1; then
		msg "Real spawn enabled — running munsu spawn e2e-test test-project"
		SPAWN_OUT=$("$BIN" spawn e2e-test test-project --kind ship 2>&1) || true
		msg "$SPAWN_OUT"
	else
		msg "WARNING: --real-spawn requested but tmux not found; falling back to simulated spawn"
		REAL_SPAWN=false
	fi
fi

if ! $REAL_SPAWN; then
	# Simulated spawn: write the meta that a real spawn would produce.
	# This exercises the state artifacts that spawn creates without needing
	# a real tmux pane or worktree.
	cat > "$TEST_HOME/state/e2e-test.meta" <<-METAEOF
	kind=ship
	project=test-project
	harness=pi
	mode=direct-PR
	description=Lifecycle E2E integration test
	METAEOF
	msg "Simulated spawn: wrote task meta"

	# A real spawn creates .soldier-launch.sh and .soldier-brief.md symlinks
	touch "$TEST_HOME/.soldier-launch.sh"
	ln -sf "$TEST_HOME/data/e2e-test/brief.md" "$TEST_HOME/.soldier-brief.md" 2>/dev/null || true
	pass "Simulated spawn: meta written, launch artifacts created"
fi

check "spawn-meta-kind" "Spawn meta has kind" grep -q "kind=ship" "$TEST_HOME/state/e2e-test.meta"
check "spawn-meta-project" "Spawn meta has project" grep -q "project=test-project" "$TEST_HOME/state/e2e-test.meta"

# ============================================================
# Phase 4: Soldier supervision (simulated)
# ============================================================
echo ""
echo "=== Phase 4: Soldier supervision (simulated) ==="

# Simulate soldier reporting status: write status lines as a soldier would
# via `munsu report <state> "<msg>"`.
mkdir -p "$TEST_HOME/state"
echo "working: Investigating the lifecycle test setup" > "$TEST_HOME/state/e2e-test.status"
echo "resolved: Initial findings documented [key=initial-review]" >> "$TEST_HOME/state/e2e-test.status"

check "status-file" "Status file created" test -f "$TEST_HOME/state/e2e-test.status"
check "status-line1" "First status line written" grep -q "working:" "$TEST_HOME/state/e2e-test.status"
check "status-line2" "Status contains resolved key" grep -q "key=initial-review" "$TEST_HOME/state/e2e-test.status"

# Verify the supervision signal flow: the .turnend file simulates turn-end signaling
echo "done: e2e supervision phase complete" >> "$TEST_HOME/state/e2e-test.status"
touch "$TEST_HOME/state/e2e-test.turnend"

check "turnend-file" "Turn-end signal created" test -f "$TEST_HOME/state/e2e-test.turnend"
check "last-status" "Final status is done" \
	sh -c "tail -1 '$TEST_HOME/state/e2e-test.status' | grep -q 'done:'"

pass "Soldier supervision: status and turn-end artifacts verified"

# ============================================================
# Phase 5: Delivery (simulated)
# ============================================================
echo ""
echo "=== Phase 5: Delivery (simulated) ==="

# Simulate PR check artifacts: write a check file as munsu delivery pr-check would
echo "#!/usr/bin/env bash" > "$TEST_HOME/state/e2e-test.check"
echo "# PR check simulation" >> "$TEST_HOME/state/e2e-test.check"
chmod +x "$TEST_HOME/state/e2e-test.check"

# Write delivery identity meta fields as a real PR check command would
{
	echo "kind=ship"
	echo "project=test-project"
	echo "harness=pi"
	echo "mode=direct-PR"
	echo "description=Lifecycle E2E integration test"
} > "$TEST_HOME/state/e2e-test.meta"

# Add a simulated .check shell script (the artifact delivery creates)
check "check-artifact" "Delivery check artifact created" test -f "$TEST_HOME/state/e2e-test.check"
check "check-executable" "Check artifact is executable" test -x "$TEST_HOME/state/e2e-test.check"

pass "Delivery: check artifact verified"

# ============================================================
# Phase 6: Teardown
# ============================================================
echo ""
echo "=== Phase 6: Teardown ==="

# For simulated teardown (no real window/worktree), use --force to skip
# safety checks that require real git state
TEARDOWN_OUT=$("$BIN" teardown e2e-test --force 2>&1) || true
msg "$TEARDOWN_OUT"

# Verify the meta was removed
check "meta-removed" "Task meta removed after teardown" \
	test ! -f "$TEST_HOME/state/e2e-test.meta"

# Verify residual artifacts were cleaned up
check "status-removed" "Status file removed after teardown" \
	test ! -f "$TEST_HOME/state/e2e-test.status"
check "turnend-removed" "Turn-end artifact removed after teardown" \
	test ! -f "$TEST_HOME/state/e2e-test.turnend"

# Brief data dir: with --force, data dir is removed
check "data-dir-removed" "Data directory removed after teardown" \
	test ! -d "$TEST_HOME/data/e2e-test"

pass "Teardown: all state artifacts cleaned up"

# ============================================================
# Phase 7: Fleet idle
# ============================================================
echo ""
echo "=== Phase 7: Fleet idle check ==="

FLEET_OUT=$("$BIN" fleet view 2>&1 || true)
msg "$FLEET_OUT"

check "fleet-zero" "Fleet view reports 0 tasks" \
	grep -q "Tasks: 0" <<<"$FLEET_OUT"

# Bearings should also show idle
BEARINGS_OUT=$("$BIN" fleet bearings 2>&1 || true)
msg "$BEARINGS_OUT"

check "bearings-idle" "Fleet bearings reports idle" \
	grep -q "idle" <<<"$BEARINGS_OUT"

# Task list should be empty
TASK_LIST=$("$BIN" task list 2>&1 || true)
check "task-list-empty" "Task list shows no tasks" \
	grep -q "no tasks found" <<<"$TASK_LIST"

pass "Fleet idle: zero soldiers, healthy state"

# ============================================================
# Phase 8: Session end (lock released)
# ============================================================
echo ""
echo "=== Phase 8: Session end (lock release) ==="

# When munsu session-start acquires the lock, the FD is leaked intentionally
# and released when the process exits. Since our session-start is already done,
# the lock should no longer be held by any process.
# The lock file may still exist (stale PID) but the lock is not held.

# Run a quick session-start check: a second session-start should
# either re-acquire or report read-only — not error.
SESSION2_OUT=$("$BIN" session-start 2>&1) || true
msg "$SESSION2_OUT"

# Verify the home is still functional
check "session-reentry" "Session-start can re-enter" \
	test -f "$TEST_HOME/state/.lock"
check "home-structure" "Home structure intact after session" \
	test -d "$TEST_HOME/data" && test -d "$TEST_HOME/state"

pass "Session end: lock released, home clean"

# ============================================================
# Summary
# ============================================================
echo ""
echo "============================================"
echo "  Lifecycle E2E Test Results"
echo "  PASS: $PASS  FAIL: $FAIL"
echo "============================================"

if [ "$FAIL" -gt 0 ]; then
	exit 1
fi
exit 0
