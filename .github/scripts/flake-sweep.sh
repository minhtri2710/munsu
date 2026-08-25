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
#   flake-sweep.sh sync     [--owner <ISSUE_ID>] [--window-days N]  rewrite the ledger table to match
#   flake-sweep.sh verify-fixed                every `fixed:<sha>` is on main
#   flake-sweep.sh applied                     observe and validate this checkout's ledger
#   flake-sweep.sh selftest                    classify against committed fixtures
#
# `observe` and `classify` are separate on purpose: classification is pure text
# in, text out, so the fixtures under .github/testdata/flake-sweep/ can pin its
# behaviour without a network, and the 13:40 cluster above is one of them.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LEDGER_WAS_SET=0
[ "${LEDGER+x}" = x ] && LEDGER_WAS_SET=1
SWEEP_RECORDS_WAS_SET=0
[ "${SWEEP_RECORDS+x}" = x ] && SWEEP_RECORDS_WAS_SET=1
LEDGER="${LEDGER:-$ROOT/.github/flake-ledger.md}"
LEDGER_REL=".github/flake-ledger.md"
FIXTURES="$ROOT/.github/testdata/flake-sweep"
WORKFLOW_FILE="${WORKFLOW_FILE:-$ROOT/.github/workflows/ci.yml}"

# Evidence older than this cannot be re-derived from the API, so it is also how
# long an entry stays grounded. It has to stay comfortably wider than the
# ledger's 14-day deadline: an entry that aged out on the same day it came due
# would be simultaneously overdue and unfounded, and the two rules would ask for
# opposite edits.
WINDOW_DAYS=30

# Days from first sighting to the deadline the bot writes.
DEADLINE_DAYS=14

# ...and "comfortably wider" is now a number this script refuses to go below,
# because the invariant above was stated in a comment and contradicted by the
# workflow that calls it: flake-ledger.yml ran `--window-days 7` on every push
# to main (its `inputs.window-days || '7'` fallback, and `workflow_run` carries
# no inputs). At 7 days every row older than a week is out of window, and a
# window narrower than the deadline makes deleting a row -- or the whole table
# -- green in both halves at once.
#
# Twice the deadline, not once: a row has to stay re-derivable for a full
# deadline period *after* it comes due, or the days when someone is actually
# dealing with an overdue row are exactly the days its evidence is ageing out
# from under them.
WINDOW_FLOOR=$((DEADLINE_DAYS * 2))

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
	local log="$1"
	[ -r "$log" ] || die "cannot read job log $log; observation is incomplete and cannot distinguish no flake from no look"
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
	' "$log" | sort -u
}

observe() {
	local since runs run_id attempts sha created a jobs job_id job_name conc lane local_log failures
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
	(
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
				local_log="$(mktemp)"
				if ! job_log "$job_id" >"$local_log"; then
					rm -f "$local_log"
					die "cannot read job log $job_id; observation is incomplete and cannot distinguish no flake from no look"
				fi
				failures="$(mktemp)"
				if ! extract_failures "$local_log" >"$failures"; then
					rm -f "$local_log" "$failures"
					die "cannot parse job log $job_id; observation is incomplete and cannot distinguish no flake from no look"
				fi
				while IFS=$'\t' read -r test pkg; do
					printf 'fail\t%s\t%s\t%s\t%s\t%s\n' "$run_id" "$a" "$lane" "$test" "$pkg"
				done <"$failures"
				rm -f "$local_log" "$failures"
			done <<<"$jobs"
		done
	done <<<"$runs"
	) >"$pass1" || {
		rm -f "$pass1"
		die "could not observe the CI window; observation is incomplete and cannot be told apart from clean"
	}
	cat "$pass1"

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
	local enrich head_sha head_run headjobs
	enrich_file="$(mktemp)"
	if ! classify <"$pass1" |
		awk -F '\t' '$1 == "pending" || ($1 == "flake" && $7 ~ /^self-healing/) { print $4 }' |
		sort -u >"$enrich_file"; then
		rm -f "$pass1" "$enrich_file"
		die "could not classify the CI window; observation is incomplete and cannot be told apart from clean"
	fi
	enrich="$(cat "$enrich_file")"
	rm -f "$enrich_file"

	# Every call here dies on failure, like every other call in this script, and
	# the distinction it is protecting is worth stating: an empty answer means
	# "this merge commit has no PR" or "that PR never ran CI", which is a fact
	# about the world and correctly leaves the red `pending`. A failed call means
	# this script does not know, and the two must not collapse into each other.
	#
	# They did, until BEO-103. A 403, a secondary rate limit or one 5xx turned a
	# `flake` into a `pending`, `check` then reported the row already in the
	# ledger as no longer derivable, and `sync` deleted it -- taking its
	# owner_issue and deadline with it -- inside a PR that asks the reader not to
	# argue with the sweep. The fast path is the only route to a verdict for the
	# median 43 minutes (p90 7.3 hours) before the next main run reaches this
	# lane, so this is not a rare corner: it is the window the fast path exists
	# to fill. Fail closed: no verdict, no row deleted.
	for sha in $enrich; do
		head_sha="$(gh api "repos/$REPO/commits/$sha/pulls" --jq '.[0].head.sha // empty')" ||
			die "cannot look up the pull request of $sha"
		[ -n "$head_sha" ] || continue
		head_run="$(api "repos/$REPO/actions/workflows/ci.yml/runs" \
			-f "head_sha=$head_sha" -f per_page=10 \
			--jq '[.workflow_runs[] | select(.event == "pull_request")] | first | .id // empty')" ||
			die "cannot list CI runs of PR head $head_sha"
		[ -n "$head_run" ] || continue
		# Attempt 1 of the head run, for the same reason the main side reads
		# attempt 1: a rerun on the PR would otherwise launder a red head into a
		# green one and turn a real regression into a ledger entry.
		headjobs="$(api "repos/$REPO/actions/runs/$head_run/attempts/1/jobs" -f per_page=100 \
			--jq '.jobs[] | [.name, .conclusion] | @tsv')" ||
			die "cannot read jobs of PR head run $head_run attempt 1"
		while IFS=$'\t' read -r job_name conc; do
			[ -n "$job_name" ] || continue
			lane="$(lane_key "$job_name")"
			[ -n "$lane" ] || continue
			printf 'headjob\t%s\t%s\t%s\n' "$sha" "$lane" "$conc"
		done <<<"$headjobs"
	done

	# Packages touched between a red main commit and the next main commit. The
	# self-heal rule needs this: a test that goes green again because the next
	# commit fixed it is not a flake, and filing it as one would put an
	# already-fixed test under a deadline. It only knows the test's own
	# directory -- a fix that landed in a dependency still reads as a flake.
	local prev_sha="" next_sha files
	while IFS=$'\t' read -r run_id attempts sha created conclusion; do
		if [ -n "$prev_sha" ] && printf '%s\n' "$enrich" | grep -qx "$prev_sha"; then
			next_sha="$sha"
			# Fetched before the pair is announced, and fatal if it fails, for
			# the same reason the fast path above is: a compare that errored
			# would otherwise be announced as "compared, nothing touched", which
			# is the record that turns a real fix into a flake entry.
			files="$(gh api "repos/$REPO/compare/$prev_sha...$next_sha" --jq '.files[].filename')" ||
				die "cannot compare $prev_sha...$next_sha"
			# The pair is announced separately from its files, so the
			# classifier can tell "compared, nothing touched" from "never
			# compared". Without that distinction an absent comparison reads
			# as an untouched package, and a fix would be filed as a flake.
			printf 'compared\t%s\t%s\n' "$prev_sha" "$next_sha"
			touch_dirs="$(mktemp)"
			if ! printf '%s\n' "$files" | sed -E 's|/[^/]+$||' | sort -u >"$touch_dirs"; then
				rm -f "$pass1" "$touch_dirs"
				die "could not classify changed files; observation is incomplete and cannot be told apart from clean"
			fi
			while IFS= read -r dir; do
				[ -n "$dir" ] || continue
				printf 'touch\t%s\t%s\t%s\n' "$prev_sha" "$next_sha" "$dir"
			done <"$touch_dirs"
			rm -f "$touch_dirs"
		fi
		prev_sha="$sha"
	done <<<"$runs"

	# `state: fixed:<sha>` is a claim that a commit stopped a test flaking, and
	# it is the one state that takes a row out of the deadline comparison
	# entirely. The hermetic half cannot check it -- it has no network, on
	# purpose -- so the answer is fetched here, with every other API call, and
	# written out as a record. That keeps `verify-fixed` a pure function of this
	# stream, so the selftest can pin all three answers without a network.
	local fsha status fixed_refs
	fixed_refs="$(mktemp)"
	if ! "$ROOT/.github/scripts/flake-ledger.sh" entries |
		awk -F '\t' '$7 ~ /^fixed:/ { print substr($7, 7) }' |
		sort -u >"$fixed_refs"; then
		rm -f "$pass1" "$fixed_refs"
		die "could not read fixed ledger entries; observation is incomplete and cannot be told apart from clean"
	fi
	while IFS= read -r fsha; do
		[ -n "$fsha" ] || continue
		if status="$(gh api "repos/$REPO/compare/$fsha...main" --jq '.status' 2>&1)"; then
			case "$status" in
			identical | ahead) printf 'fixedsha\t%s\ton-main\n' "$fsha" ;;
			*) printf 'fixedsha\t%s\toff-main\n' "$fsha" ;;
			esac
		elif printf '%s' "$status" | grep -q 'HTTP 404'; then
			printf 'fixedsha\t%s\tunknown-sha\n' "$fsha"
		else
			rm -f "$pass1" "$fixed_refs"
			die "cannot tell whether $fsha is on main: $status"
		fi
	done <"$fixed_refs"
	rm -f "$fixed_refs"

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
	local entries="$1"
	awk -F '\t' '{ print $1 "\t" $2 }' "$entries" | sort
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
	local records="$1" entries="$2" runs
	runs="$(mktemp)"
	window_runs "$records" >"$runs"
	awk -F '\t' -v runs="$runs" '
			BEGIN { while ((getline r < runs) > 0) inwindow[r] = 1 }
			{
				split($3, part, /[@\/]/)
				if (part[2] in inwindow) print $1 "\t" $2
			}' "$entries" | sort
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
	if ! (observe) >"$f"; then
		rm -f "$f"
		die "could not observe the CI window; observation is incomplete and unknown is red"
	fi
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
	local records obs obstmp failed=0 missing extra stale ledger_entries ledger_pairs obs_pairs in_scope
	records="$(records_file)"
	ledger_entries="$(mktemp)"
	if ! "$ROOT/.github/scripts/flake-ledger.sh" entries >"$ledger_entries"; then
		rm -f "$ledger_entries"
		die "could not read ledger entries; observation is incomplete and cannot be told apart from clean"
	fi

	obs="$(observed_set "$records")"
	obstmp="$(mktemp)"
	printf '%s\n' "$obs" >"$obstmp"
	ledger_pairs="$(mktemp)"
	obs_pairs="$(mktemp)"
	in_scope="$(mktemp)"
	if ! ledger_set "$ledger_entries" >"$ledger_pairs" || ! cut -f1,2 "$obstmp" | sort >"$obs_pairs"; then
		rm -f "$ledger_entries" "$ledger_pairs" "$obs_pairs" "$in_scope" "$obstmp"
		die "could not normalize ledger entries; observation is incomplete and cannot be told apart from clean"
	fi

	missing="$(comm -23 "$obs_pairs" "$ledger_pairs")"
	if [ -n "$missing" ]; then
		echo "::error::flaky tests observed in CI with no entry in $LEDGER_REL:" >&2
		printf '%s\n' "$missing" | while IFS=$'\t' read -r test lane; do
			printf '  %s (%s)\n' "$test" "$lane" >&2
			classify <"$records" | awk -F '\t' -v t="$test" -v l="$lane" '
				$1 == "flake" && $2 == t && $3 == l { printf "      %s %s attempt %s: %s\n", substr($4,1,8), $5, $6, $7 }' >&2
		done
		echo "  Run '.github/scripts/flake-sweep.sh sync --owner <ISSUE_ID>' to file them under the issue that will fix them." >&2
		failed=1
	fi

	if ! ledger_in_scope "$records" "$ledger_entries" >"$in_scope"; then
		rm -f "$ledger_entries" "$ledger_pairs" "$obs_pairs" "$in_scope" "$obstmp"
		die "could not scope ledger entries; observation is incomplete and cannot be told apart from clean"
	fi
	extra="$(comm -13 "$obs_pairs" "$in_scope")"
	if [ -n "$extra" ]; then
		echo "::error::$LEDGER_REL entries this sweep can no longer derive from the run they cite:" >&2
		printf '%s\n' "$extra" | while IFS=$'\t' read -r test lane; do
			printf '  %s (%s)\n' "$test" "$lane" >&2
		done
		echo "  Their first_seen run is inside the window just read, and nothing in it says they were" >&2
		echo "  flaky. Either the evidence moved or the row did. Run 'flake-sweep.sh sync --owner <ISSUE_ID>' to drop them" >&2
		echo "  -- the long-term record of a flake is its owning issue, not this file." >&2
		failed=1
	fi

	stale="$(awk -F '\t' -v obs="$obstmp" '
		BEGIN { while ((getline o < obs) > 0) { split(o, p, "\t"); newest[p[1] "\t" p[2]] = p[4] } }
		($1 "\t" $2) in newest && $4 != newest[$1 "\t" $2] {
			what = ($7 ~ /^fixed:/) ? "flaked again after being declared fixed; must be reopened" : "has new evidence to record"
			print $1 "\t" $2 "\t" $4 "\t" newest[$1 "\t" $2] "\t" what
		}' "$ledger_entries")"
	if [ -n "$stale" ]; then
		echo "::error::$LEDGER_REL rows whose last_seen lags the evidence this sweep observed:" >&2
		printf '%s\n' "$stale" | while IFS=$'\t' read -r test lane recorded observed what; do
			printf '  %s (%s): recorded %s, observed %s -- %s\n' "$test" "$lane" "$recorded" "$observed" "$what" >&2
		done
		echo "  Run 'flake-sweep.sh sync --owner <ISSUE_ID>' to record the evidence; a row that flaked again after being" >&2
		echo "  declared fixed is reopened with a fresh deadline, keeping its owning issue." >&2
		failed=1
	fi

	rm -f "$ledger_entries" "$ledger_pairs" "$obs_pairs" "$in_scope" "$obstmp"
	[ "$failed" -eq 0 ] || exit 1
	echo "flake sweep: $(printf '%s\n' "$obs" | grep -c . || true) flaky (test, lane) pairs observed, all filed"
}

# ---------------------------------------------------------------------------
# verify-fixed
# ---------------------------------------------------------------------------
#
# `state: fixed:<sha>` is the only value that takes a row out of the deadline
# comparison, and before BEO-103 it proved nothing: `fixed:i-never-fixed-it` and
# `fixed:0000000` both read `0 open, 0 overdue`. That made it cheaper than the
# escape this file already admits to -- moving the deadline, which has to be
# repeated every 14 days and shows up in `git log` as a widening gap -- while
# being permanent and invisible.
#
# The hermetic half now demands the shape of a commit id, which is all a file
# can judge about itself. This demands the commit exists and is reachable from
# main.
#
# A separate command rather than a fourth direction inside check(), because the
# two have different remedies: check()'s is `sync`, and the workflow treats "the
# comparison failed but sync changed nothing" as a bug in this script. A row
# whose fix ref is wrong is repaired by a person, and sync cannot produce that
# diff -- folding it in would make a human-fixable row look like a bug here.
verify_fixed() {
	local records unfounded ledger_entries
	records="$(records_file)"
	ledger_entries="$(mktemp)"
	if ! "$ROOT/.github/scripts/flake-ledger.sh" entries >"$ledger_entries"; then
		rm -f "$ledger_entries"
		die "could not read ledger entries; observation is incomplete and cannot be told apart from clean"
	fi

	# Absence of an answer is not an answer: a row whose sha this sweep never
	# asked about is reported too, so a lookup that silently stops happening
	# cannot read as a clean bill of health.
	unfounded="$(awk -F '\t' -v recs="$records" '
		BEGIN {
			while ((getline r < recs) > 0) {
				split(r, p, "\t")
				if (p[1] == "fixedsha") answer[p[2]] = p[3]
			}
		}
		$7 ~ /^fixed:/ {
			sha = substr($7, 7)
			verdict = (sha in answer) ? answer[sha] : "not-checked"
			if (verdict != "on-main") print $1 "\t" $2 "\t" sha "\t" verdict
		}' "$ledger_entries")"

	if [ -n "$unfounded" ]; then
		echo "::error::$LEDGER_REL rows whose fixed: state does not cite a commit on main:" >&2
		printf '%s\n' "$unfounded" | while IFS=$'\t' read -r test lane sha verdict; do
			case "$verdict" in
			unknown-sha) why="is not a commit in this repository" ;;
			off-main) why="is a commit, but nothing on main reaches it" ;;
			*) why="was not checked by this sweep" ;;
			esac
			printf '  %s (%s): fixed:%s %s\n' "$test" "$lane" "$sha" "$why" >&2
		done
		echo "  A fix that is not on main has not fixed main. Cite the commit that landed, or set the" >&2
		echo "  row back to 'open' with a deadline -- 'fixed:' is the one state no deadline is compared" >&2
		echo "  against, so it is the one state that has to be checkable." >&2
		rm -f "$ledger_entries"
		exit 1
	fi
	rm -f "$ledger_entries"
	echo "flake sweep: every fixed: row cites a commit on main"
}

# ---------------------------------------------------------------------------
# applied
# ---------------------------------------------------------------------------
#
# This path asks the sweep's question directly against the checkout being
# merged. It observes once, then runs `check` and `verify-fixed` over that same
# per-attempt record stream, so no prior workflow run or freshness proxy can
# certify stale evidence. A full 30-day observation costs about 130 seconds and
# about 90 API requests against the 1000-per-hour token budget; that is the
# measured cost of removing the race between CI attempts and the asynchronous
# ledger workflow. Losing the API or exhausting the budget fails closed: observe
# dies and the PR is red, which is the cheap direction for a check that should be
# green. While main CI for a new commit is still running, its attempt records do
# not exist yet, so a flake it will reveal is not observable by anyone; a merge
# can still land ahead of it, and the ledger appoints the next merger afterward.
#
# There is no exemption for a pull request touching the ledger. Re-deriving is
# the only way past the refusal, so deleting a row cannot make the fix merge.
rederive() {
	local records="$1" ledger="$2" rc=0
	# The workflow's earlier step validates the committed file; this call validates
	# the exact ledger this derivation consumes, so two steps cannot disagree silently.
	if ! LEDGER="$ledger" "$ROOT/.github/scripts/flake-ledger.sh" check >&2; then
		die "cannot derive flake results from an invalid ledger; this derivation's ledger must pass the hermetic ledger check"
	fi
	LEDGER="$ledger" SWEEP_RECORDS="$records" "$0" check --window-days "$WINDOW_DAYS" >&2 || rc=1
	LEDGER="$ledger" SWEEP_RECORDS="$records" "$0" verify-fixed --window-days "$WINDOW_DAYS" >&2 || rc=1
	return "$rc"
}

applied() {
	[ "$SWEEP_RECORDS_WAS_SET" -eq 0 ] || die "applied refuses SWEEP_RECORDS: supplying it replaces the observation question with a different one"
	[ "$LEDGER_WAS_SET" -eq 0 ] || die "applied refuses LEDGER: supplying it replaces the checkout ledger question with a different one"
	local records rc=0
	if ! records="$(records_file)"; then
		die "could not obtain a complete CI observation; incomplete cannot be told apart from clean"
	fi
	rederive "$records" "$ROOT/.github/flake-ledger.md" || rc=1
	if [ "$rc" -eq 0 ]; then
		printf 'clean\n'
		return 0
	fi

	printf 'dirty\n'
	echo "::error::this working tree does not answer the flake ledger" >&2
	echo "  The reasons are the check and verify-fixed output above, derived against this" >&2
	echo "  checkout: a flaky test observed with no row, or a fixed: row citing a commit main" >&2
	echo "  cannot reach. Fix them here and this step goes green before the merge, not after:" >&2
	echo "    .github/scripts/flake-sweep.sh sync --owner <ISSUE_ID>" >&2
	echo "  This is not a prompt to press rerun. A row closes on a fix that landed, never because" >&2
	echo "  the test has been green since -- refusing that inference is why $LEDGER_REL exists." >&2
	exit 1
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
			# --owner names the issue that will do the fixing, so a row filed
			# with it is mergeable in the same act that files the work. Without
			# the flag the bot still cannot know which issue owns a flake, so it
			# writes TBD -- and flake-ledger.sh refuses TBD, so that diff stays
			# unmergeable until a person files the issue and re-runs with
			# --owner. Filing the flake and filing the work stay the same act.
			owner="${OWNER:-TBD}"
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
#
# The classify fixtures pin the evidence and nothing else, which was the gap
# BEO-103 measured: replacing either direction of check()'s comparison with
# `if false` left this selftest green, so the two rules the whole mechanism is
# named after had no test at all. The scenarios below drive the real check(),
# sync() and verify_fixed() over a throwaway ledger, and the mutation each one
# answers is named next to it.

# A throwaway ledger with the given table rows, for the scenarios below. The
# prose around the markers is what a real ledger has and sync() must not touch.
scratch_ledger() {
	local file="$1"
	shift
	{
		echo '# Flake ledger -- selftest scratch'
		echo '<!-- flake-ledger:begin -->'
		echo '| test | lane | first_seen | last_seen | deadline | owner_issue | state |'
		echo '| --- | --- | --- | --- | --- | --- | --- |'
		if [ $# -gt 0 ]; then printf '%s\n' "$@"; fi
		echo '<!-- flake-ledger:end -->'
	} >"$file"
}

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

	# All the scenarios below run against one record set: reopen.obs.tsv observes
	# TestReopens (integration) flaking on runs 3001 and 3003, so the observed
	# set is a single pair, first_seen aaaa1111@3001/1, last_seen cccc3333@3003/1.
	# What changes between them is the ledger they are compared against.
	local ledger msg expect
	local missing_log_msg missing_log_rc
	missing_log_msg="$(mktemp)"
	missing_log_rc=0
	(extract_failures "$FIXTURES/missing-log") >"$missing_log_msg" 2>&1 || missing_log_rc=$?
	if [ "$missing_log_rc" -ne 0 ] && grep -q "observation is incomplete" "$missing_log_msg" && grep -q "cannot distinguish no flake from no look" "$missing_log_msg"; then
		echo "  ok   missing-job-log-refused"
	else
		echo "::error::missing log scenario: extract_failures did not explain the incomplete observation" >&2
		cat "$missing_log_msg" >&2
		failed=1
	fi
	rm -f "$missing_log_msg"
	local reopen="$FIXTURES/reopen.obs.tsv"
	ledger="$(mktemp)"

	# 1. Observed and unfiled. check() must be red and name the test; sync() must
	# file it with TBD in owner_issue, which is what makes the sweep's diff unmergeable
	# until a person files the work.
	#
	# Mutation this answers: replace the `if [ -n "$missing" ]` guard in check()
	# with `if false`.
	scratch_ledger "$ledger"
	msg="$(SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check 2>&1 || true)"
	case "$msg" in
	*"no entry in"*"TestReopens (integration)"*) ;;
	*)
		echo "::error::unfiled scenario: check() did not report an observed flake missing from the ledger" >&2
		printf '%s\n' "$msg" >&2
		failed=1
		;;
	esac
	SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync >/dev/null || {
		echo "::error::unfiled scenario: sync() failed" >&2
		failed=1
	}
	if ! grep -q '| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 |.*| TBD | open |' "$ledger"; then
		echo "::error::unfiled scenario: sync did not file the observed flake with owner_issue TBD" >&2
		failed=1
	fi

	# 1b. `sync --owner <ISSUE_ID>` writes that issue into every row it files,
	# so the same act that files the flake files the work, and the row it writes
	# is one flake-ledger.sh accepts. Without the flag sync still writes TBD
	# (scenario 1), and the refusal below is unchanged: --owner only moves the
	# hand-edit earlier, it does not remove it.
	#
	# Mutation this answers: ignore OWNER in sync(), or drop it from the row it
	# writes.
	local owner_fixture="BEO-541"
	scratch_ledger "$ledger"
	SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync --owner "$owner_fixture" >/dev/null || {
		echo "::error::owner scenario: sync --owner $owner_fixture failed" >&2
		failed=1
	}
	if ! grep -q "| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 |.*| $owner_fixture | open |" "$ledger"; then
		echo "::error::owner scenario: sync --owner $owner_fixture did not write the validated owner into the filed row" >&2
		failed=1
	fi
	if grep -q '| TBD |' "$ledger"; then
		echo "::error::owner scenario: sync --owner $owner_fixture still wrote a row with owner_issue TBD" >&2
		failed=1
	fi
	if ! LEDGER="$ledger" "$ROOT/.github/scripts/flake-ledger.sh" check >/dev/null 2>&1; then
		echo "::error::owner scenario: flake-ledger.sh rejects the row sync filed with --owner $owner_fixture" >&2
		failed=1
	fi

	# 1c. A bad --owner is refused at the argument, not written into the ledger:
	# TBD re-creates the refusal flake-ledger.sh exists for, an empty value is
	# no owner at all, a value with whitespace would break the markdown row it
	# lands in, and a value that looks like a flag is a swallowed argument.
	# None of them may reach the table.
	#
	# Mutation this answers: drop the owner checks in the argument handling.
	for bad in TBD "" "two words" "--window-days" not-an-issue; do
		scratch_ledger "$ledger"
		if SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync --owner "$bad" >/dev/null 2>&1; then
			echo "::error::owner scenario: sync --owner \"$bad\" was accepted" >&2
			failed=1
		fi
	done
	# A trailing --owner with nothing after it is refused too, and named, rather
	# than left to die silently under `set -e` when shift runs out of arguments.
	scratch_ledger "$ledger"
	if SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync --owner >/dev/null 2>&1; then
		echo "::error::owner scenario: sync --owner with no value was accepted" >&2
		failed=1
	fi

	# 1d. --owner is sync's argument: no other command writes rows, so none of
	# them may take it. Refusing it here keeps a command that cannot use the
	# owner from silently ignoring the issue a person meant to file under.
	#
	# classify is the probe because it succeeds when the guard is gone: it is
	# pure text in, text out, so a removed guard makes this subprocess exit 0
	# and trips the check below, where a command that already fails for another
	# reason would not.
	#
	# Mutation this answers: drop the `[ "$cmd" = "sync" ]` check.
	if "$0" classify --owner "$owner_fixture" </dev/null >/dev/null 2>&1; then
		echo "::error::owner scenario: classify --owner $owner_fixture was accepted" >&2
		failed=1
	fi

	# 1e. --owner names the issue for rows this sweep files for the first time,
	# and a row that already exists keeps the owner it has. That rule is what
	# stops a later sweep clobbering the BEO-79 a person moved a row to, and the
	# hand edit is the only way to move it: one sweep files every new row under
	# one issue, so two flakes belonging to two issues cannot both be filed
	# right (#568). .github/flake-ledger.md tells the reader to correct
	# owner_issue by hand on the strength of this rule, so the rule is pinned
	# here rather than left to hold by accident.
	#
	# Mutation this answers: apply OWNER to every row sync writes rather than
	# only to the ones it is adding.
	local kept='| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 | 9999-12-31 | BEO-99 | open |'
	scratch_ledger "$ledger" "$kept"
	SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync --owner "$owner_fixture" >/dev/null || {
		echo "::error::owner scenario: sync --owner $owner_fixture failed over a row that already existed" >&2
		failed=1
	}
	if ! grep -qF "$kept" "$ledger"; then
		echo "::error::owner scenario: sync --owner $owner_fixture overwrote the hand-set owner of an existing row" >&2
		failed=1
	fi

	# 1f. An existing row with no newer evidence keeps a hand-edited state,
	# including a non-default `fixed:` value; this is the preservation guarantee
	# the ledger documents, distinct from the fixed-row reopen rule in scenario 3.
	#
	# Mutation this answers: force every existing row sync emits to use `open`.
	local state_kept='| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 | 9999-12-31 | BEO-99 | fixed:abc1234 |'
	scratch_ledger "$ledger" "$state_kept"
	SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync --owner "$owner_fixture" >/dev/null || {
		echo "::error::state scenario: sync --owner $owner_fixture failed over a row with no newer evidence" >&2
		failed=1
	}
	if ! grep -qF "$state_kept" "$ledger"; then
		echo "::error::state scenario: sync --owner $owner_fixture did not preserve the hand-edited state without newer evidence" >&2
		failed=1
	fi

	# 2. Filed, in scope, and no longer derivable. check() must be red and name
	# TestVanished; it must not name TestOutsideWindow, whose first_seen run this
	# record set never read -- that scoping is what stops a narrow window from
	# demanding the deletion of everything older than it. sync() drops the first
	# and keeps the second.
	#
	# Mutation this answers: replace the `if [ -n "$extra" ]` guard in check()
	# with `if false`, or drop the `part[2] in inwindow` test in
	# ledger_in_scope().
	scratch_ledger "$ledger" \
		'| TestOutsideWindow | integration | eeee5555@2999/1 | eeee5555@2999/1 | 9999-12-31 | BEO-99 | open |' \
		'| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 | 9999-12-31 | BEO-99 | open |' \
		'| TestVanished | integration | aaaa1111@3001/1 | aaaa1111@3001/1 | 9999-12-31 | BEO-99 | open |'
	msg="$(SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check 2>&1 || true)"
	case "$msg" in
	*"can no longer derive"*"TestVanished (integration)"*) ;;
	*)
		echo "::error::unfounded scenario: check() did not report an in-scope row the evidence no longer supports" >&2
		printf '%s\n' "$msg" >&2
		failed=1
		;;
	esac
	case "$msg" in
	*TestOutsideWindow*)
		echo "::error::unfounded scenario: check() demanded a row whose first_seen run is outside the window" >&2
		failed=1
		;;
	esac
	SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" sync >/dev/null || {
		echo "::error::unfounded scenario: sync() failed" >&2
		failed=1
	}
	if grep -q 'TestVanished' "$ledger"; then
		echo "::error::unfounded scenario: sync kept an in-scope row nothing supports" >&2
		failed=1
	fi
	if ! grep -q 'TestOutsideWindow' "$ledger"; then
		echo "::error::unfounded scenario: sync dropped a row from outside the window it read" >&2
		failed=1
	fi
	if ! SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check >/dev/null 2>&1; then
		echo "::error::unfounded scenario: check() is still red after sync" >&2
		failed=1
	fi

	# 3. A row observed flaky again after being declared fixed must not be
	# silent: check() has to be red on the mismatch, and sync() has to reopen the
	# row with a fresh deadline, keeping its owning issue.
	#
	# Mutation this answers: drop the `stale` block in check(), or the
	# `newer "$last_seen" "$recorded_last"` reopen branch in sync().
	scratch_ledger "$ledger" \
		'| TestReopens | integration | aaaa1111@3001/1 | aaaa1111@3001/1 | 2026-08-30 | BEO-99 | fixed:abc1234 |'
	msg="$(SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" check 2>&1 || true)"
	case "$msg" in
	*reopened*) ;;
	*)
		echo "::error::reopen scenario: check() did not flag a re-flaked fixed row for reopening" >&2
		printf '%s\n' "$msg" >&2
		failed=1
		;;
	esac
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

	# 4. Every `fixed:<sha>` has to name a commit on main. reopen.obs.tsv carries
	# one answer of each kind, and the fourth row deliberately has none: a sha
	# this sweep never asked about is unfounded too, so a lookup that stops
	# happening cannot read as clean.
	#
	# Mutation this answers: drop the `verdict != "on-main"` filter in
	# verify_fixed(), or default a missing answer to "on-main".
	scratch_ledger "$ledger" \
		'| TestFixedOnMain | integration | aaaa1111@2999/1 | aaaa1111@2999/1 | 9999-12-31 | BEO-99 | fixed:abc1234 |' \
		'| TestFixedOffMain | integration | bbbb2222@2998/1 | bbbb2222@2998/1 | 9999-12-31 | BEO-99 | fixed:f00dbab |' \
		'| TestFixedUnknown | integration | cccc3333@2997/1 | cccc3333@2997/1 | 9999-12-31 | BEO-99 | fixed:deadbee |' \
		'| TestFixedUnchecked | integration | dddd4444@2996/1 | dddd4444@2996/1 | 9999-12-31 | BEO-99 | fixed:cafe123 |'
	msg="$(SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" verify-fixed 2>&1 || true)"
	for expect in \
		"TestFixedOffMain (integration): fixed:f00dbab is a commit" \
		"TestFixedUnknown (integration): fixed:deadbee is not a commit" \
		"TestFixedUnchecked (integration): fixed:cafe123 was not checked"; do
		case "$msg" in
		*"$expect"*) ;;
		*)
			echo "::error::fixed-sha scenario: verify-fixed did not report \"$expect\"" >&2
			failed=1
			;;
		esac
	done
	case "$msg" in
	*TestFixedOnMain*)
		echo "::error::fixed-sha scenario: verify-fixed rejected a sha that is on main" >&2
		failed=1
		;;
	esac
	scratch_ledger "$ledger" \
		'| TestFixedOnMain | integration | aaaa1111@2999/1 | aaaa1111@2999/1 | 9999-12-31 | BEO-99 | fixed:abc1234 |'
	if ! SWEEP_RECORDS="$reopen" LEDGER="$ledger" "$0" verify-fixed >/dev/null 2>&1; then
		echo "::error::fixed-sha scenario: verify-fixed is red on a ledger whose only fix ref is on main" >&2
		failed=1
	fi

	# 5. The window can never be narrower than the deadline. This is the rule
	# flake-ledger.yml contradicted for the whole of v0 by passing 7, so it is
	# enforced where the constant lives and pinned here rather than left to the
	# comment that already said it. `classify` needs no network, so this costs
	# nothing and still exercises the same validation every command goes through.
	if "$0" classify --window-days "$((WINDOW_FLOOR - 1))" </dev/null >/dev/null 2>&1; then
		echo "::error::window scenario: --window-days $((WINDOW_FLOOR - 1)) was accepted below the $WINDOW_FLOOR-day floor" >&2
		failed=1
	fi
	if ! "$0" classify </dev/null >/dev/null 2>&1; then
		echo "::error::window scenario: the default window of $WINDOW_DAYS days is below its own floor of $WINDOW_FLOOR" >&2
		failed=1
	fi

	rm -f "$ledger"

	# The topology validator uses a typed YAML model rather than source matching.
	# It fails closed on shapes it cannot read, making a workflow rewrite a visible
	# false red rather than a silent green. This selftest protects its own job, so
	# deleting this scenario deletes its guard too; the remaining protection is the
	# visible edit to the invariants job and the selftest step itself.
	local topology_rc
	topology_rc=0
	go run "$ROOT/.github/scripts/flake-sweep-topology" "$WORKFLOW_FILE" || topology_rc=$?
	[ "$topology_rc" -eq 0 ] || failed=1
	for topology in valid job-shell-override job-directory-override env-only-token missing-step missing-job wrong-name wrong-job comment-only swallow if-step continue-on-error no-actions if-block function extra-command step-shell job-default-shell workflow-default-shell working-directory job-default-directory workflow-default-directory needs env-workflow env-job env-step env-extra env-workflow-bash env-job-bash env-step-bash env-workflow-env env-job-env env-step-env env-step-extra; do
		topology_rc=0
		go run "$ROOT/.github/scripts/flake-sweep-topology" "$FIXTURES/topology-$topology.yml" >/dev/null 2>&1 || topology_rc=$?
		case "$topology" in
		valid|job-shell-override|job-directory-override|env-only-token)
			if [ "$topology_rc" -ne 0 ]; then
				echo "::error::topology fixture $topology was refused" >&2
				failed=1
			fi
			;;
		*)
			if [ "$topology_rc" -eq 0 ]; then
				echo "::error::topology fixture $topology was accepted" >&2
				failed=1
			fi
			;;
		esac
	done

	# 6. `applied` re-derives the ledger against this checkout.
	# The scenarios exercise both directions and verify-fixed independently.
	local applied_stdout applied_rc

	# A ledger that answers the observation passes.
	scratch_ledger "$ledger" \
		'| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 | 9999-12-31 | BEO-99 | open |'
	applied_rc=0
	applied_stdout="$(rederive "$reopen" "$ledger" 2>/dev/null)" || applied_rc=$?
	if [ "$applied_rc" -eq 0 ]; then
		echo "  ok   applied-clean"
	else
		echo "::error::applied clean scenario failed: got $applied_stdout rc=$applied_rc" >&2
		failed=1
	fi

	# An observed flake with no row stays red.
	scratch_ledger "$ledger"
	applied_rc=0
	applied_stdout="$(rederive "$reopen" "$ledger" 2>/dev/null)" || applied_rc=$?
	if [ "$applied_rc" -eq 1 ]; then
		echo "  ok   applied-unfiled"
	else
		echo "::error::applied unfiled scenario failed: got $applied_stdout rc=$applied_rc" >&2
		failed=1
	fi

	# A fixed ref that main cannot reach also stays red.
	scratch_ledger "$ledger" \
		'| TestReopens | integration | aaaa1111@3001/1 | cccc3333@3003/1 | 9999-12-31 | BEO-99 | open |' \
		'| TestFixedOffMain | integration | bbbb2222@2998/1 | bbbb2222@2998/1 | 9999-12-31 | BEO-99 | fixed:f00dbab |'
	applied_rc=0
	applied_stdout="$(rederive "$reopen" "$ledger" 2>/dev/null)" || applied_rc=$?
	if [ "$applied_rc" -eq 1 ]; then
		echo "  ok   applied-fixed-off-main"
	else
		echo "::error::applied fixed-ref scenario failed: got $applied_stdout rc=$applied_rc" >&2
		failed=1
	fi

	# A malformed ledger is refused before parsed-entry consumers can drop its rows.
	printf '%s\n' '# malformed' >"$ledger"
	if (rederive "$reopen" "$ledger") >/dev/null 2>&1; then
		echo "::error::malformed ledger scenario: rederive accepted an invalid ledger" >&2
		failed=1
	else
		echo "  ok   applied-refuses-malformed-ledger"
	fi

	# Production applied refuses alternate observation and ledger inputs.
	applied_stdout="$(GITHUB_REPOSITORY=nonexistent/repo GH_TOKEN=invalid SWEEP_RECORDS="$reopen" "$0" applied 2>&1)" || applied_rc=$?
	case "$applied_stdout" in
	*"refuses SWEEP_RECORDS"*) echo "  ok   applied-refuses-record-override" ;;
	*) echo "::error::applied accepted SWEEP_RECORDS override" >&2; failed=1 ;;
	esac
	applied_stdout="$(GITHUB_REPOSITORY=nonexistent/repo GH_TOKEN=invalid LEDGER="$ledger" "$0" applied 2>&1)" || applied_rc=$?
	case "$applied_stdout" in
	*"refuses LEDGER"*) echo "  ok   applied-refuses-ledger-override" ;;
	*) echo "::error::applied accepted LEDGER override" >&2; failed=1 ;;
	esac

	rm -f "$ledger"

	[ "$failed" -eq 0 ] || exit 1
	echo "flake sweep selftest: all fixtures agree"
}

cmd="${1:-}"
[ $# -gt 0 ] && shift
OWNER=""
while [ $# -gt 0 ]; do
	case "$1" in
	--owner)
		# Value demanded here, where it can be named: shift 2 on a missing one
		# would exit silently under set -e, and this script's fail-closed cases
		# are supposed to say what they refuse.
		[ $# -ge 2 ] || die "--owner takes an issue id, e.g. --owner <ISSUE_ID>"
		OWNER="${2}"
		shift 2
		[ -n "$OWNER" ] || die "--owner takes an issue id, e.g. --owner <ISSUE_ID>"
		;;
	--window-days)
		WINDOW_DAYS="${2:-}"
		shift 2
		;;
	*) die "unknown flag $1" ;;
	esac
done

# Checked for every command, including the default, so there is one place a
# narrower window can be introduced and it is this file. A caller that wants a
# cheaper sweep does not get to trade the invariant for it.
case "$WINDOW_DAYS" in
'' | *[!0-9]*) die "--window-days takes a whole number of days, got \"$WINDOW_DAYS\"" ;;
esac
[ "$WINDOW_DAYS" -ge "$WINDOW_FLOOR" ] ||
	die "--window-days $WINDOW_DAYS is narrower than the $WINDOW_FLOOR days this ledger's $DEADLINE_DAYS-day deadline needs: a row whose evidence ages out before its deadline is overdue and unfounded at once, and deleting it reads green in both halves"

# --owner names the issue that will own the rows sync writes. Checked here, in
# the same place every flag is checked, so a bad value is refused before it can
# reach the table: whitespace would break the markdown row it lands in, a value
# that looks like a flag is a swallowed argument, and TBD is the refusal
# flake-ledger.sh exists for -- naming it here just moves that refusal earlier.
# --owner only applies to sync; no other command writes rows, so none of them
# may take it, and refusing it keeps a command that cannot use the owner from
# silently ignoring the issue a person meant to file under.
if [ -n "$OWNER" ]; then
	case "$OWNER" in
	*[[:space:]]*) die "--owner takes one issue id, got \"$OWNER\"" ;;
	-*) die "--owner takes an issue id, got \"$OWNER\"" ;;
	TBD) die "--owner must name a real issue, not TBD -- flake-ledger.sh refuses TBD on purpose" ;;
	esac
	if [[ ! "$OWNER" =~ ^BEO-[0-9]+$ ]]; then
		die "--owner must match BEO-<number>, got \"$OWNER\""
	fi
	[ "$cmd" = "sync" ] ||
		die "--owner only applies to sync; it names the issue that owns the rows sync writes, and $cmd writes no rows"
fi

case "$cmd" in
observe) observe ;;
classify) classify ;;
check) check ;;
sync) sync ;;
verify-fixed) verify_fixed ;;
applied) applied ;;
selftest) selftest ;;
*)
	echo "usage: flake-sweep.sh {observe|classify|check|sync [--owner <ISSUE_ID>]|verify-fixed|applied|selftest} [--window-days N]" >&2
	exit 2
	;;
esac
