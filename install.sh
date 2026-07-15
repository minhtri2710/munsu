#!/usr/bin/env sh
set -e

BINDIR="${HOME}/.local/bin"
mkdir -p "$BINDIR"

COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
go build -ldflags "-X github.com/minhtri2710/munsu/internal/cli.Version=0.1.0-dev+${COMMIT}" -o munsu ./cmd/munsu
ln -sf "$(pwd -P)/munsu" "${BINDIR}/munsu"

echo "munsu installed to ${BINDIR}/munsu"
