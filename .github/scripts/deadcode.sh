#!/usr/bin/env bash
#
# Reachability lane: every function that the `munsu` binary cannot reach must be
# accounted for in .github/deadcode.allow.
#
# This exists because a guard with no call site is invisible to every other lane
# in this repo. `EnsureNotPrimary` (internal/backend/worktree.go) has four tests
# and its refusal branch has coverage count 2 -- a perfect mutation score on a
# function production never calls. No measurement taken *inside* a function can
# see that, which is why reachability is its own lane and not a test. See
# ADR-0009 and BEO-63.
#
# The analysis root is `./cmd/munsu` and `-test` is deliberately OFF. A guard
# that only its own test calls is unreachable from the shipped binary, and that
# is exactly the finding this lane exists to produce. Turning `-test` on would
# make `EnsureNotPrimary` "reachable" and silence the case the whole mechanism
# was built for. Do not add it.
#
# The allow file is compared BOTH ways, like .github/build-tags.manifest:
# unreachable in the tree but absent from the file is red, and present in the
# file but no longer unreachable is red too. The second direction is what keeps
# the file shrinking instead of rotting into a graveyard of names that no longer
# exist.
#
# What this does NOT do, so nobody trusts it further than it goes: the reason
# column is self-declared. Nothing stops someone pasting the burn-down marker
# onto a brand new dead guard and keeping the lane green. Reading the diff is
# what catches that -- the same limit .github/build-tags.manifest writes about
# itself. This lane catches the accident, not the intent.
#
# Usage:
#   deadcode.sh list    current unreachable set, in allow-file key format
#   deadcode.sh check   tree and allow file agree, in both directions
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ALLOW="$ROOT/.github/deadcode.allow"
MANIFEST="$ROOT/.github/build-tags.manifest"

die() { echo "::error::$*" >&2; exit 1; }

# Every GOOS this repo builds for. One run per platform, unioned, because a
# GOOS-gated file only exists in the build that selects it: `platformProcessIdentity`
# lives in both process_identity_linux.go and process_identity_darwin.go, and a
# single run sees exactly one of them. Analyzing only the runner's platform
# would leave the other file permanently outside the lane -- the same shape of
# hole that hid a compile break behind `//go:build e2e` for four months (BEO-25).
#
# It also makes the answer independent of who is asking: a baseline generated on
# a macOS laptop was red on the linux runner for these two pairs, which is how
# this was found. With the union, `check` gives the same result everywhere.
#
# Derived from .github/build-tags.manifest rather than listed here, so a fourth
# platform joins this lane by being classified there instead of by someone
# remembering this file exists. `linux` is classified `default` (ubuntu
# satisfies it), so it is added explicitly.
goos_list() {
	[ -f "$MANIFEST" ] || die "missing ${MANIFEST#"$ROOT"/}"
	{
		echo linux
		grep -vE '^[[:space:]]*(#|$)' "$MANIFEST" | awk -F '\t' '$2 == "goos-vet" { print $1 }'
	} | sort -u
}

# "file<TAB>func" for every unreachable function, sorted.
#
# The line:col is dropped on purpose. It is not part of the identity of an
# entry: keeping it would rewrite this file every time an unrelated edit shifts
# a function down a few lines, and a baseline that churns on unrelated diffs is
# a baseline nobody reads. file+name is unique across the current tree (checked)
# and survives the moves that line numbers do not.
#
# deadcode exits 0 whether or not it finds anything, and reports a broken build
# on stderr with a non-zero status, so the status is the only signal that the
# analysis actually ran. Fail closed on it: an empty finding set from a tool
# that never loaded the packages would otherwise read as "nothing is dead".
#
# A line this pattern does not understand is fatal for the same reason. Dropping
# it would delete a finding -- the one shape of failure this lane must never
# have -- and a future release adding a second diagnostic form would silence
# every instance of it with the lane green.
tree_entries() {
	local raw unparsed goos all="" platforms
	# Resolved before the loop: `for goos in $(goos_list)` would swallow a
	# failure there and analyze nothing, which reads as "everything is
	# reachable" -- fail-open in the one place that must not be.
	platforms="$(goos_list)" || exit 1
	[ -n "$platforms" ] || die "no GOOS to analyze -- ${MANIFEST#"$ROOT"/} classifies none"
	for goos in $platforms; do
		raw="$(cd "$ROOT" && GOOS="$goos" deadcode ./cmd/munsu)" ||
			die "deadcode could not analyze ./cmd/munsu for GOOS=${goos}, so it cannot judge reachability"
		all="${all}${raw}"$'\n'
	done
	# Paths come out relative to the module root; strip an absolute prefix
	# anyway so the keys are stable if that ever changes.
	raw="$(printf '%s\n' "$all" | sed -E "s|^${ROOT}/||" | { grep -v '^[[:space:]]*$' || true; })"
	unparsed="$(printf '%s\n' "$raw" | { grep -vE '^.+:[0-9]+:[0-9]+: unreachable func: .+$' || true; })"
	if [ -n "$unparsed" ]; then
		echo "::error::deadcode printed output this lane cannot read, so a finding could be lost:" >&2
		printf '%s\n' "$unparsed" | while IFS= read -r line; do
			printf '  %s\n' "$line" >&2
		done
		exit 1
	fi
	printf '%s\n' "$raw" |
		sed -E 's|^(.+):[0-9]+:[0-9]+: unreachable func: (.+)$|\1\t\2|' | sort -u
}

# "file<TAB>func" for every allow-file entry, sorted. -u so a duplicate cannot
# reach the comparison below and get reported there as something it is not;
# allow_format_errors has already failed the run for it by name.
allow_entries() {
	grep -vE '^[[:space:]]*(#|$)' "$ALLOW" | cut -f1,2 | sort -u
}

# Everything wrong with the file as a file, before it is compared to anything.
# A malformed line matters more than it looks: parsed as nothing, it falls out
# of the comparison in *both* directions at once, so its function ends up
# neither required nor waived -- fail-open, which is the whole thing this lane
# exists to avoid. A duplicate is the same hazard from the other side; catching
# it here keeps the comparison below working on a clean set, so it does not have
# to report a second copy as some other kind of problem.
#
# awk rather than a `read` loop: bash 3.2 -- still what ships on macOS, where
# this repo is developed -- misparses a `case` inside a command substitution,
# and the loop needs both. Errors go to stdout and the caller redirects, since
# writing to /dev/stderr from awk is not portable across mawk and BWK awk.
allow_format_errors() {
	awk -F '\t' -v name="${ALLOW#"$ROOT"/}" '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		NF < 2 || $1 == "" || $2 == "" {
			printf "::error::%s:%d: expected file<TAB>func<TAB>reason\n", name, NR
			bad = 1
			next
		}
		NF < 3 || $3 == "" {
			printf "::error::%s:%d: %s: %s: entry needs a reason in the third column\n", name, NR, $1, $2
			bad = 1
		}
		{
			key = $1 "\t" $2
			if (key in seen) {
				printf "::error::%s:%d: %s: %s: already listed on line %d\n", name, NR, $1, $2, seen[key]
				bad = 1
			} else {
				seen[key] = NR
			}
		}
		END { exit bad }
	' "$ALLOW"
}

# Entries whose reason marks them as a known-open bug rather than accepted debt,
# reprinted on every run. The two guards behind BEO-64 are real holes, listed
# only so the lane could go green on the commit that introduces it; an
# annotation on every single run is the difference between a waiver and a silent
# line.
announce_open_bugs() {
	awk -F '\t' '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		$3 ~ /^OPEN-BUG/ {
			printf "::warning file=%s::%s is unreachable and known to be a bug, not accepted debt: %s\n", $1, $2, $3
		}
	' "$ALLOW"
}

check() {
	local failed=0 tree allow added removed
	[ -f "$ALLOW" ] || die "missing ${ALLOW#"$ROOT"/}"
	command -v deadcode >/dev/null || die "deadcode is not on PATH -- go install golang.org/x/tools/cmd/deadcode"

	allow_format_errors >&2 || failed=1

	tree="$(tree_entries)"
	allow="$(allow_entries)"

	# Unreachable in the tree, absent from the file: a new function the binary
	# cannot reach. This is the direction that catches the guard nobody wired up.
	added="$(comm -23 <(printf '%s\n' "$tree") <(printf '%s\n' "$allow"))"
	if [ -n "$added" ]; then
		echo "::error::unreachable from cmd/munsu and not in ${ALLOW#"$ROOT"/}:" >&2
		printf '%s\n' "$added" | while IFS=$'\t' read -r file func; do
			printf '  %s: %s\n' "$file" "$func" >&2
		done
		echo "  Nothing in the munsu binary can call these. If one is a guard, wire it up -- a guard" >&2
		echo "  with no call site protects nothing, and its own tests will still pass. If it is" >&2
		echo "  genuinely unused, delete it. If it has to stay, add a line to that file with a reason." >&2
		failed=1
	fi

	# In the file, no longer unreachable: the function was wired up or deleted.
	# Either way the line has to go, or the file slowly fills with names that
	# mean nothing and waive nothing.
	removed="$(comm -13 <(printf '%s\n' "$tree") <(printf '%s\n' "$allow"))"
	if [ -n "$removed" ]; then
		echo "::error::${ALLOW#"$ROOT"/} entries that are no longer unreachable:" >&2
		printf '%s\n' "$removed" | while IFS=$'\t' read -r file func; do
			printf '  %s: %s\n' "$file" "$func" >&2
		done
		echo "  Each was either wired up or deleted -- both are good news. Drop the line." >&2
		failed=1
	fi

	[ "$failed" -eq 0 ] || exit 1

	announce_open_bugs
	# Printed on every run so the ratchet is visible in the log: this number is
	# only allowed to fall. A count that sits still while the tree grows is the
	# shape of a mechanism being routed around, and it is only visible if it is
	# printed.
	echo "deadcode: $(printf '%s\n' "$allow" | grep -c . || true) unreachable funcs allowed, 0 unaccounted"
}

case "${1:-}" in
list) tree_entries ;;
check) check ;;
*)
	echo "usage: deadcode.sh {list|check}" >&2
	exit 2
	;;
esac
