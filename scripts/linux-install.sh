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
GITHUB_LATEST_VERSION=$(curl -L -s -H 'Accept: application/json' https://github.com/lkshrk/omni/releases/latest | sed -e 's/.*"tag_name":"\([^"]*\)".*/\1/')
GITHUB_FILE="omni_${OS}_${ARCH}.tar.gz"
GITHUB_URL="https://github.com/lkshrk/omni/releases/download/${GITHUB_LATEST_VERSION}/${GITHUB_FILE}"

# install/update the local binary
curl -L -o omni.tar.gz "$GITHUB_URL"
tar xzf omni.tar.gz omni
install -Dm 755 omni -t "$DIR"
rm omni omni.tar.gz

echo "Installed omni ${GITHUB_LATEST_VERSION} to ${DIR}/omni"
case ":$PATH:" in
    *":$DIR:"*) ;;
    *) echo "Note: $DIR is not on your PATH." ;;
esac
