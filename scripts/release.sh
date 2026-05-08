#!/usr/bin/env bash
# release.sh — tag a new omni release and push branch+tag for CI-gated release.
#
# Usage:
#   ./scripts/release.sh major       # bump major, reset minor+patch
#   ./scripts/release.sh minor       # bump minor, reset patch
#   ./scripts/release.sh patch       # bump patch
#   ./scripts/release.sh v1.2.3      # explicit version (with or without leading v)
#
# Pushes the current branch and a vX.Y.Z tag. CI runs for the branch push.
# If CI passes and the tested commit has the tag, the release workflow runs.
set -euo pipefail

usage() {
  echo "Usage: $0 major|minor|patch|vX.Y.Z" >&2
  exit 1
}

[[ $# -ne 1 ]] && usage

INPUT="$1"

commit_remaining_changes() {
  if git diff --quiet && git diff --cached --quiet; then
    return
  fi

  echo "Uncommitted changes remain:"
  git status --short
  echo

  read -r -p "Include these changes in a release prep commit? [y/N] " INCLUDE_CHANGES
  case "${INCLUDE_CHANGES}" in
    [Yy]|[Yy][Ee][Ss])
      read -r -p "Commit message [chore: prepare release]: " RELEASE_COMMIT_MESSAGE
      RELEASE_COMMIT_MESSAGE="${RELEASE_COMMIT_MESSAGE:-chore: prepare release}"
      git add -A
      git commit -m "$RELEASE_COMMIT_MESSAGE"
      ;;
    *)
      echo "Aborted. Commit, include, or stash changes before releasing." >&2
      exit 1
      ;;
  esac
}

ensure_clean_worktree() {
  if git diff --quiet && git diff --cached --quiet; then
    return
  fi

  echo "Uncommitted changes remain:"
  git status --short
  echo
  echo "Aborted. Commit, include, or stash changes before releasing." >&2
  exit 1
}

amend_demo_gif() {
  read -r -p "Update README demo GIF before release? [y/N] " UPDATE_DEMO
  case "${UPDATE_DEMO}" in
    [Yy]|[Yy][Ee][Ss])
      make demo-gif
      if ! git diff --quiet -- docs/assets/omni-demo.gif || ! git diff --cached --quiet -- docs/assets/omni-demo.gif; then
        if ! git rev-parse --verify HEAD >/dev/null 2>&1; then
          echo "Error: cannot amend demo GIF because HEAD does not exist." >&2
          exit 1
        fi
        git add docs/assets/omni-demo.gif
        git commit --amend --no-edit --only docs/assets/omni-demo.gif
      fi
      ;;
  esac
}

# Commit optional release-prep changes before the demo GIF so the generated
# asset can be amended into that commit instead of becoming a separate commit.
commit_remaining_changes
amend_demo_gif

# Require an intentional clean working tree.
ensure_clean_worktree

# Require being on a branch.
BRANCH=$(git symbolic-ref --short HEAD 2>/dev/null || echo "")
if [[ -z "$BRANCH" ]]; then
  echo "Error: must be on a branch, not detached HEAD." >&2
  exit 1
fi

# Pull latest so we tag the right commit.
git pull --ff-only origin "$BRANCH"

# Resolve current version from latest tag (default v0.0.0 if no tags yet).
CURRENT=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
CURRENT_CLEAN="${CURRENT#v}"
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_CLEAN"

# Compute next version.
VERSION=""
case "$INPUT" in
  major)
    MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
  minor)
    MINOR=$((MINOR + 1)); PATCH=0 ;;
  patch)
    PATCH=$((PATCH + 1)) ;;
  v[0-9]*.[0-9]*.[0-9]*)
    VERSION="$INPUT" ;;
  [0-9]*.[0-9]*.[0-9]*)
    VERSION="v$INPUT" ;;
  *)
    echo "Error: unrecognised argument '$INPUT'" >&2
    usage ;;
esac

[[ -z "$VERSION" ]] && VERSION="v${MAJOR}.${MINOR}.${PATCH}"

if git rev-parse -q --verify "refs/tags/${VERSION}" >/dev/null; then
  echo "Error: local tag ${VERSION} already exists." >&2
  exit 1
fi

if git ls-remote --exit-code --tags origin "refs/tags/${VERSION}" >/dev/null 2>&1; then
  echo "Error: remote tag ${VERSION} already exists." >&2
  exit 1
fi

echo "Current tag : ${CURRENT}"
echo "New tag     : ${VERSION}"
echo "Branch      : ${BRANCH}"
echo

read -r -p "Tag ${BRANCH} at ${VERSION} and push branch+tag? [y/N] " CONFIRM
case "${CONFIRM}" in
  [Yy]|[Yy][Ee][Ss]) ;;
  *) echo "Aborted."; exit 0 ;;
esac

git tag -a "${VERSION}" -m "Release ${VERSION}"
git push origin "${BRANCH}" "${VERSION}"

REPO=$(git remote get-url origin | sed 's|.*github.com[:/]\(.*\)\.git|\1|')
echo
echo "✓ Pushed ${VERSION}."
echo "  GoReleaser will run automatically if CI passes on this tagged commit."
echo "  https://github.com/${REPO}/actions"
