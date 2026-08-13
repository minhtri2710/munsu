#!/usr/bin/env bash
#
# Single source of truth for Go build tags and their CI coverage.
#
# CI lanes derive their package lists from the source tree with `packages`
# instead of hard-coding them, so a new `//go:build integration` file in a
# sixth package joins the lane by existing. `check` guards the remaining gaps:
# a brand new tag that no lane covers at all, and a file written in the legacy
# `// +build` syntax that this derivation cannot see.
#
# What `check` does NOT do, so nobody trusts it further than it goes: the lane
# greps are textual. They assert a non-comment line in ci.yml carries the
# command; they do not evaluate `if:` conditions, so a step disabled via
# `if: false` still satisfies them. And the manifest classification is
# self-declared: `check` catches a tag whose lane was *forgotten*, not an
# author who downgrades an entry to `default` and drops the lane in the same
# diff. That case is caught by reading the diff, not by this script.
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
#
# Directories `go list ./...` deliberately skips are dropped: any path with a
# `testdata` segment, or a segment starting with `_` or `.`. Fixtures under
# `testdata/` are routinely code that is not meant to compile, so emitting them
# would hand the lane an explicit path and turn a fixture into a red lane. The
# filter is textual rather than a `go list` call because the `invariants` job
# has no Go toolchain.
packages() {
	local tag="$1" dirs
	[ -n "$tag" ] || die "packages: missing tag"
	dirs="$(grep -rlE "^//go:build\b.*\b${tag}\b" --include='*.go' "$ROOT" |
		xargs -n1 dirname | sed "s|^${ROOT}|.|" |
		{ grep -vE '/(testdata|[_.][^/]*)(/|$)' || true; } | sort -u)"
	[ -n "$dirs" ] || die "no package carries build tag '${tag}' -- stale lane or manifest entry"
	echo $dirs
}

check() {
	local failed=0 tag treatment reason
	[ -f "$MANIFEST" ] || die "missing $MANIFEST"

	# The toolchain still honors a lone `// +build` line, but every derivation
	# here reads `//go:build` only: a file carrying just the old line drops its
	# package out of the lane and never surfaces its tag to the manifest, with
	# `check` staying green -- fail-open, which is the exact failure this whole
	# mechanism exists to prevent. Fail closed instead of teaching `constraints`
	# the old grammar: the two are not interchangeable (`// +build a,b` is AND,
	# space is OR), so folding them together would mis-parse. A file carrying
	# both lines is fine -- `//go:build` is the authoritative one.
	local legacy
	legacy="$( { grep -rl --include='*.go' -E '^// \+build ' "$ROOT" || true; } |
		while IFS= read -r f; do
			[ -n "$f" ] || continue
			grep -qE '^//go:build ' "$f" || echo "${f#"$ROOT"/}"
		done)"
	if [ -n "$legacy" ]; then
		echo "::error::legacy '// +build' line with no '//go:build' line -- run gofmt on:" >&2
		printf '  %s\n' $legacy >&2
		echo "  Tag derivation reads '//go:build' only, so these files are invisible to every lane." >&2
		failed=1
	fi

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

# A test-lane tag AND-ed with a POSITIVE goos-vet tag is compiled by no lane:
# the test lane runs on linux (so GOOS=windows/darwin is off there) and the GOOS
# vet does not set -tags <tag>. That is the `//go:build e2e && windows` hole --
# invisible to every lane and, before this check, to `check` as well.
#
# Two shapes must NOT fail, or the check becomes a false-alarm generator:
#   //go:build integration && !windows   negated GOOS, builds fine on ubuntu
#   //go:build integration || windows    the integration lane still builds it
# Hence the unit of judgement is the disjunct, not the whole expression: a file
# is reachable as long as ONE disjunct is free of positive GOOS terms.
check_conjunctions() {
	local failed=0 goos_tags test_lane_tags file constraint tag
	goos_tags="$(grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" | awk -F '\t' '$2 == "goos-vet" { print $1 }')"
	test_lane_tags="$(grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" | awk -F '\t' '$2 == "test-lane" { print $1 }')"
	[ -n "$goos_tags" ] || return 0
	[ -n "$test_lane_tags" ] || return 0
	while IFS=$'\t' read -r file constraint; do
		for tag in $test_lane_tags; do
			# Only a positive mention is a hazard; a negated test-lane tag is
			# already an error of its own above.
			printf '%s' "$constraint" | terms | grep -Fx "$tag" >/dev/null || continue

			local reachable=0 blocker="" disjunct hit
			while IFS= read -r disjunct; do
				[ -n "${disjunct// /}" ] || continue
				# `|| true`: no match is the common case, and under pipefail a
				# failing grep inside $( ) would abort the script via set -e.
				hit="$(printf '%s' "$disjunct" | terms | grep -v '^!' |
					grep -Fx -f <(printf '%s\n' "$goos_tags") | head -1 || true)"
				if [ -z "$hit" ]; then
					reachable=1
					break
				fi
				[ -n "$blocker" ] || blocker="$hit"
			done < <(printf '%s' "$constraint" | awk -F '\\|\\|' '{ for (i = 1; i <= NF; i++) print $i }')

			[ "$reachable" -eq 0 ] || continue
			echo "::error::${file}: '${constraint}' pairs test-lane tag '${tag}' with positive GOOS term '${blocker}' in every branch: no lane compiles this file -- the ${tag} lane runs on linux (GOOS=${blocker} is off there) and the GOOS=${blocker} vet does not set -tags ${tag}. Split the file, or add an explicit cross-product step and classify it." >&2
			failed=1
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
