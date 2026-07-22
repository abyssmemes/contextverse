#!/usr/bin/env bash
# ContextVerse installer — installs the contextd CLI.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/abyssmemes/contextverse/main/scripts/install.sh | bash
#   CONTEXTD_VERSION=v0.1.0 bash scripts/install.sh
#   bash scripts/install.sh --version v0.1.0 --dir ~/.local/bin
#
# Env:
#   CONTEXTD_VERSION   tag or "latest" (default: latest)
#   CONTEXTD_INSTALL_DIR  install directory override
#   GITHUB_TOKEN / GH_TOKEN  required while the repo is private (release download + API)
#   CONTEXTD_REPO      owner/name (default: abyssmemes/contextverse)
#   CONTEXTD_NO_MODIFY_PATH  if set, skip PATH hints

set -euo pipefail

REPO="${CONTEXTD_REPO:-abyssmemes/contextverse}"
BINARY="contextd"
VERSION="${CONTEXTD_VERSION:-latest}"
INSTALL_DIR="${CONTEXTD_INSTALL_DIR:-}"
VERIFY_CHECKSUM="${CONTEXTD_VERIFY_CHECKSUM:-1}"

log()  { printf '%s\n' "$*" >&2; }
info() { log "==> $*"; }
warn() { log "warning: $*"; }
die()  { log "error: $*"; exit 1; }

usage() {
  cat <<'EOF'
ContextVerse installer — installs contextd

Options:
  --version <tag>   Release tag (default: latest). Also: CONTEXTD_VERSION
  --dir <path>      Install directory. Also: CONTEXTD_INSTALL_DIR
  --help            Show this help

Examples:
  curl -fsSL .../install.sh | bash
  CONTEXTD_VERSION=v0.0.1 bash install.sh
  bash install.sh --version v0.0.1 --dir "$HOME/.local/bin"
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --dir) INSTALL_DIR="${2:-}"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

github_token() {
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    printf '%s' "$GITHUB_TOKEN"
    return
  fi
  if [[ -n "${GH_TOKEN:-}" ]]; then
    printf '%s' "$GH_TOKEN"
    return
  fi
  if command -v gh >/dev/null 2>&1; then
    gh auth token 2>/dev/null || true
  fi
}

detect_os() {
  local u
  u="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$u" in
    linux*)  echo linux ;;
    darwin*) echo darwin ;;
    msys*|cygwin*|mingw*) echo windows ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
}

detect_arch() {
  local m
  m="$(uname -m)"
  case "$m" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    armv7l) echo armv7 ;;
    *) die "unsupported architecture: $m" ;;
  esac
}

resolve_install_dir() {
  if [[ -n "$INSTALL_DIR" ]]; then
    printf '%s' "$INSTALL_DIR"
    return
  fi
  if [[ -w /usr/local/bin ]] || [[ "$(id -u)" -eq 0 ]]; then
    printf '%s' "/usr/local/bin"
    return
  fi
  printf '%s' "${HOME}/.local/bin"
}

http_get() {
  local url="$1" out="$2"
  local token
  token="$(github_token)"
  if command -v curl >/dev/null 2>&1; then
    if [[ -n "$token" ]]; then
      curl -fsSL -H "Authorization: Bearer ${token}" -H "Accept: application/octet-stream" -o "$out" "$url" \
        || curl -fsSL -H "Authorization: Bearer ${token}" -o "$out" "$url"
    else
      curl -fsSL -o "$out" "$url"
    fi
  elif command -v wget >/dev/null 2>&1; then
    if [[ -n "$token" ]]; then
      wget -q --header="Authorization: Bearer ${token}" -O "$out" "$url"
    else
      wget -q -O "$out" "$url"
    fi
  else
    die "need curl or wget"
  fi
}

api_get() {
  local url="$1"
  local token
  token="$(github_token)"
  if [[ -n "$token" ]]; then
    curl -fsSL -H "Authorization: Bearer ${token}" -H "Accept: application/vnd.github+json" "$url"
  else
    curl -fsSL -H "Accept: application/vnd.github+json" "$url"
  fi
}

resolve_tag() {
  local ver="$1"
  if [[ "$ver" != "latest" ]]; then
    printf '%s' "$ver"
    return
  fi
  local json tag
  json="$(api_get "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)"
  if [[ -z "$json" ]]; then
    return 1
  fi
  tag="$(printf '%s' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  if [[ -z "$tag" ]]; then
    return 1
  fi
  printf '%s' "$tag"
}

asset_name() {
  local os="$1" arch="$2" tag="$3"
  # goreleaser name template: contextd_<version>_<os>_<arch>.tar.gz
  local ver="${tag#v}"
  printf '%s_%s_%s_%s.tar.gz' "$BINARY" "$ver" "$os" "$arch"
}

download_release() {
  local os="$1" arch="$2" tag="$3" dest_dir="$4"
  local name url tmp archive
  name="$(asset_name "$os" "$arch" "$tag")"
  url="https://github.com/${REPO}/releases/download/${tag}/${name}"
  tmp="$(mktemp -d)"
  archive="${tmp}/${name}"
  info "Downloading ${name} (${tag})"
  if ! http_get "$url" "$archive"; then
    rm -rf "$tmp"
    return 1
  fi

  if [[ "$VERIFY_CHECKSUM" == "1" ]]; then
    local sums="${tmp}/checksums.txt"
    if http_get "https://github.com/${REPO}/releases/download/${tag}/checksums.txt" "$sums" 2>/dev/null; then
      info "Verifying checksum"
      (
        cd "$tmp"
        if command -v sha256sum >/dev/null 2>&1; then
          grep " ${name}\$" checksums.txt | sha256sum -c -
        elif command -v shasum >/dev/null 2>&1; then
          grep " ${name}\$" checksums.txt | shasum -a 256 -c -
        else
          warn "no sha256sum/shasum; skipping checksum verification"
        fi
      )
    else
      warn "checksums.txt not found for ${tag}; skipping verification"
    fi
  fi

  tar -xzf "$archive" -C "$tmp"
  local bin
  bin="$(find "$tmp" -type f -name "$BINARY" | head -n1)"
  if [[ -z "$bin" ]]; then
    # windows naming inside archive unlikely on unix path
    bin="$(find "$tmp" -type f -name "${BINARY}.exe" | head -n1)"
  fi
  [[ -n "$bin" ]] || { rm -rf "$tmp"; return 1; }
  install -m 755 "$bin" "${dest_dir}/${BINARY}"
  rm -rf "$tmp"
  return 0
}

install_via_go() {
  local dest_dir="$1"
  need_cmd go
  info "No usable release binary — building with go install"
  export GOPRIVATE="${GOPRIVATE:-github.com/abyssmemes/*}"
  local ver_spec="@latest"
  if [[ "$VERSION" != "latest" ]]; then
    ver_spec="@${VERSION}"
  fi
  local gobin
  gobin="$(go env GOBIN)"
  if [[ -z "$gobin" ]]; then
    gobin="$(go env GOPATH)/bin"
  fi
  # shellcheck disable=SC2086
  go install "github.com/${REPO}/cmd/contextd${ver_spec}"
  local src="${gobin}/${BINARY}"
  [[ -x "$src" ]] || die "go install succeeded but binary not found at ${src}"
  if [[ "$(cd "$dest_dir" && pwd)" != "$(cd "$(dirname "$src")" && pwd)" ]]; then
    install -m 755 "$src" "${dest_dir}/${BINARY}"
  fi
}

ensure_path_note() {
  local dest_dir="$1"
  case ":$PATH:" in
    *":${dest_dir}:"*) return 0 ;;
  esac
  if [[ -n "${CONTEXTD_NO_MODIFY_PATH:-}" ]]; then
    warn "${dest_dir} is not on PATH"
    return 0
  fi
  log ""
  log "Add to PATH (add to your shell rc):"
  log "  export PATH=\"${dest_dir}:\$PATH\""
  if [[ -f "${HOME}/.zshrc" ]]; then
    log "  # e.g. echo 'export PATH=\"${dest_dir}:\$PATH\"' >> ~/.zshrc"
  fi
}

detect_ai_tools() {
  local found=()
  command -v claude >/dev/null 2>&1 && found+=("Claude Code")
  command -v cursor >/dev/null 2>&1 && found+=("Cursor")
  command -v code >/dev/null 2>&1 && found+=("VS Code/Copilot?")
  if [[ ${#found[@]} -eq 0 ]]; then
    log "  AI tools: (none detected on PATH)"
  else
    local IFS=', '
    log "  AI tools: ${found[*]}"
  fi
}

main() {
  need_cmd uname
  need_cmd tar
  need_cmd install
  need_cmd mktemp
  need_cmd find
  need_cmd curl

  local os arch dest tag
  os="$(detect_os)"
  arch="$(detect_arch)"
  dest="$(resolve_install_dir)"
  mkdir -p "$dest"

  log "ContextVerse Installer"
  log "━━━━━━━━━━━━━━━━━━━━"
  log "  OS/arch:  ${os}/${arch}"
  log "  Repo:     ${REPO}"
  log "  Version:  ${VERSION}"
  log "  Install:  ${dest}"
  detect_ai_tools
  if [[ "$os" == "darwin" ]] && command -v brew >/dev/null 2>&1; then
    log ""
    log "  Tip: on macOS you can also use Homebrew:"
    log "    brew tap abyssmemes/tap && brew install abyssmemes/tap/contextd"
  fi
  log ""

  tag="$(resolve_tag "$VERSION" || true)"
  local installed=0
  if [[ -n "${tag:-}" ]]; then
    info "Resolved release tag: ${tag}"
    if download_release "$os" "$arch" "$tag" "$dest"; then
      installed=1
    else
      warn "release asset download failed for ${tag}"
    fi
  else
    warn "could not resolve a GitHub release (repo private with no token, or no releases yet)"
  fi

  if [[ "$installed" -ne 1 ]]; then
    install_via_go "$dest"
  fi

  local bin_path="${dest}/${BINARY}"
  [[ -x "$bin_path" ]] || die "install finished but ${bin_path} is not executable"

  info "Installed ${BINARY} → ${bin_path}"
  if command -v "$BINARY" >/dev/null 2>&1 || [[ -x "$bin_path" ]]; then
    "$bin_path" version || true
  fi

  ensure_path_note "$dest"

  log ""
  log "✅ Done. Next:"
  log "  contextd init solo"
  log "  cd <project> && contextd activate"
}

main "$@"
