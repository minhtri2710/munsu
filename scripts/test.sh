#!/usr/bin/env bash
# scripts/test.sh — phased test runner for munsu.
#
# Usage:
#   scripts/test.sh                # run every phase (lint, build, unit, integration, coverage)
#   scripts/test.sh --all          # same as above
#   scripts/test.sh <phase>        # run a single phase:
#                                  #   lint | build | unit | integration | coverage
#
# Exits non-zero on the first failing phase. Integration is skipped (with a
# message) when tmux is not on PATH; every other phase is always run.
set -euo pipefail

# Run from the repo root regardless of where the script is invoked.
cd "$(dirname "$0")/.."

phase="${1:---all}"

run_lint()    { go vet ./...; }
run_build()   { go build ./...; }
run_unit()    { go test -race -count=1 ./...; }
run_integration() {
	if ! command -v tmux >/dev/null 2>&1; then
		echo "skip: tmux not found"
		return 0
	fi
	go test -tags=integration -count=1 ./...
}
run_coverage() {
	go test -coverprofile=cover.out -covermode=atomic ./...
	go tool cover -func=cover.out | tail -1
}

case "$phase" in
	lint)        run_lint ;;
	build)       run_build ;;
	unit)        run_unit ;;
	integration) run_integration ;;
	coverage)    run_coverage ;;
	--all)
		run_lint
		run_build
		run_unit
		run_integration
		run_coverage
		;;
	*)
		echo "unknown phase: $phase" >&2
		echo "usage: $0 [lint|build|unit|integration|coverage|--all]" >&2
		exit 2
		;;
esac
