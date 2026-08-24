#!/usr/bin/env bash
#
# Citation lane: every checkable citation in the documentation set must resolve
# against the tree it cites, or be waived in .github/citations.allow.
#
# munsu's documents carry `file:line` and symbol citations as their primary
# evidence -- docs/port-mapping.md, docs/architecture.md, the ADRs, CLAUDE.md
# and docs/workflow-topology-matrix.md are all written to be checkable by
# reading the cited code -- and until this lane nothing checked that any of them
# resolved. docs/workflow-topology-matrix.md was written, reviewed, corrected
# and reviewed again, and still shipped to its second review citing
# `home.WriteMailboxEnvelope` and `home.ReadMailboxEnvelope`, two names that
# appear nowhere in the Go tree, plus a path (`internal/cli/captain_recover.go`)
# that does not exist. Two rounds of careful review caught them at a cost that
# is exactly why it does not happen every time (#573).
#
# The derivation is a separate program, .github/scripts/citations, for the same
# reason guardsites is: what counts as a citation is read out of the tree, so a
# new document joins this lane by existing and there is no register to forget to
# update. This half compares that derivation to the waiver file in BOTH
# directions, like .github/deadcode.allow: unresolved in the tree but absent
# from the file is red, and present in the file but resolving again is red too.
# The second direction is what keeps the file shrinking instead of rotting into
# a graveyard of citations nobody has read in a year.
#
# Why a real parser and not the `git grep -E '^func|^type'` this started as: on
# the document above that grep reported four unresolved identifiers of which two
# were real, and both false positives were legitimate declaration forms it
# cannot see -- `ParentHome` is a struct field and `ReportRelay` is an
# `ObligationKind` const. A rule with that false-positive rate gets waived into
# uselessness, which is the failure this lane exists to prevent. go/parser sees
# all six forms the issue named, needs no module download, and applies no build
# constraints, so a declaration in a `_windows.go` file resolves on a linux
# runner without the per-GOOS loop deadcode.sh needs.
#
# A file path citation is judged three ways, because documents write them three
# ways and the acceptance clause is every backticked file path, not every
# root-relative one:
#
#   - Root-relative (`internal/cli/task_cmd.go`): the strongest claim, held to
#     that exact path.
#   - By basename (`spawn_runner.go`, `ci.yml`): resolves if the tree holds a
#     file with that name anywhere.
#   - By package-relative tail (`cli/git_worktree_safety.go`): resolves if some
#     file's path ends at that component boundary.
#
# The last two are weaker on purpose -- they are what the citation claimed --
# and they are what closes the gap that made an earlier revision of this lane
# print `0 unaccounted` while 310 backticked filename-shaped tokens were never
# path-checked at all.
#
# What this does NOT do, so nobody trusts it further than it goes:
#
#   - It does not check line numbers. `internal/x/y.go:41` is checked as
#     `internal/x/y.go`. See the header of .github/scripts/citations/main.go.
#   - It does not check that a symbol is declared in the file the document
#     attributes it to. That was the fifth defect in #566 and this lane does not
#     catch it; the `wrong-file-attribution` fixture pins that boundary so it is
#     a stated limit rather than an assumed capability.
#   - It does not JUDGE the ten shapes below. Each is tagged `// unjudged:` at
#     the rejection that produces it, and `citations.sh selftest` derives both
#     lists and compares them in order, so a tag and its line here cannot drift
#     apart: rename either side, delete either side or reorder either side and
#     the lane goes red.
#
#     What that does NOT prove is that this list is complete. Two kinds of
#     rejection are invisible to the check by construction, and the review of
#     #573 landed both to prove it. A rejection in fileCitation or symbolName
#     with no tag: an untagged `ext == ".yml"` moved two citations out of the
#     judged set with `0 unaccounted` still printing. And a rejection in
#     another file of the tool, tagged or not, because `from_code` reads
#     `main.go` alone: a tagged one in a new `extra.go`, called from
#     fileCitation, moved 899 checked to 897 and the lane stayed green. The
#     over-claiming direction IS caught -- a header line with no tag, or a tag
#     in the wrong order, exits 1. Deriving the rejection set from Go source in
#     bash would be a source-substring gate or a second parser, and neither is
#     proportionate to what it would buy, so completeness here is a reader's
#     job: add a rejection, in main.go, tag it, list it.
#
#       empty              a span that cleans away to nothing
#       absolute           a leading `/`: not a path in this repository
#       home-relative      a leading `~`: a path in a home this lane cannot see
#       notation           `*?<>|${}`: glob, brace or placeholder, standing for
#                          a SET of paths rather than one --
#                          `internal/fleet/process_runtime_{unix,windows}.go`
#       url                anything containing `://`
#       no-extension       no dot in the basename: `get/set`, `/afk`
#       all-extension      the basename IS its extension: `.gitignore`, `.go`
#       leading-underscore `_test.go` and the naming conventions like it
#       unused-extension   an extension no file in this tree carries, so the
#                          derived rule cannot tell it from prose:
#                          `github.com/spf13/cobra`, `design.rst`. The set
#                          comes out of the same walk, so committing this
#                          repository's first .rst file starts checking .rst
#                          citations with no edit to either script.
#       unknown-qualifier  `mailbox.WriteEnvelope`: the qualifier is not a
#                          package, type or func this tree declares. Nothing
#                          syntactic separates that from `fmt.Errorf`, which is
#                          why it is declared rather than decided.
#
#     Eight of the ten are printed with status `unchecked`, `citations.sh
#     unchecked` lists them, and `check` prints their count beside the
#     classified one -- so `0 unaccounted` cannot be read as "everything was
#     looked at". The two that are not are `empty`, which has nothing to
#     disclose, and `url`, which is not a claim about this tree.
#   - It drops several classes in silence, spanning everything from the text it
#     never extracts -- a fenced code block, an unclosed backtick run -- to a
#     token no rule can even name. They are enumerated once, at referenceShaped
#     in main.go, and that list is the complete one; every site that performs a
#     drop points back at it. Read it rather than this summary. The count has
#     been restated here twice and been wrong twice, which is the argument for
#     not restating it a third time. The largest single class, worth knowing
#     before reading any summary line: every backticked English word, and with
#     it any bare capitalised word without an interior capital -- a document
#     citing `Report` or `Digester` produces no row, while `SetTargetSafety`
#     beside it produces one.
#   - The reason column is self-declared, like .github/deadcode.allow's. Nothing
#     stops someone pasting the burn-down marker onto a citation they just broke.
#     Reading the diff is what catches that; this lane catches the accident.
#
# Usage:
#   citations.sh list       unresolved citations, in waiver-file key format
#   citations.sh unchecked  citations no rule here can judge, and their documents
#   citations.sh check      tree and waiver file agree, in both directions
#   citations.sh selftest   every rule above against a fixture that breaks it
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOL="$ROOT/.github/scripts/citations"
# Overridable so the selftest can drive both halves from a fixture tree rather
# than from the real repo: these fixtures pin the rules, and they must not change
# their verdict because somebody added a document to docs/.
SCAN_ROOT="${SCAN_ROOT:-$ROOT}"
WAIVER="${WAIVER:-$ROOT/.github/citations.allow}"
FIXTURES="$ROOT/.github/testdata/citations"

die() {
	echo "::error::$*" >&2
	exit 1
}

# Every citation the tool classified, resolved or not. Fail closed on a non-zero
# status: the tool reports an unreadable tree, an empty declaration index and an
# empty extraction as errors, and an empty finding set from a scan that never
# ran would otherwise read as "every citation resolves".
scan() {
	(cd "$TOOL" && go run . "$SCAN_ROOT") ||
		die "citations could not scan $SCAN_ROOT, so it cannot judge any citation"
}

# "doc<TAB>kind<TAB>citation" for every unresolved citation, sorted.
tree_entries() {
	scan | awk -F'\t' '$1 == "unresolved" { print $2 "\t" $3 "\t" $4 }' | sort -u
}

# The disclosure half: citations no rule here can judge. These take no waiver row
# -- there is no claim to waive, and asking for a reason per row would turn the
# waiver into a dictionary of English prose -- so this listing is the only place
# they are visible, and `check` prints their count on every run.
unchecked_entries() {
	scan | awk -F'\t' '$1 == "unchecked" { print $2 "\t" $4 }' | sort -u
}

# The same key format for every waiver line. -u so a duplicate cannot reach the
# comparison below and be reported there as something it is not;
# waiver_format_errors has already failed the run for it by name.
waiver_entries() {
	# `|| true` on the grep: an empty waiver file is the correct end state of
	# this ratchet, and under `set -e` a grep that matches nothing would take
	# the whole run down with no output at all.
	{ grep -vE '^[[:space:]]*(#|$)' "$WAIVER" || true; } | cut -f1,2,3 | sort -u
}

# Everything wrong with the file as a file, before it is compared to anything. A
# malformed line matters more than it looks: parsed as nothing, it falls out of
# the comparison in *both* directions at once, so its citation ends up neither
# required nor waived -- fail-open, which is the whole thing this lane exists to
# avoid.
#
# awk rather than a `read` loop, and errors on stdout with the caller
# redirecting, for the reasons deadcode.sh gives at the same function.
waiver_format_errors() {
	awk -F '\t' -v name="${WAIVER#"$ROOT"/}" '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		NF < 3 || $1 == "" || $2 == "" || $3 == "" {
			printf "::error::%s:%d: expected doc<TAB>kind<TAB>citation<TAB>reason\n", name, NR
			bad = 1
			next
		}
		$2 != "path" && $2 != "symbol" {
			printf "::error::%s:%d: %s: unknown kind %s -- the tool emits path or symbol\n", name, NR, $1, $2
			bad = 1
		}
		NF < 4 || $4 == "" {
			printf "::error::%s:%d: %s: %s: entry needs a reason in the fourth column\n", name, NR, $1, $3
			bad = 1
		}
		{
			key = $1 "\t" $2 "\t" $3
			if (key in seen) {
				printf "::error::%s:%d: %s: %s: already listed on line %d\n", name, NR, $1, $3, seen[key]
				bad = 1
			} else {
				seen[key] = NR
			}
		}
		END { exit bad }
	' "$WAIVER"
}

# Entries whose reason marks them as a known-open defect rather than accepted
# debt, reprinted on every run so a citation known to be wrong stays visible
# until it is corrected. Same mechanism, and same argument, as deadcode.sh's:
# an annotation on every single run is the difference between a waiver and a
# silent line. (ADR-0017 §5.)
announce_open_bugs() {
	awk -F '\t' '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		$4 ~ /^OPEN-BUG/ {
			printf "::warning file=%s::%s does not resolve and is known to be wrong, not accepted debt: %s\n", $1, $3, $4
		}
	' "$WAIVER"
}

check() {
	local failed=0 all tree waiver added removed hits
	[ -f "$WAIVER" ] || die "missing ${WAIVER#"$ROOT"/}"
	command -v go >/dev/null || die "go is not on PATH -- the citation index is built by go/parser"

	waiver_format_errors >&2 || failed=1

	all="$(scan)"
	tree="$(printf '%s\n' "$all" | awk -F'\t' '$1 == "unresolved" { print $2 "\t" $3 "\t" $4 }' | sort -u)"
	waiver="$(waiver_entries)"

	# Unresolved in the tree, absent from the file: a citation that points at
	# nothing. This is the direction that catches the fabricated symbol and the
	# renamed file.
	added="$(comm -23 <(printf '%s\n' "$tree") <(printf '%s\n' "$waiver"))"
	if [ -n "$added" ]; then
		echo "::error::citations that do not resolve and are not in ${WAIVER#"$ROOT"/}:" >&2
		printf '%s\n' "$added" | while IFS=$'\t' read -r doc kind citation; do
			printf '  %s: %s %s\n' "$doc" "$kind" "$citation" >&2
			# Best-effort context, so `|| true`: the key is the cleaned
			# citation, and a citation written `internal/x/y.go:41` or
			# `(*Store).Write` does not appear in the source in that form.
			hits="$(grep -nF -- "$citation" "$SCAN_ROOT/$doc" | head -3 || true)"
			[ -z "$hits" ] || printf '%s\n' "$hits" | while IFS= read -r hit; do
				printf '    %s:%s\n' "$doc" "$hit" >&2
			done
		done
		echo "  A path citation must name a file that exists; a symbol citation must name a func," >&2
		echo "  method, type, const, var or struct field this tree declares. Correct the citation," >&2
		echo "  or add a line to that file with a reason -- see ADR-0017 4 for what a reason has to say." >&2
		failed=1
	fi

	# In the file, resolving again: the citation was corrected or the document
	# was deleted. Either way the line has to go, or the file slowly fills with
	# waivers that waive nothing.
	removed="$(comm -13 <(printf '%s\n' "$tree") <(printf '%s\n' "$waiver"))"
	if [ -n "$removed" ]; then
		echo "::error::${WAIVER#"$ROOT"/} entries that now resolve, or that no document makes any more:" >&2
		printf '%s\n' "$removed" | while IFS=$'\t' read -r doc kind citation; do
			printf '  %s: %s %s\n' "$doc" "$kind" "$citation" >&2
		done
		echo "  Each was either corrected or removed -- both are good news. Drop the line." >&2
		failed=1
	fi

	[ "$failed" -eq 0 ] || exit 1

	announce_open_bugs
	# Printed on every run so both numbers are visible in the log. The waived
	# count is a ratchet and is only allowed to fall. The classified count is the
	# other half of the same signal: it is what the lane is looking at, and a
	# lane whose subject silently shrinks -- an extractor that stopped matching,
	# a root directory renamed out from under isRepoPath -- is a green lane that
	# checks less than it did yesterday.
	local judged paths symbols unchecked
	judged="$(printf '%s\n' "$all" | awk -F'\t' '$1 == "resolved" || $1 == "unresolved"')"
	paths="$(printf '%s\n' "$judged" | awk -F'\t' '$3 == "path"' | grep -c . || true)"
	symbols="$(printf '%s\n' "$judged" | awk -F'\t' '$3 == "symbol"' | grep -c . || true)"
	unchecked="$(printf '%s\n' "$all" | awk -F'\t' '$1 == "unchecked"' | grep -c . || true)"
	echo "citations: $(printf '%s\n' "$judged" | grep -c . || true) checked ($paths path, $symbols symbol), $(printf '%s\n' "$waiver" | grep -c . || true) waived, 0 unaccounted, $unchecked unchecked"
	# The unchecked count rides on the success line on purpose. `0 unaccounted`
	# answers "does everything this lane judged resolve"; on its own a reader
	# takes it for "do the citations resolve", which is a larger claim than any
	# classifier here can make. Printing what was never judged, next to it,
	# every run, is what keeps the two questions apart.
	[ "$unchecked" -eq 0 ] ||
		echo "  $unchecked citation(s) matched no rule and were not judged; \`citations.sh unchecked\` lists them."
}

# ---------------------------------------------------------------------------
# selftest
# ---------------------------------------------------------------------------
#
# The enforcing half runs on every PR, so it is the half a PR can break, and a
# guard nothing tests stops protecting silently -- BEO-103 is the precedent: ten
# rules of flake-ledger.sh were deleted one at a time and every lane in this repo
# stayed green. For this lane the argument is sharper than usual, because the
# thing being enforced is a classifier: loosening one rule does not fail, it just
# stops seeing a shape, and a classifier nobody can see failing is the defect
# #573 is about.
#
# Each fixture is a directory that IS the tree it is scanned against -- its own
# CLAUDE.md, README.md, docs/ and .go files -- plus a `waiver` and a `want` that
# pins stdout, stderr and the exit status together, so a deleted rule changes one
# file and says which.
#
# The mutation each fixture answers, verbatim:
#
#   clean                     none; the tree every other fixture is a copy of
#   fabricated-symbol         delete the symbol comparison -- #566's
#                             home.WriteMailboxEnvelope/ReadMailboxEnvelope
#   missing-path              delete the path comparison -- #566's
#                             internal/cli/captain_recover.go
#   accepts-struct-field      drop struct fields from the index; ParentHome, one
#                             of the two false positives the grep prototype had
#   accepts-const-and-var     drop ValueSpec from the index; ReportRelay, the other
#   accepts-method-and-iface  drop FuncDecl or interface methods from the index
#   wrong-file-attribution    the stated boundary: a symbol attributed to the
#                             file that calls it is NOT a finding here (#566's
#                             fifth defect)
#   line-number-dropped       stop stripping `:41`, so a live citation with a
#                             line reference reads as a missing file
#   fenced-block              stop skipping fenced code blocks
#   foreign-qualifier         drop the "qualifier must be declared here" rule, so
#                             fmt.Errorf reads as a fabricated symbol
#   import-path               accept a slashed token with no extension at all,
#                             so golang.org/x/tools/cmd/deadcode reads as a
#                             missing file
#   extension-derived         hard-code an extension list, or accept any
#                             extension: the tree's own set is what separates
#                             design.rst from sample.munsuext
#   dotfile-token             stop disclosing a basename that is its own
#                             extension, so `.gitignore` vanishes silently
#   unknown-qualifier         stop disclosing a qualified name whose qualifier
#                             the tree does not declare, which is #566's defect
#                             class when the rename lands on the qualifier
#   testdata-not-indexed      index files under testdata/, so a citation
#                             resolves against a fixture rather than the tree
#   prose-word                drop the interior-capital rule, so `open` reads as
#                             a symbol citation
#   bare-filename             drop by-basename resolution, so `mailbox_store.go`
#                             cited by name stops being checked at all
#   bare-filename-missing     the acceptance clause: a bare filename naming no
#                             file in the tree has to fail
#   package-relative-path     drop suffix resolution, so `home/mailbox_store.go`
#                             stops resolving against internal/home/...
#   package-relative-missing  ...and the same shape naming nothing has to fail
#   root-relative-not-rescued let the weaker rules rescue a root-relative path,
#                             so `internal/fleet/mailbox_store.go` passes on the
#                             strength of a same-named file somewhere else
#   suffix-fragment           read `_test.go` as a file claim rather than as the
#                             naming convention it is
#   unchecked-disclosure      drop the `unchecked` status or the count printed
#                             beside `0 unaccounted`, so a token no rule judged
#                             disappears instead of being declared
#   notation-token            spell the disclosure gate as an allow-list of the
#                             characters a path may contain, so a brace path
#                             vanishes -- or delete codeChars, so shell and jq
#                             fragments fill the register. Both directions.
#   plans-excluded            re-include docs/plans/, whose citations are dated
#                             and whose correct state is unchanged
#   no-citations              accept an empty extraction as "everything resolves"
#   no-go-files               accept an empty declaration index
#   waived                    stop reading the waiver file, so a citation
#                             accounted for by a reason fails anyway
#   resolved-still-waived     delete the `removed` comparison
#   no-reason                 delete the fourth-column requirement
#   bad-kind                  delete the kind check
#   short-row                 delete the NF < 3 check
#   duplicate-row             delete the `key in seen` check
#   open-bug                  delete announce_open_bugs
selftest() {
	local failed=0 dir name got rc
	[ -d "$FIXTURES" ] || die "missing ${FIXTURES#"$ROOT"/}"
	for dir in "$FIXTURES"/*/; do
		dir="${dir%/}"
		name="$(basename "$dir")"
		[ -f "$dir/want" ] || die "fixture $name has no want"
		# A subprocess, not a function call: `check` exits, and its exit status
		# is half of what each fixture pins.
		if got="$(SCAN_ROOT="$dir" WAIVER="$dir/waiver" "$0" check 2>&1)"; then rc=0; else rc=$?; fi
		got="$got
exit $rc"
		# The fixture tree is scanned by absolute path, so the tool's messages
		# would carry this checkout's location. Fold it back to the fixture name.
		got="${got//$dir/<fixture>}"
		if [ "$got" = "$(cat "$dir/want")" ]; then
			echo "  ok   $name"
		else
			echo "::error::citations check disagrees with fixture $name:" >&2
			diff -u "$dir/want" <(printf '%s\n' "$got") >&2 || true
			failed=1
		fi
	done

	# A Go file the parser cannot read is a file whose declarations are missing
	# from the index, and a missing declaration reads as a fabricated citation.
	# This has to fail closed rather than scan a short index. It is not a
	# committed fixture because it cannot be one: an unparseable .go file in the
	# tree makes `gofmt -l .` exit 2, which turns the gofmt step of this same job
	# red for a reason that is not this rule. Built in a scratch tree instead.
	local scratch
	scratch="$(mktemp -d)"
	trap 'rm -rf "$scratch"' RETURN
	mkdir -p "$scratch/docs"
	printf 'A citation of `SomethingDeclared`.\n' >"$scratch/docs/doc.md"
	: >"$scratch/AGENTS.md"
	: >"$scratch/CLAUDE.md"
	: >"$scratch/README.md"
	# A parseable file declaring the cited symbol, so that skipping the broken
	# one instead of failing on it would leave the run GREEN. Without this the
	# scratch tree has no declarations at all and the empty-index rule catches
	# the mutation, which would make this check pass for the wrong reason.
	printf 'package x\n\n// SomethingDeclared is cited by the scratch document.\nfunc SomethingDeclared() {}\n' >"$scratch/good.go"
	printf 'package x\nfunc (\n' >"$scratch/broken.go"
	: >"$scratch/waiver"
	if got="$(SCAN_ROOT="$scratch" WAIVER="$scratch/waiver" "$0" check 2>&1)"; then rc=0; else rc=$?; fi
	if [ "$rc" -eq 0 ] || ! printf '%s\n' "$got" | grep -q "broken.go"; then
		echo "::error::citations scanned a tree holding an unparseable Go file instead of failing closed:" >&2
		printf '%s\n' "$got" >&2
		failed=1
	else
		echo "  ok   unparseable-go"
	fi

	# Keeps the "does not judge" list in this file's header from drifting away
	# from the tags in the tool. It compares tags to header lines, in order --
	# and that is all it does. It does NOT prove the tags cover every rejection
	# the tool makes: an untagged rejection is invisible to it, and the review
	# of #573 landed one to prove the point. Deriving rejections from Go source
	# in bash would be a source-substring gate or a second parser; the header
	# says so rather than letting this look like more than it is.
	local from_code from_header
	from_code="$(sed -n 's|.*// unjudged: ||p' "$TOOL/main.go" | tr ',' '\n' | sed 's/^ *//;s/ *$//')"
	from_header="$(sed -n 's|^#       \([a-z][a-z-]*\) .*|\1|p' "${BASH_SOURCE[0]}")"
	# Two sed patterns over comment text, either of which stops matching when
	# somebody reformats what it reads -- and two empty lists compare equal, so
	# without this the check would pass by resolving nothing, which is the
	# failure #552 fixed one lane over.
	if [ -z "$from_code" ] || [ -z "$from_header" ]; then
		echo "::error::the unjudged enumeration derived nothing and so proves nothing:" >&2
		echo "  tags found in ${TOOL#"$ROOT"/}/main.go: $(printf '%s' "$from_code" | grep -c . || true)" >&2
		echo "  lines found in the header of ${BASH_SOURCE[0]#"$ROOT"/}: $(printf '%s' "$from_header" | grep -c . || true)" >&2
		echo "  Both are read out of comment text. Whichever is zero, its pattern no longer matches." >&2
		failed=1
	elif [ "$from_code" = "$from_header" ]; then
		echo "  ok   unjudged-enumeration"
	else
		echo "::error::the header's list of shapes this lane does not judge is not the list the tool tags:" >&2
		diff -u <(printf '%s\n' "$from_header") <(printf '%s\n' "$from_code") >&2 || true
		echo "  Every rejection in fileCitation and symbolName carries an '// unjudged:' tag." >&2
		echo "  Add one, add its line to the header of this script in the same order." >&2
		failed=1
	fi

	[ "$failed" -eq 0 ] || exit 1
	echo "citations selftest: all fixtures agree"
}

case "${1:-}" in
list) tree_entries ;;
unchecked) unchecked_entries ;;
check) check ;;
selftest) selftest ;;
*)
	echo "usage: citations.sh {list|unchecked|check|selftest}" >&2
	exit 2
	;;
esac
