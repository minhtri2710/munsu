#!/usr/bin/env bash
#
# Refusal-branch coverage lane: every guard branch no test has ever entered must
# be accounted for in .github/uncovered-guards.baseline.
#
# The reachability lane next door (deadcode.sh) sees a guard nothing calls. It
# cannot see a guard that IS called and whose refusal branch no test has ever
# taken -- `identity != Worktree` in spawn_runner.go was deleted outright and
# every lane in this repo stayed green, because deleting a guard does not change
# the happy path by a single bit. Coverage is what sees that shape, and only
# coverage of the refusal branch specifically: the enclosing function is covered
# either way.
#
# The set of guards is DERIVED, not declared (see guardsites/main.go). This file
# lists waivers only, so adding a guard without a test fails closed -- there is
# no register to forget. That is the whole inversion: a register of guards
# fails open at exactly the moment it must not.
#
# The baseline is compared BOTH ways, like .github/deadcode.allow: a guard the
# tree shows uncovered and the file does not list is red, and a line whose guard
# is now covered, or has gone, is red too. The exact-base identity ratchet also
# requires pure growth to carry an explicit disclosure and rejects mixed changes.
#
# --- Merging four lanes is a correctness condition, not an optimisation -------
#
# soldier_soldier_test.go carries `//go:build integration`, so the default lane
# never builds it. Measured on this tree, `soldier_launch.go:474.23` -- the
# refusal in FailClosedDuringLaunch -- reads count 0 in the default profile and
# count 1 in the integration one. A lane that read the default profile alone
# would call that guard untested, and would do the same to every branch those 60
# integration files are the only cover for. Every one of the four lanes named in
# .github/build-tags.manifest is therefore required, and a missing profile is
# fatal rather than treated as zeros. The fixture `merge-four-lanes` pins it.
#
# --- What is measured, and what cannot be --------------------------------------
#
# Coverage lowers "no test reaches this branch". It does NOT lower "a test runs
# through it and asserts nothing" -- that is mutation testing's question, and
# the design record on BEO-63 prices it at ~80 hours of suite for this repo,
# while still missing three of the five recorded cases. Stronger than the status
# quo, weaker than mutation, and the gap is stated here rather than discovered
# later.
#
# A guard in a file no lane compiles (`//go:build linux` on a darwin laptop,
# `_darwin.go` on the runner) has no block in any profile. Those are reported as
# unmeasured, counted on every run, and excluded from BOTH directions of the
# comparison. Excluding them from one direction only is what would make the file
# platform-dependent -- red on a laptop, green on the runner, the exact failure
# deadcode.sh's GOOS union exists to avoid. A coverage profile cannot be unioned
# the same way: tests cannot run for a GOOS they were not built for.
#
# The reason column is self-declared. Somebody can waive a brand new guard with a
# sentence nobody would accept and this stays green. Reading the diff is what
# catches that, exactly as .github/build-tags.manifest writes about itself. This
# lane catches the accident, not the intent, and must not be sold as more.
#
# Usage:
#   uncovered-guards.sh sites      derived refusal sites, tab-separated
#   uncovered-guards.sh merge      merged profile, block range and max count
#   uncovered-guards.sh classify   every site with its coverage verdict
#   uncovered-guards.sh generate   a fresh baseline body, for the first landing
#   uncovered-guards.sh check      tree and baseline agree, in both directions
#   uncovered-guards.sh selftest   every rule above against a fixture that breaks it
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASELINE="${BASELINE:-$ROOT/.github/uncovered-guards.baseline}"
BASELINE_REL="${BASELINE#"$ROOT"/}"
PROFILES="${PROFILES:-$ROOT/.github/coverage}"
FIXTURES="$ROOT/.github/testdata/uncovered-guards"
# Overridable so the selftest can pin a summary line without depending on the
# real file, which BEO-67 is actively burning down: fixtures that moved every
# time somebody deleted a dead function would be fixtures nobody trusts.
ALLOW="${GUARDS_DEADCODE_ALLOW:-$ROOT/.github/deadcode.allow}"

# Every lane that must contribute a profile, by artifact filename. Derived from
# the treatment column of .github/build-tags.manifest plus the default lane, so a
# fifth tagged lane joins this list by being classified there -- the same
# derivation build-tags.sh and deadcode.sh use, for the same reason.
lane_files() {
	{
		echo default.out
		grep -vE '^[[:space:]]*(#|$)' "$ROOT/.github/build-tags.manifest" |
			awk -F '\t' '$2 == "test-lane" { print $1 ".out" }'
	} | sort -u
}

die() {
	echo "::error::$*" >&2
	exit 1
}

# The derived set: file, func, nth, predicate, body, entries. SITES exists for
# the selftest, which needs to drive `check` from a fixed site list rather than from
# whatever the real tree happens to hold today.
sites() {
	if [ -n "${SITES:-}" ]; then
		[ -f "$SITES" ] || die "SITES=$SITES does not exist"
		cat "$SITES"
		return
	fi
	# The tool is its own module (see its go.mod), so it is run from its own
	# directory and told which tree to read. It type-checks that tree, which is
	# the parent module -- go/packages runs `go list` in the directory it is
	# given, not in the one the binary was built from.
	(cd "$ROOT/.github/scripts/guardsites" && go run . "$ROOT") ||
		die "guardsites could not derive the refusal set, so this lane cannot judge coverage"
}

# One line per coverage block, `path:start-end<TAB>statements<TAB>count`, carrying
# the largest count any lane recorded for it. The profile is the source of truth
# for the block's start: Go toolchains may place it at an if body's brace, a
# switch-default clause boundary, or the first statement. A site therefore supplies
# its refusal-body range and supported entry coordinates; classify() accepts only a
# unique entry block.
#
# The complete range and statement count are retained so a malformed or
# ambiguous profile cannot be mistaken for a match. The start selects the entry
# coordinate; the end is validated against the source body and keeps the merged
# representation faithful to the profile.
merge() {
	local module lane path missing="" max_int
	module="$(awk '/^module / { print $2; exit }' "$ROOT/go.mod")"
	case "$(go env GOARCH)" in
		386|arm|mips|mipsle|wasm) max_int=2147483647 ;;
		*) max_int=9223372036854775807 ;;
	esac
	[ -n "$module" ] || die "could not read the module path from go.mod"
	[ -d "$PROFILES" ] || die "missing coverage directory ${PROFILES#"$ROOT"/} -- the lane profiles have to be downloaded before this runs"

	for lane in $(lane_files); do
		path="$PROFILES/$lane"
		if [ ! -s "$path" ]; then
			missing="$missing $lane"
		fi
	done
	# Fail closed rather than treating an absent lane as all-zeros. A dropped
	# artifact would otherwise turn every branch that lane is the only cover for
	# into a demand for a baseline line -- and, in the other direction, into a
	# demand to delete lines that are still right.
	if [ -n "$missing" ]; then
		echo "::error::coverage profile missing or empty, so this lane cannot conclude anything about refusal coverage:" >&2
		for lane in $missing; do
			printf '  %s/%s\n' "${PROFILES#"$ROOT"/}" "$lane" >&2
		done
		echo "  Every lane in .github/build-tags.manifest has to upload one. See the guards job in ci.yml." >&2
		exit 1
	fi

	# `root` only shortens paths in the two errors below: the selftest drives this
	# with absolute fixture paths and pins the output, so an absolute FILENAME
	# would put this checkout in a committed file.
	# shellcheck disable=SC2046 # word splitting is the point: one path per lane
	awk -F' ' -v prefix="$module/" -v root="$ROOT/" -v maxInt="$max_int" '
		function short(p) { return index(p, root) == 1 ? substr(p, length(root) + 1) : p }
		function decimal(value) {
			sub(/^0+/, "", value)
			return value == "" ? "0" : value
		}
		function fitsInt(value, normalized) {
			normalized = decimal(value)
			return length(normalized) < length(maxInt) ||
				(length(normalized) == length(maxInt) && ("x" normalized) <= ("x" maxInt))
		}
		function requireInt(value, label, normalized) {
			normalized = decimal(value)
			if (!fitsInt(normalized)) {
				printf "::error::%s:%d: %s is outside Go int range: %s\n", short(FILENAME), FNR, label, value > "/dev/stderr"
				bad = 1
				return ""
			}
			return normalized
		}
		function position(spec, which,   parts) {
			if (split(spec, parts, /,/) != 2) return ""
			return parts[which]
		}
		function pointLine(point,   parts) {
			split(point, parts, /\./)
			return parts[1] + 0
		}
		function pointColumn(point,   parts) {
			split(point, parts, /\./)
			return parts[2] + 0
		}
		function before(left, right) {
			return pointLine(left) < pointLine(right) ||
				(pointLine(left) == pointLine(right) && pointColumn(left) < pointColumn(right))
		}
		function greaterDecimal(left, right) {
			left = decimal(left)
			right = decimal(right)
			return length(left) > length(right) ||
				(length(left) == length(right) && ("x" left) > ("x" right))
		}
		FNR == 1 {
			if ($0 !~ /^mode:/) {
				printf "::error::%s is not a coverage profile\n", short(FILENAME) > "/dev/stderr"
				bad = 1
				exit
			}
			if ($0 !~ /^mode: (set|count|atomic)$/) {
				printf "::error::%s:%d: unsupported or missing coverage profile mode: %s\n", short(FILENAME), FNR, $0 > "/dev/stderr"
				bad = 1
				exit
			}
			mode = substr($0, 7)
			if (profileMode == "") profileMode = mode
			else if (mode != profileMode) {
				printf "::error::coverage profile mode mismatch: expected %s, found %s in %s\n", profileMode, mode, short(FILENAME) > "/dev/stderr"
				bad = 1
				exit
			}
			next
		}
		/^mode:/ {
			printf "::error::%s:%d: repeated or misplaced coverage profile mode: %s\n", short(FILENAME), FNR, $0 > "/dev/stderr"
			bad = 1
			exit
		}
		$0 !~ /^[^[:space:]]+:[1-9][0-9]*\.[1-9][0-9]*,[1-9][0-9]*\.[1-9][0-9]* [0-9]+ [0-9]+$/ {
			printf "::error::%s:%d: unreadable profile line: %s\n", short(FILENAME), FNR, $0 > "/dev/stderr"
			bad = 1
			exit
		}
		{
			spec = $1
			if (index(spec, prefix) != 1) {
				printf "::error::%s:%d: coverage block has an unexpected module path: %s\n", short(FILENAME), FNR, $1 > "/dev/stderr"
				bad = 1
				exit
			}
			spec = substr(spec, length(prefix) + 1)
			key = position(spec, 1)
			end = position(spec, 2)
			if (key !~ /:[1-9][0-9]*\.[1-9][0-9]*$/ ||
				end !~ /^[1-9][0-9]*\.[1-9][0-9]*$/) {
				printf "::error::%s:%d: invalid coverage block coordinates: %s\n", short(FILENAME), FNR, $1 > "/dev/stderr"
				bad = 1
				next
			}
			match(key, /:[0-9]+\.[0-9]+$/)
			start = substr(key, RSTART + 1)
			startLine = substr(start, 1, index(start, ".") - 1)
			startCol = substr(start, index(start, ".") + 1)
			endLine = substr(end, 1, index(end, ".") - 1)
			endCol = substr(end, index(end, ".") + 1)
			if (requireInt(startLine, "coverage block start line") == "" ||
				requireInt(startCol, "coverage block start column") == "" ||
				requireInt(endLine, "coverage block end line") == "" ||
				requireInt(endCol, "coverage block end column") == "") next
			file = substr(key, 1, RSTART - 1)
			statement = requireInt($2, "statement count")
			count = requireInt($3, "execution count")
			if (statement == "" || count == "") next
			if (mode == "set" && count != "0" && count != "1") {
				printf "::error::%s:%d: mode set execution count must be 0 or 1: %s\n", short(FILENAME), FNR, $3 > "/dev/stderr"
				bad = 1
				next
			}
			if (!before(start, end) && start != end) {
				printf "::error::%s:%d: non-positive coverage block range: %s\n", short(FILENAME), FNR, $1 > "/dev/stderr"
				bad = 1
				next
			}
			if (start == end && statement != "0") {
				printf "::error::%s:%d: incompatible zero-width coverage block with nonzero statement count: %s\n", short(FILENAME), FNR, $1 > "/dev/stderr"
				bad = 1
				next
			}
			block = file ":" start "-" end
			if (!(block in statements)) statements[block] = statement
			else if (("x" statements[block]) != ("x" statement)) {
				printf "::error::conflicting statement counts for coverage block %s: %s versus %s\n", block, statements[block], statement > "/dev/stderr"
				bad = 1
				next
			}
			if (!(block in max) || greaterDecimal(count, max[block])) max[block] = count
		}
		END {
			if (bad) exit 1
			for (block in max) printf "%s\t%s\t%s\n", block, statements[block], max[block]
		}
	' $(for lane in $(lane_files); do printf '%s ' "$PROFILES/$lane"; done) | sort
}

# Every site with a verdict: covered, uncovered, unmeasured, or anomaly.
# The merged profile is the closed set of lane records; zero-statement records
# prove presence but never become executable blocks.
#
# `anomaly` is the one that must never be waived away: the file IS in the
# profile, so the lane compiled it, yet no unique valid entry block can be
# resolved from this site's refusal-body coordinates. That means the coverage
# instrumenter's block convention has changed, or the profile is otherwise
# incompatible with the source tree, and every verdict this script prints is
# suspect. It is fatal in check().
classify() {
	local merged derived
	# `|| exit 1` on both, rather than leaning on `set -e`: a command
	# substitution that failed must not fall through to the next check and report
	# its symptom as if it were the cause.
	merged="$(merge)" || exit 1
	[ -n "$merged" ] || die "the merged profile holds no blocks, so no branch can be judged covered"
	# Materialised before awk rather than piped into it: a `go run` that failed
	# inside a process substitution would reach awk as an empty file, and an
	# empty derived set reads as "every guard is fine".
	derived="$(sites)" || exit 1
	[ -n "$derived" ] || die "no refusal site was derived from the tree -- the recognizer has stopped matching, so this lane proves nothing"
	awk -F'\t' '
		function startPoint(range,   parts, pointParts) {
			split(range, parts, /-/)
			split(parts[1], pointParts, /\./)
			return sprintf("%d:%d", pointParts[1], pointParts[2])
		}
		function endPoint(range,   parts, pointParts) {
			split(range, parts, /-/)
			split(parts[2], pointParts, /\./)
			return sprintf("%d:%d", pointParts[1], pointParts[2])
		}
		function pointLine(point,   parts) {
			split(point, parts, /:/)
			return parts[1] + 0
		}
		function pointColumn(point,   parts) {
			split(point, parts, /:/)
			return parts[2] + 0
		}
		function before(left, right) {
			return pointLine(left) < pointLine(right) ||
				(pointLine(left) == pointLine(right) && pointColumn(left) < pointColumn(right))
		}
		function after(left, right) { return before(right, left) }
		function inside(point, lower, upper) {
			return !before(point, lower) && !after(point, upper)
		}
		function bodyStart(point, lower, upper) {
			return !before(point, lower) && before(point, upper)
		}
		function coordinate(point,   parts) {
			split(point, parts, /\./)
			return sprintf("%d:%d", parts[1], parts[2])
		}
		NR == FNR {
			file = $1
			sub(/:[0-9]+\.[0-9]+-.*/, "", file)
			split($0, mergedFields, /\t/)
			recorded[file] = 1
			if (mergedFields[2] != "0") {
				compiled[file] = 1
				blocks[file] = blocks[file] $0 "\n"
			}
			next
		}
		{
			if (NF != 6) {
				printf "::error::derived refusal site has invalid columns: %s\n", $0 > "/dev/stderr"
				bad = 1
				next
			}
			bodyLower = startPoint($5)
			bodyUpper = endPoint($5)
			split($6, entries, /,/)
			if (split($6, entries, /,/) != 2 || entries[1] !~ /^[1-9][0-9]*\.[1-9][0-9]*$/ || entries[2] !~ /^[1-9][0-9]*\.[1-9][0-9]*$/) {
				printf "::error::derived refusal site has invalid entry coordinates: %s\n", $0 > "/dev/stderr"
				bad = 1
				next
			}
			entryBrace = coordinate(entries[1])
			entryFirst = coordinate(entries[2])
			file = $1
			matches = 0
			incompatible = 0
			n = split(blocks[file], lines, /\n/)
			for (i = 1; i <= n; i++) {
				if (lines[i] == "") continue
				split(lines[i], fields, /\t/)
				blockRange = fields[1]
				sub(/^[^:]+:/, "", blockRange)
				blockStart = startPoint(blockRange)
				blockEnd = endPoint(blockRange)
				if (blockStart == blockEnd) continue
				if (!bodyStart(blockStart, bodyLower, bodyUpper)) continue
				if (!inside(blockEnd, bodyLower, bodyUpper) || !before(blockStart, blockEnd)) {
					incompatible = 1
					continue
				}
				if (blockStart != entryBrace && blockStart != entryFirst) continue
				matches++
				bestLine = lines[i]
			}
			# The refusal body range travels with every verdict, not just the anomaly: the
			# baseline has no source coordinates by design, so this is the only thing that
			# can tell an author which refusal site to go and write a test for.
			id = $1 "\t" $2 "\t" $3 "\t" $4 "\t" $5
			if (incompatible) print "anomaly\t" id
			else if (matches == 1) {
				split(bestLine, fields, /\t/)
				print (("x" fields[3]) != "x0" ? "covered" : "uncovered") "\t" id
			} else if (!(file in recorded)) print "unmeasured\t" id
			else print "anomaly\t" id
		}
		END { if (bad) exit 1 }
	' <(printf '%s\n' "$merged") <(printf '%s\n' "$derived")
}

# Everything wrong with the baseline as a file, before it is compared to
# anything. A malformed line is worse than it looks: parsed as nothing, it drops
# out of the comparison in BOTH directions at once, so its guard ends up neither
# required nor waived -- fail-open, in the file whose whole job is to fail
# closed. A duplicate is the same hazard from the other side.
#
# awk rather than a `read` loop, for the reason deadcode.sh gives: bash 3.2 still
# ships on macOS and misparses a `case` inside a command substitution.
baseline_format_errors() {
	local file="$1"
	local name="${file#"$ROOT"/}"
	awk -F '\t' -v name="$name" '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		NF != 5 || $1 == "" || $2 == "" || $3 == "" || $4 == "" || $5 == "" {
			printf "::error::%s:%d: expected exactly file<TAB>func<TAB>nth<TAB>predicate<TAB>reason\n", name, NR
			bad = 1
			next
		}
		$3 !~ /^[1-9][0-9]*$/ {
			printf "::error::%s:%d: %s: %s: nth must be a positive number, got \"%s\"\n", name, NR, $1, $2, $3
			bad = 1
			next
		}
		{
			key = $1 "\t" $2 "\t" $3 "\t" $4
			if (key in seen) {
				printf "::error::%s:%d: %s: %s: already listed on line %d\n", name, NR, $1, $2, seen[key]
				bad = 1
			} else {
				seen[key] = NR
			}
		}
		END { exit bad }
	' "$file"
}

baseline_entries() {
	local file="$1"
	{ grep -vE '^[[:space:]]*(#|$)' "$file" || true; } | cut -f1,2,3,4 | sort -u
}

# The ratchet compares identities, not comments or ordinary reason edits. A
# pure shrink is allowed. A pure growth is allowed only when every added row is
# explicitly disclosed in its reason as `growth(#<issue>): <reason>`; this lets a
# genuinely new guard class land without turning the ratchet into a blocker that
# prevents the change which expands the derived set. A mixed add/remove change,
# including equal-count replacement, remains forbidden. The base source is either
# a fixture-provided file or the exact git revision supplied by CI; missing or
# malformed base data fails closed rather than silently disabling the ratchet.
baseline_added_rows() {
	local base_file="$1" current_file="$2"
	awk -F '\t' -v base_name="$base_file" '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		FILENAME == base_name {
			seen[$1 "\t" $2 "\t" $3 "\t" $4] = 1
			next
		}
		FILENAME != base_name && seen[$1 "\t" $2 "\t" $3 "\t" $4] == 0 { print }
	' "$base_file" "$current_file"
}

baseline_print_keys() {
	while IFS=$'\t' read -r file func nth predicate; do
		[ -n "$file" ] || continue
		printf '  %s: %s (#%s): %s\n' "$file" "$func" "$nth" "$predicate"
	done
}

baseline_ratchet() {
	local base_file="${GUARDS_BASELINE_BASE:-}"
	local base_ref tmp="" current base added removed added_rows invalid_added
	if [ -z "$base_file" ]; then
		base_ref="${GUARDS_BASE_REF:-}"
		[ -n "$base_ref" ] || die "GUARDS_BASE_REF is required when GUARDS_BASELINE_BASE is not set; the baseline ratchet cannot be evaluated"
		tmp="$(mktemp)"
		if ! git -C "$ROOT" show "$base_ref:$BASELINE_REL" >"$tmp"; then
			rm -f "$tmp"
			die "could not read $BASELINE_REL from base revision $base_ref; the baseline ratchet cannot be evaluated"
		fi
		base_file="$tmp"
	fi
	if [ ! -f "$base_file" ]; then
		rm -f "$tmp"
		die "base baseline ${base_file#"$ROOT"/} is missing; the baseline ratchet cannot be evaluated"
	fi
	local base_name="${base_file#"$ROOT"/}"
	if ! baseline_format_errors "$base_file" >&2; then
		rm -f "$tmp"
		die "base baseline $base_name is malformed; the baseline ratchet cannot be evaluated"
	fi
	current="$(baseline_entries "$BASELINE")"
	base="$(baseline_entries "$base_file")"
	added="$(comm -23 <(printf '%s\n' "$current") <(printf '%s\n' "$base"))"
	removed="$(comm -13 <(printf '%s\n' "$current") <(printf '%s\n' "$base"))"
	added_rows="$(baseline_added_rows "$base_file" "$BASELINE")"
	rm -f "$tmp"
	if [ -z "$added" ]; then
		return 0
	fi

	echo "::notice::$BASELINE_REL adds these identities relative to its exact base:" >&2
	printf '%s\n' "$added" | baseline_print_keys >&2
	if [ -n "$removed" ]; then
		echo "::error::$BASELINE_REL mixes identity growth with removals; split the changes before updating the ratchet:" >&2
		printf '%s\n' "$removed" | baseline_print_keys >&2
		return 1
	fi

	invalid_added="$(printf '%s\n' "$added_rows" | awk -F '\t' 'NF != 5 || $5 !~ /^growth\(#[1-9][0-9]*\): .+$/ { print }')"
	if [ -n "$invalid_added" ]; then
		echo "::error::$BASELINE_REL adds identities without a valid growth acknowledgment (growth(#<issue>): <reason>):" >&2
		printf '%s\n' "$invalid_added" | cut -f1,2,3,4 | baseline_print_keys >&2
		return 1
	fi
}

# Count of entries in the sibling ratchet, read only. The summary line carries
# both numbers because they are one mechanism: tier 1 owns the guard nothing
# calls, tier 2 the guard nothing tests, and a burn-down that moves one while the
# other stands still is worth seeing on the same line.
deadcode_allowed() {
	if [ -f "$ALLOW" ]; then
		grep -cvE '^[[:space:]]*(#|$)' "$ALLOW" || true
	else
		echo 0
	fi
}

# Delta against the same file at the merge base, for the ratchet line. Purely
# informational: with no base ref reachable it prints nothing rather than
# guessing, because a wrong delta is worse than an absent one. Nothing is judged
# on it -- the two directions above are what fail the run.
delta() {
	local file="$1" base="${GUARDS_BASE_REF:-}" now="$2" then
	[ -n "$base" ] || return 0
	git -C "$ROOT" rev-parse --verify --quiet "$base" >/dev/null 2>&1 || return 0
	then="$(git -C "$ROOT" show "$base:$file" 2>/dev/null | grep -cvE '^[[:space:]]*(#|$)' || true)"
	[ -n "$then" ] || return 0
	printf ' (%+d)' "$((now - then))"
}

# Entries whose reason marks them as a known-open bug rather than accepted debt,
# reprinted on every run so a known-broken guard stays visible until it is wired
# up or deleted. An annotation on every single run is the difference between a
# waiver and a silent line. Same treatment and rationale as deadcode.sh's
# announce_open_bugs; the reason column is field 5 here rather than field 3.
announce_open_bugs() {
	awk -F '\t' '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		$5 ~ /^OPEN-BUG/ {
			printf "::warning file=%s::%s is uncovered and known to be a bug, not accepted debt: %s\n", $1, $2, $5
		}
	' "$BASELINE"
}

check() {
	local failed=0 verdicts anomalies unmeasured uncovered covered baseline added removed stale waived

	[ -f "$BASELINE" ] || die "missing $BASELINE_REL"
	baseline_format_errors "$BASELINE" >&2 || failed=1

	verdicts="$(classify)" || exit 1

	anomalies="$(printf '%s\n' "$verdicts" | { grep '^anomaly	' || true; })"
	if [ -n "$anomalies" ]; then
		echo "::error::a refusal site sits in a file the lanes compiled, yet no unique coverage block resolves at its refusal-body entry:" >&2
		printf '%s\n' "$anomalies" | while IFS=$'\t' read -r _ file func nth pred block; do
			printf '  %s: %s (#%s) %s -- refusal body range %s\n' "$file" "$func" "$nth" "$pred" "$block" >&2
		done
		echo "  The coverage profile does not uniquely map this refusal body under the current source/toolchain coordinates, so every verdict below is unsafe." >&2
		echo "  Check that every profile was produced from this source revision by a compatible Go toolchain before trusting this lane again." >&2
		exit 1
	fi

	# Warned by file rather than by site: this is a blind spot in the lane, and a
	# blind spot nobody is reminded of is one somebody eventually hides in.
	unmeasured="$(printf '%s\n' "$verdicts" | { grep '^unmeasured	' || true; } | cut -f2 | sort -u)"
	if [ -n "$unmeasured" ]; then
		printf '%s\n' "$unmeasured" | while IFS= read -r file; do
			printf '::warning file=%s::no lane compiles this file on this platform, so its refusal branches are outside the lane in both directions\n' "$file"
		done
	fi

	uncovered="$(printf '%s\n' "$verdicts" | { grep '^uncovered	' || true; } | cut -f2,3,4,5 | sort -u)"
	covered="$(printf '%s\n' "$verdicts" | { grep '^covered	' || true; } | cut -f2,3,4,5 | sort -u)"
	baseline="$(baseline_entries "$BASELINE")"

	# `file:body-range: func (#nth): if <predicate>` or `switch default` for
	# a list of site keys. The body range comes from the derived site data, where
	# it helps locate the guard without becoming part of the baseline identity;
	# putting it in the baseline would rewrite that file on every edit above a guard.
	sited() {
		awk -F'\t' 'NR == FNR { at[$2 "\t" $3 "\t" $4 "\t" $5] = $6; next }
			$4 == "default" { printf "  %s:%s: %s (#%s): switch default\n", $1, at[$0], $2, $3; next }
			{ printf "  %s:%s: %s (#%s): if %s\n", $1, at[$0], $2, $3, $4 }' \
			<(printf '%s\n' "$verdicts") -
	}

	# Uncovered in the tree, absent from the baseline: a refusal branch no test
	# has ever entered. This is the direction that catches the new guard nobody
	# wrote a test for.
	added="$(comm -23 <(printf '%s\n' "$uncovered") <(printf '%s\n' "$baseline"))"
	if [ -n "$added" ]; then
		echo "::error::refusal branches no test enters, and not in $BASELINE_REL:" >&2
		printf '%s\n' "$added" | sited >&2
		echo "  Nothing has ever taken these branches, so nothing would notice if the condition were" >&2
		echo "  inverted or the branch deleted. Write a test that builds the state the guard refuses --" >&2
		echo "  5 to 15 lines for the ones already burnt down. If the branch genuinely cannot be reached" >&2
		echo "  from a test, add a line to that file with a reason." >&2
		failed=1
	fi

	# In the baseline, covered now: somebody wrote the test. Good news, and the
	# line has to go with it or the file rots into a list that waives nothing.
	waived="$(comm -12 <(printf '%s\n' "$covered") <(printf '%s\n' "$baseline"))"
	if [ -n "$waived" ]; then
		echo "::error::$BASELINE_REL waives branches that are now covered:" >&2
		printf '%s\n' "$waived" | sited >&2
		echo "  A test enters each of these now. Drop the line." >&2
		failed=1
	fi

	# In the baseline, not a refusal site at all: the guard was deleted,
	# rewritten, or its predicate changed. Compared against EVERY site including
	# the unmeasured ones -- a line waiving a `_darwin.go` guard has to survive a
	# run on the runner, where that guard exists in the tree and no profile.
	# Coverage is what the platform decides here; existence is not.
	stale="$(comm -13 <(printf '%s\n' "$verdicts" | cut -f2,3,4,5 | sort -u) <(printf '%s\n' "$baseline"))"
	if [ -n "$stale" ]; then
		echo "::error::$BASELINE_REL entries that match no refusal branch in the tree:" >&2
		printf '%s\n' "$stale" | while IFS=$'\t' read -r file func nth pred; do
			if [ "$pred" = default ]; then
				printf '  %s: %s (#%s): switch default\n' "$file" "$func" "$nth" >&2
			else
				printf '  %s: %s (#%s): if %s\n' "$file" "$func" "$nth" "$pred" >&2
			fi
		done
		echo "  Each was deleted or rewritten -- both are fine. Drop the line." >&2
		failed=1
	fi

	[ "$failed" -eq 0 ] || exit 1

	baseline_ratchet || exit 1

	announce_open_bugs

	local n m p
	n="$(printf '%s\n' "$verdicts" | grep -c . || true)"
	m="$(printf '%s\n' "$baseline" | grep -c . || true)"
	p="$(deadcode_allowed)"
	# Printed on every run so the ratchet is visible in the log: waived is only
	# allowed to fall. Standing still while the site count climbs is the shape of
	# a mechanism being routed around, and it is only visible if it is printed.
	printf 'guards: %d sites, %d waived%s, deadcode: %d allowed%s\n' \
		"$n" "$m" "$(delta "$BASELINE_REL" "$m")" "$p" "$(delta "${ALLOW#"$ROOT"/}" "$p")"
}

# A fresh baseline body on stdout, for the first landing and for a burn-down that
# wants to see what is left. Deliberately not wired into check: a lane that can
# rewrite its own baseline is a lane that waives whatever it finds.
generate() {
	# Unmeasured sites are written out too, and this is the one place they are
	# not simply skipped. Their coverage depends on which platform produced the
	# profiles, so a baseline generated on a laptop would be missing exactly the
	# lines the runner then demands. Writing both halves is what makes one file
	# serve both, since a line for a site the local platform cannot measure is
	# ignored rather than rejected.
	classify | awk -F'\t' '
		$1 == "uncovered" { printf "%s\t%s\t%s\t%s\tburn-down(BEO-69): pre-existing, imported with the baseline, not yet triaged\n", $2, $3, $4, $5 }
		$1 == "unmeasured" { printf "%s\t%s\t%s\t%s\tburn-down(BEO-69): GOOS-gated, unmeasured where this baseline was generated; imported unwaived rather than left for the other platform to demand\n", $2, $3, $4, $5 }
	' | sort
}

# ---------------------------------------------------------------------------
# selftest
# ---------------------------------------------------------------------------
#
# The enforcing half runs on every PR, which makes it the half a PR can break --
# and a guard nothing tests stops protecting silently, which is the sentence this
# whole lane exists to enforce on everybody else. BEO-103 is the precedent: ten
# rules of flake-ledger.sh were deleted one at a time and every lane in this repo
# stayed green. So: one fixture per rule, each breaking exactly that rule, and
# the .want pins stdout, stderr and the exit status together, so a deleted rule
# changes one file and says which.
#
# The mutation each fixture answers, verbatim:
#
#   clean                  none; the tree every other fixture is a copy of
#   open-bug               delete announce_open_bugs -- an OPEN-BUG reason must
#                          warn on every run, an ordinary waiver must stay silent
#   merge-four-lanes       take the default profile alone instead of the max
#                          across lanes -- the trap the design record hit while measuring
#   statement-count-conflict merge identical ranges with conflicting statement counts
#                          instead of failing closed at the merge boundary
#   missing-lane           treat an absent lane profile as zeros
#   empty-profile          accept a profile with no blocks in it
#   not-a-profile          accept a file that is not a coverage profile
#   no-sites               accept an empty derived set as "nothing to enforce"
#   uncovered-not-waived   delete the `added` comparison
#   covered-still-waived   delete the `waived` comparison
#   stale-entry            delete the `stale` comparison
#   unmeasured-file        count a file no lane compiled as uncovered
#   anomaly                accept a site whose refusal body has no unique entry counter
#   no-reason              delete the fifth-column requirement
#   bad-nth                delete the numeric check on nth
#   malformed-end          accept a profile block with a malformed end coordinate
#   malformed-count        accept a profile block with malformed count text
#   overflow-statement     accept a profile block with an overflowing statement count
#   overflow-execution     accept a profile block with an overflowing execution count
#   inverted-end            accept a profile block whose end precedes its start
#   zero-width-endpoint     accept a zero-width profile block that claims statements
#   zero-width-marker-only  retain a zero-statement marker as compilation evidence
#                           and fail with an anomaly rather than calling the file unmeasured
#   zero-statement-block    let a positive-count zero-statement block cover a guard
#   invalid-set-count       accept an execution count other than 0 or 1 in set mode
#   incompatible-end        accept a profile block whose end exceeds the refusal body
#   short-row              delete the NF < 4 check
#   duplicate-row          delete the `key in seen` check
#   toolchain-block-go126  match a Go 1.26-style block start at the opening brace
#   toolchain-block-go127  match a Go 1.27-style block start at the first statement
#   later-nested           accept a covered block that starts at a later nested statement
#   baseline-growth        reject undisclosed pure growth
#   baseline-replacement   reject same-count replacement and mixed changes
#   baseline-shrink        allow pure removal from the current identity set
#   baseline-malformed     reject a malformed base baseline
#   disclosed-growth-614   allow +311 sites and +27 acknowledged rows
#   invalid-extra-column  reject current rows with more than five columns
#   invalid-zero-nth      reject current rows with a zero ordinal
#   base-extra-column     reject a malformed base row with more than five columns
#   base-zero-nth         reject a malformed base row with a zero ordinal
#   disclosed-growth-zero-issue reject growth(#0) acknowledgments
#
# Each fixture is a directory holding `sites.tsv`, `baseline`, `profiles/` and
# `want`. Driving `check` from a fixed site list rather than from the real tree
# is deliberate: these fixtures pin the comparison, and they must not change
# their verdict because somebody added a guard to internal/fleet. The recognizer
# is pinned separately, by the fixtures under .github/testdata/guardsites.
selftest() {
	local failed=0 dir name got rc
	[ -d "$FIXTURES" ] || die "missing ${FIXTURES#"$ROOT"/}"
	for dir in "$FIXTURES"/*/; do
		dir="${dir%/}"
		name="$(basename "$dir")"
		[ -f "$dir/want" ] || die "fixture $name has no want"
		# A subprocess, not a function call: `check` exits, and its exit status
		# is half of what each fixture pins. The deadcode count is pinned to a
		# stub for the reason given where GUARDS_DEADCODE_ALLOW is read.
		base="$dir/baseline"
		[ -f "$dir/base-baseline" ] && base="$dir/base-baseline"
		if got="$(SITES="$dir/sites.tsv" BASELINE="$dir/baseline" PROFILES="$dir/profiles" \
			GUARDS_BASELINE_BASE="$base" GUARDS_BASE_REF=__none__ GUARDS_DEADCODE_ALLOW="$FIXTURES/deadcode.allow" \
			"$0" check 2>&1)"; then rc=0; else rc=$?; fi
		got="$got
exit $rc"
		if [ "$got" = "$(cat "$dir/want")" ]; then
			echo "  ok   $name"
		else
			echo "::error::uncovered-guards check disagrees with fixture $name:" >&2
			diff -u "$dir/want" <(printf '%s\n' "$got") >&2 || true
			failed=1
		fi
	done

	# The recognizer half. Same shape, one tree per rule, `want` pins the derived
	# set exactly -- so loosening the `err != nil` exclusion, or widening what
	# counts as a refusal, changes a file and says which.
	local scan="$ROOT/.github/testdata/guardsites"
	[ -d "$scan" ] || die "missing ${scan#"$ROOT"/}"
	for dir in "$scan"/*/; do
		dir="${dir%/}"
		name="$(basename "$dir")"
		[ -f "$dir/want" ] || die "fixture $name has no want"
		# The relative-root fixture pins the invocation shape too: an empty
		# `relative-invocation` sentinel in the fixture makes the harness pass
		# the root the way a hand caller would, relative to the tool's own
		# directory, instead of letting the harness absolutise it. A fixture
		# cannot otherwise say how it was run.
		arg="$dir"
		if [ -f "$dir/relative-invocation" ]; then
			arg="../../testdata/guardsites/$name"
		fi
		if got="$(cd "$ROOT/.github/scripts/guardsites" && go run . "$arg" 2>&1)"; then rc=0; else rc=$?; fi
		got="$got
exit $rc"
		if [ "$got" = "$(cat "$dir/want")" ]; then
			echo "  ok   guardsites/$name"
		else
			echo "::error::guardsites disagrees with fixture $name:" >&2
			diff -u "$dir/want" <(printf '%s\n' "$got") >&2 || true
			failed=1
		fi
	done

	# The self-measure refusal is not pinnable by a fixture tree: no fixture can
	# contain the tool's own compiled-from source, which is what the check keys
	# against. Pin it directly: `go run . .` from the tool's own directory must
	# fail closed, not print the tool's handful of sites as the tree's answer.
	if out="$(cd "$ROOT/.github/scripts/guardsites" && go run . . 2>&1)"; then rc=0; else rc=$?; fi
	if [ "$rc" -eq 0 ] || ! printf '%s\n' "$out" | grep -q "tool's own source"; then
		echo "::error::guardsites measured itself from '.' instead of failing closed:" >&2
		printf '%s\n' "$out" >&2
		failed=1
	fi

	[ "$failed" -eq 0 ] || exit 1
	echo "uncovered-guards selftest: all fixtures agree"
}

case "${1:-}" in
sites) sites ;;
merge) merge ;;
classify) classify ;;
generate) generate ;;
check) check ;;
selftest) selftest ;;
*)
	echo "usage: uncovered-guards.sh {sites|merge|classify|generate|check|selftest}" >&2
	exit 2
	;;
esac
