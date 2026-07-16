#!/bin/bash
set -euo pipefail

# allow specifying different destination directory
DIR="${DIR:-"$HOME/.local/bin"}"

# map architecture variations to the names omni release assets use
ARCH=$(uname -m)
case $ARCH in
    x86_64|amd64) ARCH=x86_64 ;;
    aarch64*|arm64) ARCH=arm64 ;;
esac

# omni asset names use a lowercase OS (goreleaser .Os): linux, darwin
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

# prepare the download URL
# || true: grep exits 1 on no match; under set -e/pipefail that would kill the
# script here, before the version guard below can print its diagnostic
GITHUB_LATEST_VERSION=$(curl -fsSL -H 'Accept: application/json' https://github.com/lkshrk/omni/releases/latest | grep -o '"tag_name":"[^"]*"' | head -n1 | cut -d'"' -f4 || true)
if [[ ! "$GITHUB_LATEST_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: could not determine latest omni release version (got: '${GITHUB_LATEST_VERSION}')" >&2
    exit 1
fi
GITHUB_FILE="omni_${OS}_${ARCH}.tar.gz"
GITHUB_URL="https://github.com/lkshrk/omni/releases/download/${GITHUB_LATEST_VERSION}/${GITHUB_FILE}"

# install/update the local binary via a temp dir so cwd is never touched
TMPDIR_INSTALL=$(mktemp -d)
trap 'rm -rf "$TMPDIR_INSTALL"' EXIT
curl -fL -o "$TMPDIR_INSTALL/omni.tar.gz" "$GITHUB_URL"
tar xzf "$TMPDIR_INSTALL/omni.tar.gz" -C "$TMPDIR_INSTALL" omni
install -Dm 755 "$TMPDIR_INSTALL/omni" -t "$DIR"

echo "Installed omni ${GITHUB_LATEST_VERSION} to ${DIR}/omni"
case ":$PATH:" in
    *":$DIR:"*) ;;
    *) echo "Note: $DIR is not on your PATH." ;;
esac
