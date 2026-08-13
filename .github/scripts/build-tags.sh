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
			grep -qE -- "-tags[= ]${tag}\b" "$WORKFLOW" ||
				{ echo "::error::${tag}: classified test-lane but no lane in ci.yml passes -tags ${tag}" >&2; failed=1; }
			if tag_terms "$tag" | grep -qFx "!${tag}"; then
				echo "::error::${tag}: negated somewhere, so the derived package set for its lane is wrong" >&2
				failed=1
			fi
			;;
		goos-vet)
			grep -qE -- "GOOS=${tag}\b.*go vet" "$WORKFLOW" ||
				{ echo "::error::${tag}: classified goos-vet but no 'GOOS=${tag} go vet' step in ci.yml" >&2; failed=1; }
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
	echo "build tags: $(echo "$repo_tags_list" | wc -l | tr -d ' ') classified, lanes present"
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
