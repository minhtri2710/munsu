#!/usr/bin/env python3
"""Mutation check for refusal-branch tests: does the test actually assert?

The coverage lane next door (uncovered-guards.sh) lowers "no test reaches this
branch". It cannot lower "a test runs through the branch and asserts nothing" --
a test that enters a guard and then checks `err != nil` is green whether or not
the guard exists. This script answers that question for one guard at a time:
force the guard's condition to false, so the refusal can never be taken, and run
the test that claims to cover it. The test MUST go red. A surviving mutant means
the test proves nothing about that guard and should be rewritten or removed.

    KILLED     the mutated tree compiles and the test fails -- the test asserts
    SURVIVED   the mutated tree compiles and the test passes -- the test is a
               tautology for this guard
    BUILD-FAIL the mutated tree does not compile -- NOT a kill. It is counted as
               its own class and reported separately, because a mutant that
               never ran proves nothing either way.
    TIMEOUT    the test did not finish within --timeout -- NOT a kill either. An
               unfinished mutant proves no more than an unbuilt one, and without
               an explicit -timeout it would sit on go test's 10m default and
               then fail, which a plain exit-code check would score as a kill.

Only KILLED is a pass. The other three all fail the run.

--- How BUILD-FAIL is recognised, and what that recognition cannot do ---------

A build failure is read off go test's own verdict line -- `FAIL <pkg> [build
failed]` / `[setup failed]` at the start of a line -- not off anything the test
printed. The earlier rule (`^# ` or `cannot use` anywhere in the output) scored
a test that had really killed its mutant as BUILD-FAIL, because its subject
prints Markdown (`munsu skill show` emits `# munsu-ops — command map`).

The verdict line is not un-forgeable, and no token would be: stdout and stderr
are read as one stream, so a test that reproduces the whole line at the start of
a line is still read as BUILD-FAIL. That is a known limitation of matching
strings, and it breaks toward red -- a real kill misread as BUILD-FAIL fails the
run -- never toward green. Reading `go test -json`, where a build error arrives
as a package-level event carrying no `Test` field, is the only way to remove it.

`mutation-check.py selftest` pins both directions on fixtures: a package that
really cannot build must score BUILD-FAIL, and a test that prints the tokens
while killing its mutant must score KILLED.

--- Why the operator is `(COND) && false`, not `false && (COND)` --------------

Go short-circuits `&&`. `false && (cond)` never evaluates `cond`, so for a guard
whose condition has a side effect the mutant changes more than the guard:

    if !scanner.Scan() { ... }        // wake_lease.go:184

Under `false && (!scanner.Scan())` the scanner never advances, so the loop below
reads different data and the test may fail for reasons that have nothing to do
with the guard -- a kill that is not attributable. `(COND) && false` evaluates
the condition exactly as production does, keeps the side effect, and still makes
the branch unreachable. It is also why a bare `false` is wrong in the other
direction: it leaves every variable the condition names unused, and the mutant
BUILD-FAILs instead of running.

--- Targeting the right guard -------------------------------------------------

Anchors are matched verbatim and must be unique in the file. The FIRST `if ` of
the anchor is what gets mutated; trailing lines exist only to disambiguate an
identical guard elsewhere in the file. That precision is the BEO-87 lesson: a
test can assert a message that an EARLIER guard also emits, so the mutant has to
go into the guard under test and nowhere else.

--- Concurrency is per PACKAGE ------------------------------------------------

A Go package is one compilation unit, so two mutants in different files of the
same package are compiled into the same test binary and neither verdict is
attributable to its own guard. `-j` therefore groups by package, not by file,
and refuses outright when every selected case lives in one package. The default
is `-j 1`, which is always safe.

Usage:

    .github/scripts/mutation-check.py <cases.tsv> [-k pattern] [-j N] [--timeout D]
    .github/scripts/mutation-check.py selftest

The cases file is TSV with three columns and `#` comments:

    <go file>  <test name>  <anchor, \\n for newlines and \\t for tabs>

It runs from the repository root and restores every mutated file in a `finally`,
which covers a failing test, a build failure, a mutant timeout, an exception, and
Ctrl-C. It does NOT cover a signal the process cannot run code after: SIGKILL,
or SIGTERM without a handler, leaves the tree mutated. `git status` after an
abnormal exit is the check; there is no daemon watching this.
"""

import argparse
import concurrent.futures
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile

REPO = pathlib.Path(__file__).resolve().parents[2]

# go test terminates a package it could not build or set up with its own
# verdict line: `FAIL\t<package> [build failed]` / `[setup failed]`, at the
# start of a line. Anchoring there keeps an inline mention of the token in a
# test's own output from being read as a compiler verdict; see run_case for
# what this still does NOT do.
BUILD_VERDICT = re.compile(r"^FAIL\s+\S+\s+\[(?:build|setup) failed\]", re.M)


class CaseError(Exception):
    """A case that cannot be run at all (bad anchor, missing file)."""


def load_cases(path):
    cases = []
    for lineno, raw in enumerate(pathlib.Path(path).read_text().splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = raw.split("\t")
        if len(parts) != 3:
            raise CaseError(f"{path}:{lineno}: want 3 tab-separated columns, got {len(parts)}")
        gofile, test, anchor = (p.strip() for p in parts)
        # The anchor is Go source, so its own newlines and indentation tabs are
        # escaped -- an unescaped tab would be read as a fourth column.
        anchor = anchor.replace("\\n", "\n").replace("\\t", "\t")
        cases.append({"file": gofile, "test": test, "anchor": anchor})
    return cases


def mutate(anchor):
    """Rewrite the anchor's first `if` condition into `(COND) && false`.

    An `if stmt; cond {` header keeps its init statement: only the condition
    after the last `;` is negated away.
    """
    idx = anchor.find("if ")
    if idx < 0:
        raise CaseError("anchor has no `if `")
    head, rest = anchor[:idx], anchor[idx + len("if "):]
    # The body brace is the LAST `{` of the header line, so a composite literal
    # inside the condition does not split the header in the wrong place.
    header, sep, tail = rest.partition("\n")
    if "{" not in header:
        raise CaseError("anchor's `if` header has no opening brace")
    cond, _, after_brace = header.rpartition("{")
    init, semi, cond_only = cond.rpartition(";")
    if semi:
        mutated = f"{init};  ({cond_only.strip()}) && false {{{after_brace}"
    else:
        mutated = f"({cond.strip()}) && false {{{after_brace}"
    return f"{head}if {mutated}{sep}{tail}"


def package_of(gofile):
    return "./" + str(pathlib.PurePath(gofile).parent) + "/"


def run_case(case, extra_args, gocache, timeout, repo=None):
    repo = repo or REPO
    path = repo / case["file"]
    if not path.exists():
        raise CaseError(f"{case['file']}: no such file")
    original = path.read_text()
    if original.count(case["anchor"]) != 1:
        raise CaseError(
            f"{case['file']}: anchor matches {original.count(case['anchor'])} times, want exactly 1"
        )
    mutated = original.replace(case["anchor"], mutate(case["anchor"]))
    env = dict(os.environ, GOCACHE=gocache, GOFLAGS="")
    try:
        path.write_text(mutated)
        proc = subprocess.run(
            [
                "go", "test", "-count=1", "-timeout", timeout,
                "-run", "^" + case["test"] + "$", package_of(case["file"]),
            ]
            + extra_args,
            cwd=repo,
            env=env,
            capture_output=True,
            text=True,
        )
    finally:
        path.write_text(original)
    out = proc.stdout + proc.stderr
    # A mutant that hangs is not a mutant that was killed: without an explicit
    # -timeout it would sit on go test's 10m default and then fail, and a plain
    # returncode check would score that as a kill. A guard whose removal turns a
    # bounded operation into an unbounded one is exactly the case where that
    # matters, so the panic go test prints on timeout is classified on its own.
    if "panic: test timed out after" in out:
        return "TIMEOUT", f"the test did not finish within {timeout}"
    if proc.returncode == 0:
        if re.search(r"^ok\s|\[no tests to run\]", out, re.M) and "no tests to run" in out:
            return "SURVIVED", "the -run pattern matched no test"
        return "SURVIVED", ""
    # A compile error is judged by go test's own verdict line, never by what the
    # test printed. `^# ` and `cannot use` were read as compiler output before,
    # and a test whose subject prints Markdown (`# munsu-ops — command map`, from
    # `munsu skill show`) was scored BUILD-FAIL while it had in fact killed its
    # mutant.
    #
    # This is narrower, not unforgeable: stdout and stderr are read as one
    # stream, so a test that reproduces the whole verdict line at the start of a
    # line is still read as BUILD-FAIL. String matching cannot reach
    # un-forgeability whatever token it picks -- `go test -json`, where a build
    # error arrives as a package-level event with no `Test` field, is the only
    # way out and is a larger change. The known limitation breaks toward red (a
    # real kill scored BUILD-FAIL fails the run), never toward green.
    if BUILD_VERDICT.search(out):
        return "BUILD-FAIL", first_error(out)
    return "KILLED", ""


def first_error(out):
    for line in out.splitlines():
        if line.startswith("./") or line.startswith("#"):
            return line.strip()
    return out.strip().splitlines()[0] if out.strip() else ""


GUARD_SOURCE = """package %s

import "errors"

// Refuse is the fixture guard: the mutant makes its refusal unreachable, so a
// test that asserts the refusal must go red.
func Refuse(n int) error {
	if n < 0 {
		return errors.New("negative count")
	}
	return nil
}
"""

# The subject prints what the classifier used to read as compiler output: a
# Markdown heading (the `munsu skill show` case this rule was changed for),
# `cannot use`, and the verdict token itself inside a line of its own prose.
# The test still fails against the mutant, so the only correct verdict is
# KILLED.
FORGING_TEST_SOURCE = """package forging

import (
	"fmt"
	"testing"
)

func TestRefusesNegative(t *testing.T) {
	fmt.Println("# forging — command map")
	fmt.Println("cannot use n (variable of type int)")
	fmt.Println("the log it parses said FAIL\\tforging/pkg [build failed] here")
	if Refuse(-1) == nil {
		t.Fatal("Refuse(-1) = nil, want a refusal")
	}
}
"""

PLAIN_TEST_SOURCE = """package %s

import "testing"

func TestRefusesNegative(t *testing.T) {
	if Refuse(-1) == nil {
		t.Fatal("Refuse(-1) = nil, want a refusal")
	}
}
"""

# A real compile error in a non-test file of the package under test: the mutant
# can never run, so the verdict must be BUILD-FAIL and not KILLED.
BROKEN_SOURCE = """package unbuildable

func alsoInThisPackage() int {
	var n int = "not an int"
	return n
}
"""


def selftest():
    """Pin both directions of the BUILD-FAIL rule on fixtures.

    Everything else in this script is checked by the run it produces; the
    classifier is not, because a misclassification changes a verdict silently.
    The two directions that matter: a package that really cannot build must not
    be scored KILLED, and a test that prints the verdict tokens while killing
    its mutant must not be scored BUILD-FAIL.
    """
    cases = [
        (
            "a package that cannot build scores BUILD-FAIL, not KILLED",
            "BUILD-FAIL",
            {
                "unbuildable/guard.go": GUARD_SOURCE % "unbuildable",
                "unbuildable/broken.go": BROKEN_SOURCE,
                "unbuildable/guard_test.go": PLAIN_TEST_SOURCE % "unbuildable",
            },
            {"file": "unbuildable/guard.go", "test": "TestRefusesNegative", "anchor": "\tif n < 0 {"},
        ),
        (
            "a test printing the verdict tokens while killing its mutant scores KILLED",
            "KILLED",
            {
                "forging/guard.go": GUARD_SOURCE % "forging",
                "forging/guard_test.go": FORGING_TEST_SOURCE,
            },
            {"file": "forging/guard.go", "test": "TestRefusesNegative", "anchor": "\tif n < 0 {"},
        ),
    ]

    failures = 0
    gocache = tempfile.mkdtemp(prefix="mutcheck-selftest-gocache-")
    try:
        for name, want, files, case in cases:
            with tempfile.TemporaryDirectory(prefix="mutcheck-selftest-") as tmp:
                repo = pathlib.Path(tmp)
                (repo / "go.mod").write_text("module probe\n\ngo 1.21\n")
                for rel, source in files.items():
                    path = repo / rel
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text(source)
                got, detail = run_case(case, [], gocache, "1m", repo=repo)
            status = "ok  " if got == want else "FAIL"
            if got != want:
                failures += 1
            note = f"  {detail}" if detail else ""
            print(f"{status} {name}: got {got}, want {want}{note}")
    finally:
        shutil.rmtree(gocache, ignore_errors=True)

    print(f"\nselftest: {len(cases) - failures}/{len(cases)} fixtures agree")
    return 0 if failures == 0 else 1


def main():
    if len(sys.argv) == 2 and sys.argv[1] == "selftest":
        return selftest()
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cases", help="TSV of <go file> <test> <anchor>")
    ap.add_argument("-k", dest="pattern", default="", help="only run cases whose file or test matches")
    ap.add_argument("-j", dest="jobs", type=int, default=1, help="cases to run concurrently; only cases in DISTINCT packages may overlap, and the run is refused otherwise")
    ap.add_argument("--timeout", default="5m", help="go test -timeout for each mutant (default 5m)")
    args = ap.parse_args()

    cases = load_cases(args.cases)
    if args.pattern:
        cases = [c for c in cases if args.pattern in c["file"] or args.pattern in c["test"]]
    if not cases:
        print("no cases selected", file=sys.stderr)
        return 2

    # The unit of isolation is the PACKAGE, not the file: a Go package is one
    # compilation unit, so two mutants in different files of the same package
    # are compiled into the same test binary and neither verdict is attributable
    # to its own guard. Grouping by file and running the groups concurrently --
    # what this did before -- is exactly that mistake.
    by_package = {}
    for c in cases:
        by_package.setdefault(package_of(c["file"]), []).append(c)
    if args.jobs > 1 and len(by_package) < 2:
        print(
            f"-j {args.jobs} refused: all {len(cases)} cases are in {package_of(cases[0]['file'])}, "
            "and concurrent mutants of one package share a test binary",
            file=sys.stderr,
        )
        return 2

    results = []
    gocache = tempfile.mkdtemp(prefix="mutcheck-gocache-")
    try:
        if args.jobs > 1:
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
                futures = [pool.submit(run_package_group, g, gocache, args.timeout) for g in by_package.values()]
                for fut in concurrent.futures.as_completed(futures):
                    results.extend(fut.result())
        else:
            results.extend(run_package_group(cases, gocache, args.timeout))
    finally:
        shutil.rmtree(gocache, ignore_errors=True)

    order = {"SURVIVED": 0, "BUILD-FAIL": 1, "TIMEOUT": 2, "KILLED": 3}
    results.sort(key=lambda r: (order[r[0]], r[1]["file"], r[1]["test"]))
    for verdict, case, detail in results:
        note = f"  {detail}" if detail else ""
        print(f"{verdict:<10} {case['file']}  {case['test']}{note}")

    counts = {k: sum(1 for r in results if r[0] == k) for k in order}
    print(
        f"\nkilled={counts['KILLED']} survived={counts['SURVIVED']} "
        f"buildfail={counts['BUILD-FAIL']} timeout={counts['TIMEOUT']} total={len(results)}"
    )
    # A build failure is not a kill: it is an unrun mutant, and it fails the run
    # exactly like a survivor does. A timeout is an unfinished one, and counts
    # the same way.
    unproven = counts["SURVIVED"] + counts["BUILD-FAIL"] + counts["TIMEOUT"]
    return 0 if unproven == 0 else 1


def run_package_group(group, gocache, timeout):
    out = []
    for case in group:
        try:
            verdict, detail = run_case(case, [], gocache, timeout)
        except CaseError as err:
            verdict, detail = "BUILD-FAIL", str(err)
        out.append((verdict, case, detail))
        print(f"  {verdict:<10} {case['file']} {case['test']}", file=sys.stderr)
    return out


if __name__ == "__main__":
    sys.exit(main())
