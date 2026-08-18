#!/usr/bin/env bash
#
# The hermetic half of the flake ledger: read .github/flake-ledger.md, judge it
# as a file, and fail the run when an entry is past its deadline.
#
# This half calls no API and reads nothing but a committed file, on purpose. It
# runs inside the `invariants` job next to the ADR-number, gofmt and build-tag
# rules, and those five steps are worth trusting precisely because they cannot
# be affected by anything outside the checkout. A deadline that needed the
# Actions API to be judged would put a network dependency in the one job that
# has none. The half that does need the API lives in flake-sweep.sh and runs in
# its own workflow.
#
# The division of labour between the two:
#
#   flake-sweep.sh   derives which tests are flaky from what CI already ran,
#                    and reports the diff that adds them here on its run
#                    summary; a person applies it. Network, main only.
#   flake-ledger.sh  enforces the file. No network, every PR.
#
# Why a deadline at all: an entry with no expiry is `t.Skip` with better
# paperwork. The deadline is what turns "this test is flaky" into work with a
# date on it -- when it passes, this lane goes red on every open PR, including
# PRs that have nothing to do with the flake, and the person who needs to merge
# has to triage it. That is deliberate. The owner of a flake is not a name in a
# column, it is whoever needs main to be green next, and that person always
# exists.
#
# What this does NOT catch, stated here rather than discovered later:
#
#   - Someone pushing the deadline out. `first_seen` is bot-derived, so a moved
#     deadline shows up as a widening first_seen -> deadline gap that `git log`
#     on this file makes visible, but nothing here refuses it. That is a social
#     hole, same class as the self-declared reason column in deadcode.allow.
#   - Someone deleting an entry. Only the sweep sees that, and only while the
#     evidence is still inside its window (see flake-sweep.sh).
#
# Usage:
#   flake-ledger.sh entries   parsed rows, tab-separated, for other scripts
#   flake-ledger.sh check     format and deadlines, fail-closed
#   flake-ledger.sh selftest  every rule above against a fixture that breaks it
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LEDGER="${LEDGER:-$ROOT/.github/flake-ledger.md}"
LEDGER_REL="${LEDGER#"$ROOT"/}"
FIXTURES="$ROOT/.github/testdata/flake-ledger"

# The header row the table must have, in this order. Checked rather than
# assumed: a reordered column would otherwise be parsed as a different field
# entirely -- a deadline read out of the owner_issue column would compare as a
# string that is never past due, so the whole lane would quietly stop failing.
readonly COLUMNS='test|lane|first_seen|last_seen|deadline|owner_issue|state'

# Lane keys, not job display names. The sweep maps `Integration tests` to
# `integration`; the display name can be reworded in ci.yml without rewriting
# every historical entry here.
readonly LANES='build|race|integration'

die() {
	echo "::error::$*" >&2
	exit 1
}

# Only the block between the markers is data. The rest of the file is a document
# meant to be read, and it contains tables of its own -- treating every line that
# starts with `|` as an entry would make the prose part of the schema, and an
# edit to the explanation would fail the lane.
#
# Checked rather than assumed, because a missing marker would silently reduce
# the entry set to nothing, and an empty ledger is indistinguishable from a
# clean one at the point where it matters.
markers() {
	# `|| true` because a file with no marker at all is the case this function
	# exists to name: grep exits 1 there, and under `set -e -o pipefail` that
	# aborted the script one line before the message that says what is wrong --
	# fail-closed, but silently, which is the failure mode this repo asks its
	# guards not to have.
	local begin end
	begin="$(grep -n '^<!-- flake-ledger:begin -->$' "$LEDGER" | cut -d: -f1 || true)"
	end="$(grep -n '^<!-- flake-ledger:end -->$' "$LEDGER" | cut -d: -f1 || true)"
	case "$begin$end" in
	*$'\n'*) die "$LEDGER_REL has duplicate flake-ledger markers" ;;
	esac
	[ -n "$begin" ] && [ -n "$end" ] ||
		die "$LEDGER_REL needs a <!-- flake-ledger:begin --> and a <!-- flake-ledger:end --> marker around the table"
	[ "$begin" -lt "$end" ] || die "$LEDGER_REL has its flake-ledger markers in the wrong order"
}

# Every table row, tab-separated, one line per entry. Prose lines are ignored,
# which is what lets the file stay a document a person can read rather than a
# database dump: only lines starting with `|` are data.
#
# awk rather than a read loop for the same reason deadcode.sh gives: bash 3.2
# still ships on macOS, where this repo is developed, and it misparses a `case`
# inside a command substitution.
entries() {
	[ -f "$LEDGER" ] || die "missing $LEDGER_REL"
	markers
	awk -F '|' '
		function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
		/^<!-- flake-ledger:begin -->$/ { inside = 1; next }
		/^<!-- flake-ledger:end -->$/   { inside = 0; next }
		!inside { next }
		# A markdown table row is `| a | b |`, so splitting on | yields an
		# empty field on each end: seven columns is NF == 9, in $2..$8. A row of
		# any other width is not emitted at all -- it is malformed, and saying
		# so is format_errors() job, not this one. This function is only ever
		# read after that has passed.
		/^[[:space:]]*\|/ && NF == 9 {
			sep = 1
			for (i = 2; i <= 8; i++) {
				f[i] = trim($i)
				if (f[i] !~ /^-+$/) sep = 0
			}
			# The header and the ---|--- rule under it are table syntax.
			if (sep) next
			if (f[2] == "test" && f[3] == "lane") next
			printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f[2], f[3], f[4], f[5], f[6], f[7], f[8]
		}
	' "$LEDGER"
}

# Everything wrong with the file as a file, before any deadline is compared.
# A malformed row matters more than it looks: parsed as nothing, it falls out of
# the sweep's two-way comparison in both directions at once, so its test ends up
# neither required nor waived. Fail closed on shape first, meaning second.
format_errors() {
	awk -F '|' -v name="$LEDGER_REL" -v cols="$COLUMNS" -v lanes="$LANES" '
		function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
		function bad(msg) { printf "::error::%s:%d: %s\n", name, NR, msg; failed = 1 }

		/^<!-- flake-ledger:begin -->$/ { inside = 1; next }
		/^<!-- flake-ledger:end -->$/   { inside = 0; next }
		!inside { next }

		/^[[:space:]]*\|/ {
			line = $0
			gsub(/[ \t]/, "", line)
			if (line ~ /^\|-+(\|-+)*\|$/) { next }

			if (line !~ /^\|.*\|$/ || NF != 9) {
				bad("expected a 7-column row `| " cols " |`, got " (NF > 2 ? NF - 2 : 0) " columns")
				next
			}
			test = trim($2); lane = trim($3); seen = trim($4); last = trim($5)
			deadline = trim($6); owner = trim($7); state = trim($8)

			# The header is checked rather than skipped by position: a
			# reordered column would otherwise be parsed as a different field
			# entirely, and a deadline read out of the owner_issue column
			# compares as a string that is never past due -- the lane would
			# stop failing and look healthy doing it.
			if (!seenheader) {
				seenheader = 1
				if (test "|" lane "|" seen "|" last "|" deadline "|" owner "|" state != cols)
					bad("first table row must be the header `| " cols " |`")
				next
			}

			if (test !~ /^Test[A-Za-z0-9_]*$/) bad("first column must be a Go test name, got \"" test "\"")
			if (lane !~ ("^(" lanes ")$")) bad(test ": lane must be one of " lanes ", got \"" lane "\"")
			# sha@run/attempt. The attempt is the load-bearing part: a rerun
			# overwrites a run conclusion, so a citation without an attempt
			# number points at evidence that may already read green.
			if (seen !~ /^[0-9a-f]{8,40}@[0-9]+\/[0-9]+$/) bad(test ": first_seen must be <sha>@<run_id>/<attempt>, got \"" seen "\"")
			if (last !~ /^[0-9a-f]{8,40}@[0-9]+\/[0-9]+$/) bad(test ": last_seen must be <sha>@<run_id>/<attempt>, got \"" last "\"")
			if (deadline !~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/) bad(test ": deadline must be YYYY-MM-DD, got \"" deadline "\"")
			# The bot cannot know which issue owns a flake, so it writes TBD and
			# this rule refuses to let TBD reach main. That is the point at
			# which the alarm becomes work: whoever applies the diff the sweep
			# reports cannot merge it until they file the issue that will do
			# the fixing.
			#
			# No apostrophe in this comment, and that is not a style choice:
			# it sits inside the single-quoted awk program that starts above,
			# so one would close the quote and break this script.
			if (owner == "" || owner == "TBD") bad(test ": entry needs an owning issue in owner_issue, not \"" owner "\"")
			# `fixed:` is the one state no deadline is compared against, so it
			# is the one state that has to be checkable. It used to accept any
			# ref with no whitespace in it: `fixed:i-never-fixed-it` read as
			# `0 open, 0 overdue`, permanently, for the cost of one word --
			# cheaper than moving the deadline, which has to be repeated every
			# 14 days and widens a gap `git log` shows. A commit id does not
			# prove the fix; it makes the claim something the other half can
			# check, and `flake-sweep.sh verify-fixed` then checks it is on main.
			if (state !~ /^(open|fixed:[0-9a-f]{7,40})$/) bad(test ": state must be `open` or `fixed:<sha>` naming the commit that landed the fix, got \"" state "\"")

			key = test "\t" lane
			if (key in first) bad(test " (" lane "): already listed on line " first[key])
			else first[key] = NR
		}
		END { exit failed }
	' "$LEDGER"
}

check() {
	local failed=0 today overdue open_count
	[ -f "$LEDGER" ] || die "missing $LEDGER_REL"
	markers

	format_errors >&2 || failed=1
	[ "$failed" -eq 0 ] || exit 1

	# UTC, so the lane flips at the same instant for everyone and a deadline
	# does not arrive a day early for a reviewer in Asia and a day late for one
	# in California. ISO dates compare correctly as strings; no date arithmetic
	# is needed here, and none is done, so this stays free of the date(1)
	# incompatibilities between BSD and GNU that the sweep has to deal with.
	today="$(date -u +%F)"

	overdue="$(entries | awk -F '\t' -v today="$today" '$7 == "open" && $5 < today')"
	if [ -n "$overdue" ]; then
		echo "::error::flake ledger entries are past their deadline:" >&2
		printf '%s\n' "$overdue" | while IFS=$'\t' read -r test lane _seen _last deadline owner _state; do
			printf '  %s (%s): due %s, owned by %s\n' "$test" "$lane" "$deadline" "$owner" >&2
		done
		echo "  A flake with an expired deadline is a skipped test with paperwork. Fix the test and set" >&2
		echo "  its state to fixed:<sha>, or argue in the owning issue why the date moves and move it" >&2
		echo "  in a diff someone reads. This lane is red on every open PR until then -- that is the" >&2
		echo "  mechanism, not a bug: the owner of a flake is whoever needs main green next." >&2
		failed=1
	fi

	[ "$failed" -eq 0 ] || exit 1

	# Printed on every run for the same reason deadcode.sh prints its count:
	# a ledger that never shrinks is a mechanism being routed around, and that
	# is only visible if the number is in the log.
	open_count="$(entries | awk -F '\t' '$7 == "open"' | grep -c . || true)"
	echo "flake ledger: $(entries | grep -c . || true) entries, $open_count open, 0 overdue"
}

# ---------------------------------------------------------------------------
# selftest
# ---------------------------------------------------------------------------
#
# This is the half that runs on every PR, and until BEO-103 it was the only
# guard in `invariants` with nothing watching it: ten of its rules were deleted
# one at a time and every lane in the repo stayed green, because removing a rule
# does not change the happy path by a single bit. That is the same shape the
# reachability lane exists for, one level up -- a guard nothing exercises
# protects nothing, and a guard nothing *tests* stops protecting silently.
#
# One fixture per rule, each breaking exactly that rule, and the .want pins the
# whole run -- stdout, stderr and the exit status -- so a deleted rule changes
# one file and says which. The mutation each fixture answers, verbatim:
#
#   clean                  none; the row every other fixture is a copy of
#   bad-test-name          delete the `test !~ /^Test[A-Za-z0-9_]*$/` rule
#   bad-lane               delete the `lane !~ ("^(" lanes ")$")` rule
#   bad-first-seen         delete the `seen !~ /^[0-9a-f]{8,40}@.../` rule
#   bad-last-seen          delete the `last !~ /^[0-9a-f]{8,40}@.../` rule
#   bad-deadline           delete the `deadline !~ /^[0-9]{4}-.../` rule
#   owner-tbd              delete the `owner == "" || owner == "TBD"` rule
#   bad-state              delete the `state !~ /^(open|fixed:...)$/` rule
#   fixed-not-a-sha        loosen that same rule back to `fixed:[^ \t]+`
#   duplicate-row          delete the `key in first` rule
#   wrong-column-count     delete the `NF != 9` rule
#   header-reordered       delete the `!seenheader` comparison against cols
#   overdue-deadline       delete the `$7 == "open" && $5 < today` comparison
#   missing-markers        delete the `[ -n "$begin" ] && [ -n "$end" ]` check
#   duplicate-markers      delete the `*$'\n'*` case in markers()
#   markers-out-of-order   delete the `[ "$begin" -lt "$end" ]` check
#
# Dates are 9999-12-31 for "not due" and 2020-01-01 for "overdue" so that no
# fixture expires and starts failing on a date nobody chose.
selftest() {
	local failed=0 fixture name want got rc
	[ -d "$FIXTURES" ] || die "missing ${FIXTURES#"$ROOT"/}"
	for fixture in "$FIXTURES"/*.md; do
		name="$(basename "$fixture" .md)"
		want="${fixture%.md}.want"
		[ -f "$want" ] || die "fixture $name has no .want"
		# A subprocess, not a function call: `check` exits, and its exit status
		# is half of what each fixture pins.
		if got="$(LEDGER="$fixture" "$0" check 2>&1)"; then rc=0; else rc=$?; fi
		got="$got
exit $rc"
		if [ "$got" = "$(cat "$want")" ]; then
			echo "  ok   $name"
		else
			echo "::error::flake-ledger check disagrees with fixture $name:" >&2
			diff -u "$want" <(printf '%s\n' "$got") >&2 || true
			failed=1
		fi
	done
	[ "$failed" -eq 0 ] || exit 1
	echo "flake ledger selftest: all fixtures agree"
}

case "${1:-}" in
entries) entries ;;
check) check ;;
selftest) selftest ;;
*)
	echo "usage: flake-ledger.sh {entries|check|selftest}" >&2
	exit 2
	;;
esac
