#!/usr/bin/env bash
#
# Report-only ledger of the tests the CI lanes skip, compared both ways against
# .github/skips.allow.
#
# Each test lane writes its `go test -json` stream to coverage/skips-<lane>.json
# (the lane steps in ci.yml), and the guards job downloads those with the
# profiles. The lanes are the default tag plus every test-lane tag in
# .github/build-tags.manifest, the same derivation uncovered-guards.sh uses.
#
# Report-only on purpose: unlisted skips and stale rows are printed and the
# script exits 0. It fails only when a lane file is missing, because a lane that
# was not observed cannot be reported as clean. Making findings fail the job
# needs the Human's words; this script is not the place to decide that.
#
# Usage:
#   skip-ledger.sh check   lanes' skips against .github/skips.allow
#   skip-ledger.sh list    every skip the lanes observed, as allow rows
set -euo pipefail

ROOT="${SKIP_LEDGER_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
MANIFEST="$ROOT/.github/build-tags.manifest"
ALLOW="$ROOT/.github/skips.allow"
DIR="${SKIP_LEDGER_DIR:-$ROOT/.github/coverage}"

die() {
	echo "::error::$*" >&2
	exit 1
}

lanes() {
	{
		echo default
		grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" |
			awk -F '\t' '$2 == "test-lane" { print $1 }'
	} | sort -u
}

lane_files() {
	local lane file
	while IFS= read -r lane; do
		file="$DIR/skips-$lane.json"
		[ -s "$file" ] || die "missing lane output ${file#"$ROOT"/} -- the lane did not write its test stream, so its skips are unobserved"
		printf '%s\n' "$file"
	done < <(lanes)
}

mode="${1:-}"
case "$mode" in
check | list) ;;
*) die "usage: skip-ledger.sh check | list" ;;
esac

listed="$(lane_files)"
files=()
while IFS= read -r f; do files+=("$f"); done <<<"$listed"
cd "$ROOT/.github/scripts/skipledger"
if [ "$mode" = check ]; then
	go run . check "$ALLOW" "${files[@]}"
else
	go run . list "${files[@]}"
fi
