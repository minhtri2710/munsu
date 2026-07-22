#!/usr/bin/env bash
# scripts/test-intent.sh — Intent-targeted local test runner for no-mistakes.
#
# Runs `go test` on packages with changed Go files since the base branch.
# Falls back to `go test ./...` when:
#   - No base ref is resolvable
#   - go.mod or go.sum changed (module metadata requires full suite)
#   - No Go source changes are detected
#   - Package resolution fails
#
# CI retains `go test ./...` (full suite) — this script is the narrower
# local gate, not a replacement for CI's full verification.
#
# Usage:
#   scripts/test-intent.sh              # test changed packages
#   scripts/test-intent.sh --full       # force full suite (debug/override)
#   scripts/test-intent.sh --print-packages  # dry-run: print selected packages only
set -euo pipefail

# Always run from repository root.
cd "$(dirname "$0")/.."

# --- Dry-run flag ---
DRY_RUN=false
for arg in "$@"; do
	if [ "$arg" = "--print-packages" ]; then
		DRY_RUN=true
	fi
	if [ "$arg" = "--full" ]; then
		echo "test-intent: --full override, running full suite" >&2
		exec go test -count=1 ./...
	fi
done

# --- Resolve base ref for comparison ---
base=""
for candidate in "origin/main" "origin/master" "main" "master"; do
	if git rev-parse --verify "$candidate" >/dev/null 2>&1; then
		base="$candidate"
		break
	fi
done

if [ -z "$base" ]; then
	echo "test-intent: no base ref found, falling back to full suite" >&2
	if $DRY_RUN; then
		echo "FULL_SUITE:no-base-ref"
		exit 0
	fi
	exec go test -count=1 ./...
fi

merge_base=$(git merge-base "$base" HEAD 2>/dev/null || echo "")
if [ -z "$merge_base" ]; then
	echo "test-intent: cannot determine merge base against $base, falling back to full suite" >&2
	if $DRY_RUN; then
		echo "FULL_SUITE:no-merge-base"
		exit 0
	fi
	exec go test -count=1 ./...
fi

# --- Check for module metadata changes (full suite required) ---
module_changed=$(git diff --name-only "$merge_base" -- 'go.mod' 'go.sum' 2>/dev/null)
if [ -n "$module_changed" ]; then
	echo "test-intent: go.mod/go.sum changed since $base, running full suite (module metadata affects all packages)" >&2
	if $DRY_RUN; then
		echo "FULL_SUITE:module-changed"
		exit 0
	fi
	exec go test -count=1 ./...
fi

# --- Collect changed Go source directories ---
changed_dirs=$(git diff --name-only "$merge_base" -- '*.go' 2>/dev/null | sed 's|/[^/]*$||' | sort -u)

# Fallback: try single-commit diff if no merge-base diff (e.g. new branch with no shared history)
if [ -z "$changed_dirs" ]; then
	changed_dirs=$(git diff --name-only "$merge_base" HEAD -- '*.go' 2>/dev/null | sed 's|/[^/]*$||' | sort -u)
fi

if [ -z "$changed_dirs" ]; then
	echo "test-intent: no changed Go files detected, falling back to full suite" >&2
	if $DRY_RUN; then
		echo "FULL_SUITE:no-changed-files"
		exit 0
	fi
	exec go test -count=1 ./...
fi

# --- Resolve Go packages from directories ---
packages=""
for d in $changed_dirs; do
	# Skip directories without .go files
	if [ ! -d "$d" ]; then
		# File may have been deleted; check if its directory still exists
		parent="$(dirname "$d")"
		[ -d "$parent" ] || continue
		d="$parent"
	fi
	# Check for Go files in the directory
	has_go=false
	for f in "$d"/*.go; do
		[ -f "$f" ] && has_go=true && break
	done
	$has_go || continue
	pkg="$(cd "$d" && go list 2>/dev/null || true)"
	[ -n "$pkg" ] && packages="$packages $pkg"
done

if [ -z "$packages" ]; then
	echo "test-intent: no Go packages resolved from changed files, falling back to full suite" >&2
	if $DRY_RUN; then
		echo "FULL_SUITE:no-packages-resolved"
		exit 0
	fi
	exec go test -count=1 ./...
fi

# --- Deduplicate (preserve order from sort) ---
unique=""
for p in $packages; do
	skip=false
	for u in $unique; do
		[ "$u" = "$p" ] && skip=true && break
	done
	$skip || unique="$unique $p"
done

echo "test-intent: targeting $unique" >&2
if $DRY_RUN; then
	echo "SELECTED:$unique"
	exit 0
fi
exec go test -count=1 $unique
