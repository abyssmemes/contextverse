#!/usr/bin/env bash
# Compute next SemVer tag for contextverse releases.
#
# Usage:
#   next-version.sh minor|major|patch|initial [latest_tag]
#
# If latest_tag is omitted, reads the highest vMAJOR.MINOR.PATCH from git tags.
# Prints: TAG=vX.Y.Z  VER=X.Y.Z  (one line each; also echoes TAG on stdout alone as last convenience via VER exports)
#
# Environment:
#   NEXT_VERSION_LATEST — override latest tag (for tests)
set -euo pipefail

MODE="${1:-}"
OVERRIDE_LATEST="${2:-${NEXT_VERSION_LATEST:-}}"

usage() {
  echo "usage: $0 minor|major|patch|initial [latest_tag]" >&2
  exit 2
}

[[ -n "$MODE" ]] || usage

latest_tag() {
  if [[ -n "$OVERRIDE_LATEST" ]]; then
    echo "$OVERRIDE_LATEST"
    return
  fi
  local t
  # Prefer version-sorted SemVer tags; ignore non-matching.
  while read -r t; do
    if [[ "$t" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
      echo "$t"
      return 0
    fi
  done < <(git tag -l 'v*.*.*' --sort=-v:refname 2>/dev/null || true)
}

parse() {
  local t="$1"
  if [[ ! "$t" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    echo "invalid SemVer tag: $t (want vMAJOR.MINOR.PATCH)" >&2
    exit 1
  fi
  MAJOR="${BASH_REMATCH[1]}"
  MINOR="${BASH_REMATCH[2]}"
  PATCH="${BASH_REMATCH[3]}"
}

LATEST="$(latest_tag || true)"

case "$MODE" in
  initial)
    if [[ -n "$LATEST" ]]; then
      echo "initial requested but tags already exist (latest=$LATEST)" >&2
      exit 1
    fi
    MAJOR=0; MINOR=1; PATCH=0
    ;;
  minor)
    if [[ -z "$LATEST" ]]; then
      MAJOR=0; MINOR=1; PATCH=0
    else
      parse "$LATEST"
      MINOR=$((MINOR + 1))
      PATCH=0
    fi
    ;;
  major)
    if [[ -z "$LATEST" ]]; then
      MAJOR=1; MINOR=0; PATCH=0
    else
      parse "$LATEST"
      MAJOR=$((MAJOR + 1))
      MINOR=0
      PATCH=0
    fi
    ;;
  patch)
    if [[ -z "$LATEST" ]]; then
      echo "patch bump requires an existing tag (or pass base as \$2 / NEXT_VERSION_LATEST)" >&2
      exit 1
    fi
    parse "$LATEST"
    PATCH=$((PATCH + 1))
    ;;
  *)
    usage
    ;;
esac

VER="${MAJOR}.${MINOR}.${PATCH}"
TAG="v${VER}"
echo "TAG=${TAG}"
echo "VER=${VER}"
