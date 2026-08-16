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
