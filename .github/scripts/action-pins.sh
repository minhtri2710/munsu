#!/usr/bin/env bash
#
# Every `uses:` in a workflow must name its action by full commit SHA.
#
# A tag or branch ref is mutable: whoever controls the action repository can
# move `@v7` to new code, and the next run executes it with this repository's
# token. A 40-hex SHA cannot be moved. Every workflow already pins this way;
# this script makes the next unpinned `uses:` fail instead of relying on review.
#
# Rules:
#   - owner/repo[/path]@<40 lowercase hex>   allowed
#   - ./local/path                           allowed (lives in this checkout)
#   - anything else is refused: a tag, a branch, a short SHA, a missing `@`,
#     and `docker://` refs. No workflow uses docker today, so there is no
#     digest rule to get wrong; add one when a workflow needs it.
#
# Workflow files are discovered from the tree (`git ls-files`, tracked plus
# untracked-not-ignored), never listed, so a new workflow is scanned by existing.
#
# What `check` does NOT do: it is line-based, not a YAML parser. It reads
# `uses:` as a mapping key, optionally after a list `- `, with the value bare
# or quoted and an optional trailing `# comment`. A `uses:` value spread over a
# block scalar or flow mapping is not seen. Such a form would be unusual
# enough to catch in review.
#
# Usage:
#   action-pins.sh check      every workflow `uses:` is SHA-pinned or local
#   action-pins.sh selftest   every rule above against a scratch tree
set -euo pipefail

# Overridable so the selftest can drive `check` against a scratch tree.
ROOT="${ACTION_PINS_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"

die() { echo "::error::$*" >&2; exit 1; }

workflow_files() {
	git -C "$ROOT" ls-files --cached --others --exclude-standard -- \
		'.github/workflows/*.yml' '.github/workflows/*.yaml'
}

check() {
	local files f n line ref bad=0 count=0
	files="$(workflow_files)" || die "cannot list workflow files under $ROOT"
	[ -n "$files" ] || die "no workflow files under $ROOT/.github/workflows"
	while IFS= read -r f; do
		n=0
		while IFS= read -r line || [ -n "$line" ]; do
			n=$((n + 1))
			[[ "$line" =~ ^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*(.*)$ ]] || continue
			ref="${BASH_REMATCH[2]}"
			ref="$(printf '%s' "$ref" | sed -E 's/[[:space:]]+#.*$//; s/[[:space:]]+$//')"
			ref="$(printf '%s' "$ref" | sed -E "s/^\"(.*)\"$/\\1/; s/^'(.*)'$/\\1/")"
			count=$((count + 1))
			if [[ "$ref" == ./* ]] || [[ "$ref" =~ ^[^@[:space:]]+@[0-9a-f]{40}$ ]]; then
				continue
			fi
			echo "::error::$f:$n: '$ref' is not pinned to a full 40-hex commit SHA (or a ./ local action)" >&2
			bad=1
		done <"$ROOT/$f"
	done <<<"$files"
	[ "$bad" -eq 0 ] || exit 1
	echo "action pins: $count uses: pinned"
}

selftest() {
	local failed=0 scratch t name want_rc want mutation got rc
	scratch="$(mktemp -d)"
	trap 'rm -rf "$scratch"' RETURN
	local sha=3d3c42e5aac5ba805825da76410c181273ba90b1

	# name|expected exit|expected output line|mutation (shell, run inside the
	# tree). Empty mutation is the clean tree. The clean tree carries a bare
	# list-item pin, a quoted pin with a comment, a mapping-key pin and a local
	# action, so every accepted form is exercised on every case.
	while IFS='|' read -r name want_rc want mutation; do
		t="$scratch/$name"
		mkdir -p "$t/.github/workflows"
		git init -q "$t"
		printf '    steps:\n      - uses: actions/checkout@%s # v7.0.1\n      - uses: "actions/setup-go@%s"  # v7\n      - name: x\n        uses: actions/upload-artifact@%s\n      - uses: ./.github/actions/local\n' \
			"$sha" "$sha" "$sha" >"$t/.github/workflows/a.yml"
		(cd "$t" && eval "$mutation")
		if got="$(ACTION_PINS_ROOT="$t" "$0" check 2>&1)"; then rc=0; else rc=$?; fi
		if [ "$rc" = "$want_rc" ] && printf '%s\n' "$got" | grep -Fq -- "$want"; then
			echo "  ok   $name"
		else
			echo "::error::action-pins check disagrees with case $name (want exit $want_rc and '$want'):" >&2
			printf '%s\nexit %s\n' "$got" "$rc" >&2
			failed=1
		fi
	done <<'CASES'
clean|0|action pins: 4 uses: pinned|
tag|1|a.yml:7: 'actions/checkout@v7' is not pinned|printf '      - uses: actions/checkout@v7\n' >>.github/workflows/a.yml
branch|1|a.yml:7: 'actions/checkout@main' is not pinned|printf '      - uses: actions/checkout@main\n' >>.github/workflows/a.yml
short-sha|1|'actions/checkout@3d3c42e' is not pinned|printf '      - uses: actions/checkout@3d3c42e\n' >>.github/workflows/a.yml
no-at|1|'actions/checkout' is not pinned|printf '      - uses: actions/checkout\n' >>.github/workflows/a.yml
quoted-tag|1|'actions/checkout@v7' is not pinned|printf '      - uses: "actions/checkout@v7" # pinned?\n' >>.github/workflows/a.yml
single-quoted-tag|1|'actions/checkout@v7' is not pinned|printf "        uses: 'actions/checkout@v7'\n" >>.github/workflows/a.yml
upper-hex|1|is not pinned|printf '      - uses: actions/checkout@3D3C42E5AAC5BA805825DA76410C181273BA90B1\n' >>.github/workflows/a.yml
docker|1|'docker://alpine@sha256:0000' is not pinned|printf '      - uses: docker://alpine@sha256:0000\n' >>.github/workflows/a.yml
yaml-ext|1|b.yaml:1: 'actions/checkout@v7' is not pinned|printf -- '- uses: actions/checkout@v7\n' >.github/workflows/b.yaml
no-workflows|1|no workflow files|rm .github/workflows/a.yml
CASES

	[ "$failed" -eq 0 ] || exit 1
	echo "action-pins selftest: all cases agree"
}

case "${1:-}" in
check) check ;;
selftest) selftest ;;
*)
	echo "usage: action-pins.sh {check|selftest}" >&2
	exit 2
	;;
esac
