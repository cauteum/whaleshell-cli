#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 the cauteum authors
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the linux helpers every release archive ships under
# libexec/cauteum/linux-<arch>/ so installs never need Go or a checkout:
#   cauteum       — proxy sidecar / supervisor relay (linux build of the CLI)
#   cauteum-init  — guest init / harden
#   cauteum-sshd  — guest sshd on a Unix socket (IDE / connect)
#
# Usage: scripts/build-linux-helpers.sh VERSION [OUT_DIR] [ARCH...]
# Run from cauteum-cli with ../cauteum-runtime checked out (GOWORK set).
set -euo pipefail

version="${1:?usage: build-linux-helpers.sh VERSION [OUT_DIR] [ARCH...]}"
out="${2:-build/helpers}"
shift $(( $# >= 2 ? 2 : 1 ))
arches=("$@")
[[ ${#arches[@]} -gt 0 ]] || arches=(amd64 arm64)

cli_dir="$(cd "$(dirname "$0")/.." && pwd)"
runtime_dir="${CAUTEUM_RUNTIME_DIR:-${cli_dir}/../cauteum-runtime}"
[[ -f "${runtime_dir}/go.mod" ]] || { echo "build-linux-helpers: ${runtime_dir} is not cauteum-runtime" >&2; exit 1; }
mkdir -p "$out"
out="$(cd "$out" && pwd)"

ldflags="-s -w -X github.com/cautem/cauteum-cli/internal/service.BuildVersion=${version}"
for arch in "${arches[@]}"; do
  dst="${out}/linux-${arch}"
  rm -rf "$dst"
  mkdir -p "$dst"
  echo "→ linux/${arch} helpers → ${dst}"
  export GOOS=linux GOARCH="$arch" CGO_ENABLED=0
  go build -C "$cli_dir" -trimpath -ldflags="$ldflags" -o "${dst}/cauteum" ./cmd/cauteum
  go build -C "$runtime_dir" -trimpath -ldflags="-s -w" -o "${dst}/cauteum-init" ./cmd/cauteum-init
  go build -C "$runtime_dir" -trimpath -ldflags="-s -w" -o "${dst}/cauteum-sshd" ./cmd/cauteum-sshd
  chmod 0755 "${dst}"/*
done
