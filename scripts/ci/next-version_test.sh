#!/usr/bin/env bash
# Self-test for next-version.sh (no git repo required).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
NV="$ROOT/next-version.sh"

run() {
  local mode="$1" latest="$2"
  NEXT_VERSION_LATEST="$latest" bash "$NV" "$mode" | paste -sd' ' -
}

expect() {
  local got="$1" want="$2" label="$3"
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $label: got [$got] want [$want]" >&2
    exit 1
  fi
  echo "ok $label"
}

expect "$(run minor '')" "TAG=v0.1.0 VER=0.1.0" "minor from empty"
expect "$(run minor 'v0.1.0')" "TAG=v0.2.0 VER=0.2.0" "minor bump"
expect "$(run major 'v0.9.0')" "TAG=v1.0.0 VER=1.0.0" "major bump"
expect "$(run patch 'v0.1.0')" "TAG=v0.1.1 VER=0.1.1" "patch bump"
expect "$(run major '')" "TAG=v1.0.0 VER=1.0.0" "major from empty"

if NEXT_VERSION_LATEST=v0.1.0 bash "$NV" initial >/dev/null 2>&1; then
  echo "FAIL initial should error when tag exists" >&2
  exit 1
fi
echo "ok initial rejects existing"

echo "all next-version tests passed"
