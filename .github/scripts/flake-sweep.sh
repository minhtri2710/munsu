#!/usr/bin/env bash
#
# The networked half of the flake ledger: derive which tests are flaky from the
# runs CI has already produced, and compare that set against
# .github/flake-ledger.md in both directions.
#
# ---------------------------------------------------------------------------
# Why this reads attempts and not runs
# ---------------------------------------------------------------------------
#
# A rerun overwrites a run's `conclusion`. Four consecutive pushes to main on
# 2026-08-14 13:40-13:41 produced three red `integration` lanes; two of those
# runs read `success` today, because they were rerun afterwards. `gh run list`
# says that cluster was 2 red out of 4. It was 3.
#
#   run 31805801020  conclusion success   attempt 1 integration failure (job 94784299072)
#   run 31805831782  conclusion success   attempt 1 integration failure (job 94784399392)
#   run 31805867146  conclusion failure   attempt 1 integration failure (job 94784517378)
#
# So the gap this mechanism fills is not a missing signal. The signal fired
# three times and was erased by the reflex that a flaky-looking lane provokes:
# press rerun. Anything that generates *more* signal -- running the suite N
# times, running it under load -- is pouring water into a leaking bucket. This
# reads `/actions/runs/<id>/attempts/<n>/jobs`, which reruns do not overwrite.
#
# ---------------------------------------------------------------------------
# Why there is no failure-rate threshold
# ---------------------------------------------------------------------------
#
# BEO-82 measured 14 reruns of the red lane on the red SHA and got 0 failures,
# against 3 failures in 4 original pushes. If the failure rate were a fixed
# property of the lane, 0/14 has probability ~1e-8. The rate is not fixed: it
# tracks the state of the runner fleet at the time of the run. Any threshold
# placed on a quantity that does not converge is arithmetic on noise, so this
# script computes no rate and has no threshold. It asks a binary question of
# each individual red instead:
#
#   rerun-disagreement  the same run, same lane, same code: one attempt failed
#                       and another passed. Same commit, two verdicts. This is
#                       the strongest evidence there is and it needs no second
#                       run to exist -- it is the reruns themselves, read
#                       instead of discarded.
#   fast-path           the test failed on the main run of a merge commit while
#                       the same lane passed on that PR's own head. Two runs of
#                       near-identical code that CI already paid for. Immediate.
#   self-healing        the test passed again on the next main run, and the diff
#                       between the two commits did not touch its package. A
#                       real regression does not heal itself.
#   persistent          still failing on the next main run. A real regression.
#                       Someone fixes it; it does not belong in the ledger.
#
# Only the first three enter the ledger. None of them estimate anything.
#
# The asymmetry is deliberate and it is what keeps this compatible with BEO-79:
# the ledger only ever accuses, it never certifies. Absence from it proves
# nothing -- 0/14 is what that lesson cost. An entry closes when the issue that
# owns it closes on an argument from the code, never because N days passed with
# no red.
#
# ---------------------------------------------------------------------------
# What it does not see, said here rather than discovered later
# ---------------------------------------------------------------------------
#
#   - It does not prevent the first red. This is accounting, not prevention.
#   - A flake red on two consecutive main runs is filed as a regression, and
#     someone will bisect for nothing. The fast path covers most of that, not
#     all of it.
#   - `build` and `race` run without -v, so for those lanes a test that turned
#     into `t.Skip` is indistinguishable from one that passes. Only
#     `integration` runs -v (ci.yml). Closing that is what v1's `-json` is for.
#   - A self-heal caused by a fix in a *dependency* of the test's package is
#     filed as a flake. The touch check below only knows the test's own
#     directory; a call-graph is out of scope at this size.
#   - The window is bounded by log retention. Evidence older than
#     --window-days is not re-derivable: a row is mandatory while the run it
#     cites is inside the window (deleting it there is red, and while the test
#     is still flaky the sweep re-files it), and once that evidence ages out
#     nothing can re-derive the row, so a fixed row can then be removed by hand
#     and will not come back. Rows are never dropped automatically by age.
#
# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
#
#   flake-sweep.sh observe [--window-days N]   API -> observation records (TSV)
#   flake-sweep.sh classify < records          records -> verdicts (pure text)
#   flake-sweep.sh check    [--window-days N]  observed set vs ledger, both ways
#   flake-sweep.sh sync     [--window-days N]  rewrite the ledger table to match
#   flake-sweep.sh selftest                    classify against committed fixtures
#
# `observe` and `classify` are separate on purpose: classification is pure text
# in, text out, so the fixtures under .github/testdata/flake-sweep/ can pin its
# behaviour without a network, and the 13:40 cluster above is one of them.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LEDGER="${LEDGER:-$ROOT/.github/flake-ledger.md}"
LEDGER_REL=".github/flake-ledger.md"
FIXTURES="$ROOT/.github/testdata/flake-sweep"

# Evidence older than this cannot be re-derived from the API, so it is also how
# long an entry stays grounded. It has to stay comfortably wider than the
# ledger's 14-day deadline: an entry that aged out on the same day it came due
# would be simultaneously overdue and unfounded, and the two rules would ask for
# opposite edits.
WINDOW_DAYS=30

# Days from first sighting to the deadline the bot writes.
DEADLINE_DAYS=14

REPO="${GITHUB_REPOSITORY:-minhtri2710/munsu}"

die() {
	echo "::error::$*" >&2
	exit 1
}

# BSD and GNU date share no syntax for relative dates, and this repo is written
# on macOS and run on ubuntu, so both have to work. Try GNU first, fall back to
# BSD: this is one operation with two spellings, not two implementations.
date_shift() {
	local base="$1" days="$2"
	date -u -d "$base $days days" +%F 2>/dev/null ||
		date -u -j -f %Y-%m-%d -v"${days}d" "$base" +%F
}

today() { date -u +%F; }

# Job display name -> lane key. The ledger stores keys, so ci.yml can reword a
# job name without invalidating every historical entry.
lane_key() {
	case "$1" in
	"Build and test") echo build ;;
	"Race detector") echo race ;;
	"Integration tests") echo integration ;;
	*) echo "" ;;
	esac
}

# ---------------------------------------------------------------------------
# observe
# ---------------------------------------------------------------------------
#
# Emits typed, tab-separated records. Everything downstream reads these and
# nothing else, so `classify` can be tested offline:
#
#   run       run_id  attempt  sha       created_at
#   job       run_id  attempt  lane      conclusion
#   fail      run_id  attempt  lane      test         package
#   headjob   sha     lane     conclusion
#   compared  sha     next_sha
#   touch     sha     next_sha directory
#
# `run` rows arrive in ascending created_at order and the classifier takes that
# order as given -- it is what "the next main run" means. observe sorts them; a
# hand-written fixture has to keep them sorted, and the timestamp is in the
# record so that is checkable by reading it.
#
# Logs are fetched for failed test jobs only. A green job needs no log: a test
# cannot fail inside a job that succeeded, so "not named in any fail record of a
# successful job" is the same information at zero bytes. That is what keeps the
# whole sweep inside a couple of minutes.

api() { gh api -X GET "$@"; }

# The log endpoint returns raw job output, which contains terminal escape
# sequences; `gh api` refuses to write those out, and the flag that overrides it
# is too new to rely on. curl is the stable spelling. Auth is dropped by curl on
# the cross-host redirect to blob storage, which is correct -- that URL is
# pre-signed.
job_log() {
	local job="$1"
	curl -sS --fail-with-body -L \
		-H "Authorization: Bearer ${GH_TOKEN:-$(gh auth token)}" \
		-H "Accept: application/vnd.github+json" \
		"https://api.github.com/repos/$REPO/actions/jobs/$job/logs"
}

# `--- FAIL: TestX (1.23s)` at top level only: a subtest is indented under the
# timestamp, and its parent is already named on its own line, so filing both
# would double-count one red. The package comes from the `FAIL<TAB>import/path`
# line that `go test` prints after each package's output; it is only used by the
# self-heal touch check, and `-` when the line is missing simply means that
# check cannot exclude anything.
extract_failures() {
	awk '
		{
			line = $0
			sub(/^[^ ]+ /, "", line)   # drop the runner timestamp
			if (line ~ /^--- FAIL: [A-Za-z0-9_]+/) {
				name = line
				sub(/^--- FAIL: /, "", name)
				sub(/[^A-Za-z0-9_].*$/, "", name)
				pending[++np] = name
			} else if (line ~ /^(ok|FAIL)[ \t]+[a-z]/) {
				pkg = line
				sub(/^(ok|FAIL)[ \t]+/, "", pkg)
				sub(/[ \t].*$/, "", pkg)
				for (i = 1; i <= np; i++) print pending[i] "\t" pkg
				np = 0
			}
		}
		END { for (i = 1; i <= np; i++) print pending[i] "\t-" }
	' | sort -u
}

observe() {
	local since runs run_id attempts sha created a jobs job_id job_name conc lane
	since="$(date_shift "$(today)" "-$WINDOW_DAYS")"

	# The CI workflow by file name, not every workflow in the repo: this
	# script's own workflow must not become an input to itself.
	runs="$(api "repos/$REPO/actions/workflows/ci.yml/runs" \
		-f branch=main -f event=push -f per_page=100 -f "created=>=$since" --paginate \
		--jq '.workflow_runs[] | [.id, .run_attempt, .head_sha, .created_at, .conclusion] | @tsv' |
		sort -t"$(printf '\t')" -k4,4)" || die "cannot list CI runs on main"
	[ -n "$runs" ] || die "no CI runs on main since $since -- refusing to report an empty world as clean"

	# Pass one is written to a file as well as to stdout, because pass two asks
	# the classifier what it still cannot decide and only pays for those. See
	# below.
	local pass1 conclusion
	pass1="$(mktemp)"
	{
	while IFS=$'\t' read -r run_id attempts sha created conclusion; do
		# A run with one attempt and conclusion `success` has one green job per
		# lane and nothing else to learn, so its jobs are written out rather
		# than fetched. That is not a shortcut around the attempt-level reading
		# this script exists for -- the case it skips is the one where there is
		# only one attempt and it passed. It is what makes the sweep affordable
		# after every push: 263 of the 299 main runs in a 30-day window are
		# this shape, so 88% of the per-attempt calls are for runs that have
		# nothing to say. Measured, not assumed.
		#
		# Every lane in ci.yml runs unconditionally (no `if:`), so "the run was
		# green" does mean "each of these three lanes was green". A lane that
		# grows an `if:` would read as green here while never having run; that
		# is the one thing to re-check before adding one.
		if [ "$attempts" = "1" ] && [ "$conclusion" = "success" ]; then
			printf 'run\t%s\t1\t%s\t%s\n' "$run_id" "$sha" "$created"
			printf 'job\t%s\t1\tbuild\tsuccess\n' "$run_id"
			printf 'job\t%s\t1\trace\tsuccess\n' "$run_id"
			printf 'job\t%s\t1\tintegration\tsuccess\n' "$run_id"
			continue
		fi
		for a in $(seq 1 "$attempts"); do
			printf 'run\t%s\t%s\t%s\t%s\n' "$run_id" "$a" "$sha" "$created"
			jobs="$(api "repos/$REPO/actions/runs/$run_id/attempts/$a/jobs" -f per_page=100 \
				--jq '.jobs[] | [.id, .name, .conclusion] | @tsv')" ||
				die "cannot read jobs of run $run_id attempt $a"
			while IFS=$'\t' read -r job_id job_name conc; do
				[ -n "$job_name" ] || continue
				lane="$(lane_key "$job_name")"
				[ -n "$lane" ] || continue
				printf 'job\t%s\t%s\t%s\t%s\n' "$run_id" "$a" "$lane" "$conc"
				[ "$conc" = "failure" ] || continue
				job_log "$job_id" | extract_failures |
					while IFS=$'\t' read -r test pkg; do
						printf 'fail\t%s\t%s\t%s\t%s\t%s\n' "$run_id" "$a" "$lane" "$test" "$pkg"
					done
			done <<<"$jobs"
		done
	done <<<"$runs"
	} | tee "$pass1"

	# Pass two: the fast path and the touch check cost three or four calls per
	# SHA, and most reds do not need them. Ask the classifier what pass one
	# already settled and enrich only the rest:
	#
	#   - a red the reruns already contradict is decided, and
	#   - a red that is still red on the next main commit is a regression.
	#
	# What is left is the pending reds (the fast path may settle them now rather
	# than in up to 12.8 hours) and the self-healing ones (which are not allowed
	# to count until the touch check has had its say). In a 30-day window that is
	# a handful of SHAs instead of the 31 that went red.
	local enrich head_sha head_run
	enrich="$(classify <"$pass1" |
		awk -F '\t' '$1 == "pending" || ($1 == "flake" && $7 ~ /^self-healing/) { print $4 }' |
		sort -u)"

	for sha in $enrich; do
		head_sha="$(gh api "repos/$REPO/commits/$sha/pulls" --jq '.[0].head.sha // empty')" || head_sha=""
		[ -n "$head_sha" ] || continue
		head_run="$(api "repos/$REPO/actions/workflows/ci.yml/runs" \
			-f "head_sha=$head_sha" -f per_page=10 \
			--jq '[.workflow_runs[] | select(.event == "pull_request")] | first | .id // empty')" || head_run=""
		[ -n "$head_run" ] || continue
		# Attempt 1 of the head run, for the same reason the main side reads
		# attempt 1: a rerun on the PR would otherwise launder a red head into a
		# green one and turn a real regression into a ledger entry.
		api "repos/$REPO/actions/runs/$head_run/attempts/1/jobs" -f per_page=100 \
			--jq '.jobs[] | [.name, .conclusion] | @tsv' |
			while IFS=$'\t' read -r job_name conc; do
				lane="$(lane_key "$job_name")"
				[ -n "$lane" ] || continue
				printf 'headjob\t%s\t%s\t%s\n' "$sha" "$lane" "$conc"
			done
	done

	# Packages touched between a red main commit and the next main commit. The
	# self-heal rule needs this: a test that goes green again because the next
	# commit fixed it is not a flake, and filing it as one would put an
	# already-fixed test under a deadline. It only knows the test's own
	# directory -- a fix that landed in a dependency still reads as a flake.
	local prev_sha="" next_sha
	while IFS=$'\t' read -r run_id attempts sha created conclusion; do
		if [ -n "$prev_sha" ] && printf '%s\n' "$enrich" | grep -qx "$prev_sha"; then
			next_sha="$sha"
			# The pair is announced separately from its files, so the
			# classifier can tell "compared, nothing touched" from "never
			# compared". Without that distinction an absent comparison reads
			# as an untouched package, and a fix would be filed as a flake.
			printf 'compared\t%s\t%s\n' "$prev_sha" "$next_sha"
			gh api "repos/$REPO/compare/$prev_sha...$next_sha" --jq '.files[].filename' |
				sed -E 's|/[^/]+$||' | sort -u |
				while IFS= read -r dir; do
					printf 'touch\t%s\t%s\t%s\n' "$prev_sha" "$next_sha" "$dir"
				done
		fi
		prev_sha="$sha"
	done <<<"$runs"

	rm -f "$pass1"
}

# ---------------------------------------------------------------------------
# classify
# ---------------------------------------------------------------------------
#
# Pure: observation records on stdin, verdicts on stdout. No network, no clock,
# no filesystem. Every rule it applies is named in the header of this file.
#
# Output, tab-separated and sorted so a fixture can pin it:
#
#   verdict  test  lane  sha  run_id  attempt  detail
#
# with verdict one of flake / persistent / ambiguous / pending / unattributed.

classify() {
	local module
	module="$(awk '$1 == "module" { print $2 }' "$ROOT/go.mod" 2>/dev/null || true)"
	awk -F '\t' -v module="${module:-}" '
		function pkgdir(importpath,   d) {
			if (importpath == "-" || module == "") return ""
			if (index(importpath, module "/") != 1) return ""
			d = substr(importpath, length(module) + 2)
			return d
		}
		$1 == "run" {
			# run_id attempt sha created_at
			runsha[$2] = $4
			if (!($2 in seenrun)) { seenrun[$2] = 1; order[++n] = $2; idx[$2] = n }
			if ($3 + 0 > maxattempt[$2] + 0) maxattempt[$2] = $3
		}
		$1 == "job"     { jobconc[$2 SUBSEP $3 SUBSEP $4] = $5 }
		$1 == "fail"    { failed[$2 SUBSEP $3 SUBSEP $4 SUBSEP $5] = 1; pkg[$2 SUBSEP $3 SUBSEP $4 SUBSEP $5] = $6
		                  fails[++nf] = $2 SUBSEP $3 SUBSEP $4 SUBSEP $5 }
		$1 == "headjob"  { headconc[$2 SUBSEP $3] = $4 }
		$1 == "compared" { compared[$2 SUBSEP $3] = 1 }
		$1 == "touch"    { touched[$2 SUBSEP $3 SUBSEP $4] = 1 }

		END {
			# A test lane that failed while naming no test at all is a build
			# break, a timeout or an infrastructure failure. Reporting it as a
			# flaky test would be a lie; dropping it silently would hide the
			# one case where this script cannot see what happened, so it gets
			# its own verdict.
			for (k in jobconc) {
				split(k, p, SUBSEP)
				if (jobconc[k] != "failure") continue
				any = 0
				for (j = 1; j <= nf; j++) {
					split(fails[j], q, SUBSEP)
					if (q[1] == p[1] && q[2] == p[2] && q[3] == p[3]) { any = 1; break }
				}
				if (!any)
					out[++no] = "unattributed\t-\t" p[3] "\t" runsha[p[1]] "\t" p[1] "\t" p[2] "\tlane failed naming no test"
			}

			for (j = 1; j <= nf; j++) {
				split(fails[j], p, SUBSEP)
				run = p[1]; att = p[2]; lane = p[3]; test = p[4]
				sha = runsha[run]

				# 1. Rerun disagreement: same run, same code, both verdicts.
				verdict = ""
				for (a = 1; a <= maxattempt[run]; a++) {
					if (a == att) continue
					if (jobconc[run SUBSEP a SUBSEP lane] == "success") {
						verdict = "flake"; detail = "rerun-disagreement: attempt " a " of the same run passed"
						break
					}
				}

				# 2. Fast path: the PR head ran the same lane green.
				if (verdict == "" && headconc[sha SUBSEP lane] == "success") {
					verdict = "flake"; detail = "fast-path: same lane green on the PR head for this merge"
				}

				# 3. Self-healing vs persistent, judged on the next main run
				# that actually ran this lane on a different commit. Only a
				# decisive verdict -- success or failure -- decides; a run whose
				# lane conclusion is cancelled, skipped or empty decides nothing,
				# so it is stepped over rather than read as a verdict.
				if (verdict == "") {
					nxt = ""
					for (i = idx[run] + 1; i <= n; i++) {
						if (runsha[order[i]] == sha) continue
						c = jobconc[order[i] SUBSEP 1 SUBSEP lane]
						if (c == "success" || c == "failure") { nxt = order[i]; break }
					}
					nxtsha = (nxt == "" ? "" : runsha[nxt])
					if (nxt == "") {
						verdict = "pending"; detail = "no later main commit has run this lane yet"
					} else if ((nxt SUBSEP 1 SUBSEP lane SUBSEP test) in failed) {
						verdict = "persistent"; detail = "still failing on the next main commit " substr(nxtsha, 1, 8)
					} else if (!((sha SUBSEP nxtsha) in compared)) {
						# Green again, but with no diff in hand there is no way to
						# tell a flake from a fix. Say so instead of guessing: a
						# guess here files a real fix as a flaky test.
						verdict = "pending"
						detail = "green again on " substr(nxtsha, 1, 8) ", but that pair was never compared"
					} else {
						d = pkgdir(pkg[fails[j]])
						if (d != "" && ((sha SUBSEP nxtsha SUBSEP d) in touched)) {
							verdict = "ambiguous"
							detail = "green again on " substr(nxtsha, 1, 8) ", but that diff touched " d
						} else {
							verdict = "flake"
							detail = "self-healing: green again on " substr(nxtsha, 1, 8) " with no change to its package"
						}
					}
				}
				out[++no] = verdict "\t" test "\t" lane "\t" sha "\t" run "\t" att "\t" detail
			}
			for (i = 1; i <= no; i++) print out[i]
		}
	' | sort -u
}

# ---------------------------------------------------------------------------
# The observed set, reduced to one row per (test, lane)
# ---------------------------------------------------------------------------
#
# first_seen is the earliest flake observation and last_seen the newest one
# this window saw, both cited as sha@run/attempt. The attempt is not
# decoration: it is the only citation a later rerun cannot invalidate, which is
# the whole reason this mechanism exists. last_seen is what keeps a re-flake of
# a `fixed:` row from being silent: check() is red while the ledger lags the
# evidence, and sync() reopens the row when the evidence moves.

observed_set() {
	local records="$1"
	classify <"$records" |
		awk -F '\t' '$1 == "flake" { print $2 "\t" $3 "\t" $4 "\t" $5 "\t" $6 }' |
		sort -t"$(printf '\t')" -k1,1 -k2,2 -k4,4n -k5,5n |
		awk -F '\t' '
			!seen[$1 "\t" $2]++ { first[$1 "\t" $2] = sprintf("%.8s@%s/%s", $3, $4, $5) }
			{ last[$1 "\t" $2] = sprintf("%.8s@%s/%s", $3, $4, $5) }
			END { for (k in first) print k "\t" first[k] "\t" last[k] }
		' |
		sort
}

ledger_set() {
	"$ROOT/.github/scripts/flake-ledger.sh" entries | awk -F '\t' '{ print $1 "\t" $2 }' | sort
}

# The run ids this sweep actually read, one per line, in a file awk can load.
window_runs() {
	awk -F '\t' '$1 == "run" { print $2 }' "$1" | sort -u
}

# The ledger rows this sweep is entitled to judge: the ones whose first_seen run
# is inside the window it looked at.
#
# Without this the window would be load-bearing in a way nobody would notice
# until it hurt: a sweep over 7 days would find an entry filed three weeks ago
# "no longer grounded", demand its deletion, and the next wide sweep would
# re-file it. Scoping by run id rather than by date keeps both directions of the
# comparison honest at any window, which is what lets the workflow run a cheap
# window after every push and a wide one by hand.
ledger_in_scope() {
	local records="$1" runs
	runs="$(mktemp)"
	window_runs "$records" >"$runs"
	"$ROOT/.github/scripts/flake-ledger.sh" entries |
		awk -F '\t' -v runs="$runs" '
			BEGIN { while ((getline r < runs) > 0) inwindow[r] = 1 }
			{
				split($3, part, /[@\/]/)
				if (part[2] in inwindow) print $1 "\t" $2
			}' | sort
	rm -f "$runs"
}

records_file() {
	local f="${SWEEP_RECORDS:-}"
	if [ -n "$f" ]; then
		[ -f "$f" ] || die "SWEEP_RECORDS=$f does not exist"
		printf '%s' "$f"
		return
	fi
	f="$(mktemp)"
	observe >"$f"
	printf '%s' "$f"
}

# Three directions, in the shape .github/deadcode.allow already uses in this
# repo: observed but unfiled is red, filed but no longer observable is red too,
# and a row this sweep observed must carry the newest observation as its
# last_seen. The second direction is what stops the file rotting into a
# graveyard; the third is what keeps a re-flake of a `fixed:` row from being
# silent.
#
# The directions are not symmetrical in scope, and should not be. A test
# observed flaky now is missing from the ledger whether or not its entry would
# have come from this window, so that direction compares against every entry. An
# entry is only demanded for deletion when this sweep actually read the run it
# cites.
check() {
	local records obs obstmp failed=0 missing extra stale
	records="$(records_file)"

	obs="$(observed_set "$records")"
	obstmp="$(mktemp)"
	printf '%s\n' "$obs" >"$obstmp"

	missing="$(comm -23 <(printf '%s\n' "$obs" | cut -f1,2) <(ledger_set))"
	if [ -n "$missing" ]; then
		echo "::error::flaky tests observed in CI with no entry in $LEDGER_REL:" >&2
		printf '%s\n' "$missing" | while IFS=$'\t' read -r test lane; do
			printf '  %s (%s)\n' "$test" "$lane" >&2
			classify <"$records" | awk -F '\t' -v t="$test" -v l="$lane" '
				$1 == "flake" && $2 == t && $3 == l { printf "      %s %s attempt %s: %s\n", substr($4,1,8), $5, $6, $7 }' >&2
		done
		echo "  Run '.github/scripts/flake-sweep.sh sync' to file them, then fill in owner_issue." >&2
		failed=1
	fi

	extra="$(comm -13 <(printf '%s\n' "$obs" | cut -f1,2) <(ledger_in_scope "$records"))"
	if [ -n "$extra" ]; then
		echo "::error::$LEDGER_REL entries this sweep can no longer derive from the run they cite:" >&2
		printf '%s\n' "$extra" | while IFS=$'\t' read -r test lane; do
			printf '  %s (%s)\n' "$test" "$lane" >&2
		done
		echo "  Their first_seen run is inside the window just read, and nothing in it says they were" >&2
		echo "  flaky. Either the evidence moved or the row did. Run 'flake-sweep.sh sync' to drop them" >&2
		echo "  -- the long-term record of a flake is its owning issue, not this file." >&2
		failed=1
	fi

	stale="$(awk -F '\t' -v obs="$obstmp" '
		BEGIN { while ((getline o < obs) > 0) { split(o, p, "\t"); newest[p[1] "\t" p[2]] = p[4] } }
		($1 "\t" $2) in newest && $4 != newest[$1 "\t" $2] {
			what = ($7 ~ /^fixed:/) ? "flaked again after being declared fixed; must be reopened" : "has new evidence to record"
			print $1 "\t" $2 "\t" $4 "\t" newest[$1 "\t" $2] "\t" what
		}' <<<"$("$ROOT/.github/scripts/flake-ledger.sh" entries)")"
	if [ -n "$stale" ]; then
		echo "::error::$LEDGER_REL rows whose last_seen lags the evidence this sweep observed:" >&2
		printf '%s\n' "$stale" | while IFS=$'\t' read -r test lane recorded observed what; do
			printf '  %s (%s): recorded %s, observed %s -- %s\n' "$test" "$lane" "$recorded" "$observed" "$what" >&2
		done
		echo "  Run 'flake-sweep.sh sync' to record the evidence; a row that flaked again after being" >&2
		echo "  declared fixed is reopened with a fresh deadline, keeping its owning issue." >&2
		failed=1
	fi

	rm -f "$obstmp"
	[ "$failed" -eq 0 ] || exit 1
	echo "flake sweep: $(printf '%s\n' "$obs" | grep -c . || true) flaky (test, lane) pairs observed, all filed"
}

# Strictly-newer comparison of two <sha>@<run_id>/<attempt> citations: run ids
# grow with time, and equal run ids are disambiguated by attempt.
newer() {
	local a_run a_att b_run b_att
	a_run="${1#*@}"; a_run="${a_run%%/*}"
	a_att="${1##*/}"
	b_run="${2#*@}"; b_run="${b_run%%/*}"
	b_att="${2##*/}"
	[ "$a_run" -gt "$b_run" ] 2>/dev/null ||
		{ [ "$a_run" -eq "$b_run" ] 2>/dev/null && [ "$a_att" -gt "$b_att" ] 2>/dev/null; }
}

# Rewrite the table between the markers to match the observed set: keep the
# deadline, owner and state of a row that is still grounded, add a row for a new
# flake, drop an in-scope row the evidence no longer supports, and leave rows
# from outside this window untouched. Rows this window observed get their
# last_seen updated to the newest observation, and a `fixed:` row with newer
# evidence is reopened with a fresh deadline. Never edits the prose around it.
sync() {
	local records rows runs observed tmp existing seen last_seen recorded_last deadline owner state
	records="$(records_file)"
	grep -q '^<!-- flake-ledger:begin -->$' "$LEDGER" || die "$LEDGER_REL has no <!-- flake-ledger:begin --> marker"

	rows="$(mktemp)"
	runs="$(mktemp)"
	observed="$(mktemp)"
	window_runs "$records" >"$runs"
	observed_set "$records" | cut -f1,2 >"$observed"

	observed_set "$records" | while IFS=$'\t' read -r test lane first_seen last_seen; do
		existing="$("$ROOT/.github/scripts/flake-ledger.sh" entries |
			awk -F '\t' -v t="$test" -v l="$lane" '$1 == t && $2 == l')"
		if [ -n "$existing" ]; then
			# An existing row keeps its own first_seen. Re-deriving it on every
			# sweep would let a deadline walk forward on its own, which is
			# exactly the escape this file has to make visible rather than
			# provide. A `fixed:` row with newer evidence has flaked again after
			# being declared fixed: it is reopened with a fresh deadline,
			# keeping the issue that owns the fix.
			IFS=$'\t' read -r _ _ seen recorded_last deadline owner state <<<"$existing"
			if [[ "$state" == fixed:* ]] && newer "$last_seen" "$recorded_last"; then
				state="open"
				deadline="$(date_shift "$(today)" "+$DEADLINE_DAYS")"
			fi
		else
			seen="$first_seen"
			deadline="$(date_shift "$(today)" "+$DEADLINE_DAYS")"
			# The bot cannot know which issue will own a flake, and
			# flake-ledger.sh refuses TBD, so the PR this produces cannot merge
			# until a person files that issue. Filing the flake and filing the
			# work are meant to be the same act.
			owner="TBD"
			state="open"
		fi
		printf '| %s | %s | %s | %s | %s | %s | %s |\n' "$test" "$lane" "$seen" "$last_seen" "$deadline" "$owner" "$state"
	done >"$rows"

	"$ROOT/.github/scripts/flake-ledger.sh" entries |
		awk -F '\t' -v runs="$runs" -v obs="$observed" '
			BEGIN {
				while ((getline r < runs) > 0) inwindow[r] = 1
				while ((getline o < obs) > 0) observed[o] = 1
			}
			{
				if (($1 "\t" $2) in observed) next   # already rewritten above
				split($3, part, /[@\/]/)
				if (part[2] in inwindow) next         # in scope and unsupported: dropped
				printf "| %s | %s | %s | %s | %s | %s | %s |\n", $1, $2, $3, $4, $5, $6, $7
			}' >>"$rows"

	tmp="$(mktemp)"
	{
		sed -n '1,/^<!-- flake-ledger:begin -->$/p' "$LEDGER"
		echo '| test | lane | first_seen | last_seen | deadline | owner_issue | state |'
		echo '| --- | --- | --- | --- | --- | --- | --- |'
		sort "$rows"
		sed -n '/^<!-- flake-ledger:end -->$/,$p' "$LEDGER"
	} >"$tmp"
	mv "$tmp" "$LEDGER"
	rm -f "$rows" "$runs" "$observed"
	echo "flake sweep: $LEDGER_REL rewritten, $(observed_set "$records" | grep -c . || true) pairs observed in this window"
}

# ---------------------------------------------------------------------------
# selftest
# ---------------------------------------------------------------------------
#
# classify() is the part that can be wrong in a way nobody notices, so it is
# pinned by fixtures rather than by trust. cluster-13-40 is the real record set
# of the four runs in the header: it must recover all three original reds, the
# two that read `success` at run level included. That is the only test that
# proves this reads attempts rather than conclusions.

selftest() {
	local failed=0 obs want got name
	[ -d "$FIXTURES" ] || die "missing ${FIXTURES#"$ROOT"/}"
	for obs in "$FIXTURES"/*.obs.tsv; do
		name="$(basename "$obs" .obs.tsv)"
		want="${obs%.obs.tsv}.want.tsv"
		[ -f "$want" ] || die "fixture $name has no .want.tsv"
		got="$(classify <"$obs")"
		if [ "$got" = "$(cat "$want")" ]; then
			echo "  ok   $name"
		else
			echo "::error::flake-sweep classify disagrees with fixture $name:" >&2
			diff -u "$want" <(printf '%s\n' "$got") >&2 || true
			failed=1
		fi
	done

	# A row observed flaky again after being declared fixed must not be silent:
	# check() has to be red on the mismatch, and sync() has to reopen the row
	# with a fresh deadline, keeping its owning issue. Driven end to end through
	# the real check() and sync() on a throwaway ledger, because the classify
	# fixtures above only pin the evidence, not what the comparison does with it.
	local ledger reopen reopen_msg
	ledger="$(mktemp)"
	reopen="$FIXTURES/reopen.obs.tsv"
	{
		echo '# Flake ledger -- selftest fixture'
		echo '<!-- flake-ledger:begin -->'
		echo '| test | lane | first_seen | last_seen | deadline | owner_issue | state |'
		echo '| --- | --- | --- | --- | --- | --- | --- |'
		echo '| TestReopens | integration | aaaa1111@3001/1 | aaaa1111@3001/1 | 2026-08-30 | BEO-99 | fixed:abc123 |'
		echo '<!-- flake-ledger:end -->'
	} >"$ledger"
	if SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check >/dev/null 2>&1; then
		echo "::error::reopen scenario: check() accepted a re-flaked fixed row" >&2
		failed=1
	else
		reopen_msg="$(SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check 2>&1 || true)"
		if ! printf '%s\n' "$reopen_msg" | grep -q 'reopened'; then
			echo "::error::reopen scenario: check() flagged the re-flake without the reopen message" >&2
			failed=1
		fi
	fi
	SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync >/dev/null || {
		echo "::error::reopen scenario: sync() failed" >&2
		failed=1
	}
	if ! grep -q '| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 |.*| BEO-99 | open |' "$ledger"; then
		echo "::error::reopen scenario: sync did not reopen the fixed row with a fresh deadline" >&2
		failed=1
	fi
	if ! SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check >/dev/null 2>&1; then
		echo "::error::reopen scenario: check() is still red after sync reopened the row" >&2
		failed=1
	fi
	rm -f "$ledger"

	[ "$failed" -eq 0 ] || exit 1
	echo "flake sweep selftest: all fixtures agree"
}

cmd="${1:-}"
[ $# -gt 0 ] && shift
while [ $# -gt 0 ]; do
	case "$1" in
	--window-days)
		WINDOW_DAYS="$2"
		shift 2
		;;
	*) die "unknown flag $1" ;;
	esac
done

case "$cmd" in
observe) observe ;;
classify) classify ;;
check) check ;;
sync) sync ;;
selftest) selftest ;;
*)
	echo "usage: flake-sweep.sh {observe|classify|check|sync|selftest} [--window-days N]" >&2
	exit 2
	;;
esac
