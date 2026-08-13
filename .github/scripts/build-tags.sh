#!/usr/bin/env bash
#
# Single source of truth for Go build tags and their CI coverage.
#
# CI lanes derive their package lists from the source tree with `packages`
# instead of hard-coding them, so a new `//go:build integration` file in a
# sixth package joins the lane by existing. `check` guards the remaining gap:
# a brand new tag that no lane covers at all.
#
# Usage:
#   build-tags.sh packages <tag>   packages holding a file guarded by <tag>
#   build-tags.sh tags             every tag identifier used in the tree
#   build-tags.sh check            manifest and lanes agree with the tree
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MANIFEST="$ROOT/.github/build-tags.manifest"
WORKFLOW="$ROOT/.github/workflows/ci.yml"

die() { echo "::error::$*" >&2; exit 1; }

# Constraint lines, one raw expression per line ("integration", "!darwin && !linux", ...).
constraints() {
	grep -rh --include='*.go' -E '^//go:build ' "$ROOT" | sed 's|^//go:build[[:space:]]*||'
}

# "file<TAB>expression" pairs for every //go:build line, so checks can name the
# file a constraint lives in instead of just quoting the expression. -n forces
# the path:linenum:content prefix that GNU grep prints anyway, so the sed below
# also works on BSD grep, where -H alone omits the line number.
constraint_pairs() {
	grep -rHn --include='*.go' -E '^//go:build ' "$ROOT" |
		sed -E 's|^(.*):[0-9]+:[[:space:]]*//go:build[[:space:]]*(.*)$|\1\t\2|'
}

# ci.yml with comment lines dropped, so dead lane commands preserved in
# comments cannot satisfy the lane-presence greps below. Note the residual
# limitation: this filters comments, it does not evaluate `if:` conditions,
# so a step disabled via `if: false` would still satisfy the check.
workflow_body() {
	grep -vE '^[[:space:]]*#' "$WORKFLOW"
}

# Split constraint expressions into terms, keeping any leading "!".
terms() {
	sed -e 's/&&/ /g' -e 's/||/ /g' -e 's/[()]/ /g' | tr -s '[:space:]' '\n' | sed '/^$/d'
}

# Every tag identifier in the tree, polarity stripped.
repo_tags() {
	constraints | terms | sed 's/^!//' | sort -u
}

# Constraint terms mentioning <tag>, polarity kept: "integration" or "!race".
tag_terms() {
	constraints | terms | grep -Fx -e "$1" -e "!$1" || true
}

# Packages carrying <tag>. Matches the identifier anywhere in the expression,
# which is exact as long as the tag is never negated -- `check` enforces that
# for every test-lane tag, so a `//go:build !integration` file cannot quietly
# widen a lane's package set.
packages() {
	local tag="$1" dirs
	[ -n "$tag" ] || die "packages: missing tag"
	dirs="$(grep -rlE "^//go:build\b.*\b${tag}\b" --include='*.go' "$ROOT" |
		xargs -n1 dirname | sort -u | sed "s|^${ROOT}|.|")"
	[ -n "$dirs" ] || die "no package carries build tag '${tag}' -- stale lane or manifest entry"
	echo $dirs
}

check() {
	local failed=0 tag treatment reason
	[ -f "$MANIFEST" ] || die "missing $MANIFEST"

	local manifest_tags repo_tags_list
	manifest_tags="$(grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" | cut -f1 | sort -u)"
	repo_tags_list="$(repo_tags)"

	# A tag in the tree with no manifest entry is exactly the invisible-suite bug.
	local unclassified
	unclassified="$(comm -23 <(echo "$repo_tags_list") <(echo "$manifest_tags"))"
	if [ -n "$unclassified" ]; then
		echo "::error::build tag(s) used in the tree but absent from .github/build-tags.manifest:" >&2
		printf '  %s\n' $unclassified >&2
		echo "  Add a CI lane (or a documented reason it cannot have one) and classify it there." >&2
		failed=1
	fi

	# A manifest entry with no files left is a lane guarding nothing.
	local stale
	stale="$(comm -13 <(echo "$repo_tags_list") <(echo "$manifest_tags"))"
	if [ -n "$stale" ]; then
		echo "::error::manifest entries no longer matching any file:" >&2
		printf '  %s\n' $stale >&2
		failed=1
	fi

	while IFS=$'\t' read -r tag treatment reason; do
		case "$tag" in '#'* | '') continue ;; esac
		[ -n "${reason:-}" ] || { echo "::error::${tag}: manifest entry needs a reason" >&2; failed=1; }
		case "$treatment" in
		test-lane)
			if ! workflow_body | grep -E -- "-tags[= ]${tag}\b" >/dev/null; then
				echo "::error::${tag}: classified test-lane but no lane in ci.yml passes -tags ${tag}" >&2
				failed=1
			fi
			if tag_terms "$tag" | grep -qFx "!${tag}"; then
				echo "::error::${tag}: negated somewhere, so the derived package set for its lane is wrong" >&2
				failed=1
			fi
			;;
		goos-vet)
			if ! workflow_body | grep -E -- "GOOS=${tag}\b.*go vet" >/dev/null; then
				echo "::error::${tag}: classified goos-vet but no 'GOOS=${tag} go vet' step in ci.yml" >&2
				failed=1
			fi
			;;
		race-excluded)
			if tag_terms "$tag" | grep -qFx "$tag"; then
				echo "::error::${tag}: classified race-excluded but used un-negated, which no lane covers" >&2
				failed=1
			fi
			;;
		default) ;;
		*)
			echo "::error::${tag}: unknown treatment '${treatment}'" >&2
			failed=1
			;;
		esac
	done < <(grep -vE '^[[:space:]]*(#|$)' "$MANIFEST")

	[ "$failed" -eq 0 ] || exit 1
	check_conjunctions
	echo "build tags: $(echo "$repo_tags_list" | wc -l | tr -d ' ') classified, lanes present"
}

# A test-lane tag conjoined with a POSITIVE goos-vet tag is compiled by no lane:
# the test lane runs on linux (so GOOS=windows/darwin is off there) and the GOOS
# vet does not set -tags <tag>. A NEGATED goos term is legitimate and must not
# fail: `//go:build integration && !windows` compiles fine on ubuntu.
check_conjunctions() {
	local failed=0 goos_tags test_lane_tags file constraint terms_line tag term
	goos_tags="$(grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" | awk -F '\t' '$2 == "goos-vet" { print $1 }')"
	test_lane_tags="$(grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" | awk -F '\t' '$2 == "test-lane" { print $1 }')"
	[ -n "$goos_tags" ] || return 0
	[ -n "$test_lane_tags" ] || return 0
	while IFS=$'\t' read -r file constraint; do
		terms_line="$(printf '%s' "$constraint" | terms)"
		for tag in $test_lane_tags; do
			# Only a positive mention of the test-lane tag is a conjunction
			# hazard; a negated one is already an error of its own elsewhere.
			printf '%s\n' "$terms_line" | grep -Fx "$tag" >/dev/null || continue
			for term in $terms_line; do
				[[ "$term" == !* ]] && continue
				printf '%s\n' "$goos_tags" | grep -Fx "$term" >/dev/null || continue
				echo "::error::${file}: '${constraint}' conjoins test-lane tag '${tag}' with positive GOOS term '${term}': no lane compiles this file -- the ${tag} lane runs on linux (GOOS=${term} off there) and the GOOS=${term} vet does not set -tags ${tag}. Split the file or add an explicit cross-product step." >&2
				failed=1
				break
			done
		done
	done < <(constraint_pairs)
	[ "$failed" -eq 0 ] || exit 1
}

case "${1:-}" in
packages) packages "${2:-}" ;;
tags) repo_tags ;;
check) check ;;
*)
	echo "usage: build-tags.sh {packages <tag>|tags|check}" >&2
	exit 2
	;;
esac
