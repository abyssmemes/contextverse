#!/usr/bin/env bash
# Tag HEAD, create release/X.Y.Z pin branch, push both.
# Usage: create-release-refs.sh v0.2.0
set -euo pipefail

TAG="${1:-}"
[[ -n "$TAG" ]] || { echo "usage: $0 <tag>" >&2; exit 2; }
[[ "$TAG" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]] || { echo "tag must be vX.Y.Z" >&2; exit 1; }
VER="${BASH_REMATCH[1]}"
BRANCH="release/${VER}"

if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "tag $TAG already exists" >&2
  exit 1
fi

git tag -a "$TAG" -m "Release $TAG"
git push origin "refs/tags/${TAG}"

if git rev-parse --verify "refs/heads/${BRANCH}" >/dev/null 2>&1 || \
   git ls-remote --exit-code --heads origin "$BRANCH" >/dev/null 2>&1; then
  echo "branch $BRANCH already exists — leaving tag in place; not recreating branch" >&2
  exit 1
fi

git branch "$BRANCH" "$TAG"
git push origin "refs/heads/${BRANCH}"
echo "TAG=${TAG}"
echo "BRANCH=${BRANCH}"
