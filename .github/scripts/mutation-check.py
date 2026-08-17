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

Usage:

    .github/scripts/mutation-check.py <cases.tsv> [-k pattern] [-j N]

The cases file is TSV with three columns and `#` comments:

    <go file>  <test name>  <anchor, \\n for newlines and \\t for tabs>

It runs from the repository root and restores every mutated file, including on
interrupt.
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


def run_case(case, extra_args, gocache):
    path = REPO / case["file"]
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
            ["go", "test", "-count=1", "-run", "^" + case["test"] + "$", package_of(case["file"])]
            + extra_args,
            cwd=REPO,
            env=env,
            capture_output=True,
            text=True,
        )
    finally:
        path.write_text(original)
    out = proc.stdout + proc.stderr
    if proc.returncode == 0:
        if re.search(r"^ok\s|\[no tests to run\]", out, re.M) and "no tests to run" in out:
            return "SURVIVED", "the -run pattern matched no test"
        return "SURVIVED", ""
    if "[build failed]" in out or "cannot use" in out or re.search(r"^# ", out, re.M):
        return "BUILD-FAIL", first_error(out)
    return "KILLED", ""


def first_error(out):
    for line in out.splitlines():
        if line.startswith("./") or line.startswith("#"):
            return line.strip()
    return out.strip().splitlines()[0] if out.strip() else ""


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cases", help="TSV of <go file> <test> <anchor>")
    ap.add_argument("-k", dest="pattern", default="", help="only run cases whose file or test matches")
    ap.add_argument("-j", dest="jobs", type=int, default=1, help="parallel cases (each mutates a file, so >1 is only safe across distinct files)")
    args = ap.parse_args()

    cases = load_cases(args.cases)
    if args.pattern:
        cases = [c for c in cases if args.pattern in c["file"] or args.pattern in c["test"]]
    if not cases:
        print("no cases selected", file=sys.stderr)
        return 2

    results = []
    gocache = tempfile.mkdtemp(prefix="mutcheck-gocache-")
    try:
        if args.jobs > 1:
            by_file = {}
            for c in cases:
                by_file.setdefault(c["file"], []).append(c)
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
                futures = {pool.submit(run_file_group, group, gocache): group for group in by_file.values()}
                for fut in concurrent.futures.as_completed(futures):
                    results.extend(fut.result())
        else:
            results.extend(run_file_group(cases, gocache))
    finally:
        shutil.rmtree(gocache, ignore_errors=True)

    order = {"SURVIVED": 0, "BUILD-FAIL": 1, "KILLED": 2}
    results.sort(key=lambda r: (order[r[0]], r[1]["file"], r[1]["test"]))
    for verdict, case, detail in results:
        note = f"  {detail}" if detail else ""
        print(f"{verdict:<10} {case['file']}  {case['test']}{note}")

    counts = {k: sum(1 for r in results if r[0] == k) for k in order}
    print(
        f"\nkilled={counts['KILLED']} survived={counts['SURVIVED']} "
        f"buildfail={counts['BUILD-FAIL']} total={len(results)}"
    )
    # A build failure is not a kill: it is an unrun mutant, and it fails the run
    # exactly like a survivor does.
    return 0 if counts["SURVIVED"] == 0 and counts["BUILD-FAIL"] == 0 else 1


def run_file_group(group, gocache):
    out = []
    for case in group:
        try:
            verdict, detail = run_case(case, [], gocache)
        except CaseError as err:
            verdict, detail = "BUILD-FAIL", str(err)
        out.append((verdict, case, detail))
        print(f"  {verdict:<10} {case['file']} {case['test']}", file=sys.stderr)
    return out


if __name__ == "__main__":
    sys.exit(main())
