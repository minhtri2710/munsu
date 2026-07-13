#!/usr/bin/env sh
set -e

BINDIR="${HOME}/.local/bin"
mkdir -p "$BINDIR"

go build -o munsu ./cmd/munsu
ln -sf "$(pwd -P)/munsu" "${BINDIR}/munsu"

echo "munsu installed to ${BINDIR}/munsu"
