#!/usr/bin/env bash
# ==============================================================================
# g (gvm) — Cross-Platform Golang Version Manager One-Line Installer
# Repository: https://github.com/ducconit/gvm
# Author:     DNT (@ducconit)
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ducconit/gvm/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/ducconit/gvm/main/install.sh | GVM_VERSION=v1.0.0 bash
# ==============================================================================

set -euo pipefail

REPO="ducconit/gvm"
BIN_NAME="g"
GVM_HOME="${GVM_HOME:-$HOME/.gvm}"
INSTALL_DIR="${GVM_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${GVM_VERSION:-latest}"

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m!\033[0m %s\n" "$*"; }
error() { printf "\033[1;31m✗ Error:\033[0m %s\n" "$*" >&2; exit 1; }

# 1. Detect OS (darwin / linux)
OS_RAW="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS_RAW" in
  linux*)  GOOS="linux" ;;
  darwin*) GOOS="darwin" ;;
  msys*|mingw*|cygwin*) GOOS="windows" ;;
  *) error "Unsupported operating system: $OS_RAW" ;;
esac

# 2. Detect Architecture (amd64 / arm64)
ARCH_RAW="$(uname -m | tr '[:upper:]' '[:lower:]')"
case "$ARCH_RAW" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) error "Unsupported architecture: $ARCH_RAW" ;;
esac

ASSET_NAME="${BIN_NAME}-${GOOS}-${GOARCH}"
if [ "$GOOS" = "windows" ]; then
  ASSET_NAME="${ASSET_NAME}.exe"
fi

if [ "$VERSION" = "latest" ]; then
  DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET_NAME}"
else
  TAG="v${VERSION#v}"
  DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET_NAME}"
fi

info "Detected platform: ${GOOS}/${GOARCH}"
info "Installing '${BIN_NAME}' (${VERSION}) to ${INSTALL_DIR}/${BIN_NAME}..."

mkdir -p "$INSTALL_DIR" "$GVM_HOME/versions" "$GVM_HOME/tmp"

TMP_BIN="$(mktemp "${TMPDIR:-/tmp}/gvm-install.XXXXXX")"
trap 'rm -f "$TMP_BIN"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$DOWNLOAD_URL" -o "$TMP_BIN" || error "Failed to download ${DOWNLOAD_URL}"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TMP_BIN" "$DOWNLOAD_URL" || error "Failed to download ${DOWNLOAD_URL}"
else
  error "Neither 'curl' nor 'wget' was found. Please install curl or wget."
fi

chmod +x "$TMP_BIN"
mv "$TMP_BIN" "${INSTALL_DIR}/${BIN_NAME}"

ok "Installed '${BIN_NAME}' binary to ${INSTALL_DIR}/${BIN_NAME}"

# 3. Configure Shell Profile (Bash / Zsh / Fish)
EXPORT_BLOCK='
# >>> gvm (g) initialize >>>
export GVM_HOME="$HOME/.gvm"
export GOROOT="$GVM_HOME/current"
export GOPATH="${GOPATH:-$HOME/go}"
export PATH="$HOME/.local/bin:$GOROOT/bin:$GOPATH/bin:$PATH"
# <<< gvm (g) initialize <<<'

append_profile() {
  local rc_file="$1"
  if [ -f "$rc_file" ]; then
    if ! grep -q "gvm (g) initialize" "$rc_file" 2>/dev/null; then
      printf "%s\n" "$EXPORT_BLOCK" >> "$rc_file"
      ok "Updated shell profile: ${rc_file}"
    fi
  fi
}

append_profile "$HOME/.bashrc"
append_profile "$HOME/.zshrc"
if [ ! -f "$HOME/.bashrc" ] && [ ! -f "$HOME/.zshrc" ]; then
  touch "$HOME/.profile"
  append_profile "$HOME/.profile"
fi

printf "\n"
ok "g (gvm) installation complete!"
printf "   Run the following command or restart your terminal:\n\n"
printf "     \033[1;36mexport PATH=\"%s:%s/current/bin:\$HOME/go/bin:\$PATH\"\033[0m\n" "$INSTALL_DIR" "$GVM_HOME"
printf "     \033[1;36mg -v\033[0m\n"
printf "     \033[1;36mg i latest\033[0m\n\n"
