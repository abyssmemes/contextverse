#!/usr/bin/env bash
# Bump Homebrew tap + Scoop bucket for a contextverse GitHub release tag.
#
# Usage:
#   PACKAGING_TOKEN=ghp_… ./scripts/ci/publish-packages.sh v0.2.0
#
# Env:
#   PACKAGING_TOKEN  (required) — PAT with contents:write on homebrew-tap + scoop-bucket
#   HOMEBREW_TAP_REPO — default abyssmemes/homebrew-tap
#   SCOOP_BUCKET_REPO — default abyssmemes/scoop-bucket
#   CONTEXTVERSE_REPO — passed through to bump scripts (default abyssmemes/contextverse)
set -euo pipefail

TAG="${1:-}"
[[ -n "$TAG" ]] || { echo "usage: $0 <tag>  (e.g. v0.2.0)" >&2; exit 2; }
[[ -n "${PACKAGING_TOKEN:-}" ]] || { echo "PACKAGING_TOKEN is required" >&2; exit 1; }

HOMEBREW_TAP_REPO="${HOMEBREW_TAP_REPO:-abyssmemes/homebrew-tap}"
SCOOP_BUCKET_REPO="${SCOOP_BUCKET_REPO:-abyssmemes/scoop-bucket}"
CONTEXTVERSE_REPO="${CONTEXTVERSE_REPO:-abyssmemes/contextverse}"
export CONTEXTVERSE_REPO

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

export GH_TOKEN="$PACKAGING_TOKEN"
export GITHUB_TOKEN="$PACKAGING_TOKEN"

clone_bump_push() {
  local repo="$1" script_rel="$2"
  local name
  name="$(basename "$repo")"
  local dir="$TMP/$name"
  echo "==> Cloning $repo"
  git clone --depth 1 "https://x-access-token:${PACKAGING_TOKEN}@github.com/${repo}.git" "$dir"
  (
    cd "$dir"
    git config user.name "github-actions[bot]"
    git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
    bash "$script_rel" "$TAG"
    if git diff --quiet; then
      echo "==> No changes in $repo (already at $TAG?)"
      return 0
    fi
    git add -A
    git commit -m "chore: bump contextd to ${TAG}"
    git push origin HEAD
    echo "==> Pushed $repo"
  )
}

clone_bump_push "$HOMEBREW_TAP_REPO" "./scripts/bump-formula.sh"
clone_bump_push "$SCOOP_BUCKET_REPO" "./scripts/bump-manifest.sh"
echo "==> Package managers updated for ${TAG}"
