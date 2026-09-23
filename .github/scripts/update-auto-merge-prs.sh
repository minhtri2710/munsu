#!/usr/bin/env bash
#
# Rebase every open PR that has auto-merge enabled onto the branch that just
# moved.
#
# `strict: true` on main's required status checks means a PR that was green on
# an older base stops being mergeable the moment main advances -- GitHub blocks
# the merge and shows an "Update branch" button, but it never presses that
# button itself. With four lanes open at once that button was being pressed by
# hand after every merge (four times on 15/08 alone). GitHub's merge queue is
# the built-in answer and is unavailable here: it requires an org-owned repo
# and this one belongs to a user account (BEO-54).
#
# Together with `allow_auto_merge`, this script reproduces the half of a merge
# queue that matters: main moves -> the queued PRs rebase -> CI re-runs on the
# new base -> green ones merge themselves.
#
# The middle arrow is not free: a branch this script updates carries a commit
# authored by github-actions[bot], and GitHub parks the resulting CI run at
# `action_required` rather than running it. See release_gated_runs below for
# what that costs in token scope -- it is the reason this job holds three write
# permissions instead of the one the API docs suggest.
#
# Three limits are deliberate, not oversights:
#
#   - Only PRs with auto-merge ALREADY enabled are touched. A PR without it is
#     someone's work in progress; rebasing it underneath them rewrites the base
#     of a branch they may be mid-edit on. Enabling auto-merge is the author's
#     explicit "this is done, land it when green", and that is the consent this
#     script acts on.
#   - Fork PRs are skipped. Updating one means writing to a branch in a
#     repository this token does not own, and `pull-requests: write` on
#     GITHUB_TOKEN is scoped to this repo -- the call would fail anyway. Left
#     as an explicit skip with a reason rather than an unexplained 403.
#   - `expected_head_sha` is always sent. Between listing the PRs and updating
#     one, its author may push; without the guard this would merge into a head
#     that is no longer the one whose behind-ness was measured. GitHub rejects
#     the stale write with 422 and the next push to main retries it.
#
# Nothing here is a gate. Every failure is a warning and the script exits 0:
# a PR that fails to update is exactly as merge-blocked as it was before this
# script ran, whereas a red run on main is a false alarm about main itself.
#
# Usage:
#   update-auto-merge-prs.sh <base-branch>   update PRs behind <base-branch>
#   update-auto-merge-prs.sh selftest        every rule above against a fake gh
#
# The base is an argument rather than a read of GITHUB_REF_NAME so the branch
# being swept is visible in the workflow file next to the trigger that fired,
# and so this can be run by hand against a branch to see what it would do.
# (It cannot be an env override either: Actions refuses to overwrite its own
# GITHUB_* defaults, so a workflow could not point this at another branch.)
#
# Environment:
#   GH_TOKEN            required by gh
#   GITHUB_REPOSITORY   owner/name of the repo
set -uo pipefail

# ---------------------------------------------------------------------------
# selftest
# ---------------------------------------------------------------------------
#
# This script holds `actions: write` and approves CI runs, so which runs it
# approves is the property worth pinning: loosen the run query or drop the
# fork skip and every lane stays green. The selftest puts a fake `gh` (and a
# no-op `sleep`) first on PATH and replays one repository with one PR per
# rule. The fake serves canned JSON through the real `jq`, so the `--jq`
# filters in this file run as written. The run asserts the exact set of
# approved runs and updated PRs, not just the exit status: this script exits 0
# on every failure by design.
#
#   #1  behind, updated, two runs parked on the NEW head   -> approve 101, 102
#       (102's approval is refused and must warn)
#   #2  fork with auto-merge                               -> never compared
#   #3  no auto-merge                                      -> never touched
#   #4  up to date                                         -> not updated
#   #5  behind, update-branch refused                      -> nothing approved
#   #6  behind, updated, nothing parked on the new head    -> nothing approved
#   #7  compare fails                                      -> not updated
#
# Decoys the script must NOT approve: 666 is parked on an OLD head, and 999 is
# returned only when the query omits `status=action_required`.
selftest_fake_gh() {
	cat <<'FAKE'
#!/usr/bin/env bash
set -u
state="$FAKE_GH_STATE"
jqexpr="" method=GET base="" pos=()
while [ $# -gt 0 ]; do
	case "$1" in
	--jq) jqexpr="$2"; shift 2 ;;
	--method) method="$2"; shift 2 ;;
	--raw-field) field="$2"; shift 2 ;;
	--base) base="$2"; shift 2 ;;
	--repo | --state | --limit | --json | --header) shift 2 ;;
	*) pos+=("$1"); shift ;;
	esac
done
out() { if [ -n "$jqexpr" ]; then jq -r "$jqexpr" <<<"$1"; else printf '%s\n' "$1"; fi; }
if [ "${pos[0]}" = pr ]; then
	[ -z "${FAKE_GH_LIST_FAIL:-}" ] || { echo "HTTP 502" >&2; exit 1; }
	echo "list base=$base" >>"$state/log"
	out '[
	 {"number":1,"headRefOid":"sha1","headRefName":"b1","isCrossRepository":false,"autoMergeRequest":{}},
	 {"number":2,"headRefOid":"sha2","headRefName":"b2","isCrossRepository":true,"autoMergeRequest":{}},
	 {"number":3,"headRefOid":"sha3","headRefName":"b3","isCrossRepository":false,"autoMergeRequest":null},
	 {"number":4,"headRefOid":"sha4","headRefName":"b4","isCrossRepository":false,"autoMergeRequest":{}},
	 {"number":5,"headRefOid":"sha5","headRefName":"b5","isCrossRepository":false,"autoMergeRequest":{}},
	 {"number":6,"headRefOid":"sha6","headRefName":"b6","isCrossRepository":false,"autoMergeRequest":{}},
	 {"number":7,"headRefOid":"sha7","headRefName":"b7","isCrossRepository":false,"autoMergeRequest":{}}]'
	exit 0
fi
ep="${pos[1]}"
case "$method $ep" in
"GET repos/o/r/compare/main..."*)
	sha="${ep##*...}"
	echo "compare $sha" >>"$state/log"
	case "$sha" in
	sha1) out '{"behind_by":2}' ;;
	sha4) out '{"behind_by":0}' ;;
	sha5 | sha6) out '{"behind_by":1}' ;;
	*) echo "HTTP 500" >&2; exit 1 ;;
	esac
	;;
"PUT repos/o/r/pulls/"*/update-branch)
	n="${ep#repos/o/r/pulls/}" n="${n%/update-branch}"
	echo "update $n $field" >>"$state/log"
	[ "$n" != 5 ] || { echo "HTTP 409 merge conflict" >&2; exit 1; }
	touch "$state/updated-$n"
	;;
"GET repos/o/r/pulls/"*)
	n="${ep#repos/o/r/pulls/}"
	echo "head $n" >>"$state/log"
	if [ -e "$state/updated-$n" ]; then out "{\"head\":{\"sha\":\"new$n\"}}"; else out "{\"head\":{\"sha\":\"sha$n\"}}"; fi
	;;
"GET repos/o/r/actions/runs?"*)
	echo "runs ${ep#repos/o/r/actions/runs?}" >>"$state/log"
	case "$ep" in
	*status=action_required*head_sha=new1* | *head_sha=new1*status=action_required*) out '{"workflow_runs":[{"id":101},{"id":102}]}' ;;
	*head_sha=new6*status=action_required* | *status=action_required*head_sha=new6*) out '{"workflow_runs":[]}' ;;
	*status=action_required*) out '{"workflow_runs":[{"id":666}]}' ;;
	*) out '{"workflow_runs":[{"id":999}]}' ;;
	esac
	;;
"POST repos/o/r/actions/runs/"*/approve)
	r="${ep#repos/o/r/actions/runs/}" r="${r%/approve}"
	echo "approve $r" >>"$state/log"
	[ "$r" != 102 ] || { echo "HTTP 403" >&2; exit 1; }
	;;
*) echo "fake gh: unexpected call: $method $ep" >&2; echo "unexpected $method $ep" >>"$state/log"; exit 1 ;;
esac
FAKE
}

selftest() {
	local failed=0 got rc want
	# Global, not local: the EXIT trap runs after this function has returned.
	scratch="$(mktemp -d)"
	trap 'rm -rf "$scratch"' EXIT
	mkdir -p "$scratch/bin" "$scratch/state"
	selftest_fake_gh >"$scratch/bin/gh"
	printf '#!/bin/sh\nexit 0\n' >"$scratch/bin/sleep"
	chmod +x "$scratch/bin/gh" "$scratch/bin/sleep"

	check_case() {
		local name="$1" want_log="$2" want_line="$3" log
		log="$(cat "$scratch/state/log" 2>/dev/null)"
		if [ "$rc" -eq 0 ] && [ "$log" = "$want_log" ] && printf '%s\n' "$got" | grep -Fq -- "$want_line"; then
			echo "  ok   $name"
		else
			echo "::error::update-auto-merge-prs disagrees with case $name (want exit 0 and '$want_line'):" >&2
			diff -u <(printf '%s\n' "$want_log") <(printf '%s\n' "$log") >&2 || true
			printf '%s\nexit %s\n' "$got" "$rc" >&2
			failed=1
		fi
	}

	# Every gh call that reaches the fake, in order. Absent lines are the
	# refusals: no compare for #2 or #3, no update for #4 or #7, no approve for
	# #5 or #6 and none for decoys 666 and 999, and no head read for
	# #5, whose update was refused. #6 polls its empty queue the full 12 times.
	want='list base=main
compare sha1
update 1 expected_head_sha=sha1
head 1
runs head_sha=new1&status=action_required
approve 101
approve 102
compare sha4
compare sha5
update 5 expected_head_sha=sha5
compare sha6
update 6 expected_head_sha=sha6
head 6
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
runs head_sha=new6&status=action_required
compare sha7'
	if got="$(PATH="$scratch/bin:$PATH" FAKE_GH_STATE="$scratch/state" GITHUB_REPOSITORY=o/r "$0" main 2>&1)"; then rc=0; else rc=$?; fi
	check_case sweep "$want" "updated=2 skipped=2 failed=2"
	for line in "PR #2 (b2): skipped, head is in a fork" \
		"PR #1 (b1): released parked CI run 101 on new1" \
		"::warning::PR #1 (b1): could not release parked CI run 102" \
		"PR #6 (b6): no CI run needed releasing on new6"; do
		printf '%s\n' "$got" | grep -Fq -- "$line" || {
			echo "::error::update-auto-merge-prs sweep did not print: $line" >&2
			failed=1
		}
	done

	rm -f "$scratch/state/"*
	if got="$(PATH="$scratch/bin:$PATH" FAKE_GH_STATE="$scratch/state" FAKE_GH_LIST_FAIL=1 GITHUB_REPOSITORY=o/r "$0" main 2>&1)"; then rc=0; else rc=$?; fi
	check_case list-fails "" "could not list open PRs for o/r; no branch was updated"

	[ "$failed" -eq 0 ] || exit 1
	echo "update-auto-merge-prs selftest: all cases agree"
}

if [ "${1:-}" = selftest ]; then
	selftest
	exit 0
fi

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is unset}"
BASE="${1:?usage: update-auto-merge-prs.sh <base-branch>}"

warn() { echo "::warning::$*"; }

# Release the CI run that our own update parked.
#
# A branch updated with GITHUB_TOKEN gets a merge commit authored by
# github-actions[bot], and GitHub will not let a token-driven event start a
# workflow run unsupervised: it creates the `pull_request` run and leaves it at
# `action_required`. Measured on PR #478 rather than assumed -- with only
# contents+pull-requests write, the bot-written head carried 0 check runs 90
# seconds later and the PR sat BLOCKED, while the same PR updated by a human
# token had all four lanes running at once.
#
# That is why this step exists at all. Without it the update makes a PR *less*
# mergeable than it was: up to date with the base and permanently unchecked,
# with auto-merge waiting on required checks that will never be reported. The
# button-press this workflow removes would come straight back as an "Approve
# and run" press, which is the same human in the same loop.
#
# The approval is narrow by construction: the only commit it can release is one
# this script just wrote, by merging the protected base branch into the head of
# a PR whose author had already asked for auto-merge. No new code enters the
# tree at this step -- the diff is exactly the base commits the PR was behind.
#
# Both waits are bounded and best-effort. A run that never appears, or an
# approval that is refused, leaves the PR exactly where the update left it and
# a human presses the button as before.
release_gated_runs() {
	local number="$1" old_sha="$2" head_ref="$3"
	local new_sha="" gated="" run=""

	for _ in $(seq 1 12); do
		new_sha="$(gh api "repos/$REPO/pulls/$number" --jq '.head.sha' 2>/dev/null)" || new_sha=""
		[ -n "$new_sha" ] && [ "$new_sha" != "$old_sha" ] && break
		sleep 5
	done
	if [ -z "$new_sha" ] || [ "$new_sha" = "$old_sha" ]; then
		warn "PR #$number ($head_ref): updated, but its new head never appeared; any parked CI run needs approving by hand"
		return
	fi

	for _ in $(seq 1 12); do
		gated="$(gh api "repos/$REPO/actions/runs?head_sha=$new_sha&status=action_required" \
			--jq '.workflow_runs[].id' 2>/dev/null)" || gated=""
		[ -n "$gated" ] && break
		sleep 5
	done
	if [ -z "$gated" ]; then
		echo "PR #$number ($head_ref): no CI run needed releasing on $new_sha"
		return
	fi

	while IFS= read -r run; do
		[ -n "$run" ] || continue
		if gh api --method POST "repos/$REPO/actions/runs/$run/approve" >/dev/null 2>&1; then
			echo "PR #$number ($head_ref): released parked CI run $run on $new_sha"
		else
			warn "PR #$number ($head_ref): could not release parked CI run $run; approve it by hand"
		fi
	done <<<"$gated"
}

# --jq over `.[]` with @base64 so a field can never word-split a loop
# iteration: PR bodies and branch names are attacker-adjacent free text.
candidates="$(
	gh pr list \
		--repo "$REPO" \
		--state open \
		--base "$BASE" \
		--limit 100 \
		--json number,headRefOid,headRefName,isCrossRepository,autoMergeRequest \
		--jq '.[] | select(.autoMergeRequest != null) | @base64'
)" || {
	warn "could not list open PRs for $REPO; no branch was updated"
	exit 0
}

if [ -z "$candidates" ]; then
	echo "no open PR targeting $BASE has auto-merge enabled; nothing to update"
	exit 0
fi

updated=0
skipped=0
failed=0

while IFS= read -r row; do
	[ -n "$row" ] || continue
	pr="$(printf '%s' "$row" | base64 --decode)"

	number="$(printf '%s' "$pr" | jq -r '.number')"
	head_sha="$(printf '%s' "$pr" | jq -r '.headRefOid')"
	head_ref="$(printf '%s' "$pr" | jq -r '.headRefName')"
	is_fork="$(printf '%s' "$pr" | jq -r '.isCrossRepository')"

	if [ "$is_fork" = "true" ]; then
		echo "PR #$number ($head_ref): skipped, head is in a fork"
		skipped=$((skipped + 1))
		continue
	fi

	# The API's own comparison rather than `mergeStateStatus`: GitHub
	# recomputes mergeability asynchronously, so within seconds of a push to
	# main a PR that is genuinely behind still reports CLEAN or UNKNOWN.
	# `behind_by` is computed from the commit graph on request and is true
	# the first time it is asked.
	behind="$(gh api "repos/$REPO/compare/$BASE...$head_sha" --jq '.behind_by' 2>&1)" || {
		warn "PR #$number ($head_ref): could not compare against $BASE: $behind"
		failed=$((failed + 1))
		continue
	}

	if [ "$behind" -eq 0 ]; then
		echo "PR #$number ($head_ref): already up to date with $BASE"
		skipped=$((skipped + 1))
		continue
	fi

	if err="$(gh api \
		--method PUT \
		--header 'Accept: application/vnd.github+json' \
		"repos/$REPO/pulls/$number/update-branch" \
		--raw-field "expected_head_sha=$head_sha" 2>&1)"; then
		echo "PR #$number ($head_ref): updated, was $behind commit(s) behind $BASE"
		updated=$((updated + 1))
		release_gated_runs "$number" "$head_sha" "$head_ref"
	else
		# Conflicts land here, and they are the author's to resolve -- the PR
		# was already unmergeable before this ran.
		warn "PR #$number ($head_ref): update failed ($behind behind): $err"
		failed=$((failed + 1))
	fi
done <<<"$candidates"

echo "updated=$updated skipped=$skipped failed=$failed"
exit 0
