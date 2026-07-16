#!/usr/bin/env bash
# Bumps Formula/omni.rb in lkshrk/homebrew-tap directly, bypassing goreleaser's
# deprecated `brews` publisher (see .goreleaser.yml comment).
set -euo pipefail

VERSION="${1:?usage: update-homebrew-formula.sh <version-without-v> <dist-dir> <tap-dir>}"
DIST_DIR="${2:?usage: update-homebrew-formula.sh <version-without-v> <dist-dir> <tap-dir>}"
TAP_DIR="${3:?usage: update-homebrew-formula.sh <version-without-v> <dist-dir> <tap-dir>}"

checksum() {
  grep " ${1}\$" "${DIST_DIR}/checksums.txt" | awk '{print $1}'
}

DARWIN_AMD64_SHA=$(checksum "omni_darwin_x86_64.tar.gz")
DARWIN_ARM64_SHA=$(checksum "omni_darwin_arm64.tar.gz")
LINUX_AMD64_SHA=$(checksum "omni_linux_x86_64.tar.gz")
LINUX_ARM64_SHA=$(checksum "omni_linux_arm64.tar.gz")

for name in DARWIN_AMD64_SHA DARWIN_ARM64_SHA LINUX_AMD64_SHA LINUX_ARM64_SHA; do
  if [ -z "${!name}" ]; then
    echo "::error::missing checksum for ${name}" >&2
    exit 1
  fi
done

mkdir -p "${TAP_DIR}/Formula"
cat > "${TAP_DIR}/Formula/omni.rb" <<EOF
# typed: false
# frozen_string_literal: true

class Omni < Formula
  desc "Manage all your dev tools from a single JSON config file."
  homepage "https://github.com/lkshrk/omni"
  version "${VERSION}"
  license "MIT"

  depends_on "stow"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/lkshrk/omni/releases/download/v${VERSION}/omni_darwin_x86_64.tar.gz"
      sha256 "${DARWIN_AMD64_SHA}"

      def install
        bin.install "omni"
      end
    end
    if Hardware::CPU.arm?
      url "https://github.com/lkshrk/omni/releases/download/v${VERSION}/omni_darwin_arm64.tar.gz"
      sha256 "${DARWIN_ARM64_SHA}"

      def install
        bin.install "omni"
      end
    end
  end

  on_linux do
    if Hardware::CPU.intel? && Hardware::CPU.is_64_bit?
      url "https://github.com/lkshrk/omni/releases/download/v${VERSION}/omni_linux_x86_64.tar.gz"
      sha256 "${LINUX_AMD64_SHA}"
      def install
        bin.install "omni"
      end
    end
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/lkshrk/omni/releases/download/v${VERSION}/omni_linux_arm64.tar.gz"
      sha256 "${LINUX_ARM64_SHA}"
      def install
        bin.install "omni"
      end
    end
  end

  test do
    system "#{bin}/omni", "--version"
  end
end
EOF

cd "${TAP_DIR}"
if [ -z "$(git status --porcelain -- Formula/omni.rb)" ]; then
  echo "Formula/omni.rb already up to date for ${VERSION}"
  exit 0
fi

git add Formula/omni.rb
git -c user.name="lkshrk-bot" -c user.email="lkshrk@users.noreply.github.com" \
  commit -m "chore(formula): bump omni to v${VERSION}"
git push origin HEAD:main
