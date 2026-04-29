#!/usr/bin/env bash
set -euo pipefail

REPO="omattsson/stackctl"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')"
URL="https://github.com/$REPO/releases/download/v${VERSION}/stackctl_${VERSION}_${OS}_${ARCH}.tar.gz"

echo "Installing stackctl v${VERSION} (${OS}/${ARCH})..."
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$URL" -o "$TMP/stackctl.tar.gz"
tar -xzf "$TMP/stackctl.tar.gz" -C "$TMP"
install -m 755 "$TMP/stackctl" "$INSTALL_DIR/stackctl"
echo "Installed stackctl to $INSTALL_DIR/stackctl"
stackctl version
