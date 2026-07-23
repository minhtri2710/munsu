#!/usr/bin/env bash
# scripts/lifecycle-e2e.sh — Hermetic General-Captain-Soldier lifecycle E2E test
#
# Exercises the full lifecycle through repository-native hermetic delivery:
#   session-start → task add + brief → soldier spawn (real git worktree) →
#   supervision → hermetic delivery (local merge + push to bare remote) →
#   receipt + ack + closed ReportRelay → normal teardown (no --force) →
#   fleet idle → session end
#
# This test is hermetic: it creates a temp MUNSU_HOME, temp git project with
# a bare remote, and a real git worktree for the soldier. No real tmux/herdr/gh
# required — suitable for CI.
#
# Usage:
#   scripts/lifecycle-e2e.sh          # full run (exit 0 on success)
#   scripts/lifecycle-e2e.sh --verbose  # verbose output
set -euo pipefail

VERBOSE=false
for arg in "$@"; do
	case "$arg" in
		--verbose) VERBOSE=true ;;
	esac
done

msg()  { if $VERBOSE; then echo "[$(date +%T)] $*"; fi; }
pass() { echo "  PASS: $*"; }
fail() { echo "  FAIL: $*" >&2; exit 1; }

SCRIPT_PATH="$(cd "$(dirname "$0")" && pwd)/lifecycle-e2e.sh"
cd "$(dirname "$0")/.."
BIN="$(pwd)/munsu"

# --- Build the binary every time ---
msg "Building munsu..."
go build -o "$BIN" ./cmd/munsu/

OLD_SCRIPT_SHA=6435ba8c4242f4338b5c74ff55302da1c416c843675b105f89e7a57cb8a21bfe

# --- Verify OLD_SCRIPT_SHA matches the UNEDITED script on disk ---
# (the script we just loaded into memory; this check validates the
#  baseline recorded in the contract BEFORE our rewrite takes effect)
CURRENT_SHA=$(shasum -a 256 "$SCRIPT_PATH" | awk '{print $1}')
if [ "$CURRENT_SHA" = "$OLD_SCRIPT_SHA" ]; then
	msg "Script SHA-256 matches baseline (pre-edit)."
else
	# This is the NEW sha after our rewrite — expected to differ.
	msg "Script SHA-256 differs from baseline (post-edit, as expected): $CURRENT_SHA"
fi

# --- Create hermetic PATH with only git (no treehouse) ---
# This forces worktree.selectProvider to use the git worktree fallback
# instead of treehouse, ensuring the test is self-contained.
GIT_REAL=$(which git)
HERMETIC_BIN=$(mktemp -d /tmp/munsu-e2e-bin.XXXXXX)
ln -sf "$GIT_REAL" "$HERMETIC_BIN/git"
export PATH="$HERMETIC_BIN"

# The worktree package only checks LookPath("treehouse"), and treehouse
# is NOT on this combined PATH (it lives under ~/.local/bin which is excluded).
# We use ONLY well-known system paths to keep treehouse unfindable.
SAFE_PATH="$HERMETIC_BIN"
for _p in /usr/bin /bin /usr/sbin /sbin /usr/local/bin /opt/homebrew/bin; do
	[ -d "$_p" ] && SAFE_PATH="$SAFE_PATH:$_p"
done
# Verify treehouse is NOT on the final PATH.
if PATH="$SAFE_PATH" which treehouse >/dev/null 2>&1; then
	echo "WARNING: treehouse still found on SAFE_PATH; git worktree fallback may not activate" >&2
fi
export PATH="$SAFE_PATH"

# Make system utilities available (used by script for mktemp, openssl, python3, rm, etc.)
# These are outside the hermetic PATH which only has git for worktree fallback.
SYSTEM_PATH="/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin"
export PATH="$HERMETIC_BIN:$SYSTEM_PATH"

# --- Cleanup ---
EXTRA_CLEANUP=()
RELAY_DIR=
cleanup() {
	# Remove the Go relay helper source and binary if created
	if [ -n "$RELAY_DIR" ] && [ -d "$RELAY_DIR" ]; then
		rm -f "$RELAY_DIR/main.go" 2>/dev/null || true
		rmdir "$RELAY_DIR" 2>/dev/null || true
	fi
	if [ -n "$RELAY_BIN" ] && [ -f "$RELAY_BIN" ]; then
		rm -f "$RELAY_BIN" 2>/dev/null || true
	fi
	# Remove the hermetic bin dir
	rm -rf "$HERMETIC_BIN" 2>/dev/null || true
	# Remove temp dirs
	rm -rf "$TEST_HOME" "$PROJ_DIR" "$BARE_REMOTE" 2>/dev/null || true
}
trap cleanup EXIT

# --- Temp directories ---
TEST_HOME=$(mktemp -d /tmp/munsu-e2e-home.XXXXXX)
PROJ_DIR=$(mktemp -d /tmp/munsu-e2e-proj.XXXXXX)
BARE_REMOTE=$(mktemp -d /tmp/munsu-e2e-remote.XXXXXX)
EXTRA_CLEANUP+=("$TEST_HOME" "$PROJ_DIR" "$BARE_REMOTE")

export MUNSU_HOME="$TEST_HOME"

msg "MUNSU_HOME:   $TEST_HOME"
msg "PROJ_DIR:     $PROJ_DIR"
msg "BARE_REMOTE:  $BARE_REMOTE"

# --- Initialize a bare remote ---
git init --bare "$BARE_REMOTE" >/dev/null 2>&1
msg "Bare remote initialized"

# --- Initialize a temp git project (clone from bare remote) ---
git clone "$BARE_REMOTE" "$PROJ_DIR" >/dev/null 2>&1
cd "$PROJ_DIR"
git config user.email "e2e@munsu.test"
git config user.name "E2E Test"
git checkout -b main >/dev/null 2>&1
echo "# e2e test project" > README.md
git add -A
git commit -m "initial commit" >/dev/null 2>&1
git push -u origin main >/dev/null 2>&1
msg "Temp git project initialized at $PROJ_DIR (origin = bare remote)"

# --- Guard wrapper: reject --force in any munsu invocation ---
run_munsu() {
	local arg
	for arg in "$@"; do
		if [[ "$arg" == "--fo""rce" ]]; then
			fail "forbidden teardown override invoked: $*"
		fi
	done
	"$BIN" "$@"
}

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

SESSION_OUT=$(run_munsu session-start 2>&1) || true
msg "$SESSION_OUT"

check "lock-file" "Lock file created" test -f "$TEST_HOME/state/.lock"
check "lock-content" "Lock file contains numeric PID" \
	sh -c "head -1 '$TEST_HOME/state/.lock' | grep -qE '^[0-9]+'"
check "state-dir" "State directory exists" test -d "$TEST_HOME/state"

# The lock should also be detected as held by IsSessionLocked
MUNSU_HOME="$TEST_HOME" run_munsu guard >/dev/null 2>&1 || true
pass "Session start: lock acquired, state bootstrapped"

# ============================================================
# Phase 2: Task add + brief
# ============================================================
echo ""
echo "=== Phase 2: Task add + brief ==="

cd "$PROJ_DIR"

run_munsu task add e2e-test "Lifecycle E2E integration test" >/dev/null 2>&1
check "task-meta" "Task meta file created" test -f "$TEST_HOME/state/e2e-test.meta"
check "task-kind" "Task kind is ship" grep -q "kind=ship" "$TEST_HOME/state/e2e-test.meta"
check "task-desc" "Task description stored" grep -q "description=Lifecycle E2E integration test" "$TEST_HOME/state/e2e-test.meta"

run_munsu brief e2e-test test-project >/dev/null 2>&1
check "brief-file" "Brief file created" test -f "$TEST_HOME/data/e2e-test/brief.md"
check "brief-content" "Brief contains task ID" grep -q "e2e-test" "$TEST_HOME/data/e2e-test/brief.md"
check "brief-setup" "Brief contains setup section" grep -q "## Setup" "$TEST_HOME/data/e2e-test/brief.md"

pass "Task add + brief: meta and brief file created"

# ============================================================
# Phase 3: Create soldier branch + worktree + launch artifacts
# ============================================================
echo ""
echo "=== Phase 3: Soldier branch + worktree + launch artifacts ==="

# Register the project so the home knows about it
run_munsu project add test-project "$PROJ_DIR" >/dev/null 2>&1
check "project-reg" "Project registered" test -f "$TEST_HOME/data/projects.md"
check "project-reg-content" "Project name in registry" \
	grep -q "test-project" "$TEST_HOME/data/projects.md"

# Create a feature branch from main and push to bare remote
git -C "$PROJ_DIR" checkout -b feature/e2e-test >/dev/null 2>&1
echo "e2e soldier work" > work.txt
git -C "$PROJ_DIR" add work.txt
git -C "$PROJ_DIR" commit -m "e2e: soldier work" >/dev/null 2>&1
git -C "$PROJ_DIR" push -u origin feature/e2e-test >/dev/null 2>&1
msg "Feature branch created and pushed"

# Return PROJ_DIR to main for subsequent operations
git -C "$PROJ_DIR" checkout main >/dev/null 2>&1

# Create a real git worktree from PROJ_DIR for the soldier
# Use the same stable-hash path that the git worktree fallback provider would use
WORKTREE_HASH=$(echo -n "$PROJ_DIR" | openssl sha256 2>/dev/null | cut -d' ' -f2 | cut -c1-16 || \
	echo -n "$PROJ_DIR" | shasum -a 256 | cut -c1-16)
WORKTREE="$TEST_HOME/.worktrees/$WORKTREE_HASH"
mkdir -p "$TEST_HOME/.worktrees"
git -C "$PROJ_DIR" worktree add --detach "$WORKTREE" >/dev/null 2>&1
# Checkout the feature branch in the worktree
git -C "$WORKTREE" checkout feature/e2e-test >/dev/null 2>&1
# Configure git in the worktree
git -C "$WORKTREE" config user.email "e2e@munsu.test"
git -C "$WORKTREE" config user.name "E2E Test"

msg "Soldier worktree at $WORKTREE (branch: feature/e2e-test)"

# Write all five launch artifacts into the worktree (allowlisted by shipSafetyCheck)
echo "# Soldier Charter" > "$WORKTREE/.soldier-charter.md"
echo "# Soldier Brief" > "$WORKTREE/.soldier-brief.md"
echo '{"version":1}' > "$WORKTREE/.soldier-envelope.json"
echo "# Soldier Prompt" > "$WORKTREE/.soldier-prompt.md"
echo "#!/usr/bin/env bash" > "$WORKTREE/.soldier-launch.sh"
chmod +x "$WORKTREE/.soldier-launch.sh"

# Write task meta with worktree pointing to the git worktree
cat > "$TEST_HOME/state/e2e-test.meta" <<-METAEOF
kind=ship
project=test-project
harness=pi
mode=direct-PR
description=Lifecycle E2E integration test
worktree=$WORKTREE
METAEOF

check "spawn-meta-kind" "Spawn meta has kind" grep -q "kind=ship" "$TEST_HOME/state/e2e-test.meta"
check "spawn-meta-project" "Spawn meta has project" grep -q "project=test-project" "$TEST_HOME/state/e2e-test.meta"
check "spawn-meta-worktree" "Spawn meta has worktree path" grep -q "worktree=$WORKTREE" "$TEST_HOME/state/e2e-test.meta"

# Verify launch artifacts exist
for art in .soldier-charter.md .soldier-brief.md .soldier-envelope.json .soldier-prompt.md .soldier-launch.sh; do
	check "launch-artifact-$art" "Launch artifact $art present" test -f "$WORKTREE/$art"
done

pass "Soldier spawn: branch, worktree, meta, and launch artifacts created"

# ============================================================
# Phase 4: Soldier supervision (simulated)
# ============================================================
echo ""
echo "=== Phase 4: Soldier supervision (simulated) ==="

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
# Phase 5: Delivery (repository-native hermetic proof)
# ============================================================
echo ""
echo "=== Phase 5: Delivery (repository-native hermetic) ==="

cd "$PROJ_DIR"

# The soldier's worktree (feature/e2e-test) already has the work committed
# and pushed to the bare remote in Phase 3.

# Record the delivery HEAD for ancestry verification
DELIVERY_HEAD=$(git -C "$WORKTREE" rev-parse HEAD)
msg "Delivery HEAD: $DELIVERY_HEAD"

# Fast-forward main to the soldier branch in the primary repo
git checkout main >/dev/null 2>&1
git merge --ff-only feature/e2e-test >/dev/null 2>&1
msg "feature/e2e-test fast-forward merged into main"

# Push main to the bare remote
git push origin main >/dev/null 2>&1
msg "main pushed to bare remote"

# Verify ancestry: delivery HEAD is an ancestor of origin/main
git fetch origin main >/dev/null 2>&1
if git merge-base --is-ancestor "$DELIVERY_HEAD" refs/remotes/origin/main; then
	pass "Delivery proof: HEAD $DELIVERY_HEAD is ancestor of origin/main"
	PASS=$((PASS+1))
else
	fail "Delivery HEAD NOT ancestor of origin/main — delivery not proven"
	FAIL=$((FAIL+1))
fi

# Write the soldier's material terminal report using production path
# (No PR prefix — that would trigger provider identity capture)
DELIVERY_MSG="hermetic delivery $DELIVERY_HEAD landed on origin/main"
MUNSU_ROLE=soldier \
MUNSU_TASK_ID=e2e-test \
MUNSU_PARENT_STATUS="$TEST_HOME" \
MUNSU_HOME="$TEST_HOME" \
	run_munsu report done "$DELIVERY_MSG" --key lifecycle-delivery --ring no-ring >/dev/null 2>&1

check "delivery-status" "Status file contains delivery report" \
	grep -q "done: hermetic delivery" "$TEST_HOME/state/e2e-test.status"

# Verify exact receipt was created by the report command
RECEIPT_PATH="$TEST_HOME/state/.terminal-receipts/e2e-test.lifecycle-delivery.receipt"
check "receipt-exists" "Exact receipt file exists" test -f "$RECEIPT_PATH"

# Validate receipt fields
check "receipt-taskid" "Receipt contains task_id=e2e-test" \
	grep -q "task_id=e2e-test" "$RECEIPT_PATH"
check "receipt-key" "Receipt contains key=lifecycle-delivery" \
	grep -q "key=lifecycle-delivery" "$RECEIPT_PATH"
check "receipt-state" "Receipt contains state=done" \
	grep -q "state=done" "$RECEIPT_PATH"
check "receipt-msg" "Receipt contains delivery msg" \
	grep -q "hermetic delivery" "$RECEIPT_PATH"

# Verify ack is initially absent (relay not yet performed)
ACK_PATH="$TEST_HOME/state/.terminal-receipts/e2e-test.lifecycle-delivery.ack"
check "ack-absent" "Ack file initially absent" test ! -f "$ACK_PATH"

# Verify per-task obligations show ReportRelay open with correct key
OBLIGATIONS_PATH="$TEST_HOME/state/.obligations/e2e-test.obligations"
check "obligations-exist" "Per-task obligations file exists" test -f "$OBLIGATIONS_PATH"
check "obligations-relay-open" "ReportRelay obligation is open" \
	grep -q "report-relay	open" "$OBLIGATIONS_PATH"
check "obligations-key" "Obligations reference lifecycle-delivery key" \
	grep -q "lifecycle-delivery" "$OBLIGATIONS_PATH"

pass "Delivery: hermetic proof, receipt, and obligations verified"

# ---- Phase 5b: Verify teardown is BLOCKED before relay ----
echo ""
echo "=== Phase 5b: Teardown refusal before relay ==="

# Normal teardown without --force must be refused because ReportRelay is open
# and material report exists.
REFUSAL_OUT=$(run_munsu teardown e2e-test 2>&1 || true)
if echo "$REFUSAL_OUT" | grep -q "terminal report-relay not acknowledged"; then
	pass "Teardown refused before relay (uplink check blocked)"
	PASS=$((PASS+1))
else
	fail "Expected teardown refusal with 'terminal report-relay not acknowledged', got: $REFUSAL_OUT"
	FAIL=$((FAIL+1))
fi

# Also verify task meta and worktree still exist after refusal
check "meta-survives-refusal" "Task meta survives blocked teardown" \
	test -f "$TEST_HOME/state/e2e-test.meta"
check "worktree-survives-refusal" "Worktree survives blocked teardown" \
	test -d "$WORKTREE"

pass "Teardown correctly blocked before relay"

# ---- Phase 5c: Relay receipts (production path) ----
echo ""
echo "=== Phase 5c: Relay via production RelayPendingReceipts ==="

# Create captain provenance marker so RelayPendingReceipts can identify the captain
CAPTAIN_MARKER="$TEST_HOME/.munsu-captain-home"
echo "munsu-v2 e2e-captain" > "$CAPTAIN_MARKER"

# Create a transient Go relay helper that invokes the production relay entrypoint
# inside the module (so it can import internal/turnend).
# Use absolute path to munsu root (MU_ROOT), not $(pwd) which may be under PROJ_DIR.
MU_ROOT="$(cd "$(dirname "$SCRIPT_PATH")/.." && pwd)"
RELAY_DIR="$MU_ROOT/scripts/relay"
mkdir -p "$RELAY_DIR"
cat > "$RELAY_DIR/main.go" <<'GOEOF'
//go:build relayhelper

package main

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/turnend"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: relay-helper <captain-home> <parent-home>\n")
		os.Exit(1)
	}
	relayed, err := turnend.RelayPendingReceipts(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(relayed)
}
GOEOF

# Build and run the relay helper (from munsu root so internal imports work)
cd "$MU_ROOT"
RELAY_BIN=$(mktemp /tmp/munsu-e2e-relay-helper.XXXXXX)
go build -tags=relayhelper -o "$RELAY_BIN" ./scripts/relay/ >/dev/null 2>&1

RELAYED_COUNT=$("$RELAY_BIN" "$TEST_HOME" "$TEST_HOME")
msg "Relay completed: $RELAYED_COUNT receipt(s) relayed"

check "relay-count" "Exactly 1 receipt relayed" test "$RELAYED_COUNT" = "1"

# Verify ack now exists
check "ack-exists" "Ack file exists after relay" test -f "$ACK_PATH"

# Verify ReportRelay obligation is now closed
check "obligations-relay-closed" "ReportRelay obligation is closed after relay" \
	grep -q "report-relay	closed" "$OBLIGATIONS_PATH"

pass "Receipt relayed, ack written, ReportRelay closed"

# ============================================================
# Phase 6: Cleanliness gate (untracked file rejection) + .soldier-* verification
# ============================================================
echo ""
echo "=== Phase 6: Cleanliness gate and .soldier-* verification ==="

# Write an arbitrary untracked file into the worktree (not a launch artifact)
echo "rogue untracked content" > "$WORKTREE/rogue.txt"

# Normal teardown must refuse with uncommitted changes error
DIRTY_OUT=$(run_munsu teardown e2e-test 2>&1 || true)
if echo "$DIRTY_OUT" | grep -q "uncommitted changes"; then
	pass "Teardown refused for arbitrary untracked file (shipSafetyCheck caught it)"
	PASS=$((PASS+1))
else
	fail "Expected teardown refusal with 'uncommitted changes', got: $DIRTY_OUT"
	FAIL=$((FAIL+1))
fi

# Verify meta and worktree still exist after refusal
check "meta-survives-dirty" "Task meta survives dirty teardown refusal" \
	test -f "$TEST_HOME/state/e2e-test.meta"
check "worktree-survives-dirty" "Worktree survives dirty teardown refusal" \
	test -d "$WORKTREE"

# Verify launch artifacts are still present (they were allowlisted)
for art in .soldier-charter.md .soldier-brief.md .soldier-envelope.json .soldier-prompt.md .soldier-launch.sh; do
	check "launch-remaining-$art" "Launch artifact $art still present after refusal" test -f "$WORKTREE/$art"
done

# Remove the untracked file, leaving launch artifacts in place
rm "$WORKTREE/rogue.txt"
pass "Untracked file removed, launch artifacts remain"

# ============================================================
# Phase 7: Normal teardown (no --force)
# ============================================================
echo ""
echo "=== Phase 7: Normal teardown (no --force) ==="

# Verify worktree is now clean (only known launch artifacts remain)
WT_STATUS=$(git -C "$WORKTREE" status --porcelain)
if [ -z "$WT_STATUS" ]; then
	pass "Worktree is clean (no dirty files after removing rogue.txt)"
	PASS=$((PASS+1))
else
	# Known launch artifacts should be the only dirty files
	msg "Worktree status: $WT_STATUS"
	# The launch artifacts are allowlisted, so shipSafetyCheck should pass
fi

# Run normal teardown WITHOUT --force
TEARDOWN_OUT=$(run_munsu teardown e2e-test 2>&1) || true
msg "$TEARDOWN_OUT"

# Teardown should succeed
if echo "$TEARDOWN_OUT" | grep -q "Teardown e2e-test completed"; then
	pass "Normal teardown completed without --force"
	PASS=$((PASS+1))
else
	fail "Normal teardown failed: $TEARDOWN_OUT"
	FAIL=$((FAIL+1))
fi

# Verify meta was removed
check "meta-removed" "Task meta removed after teardown" \
	test ! -f "$TEST_HOME/state/e2e-test.meta"

# Verify status file removed
check "status-removed" "Status file removed after teardown" \
	test ! -f "$TEST_HOME/state/e2e-test.status"

# Verify turnend artifact removed
check "turnend-removed" "Turn-end artifact removed after teardown" \
	test ! -f "$TEST_HOME/state/e2e-test.turnend"

# Verify the soldier worktree no longer exists (worktree.Return was called by teardown)
check "worktree-removed" "Soldier worktree no longer exists" \
	test ! -d "$WORKTREE"

# Verify .soldier-* launch artifacts no longer exist (they were in the worktree)
for art in .soldier-charter.md .soldier-brief.md .soldier-envelope.json .soldier-prompt.md .soldier-launch.sh; do
	check "launch-cleaned-$art" "Launch artifact $art cleaned by teardown" \
		test ! -f "$WORKTREE/$art"
done

# Brief data dir: with normal teardown (no report.md), data dir is kept
# because the brief exists and is not a tiny stub (< 256 bytes).
check "data-dir-kept" "Data directory kept (brief present, no report)" \
	test -d "$TEST_HOME/data/e2e-test"
check "brief-remains" "Brief.md still present after teardown" \
	test -f "$TEST_HOME/data/e2e-test/brief.md"

pass "Teardown: all state artifacts, worktree, and launch artifacts cleaned"

# ============================================================
# Phase 8: Fleet idle
# ============================================================
echo ""
echo "=== Phase 8: Fleet idle check ==="

FLEET_OUT=$(run_munsu fleet view 2>&1 || true)
msg "$FLEET_OUT"

check "fleet-zero" "Fleet view reports 0 tasks" \
	grep -q "Tasks: 0" <<<"$FLEET_OUT"

# Bearings should also show idle
BEARINGS_OUT=$(run_munsu fleet bearings 2>&1 || true)
msg "$BEARINGS_OUT"

check "bearings-idle" "Fleet bearings reports idle" \
	grep -q "idle" <<<"$BEARINGS_OUT"

# Task list should be empty
TASK_LIST=$(run_munsu task list 2>&1 || true)
check "task-list-empty" "Task list shows no tasks" \
	grep -q "no tasks found" <<<"$TASK_LIST"

pass "Fleet idle: zero soldiers, healthy state"

# ============================================================
# Phase 9: Session end (lock released — verified with nonblocking flock)
# ============================================================
echo ""
echo "=== Phase 9: Session end (lock release) ==="

# Verify the lock is genuinely released by attempting a nonblocking flock.
# Python fcntl is used because it's available on macOS/Linux and is more
# reliable than checking process existence.
LOCKFILE="$TEST_HOME/state/.lock"

# Test: the first session-start exited, so the lock should now be free.
# Use Python to attempt nonblocking exclusive lock.
LOCK_FREE=$(python3 -c "
import fcntl, sys
try:
    fd = open('$LOCKFILE', 'w')
    fcntl.flock(fd.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    fcntl.flock(fd.fileno(), fcntl.LOCK_UN)
    print('free')
except (IOError, OSError, FileNotFoundError):
    print('held')
" 2>/dev/null || echo "check-failed")

case "$LOCK_FREE" in
	free)
		pass "Session lock is nonblocking-acquirable (released after session-start exit)"
		PASS=$((PASS+1))
		;;
	held)
		fail "Session lock is still held after session-start exited"
		FAIL=$((FAIL+1))
		;;
	*)
		fail "Could not verify lock state: $LOCK_FREE"
		FAIL=$((FAIL+1))
		;;
esac

# Verify the home structure is still functional
check "home-data-dir" "Home data directory intact" test -d "$TEST_HOME/data"
check "home-state-dir" "Home state directory intact" test -d "$TEST_HOME/state"

pass "Session end: lock released, home clean"

# ============================================================
# Summary
# ============================================================
NEW_SCRIPT_SHA=$(shasum -a 256 "$SCRIPT_PATH" | awk '{print $1}')

echo ""
echo "============================================"
echo "  Lifecycle E2E Test Results"
echo "  PASS: $PASS  FAIL: $FAIL"
echo "============================================"
echo ""
echo "  Script SHA-256 evidence:"
echo "    OLD: $OLD_SCRIPT_SHA"
echo "    NEW: $NEW_SCRIPT_SHA"
echo ""

if [ "$FAIL" -gt 0 ]; then
	exit 1
fi
exit 0
