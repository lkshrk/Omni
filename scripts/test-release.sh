#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

assert_goreleaser_declares_stow_dependencies() {
  local config="$ROOT/.goreleaser.yml"
  if ! grep -q '^      - stow$' "$config"; then
    echo "goreleaser Linux packages must declare stow as a dependency" >&2
    exit 1
  fi
  if ! grep -q 'depends_on "stow"' "$ROOT/scripts/update-homebrew-formula.sh"; then
    echo "Homebrew formula template must declare stow via depends_on" >&2
    exit 1
  fi
}

FAKEBIN="$TMPDIR/bin"
mkdir -p "$FAKEBIN"

cat > "$FAKEBIN/make" <<'MAKE'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != "demo-gif" ]]; then
  echo "unexpected make target: ${1:-}" >&2
  exit 1
fi
printf 'updated demo\n' > docs/assets/omni-demo.gif

if [[ -n "${OMNI_RELEASE_TEST_ADVANCE_ORIGIN:-}" ]]; then
  advance_dir=$(mktemp -d)
  trap 'rm -rf "$advance_dir"' EXIT
  git clone "$OMNI_RELEASE_TEST_ADVANCE_ORIGIN" "$advance_dir/repo" >/dev/null 2>&1
  git -C "$advance_dir/repo" checkout main >/dev/null 2>&1
  git -C "$advance_dir/repo" config user.name "Release Test"
  git -C "$advance_dir/repo" config user.email "release-test@example.invalid"
  git -C "$advance_dir/repo" config commit.gpgsign false
  git -C "$advance_dir/repo" config tag.gpgsign false
  git -C "$advance_dir/repo" config core.hooksPath /dev/null
  printf 'concurrent change\n' > "$advance_dir/repo/concurrent.txt"
  git -C "$advance_dir/repo" add concurrent.txt
  git -C "$advance_dir/repo" commit -m "chore: concurrent change" >/dev/null
  git -C "$advance_dir/repo" push origin main >/dev/null 2>&1
fi
MAKE
chmod +x "$FAKEBIN/make"

setup_repo() {
  local name="$1"
  local origin="$TMPDIR/${name}-origin.git"
  local work="$TMPDIR/${name}-work"

  git init --bare "$origin" >/dev/null
  git clone "$origin" "$work" >/dev/null 2>&1
  git -C "$work" checkout -b main >/dev/null 2>&1
  git -C "$work" config user.name "Release Test"
  git -C "$work" config user.email "release-test@example.invalid"
  git -C "$work" config commit.gpgsign false
  git -C "$work" config tag.gpgsign false
  git -C "$work" config core.hooksPath /dev/null

  mkdir -p "$work/docs/assets"
  printf 'initial demo\n' > "$work/docs/assets/omni-demo.gif"
  git -C "$work" add docs/assets/omni-demo.gif
  git -C "$work" commit -m "chore: initial" >/dev/null
  git -C "$work" tag -a v0.0.0 -m "Release v0.0.0"
  git -C "$work" push origin main v0.0.0 >/dev/null 2>&1
  git --git-dir="$origin" symbolic-ref HEAD refs/heads/main

  cp "$ROOT/scripts/release.sh" "$work/release.sh"
  printf '%s\n' "$work"
}

run_release() {
  local work="$1"
  local input="$2"
  (
    cd "$work"
    PATH="$FAKEBIN:$PATH" bash ./release.sh patch <<< "$input"
  )
}

assert_release_tag_matches_remote_head() {
  local work="$1"
  git -C "$work" fetch origin main --tags >/dev/null 2>&1
  local remote_head tag_head
  remote_head=$(git -C "$work" rev-parse origin/main)
  tag_head=$(git -C "$work" rev-parse v0.0.1^{commit})
  if [[ "$tag_head" != "$remote_head" ]]; then
    echo "release tag does not point at pushed branch head" >&2
    exit 1
  fi
}

scenario_published_head_gets_new_demo_commit() {
  local work
  work=$(setup_repo published)
  local published_head
  published_head=$(git -C "$work" rev-parse HEAD)

  run_release "$work" $'y\ny'
  assert_release_tag_matches_remote_head "$work"

  local remote_head commit_count
  remote_head=$(git -C "$work" rev-parse origin/main)
  commit_count=$(git -C "$work" rev-list --count origin/main)
  if [[ "$remote_head" == "$published_head" ]]; then
    echo "release did not push an updated branch commit" >&2
    exit 1
  fi
  if [[ "$commit_count" -ne 2 ]]; then
    echo "expected demo update to create a second commit, got $commit_count" >&2
    exit 1
  fi
  if ! git -C "$work" merge-base --is-ancestor "$published_head" "$remote_head"; then
    echo "published origin head was rewritten instead of extended" >&2
    exit 1
  fi
}

scenario_unpushed_release_commit_gets_amended() {
  local work
  work=$(setup_repo unpushed)
  printf 'release notes\n' > "$work/README.md"
  git -C "$work" add README.md
  git -C "$work" commit -m "chore: prepare release" >/dev/null

  run_release "$work" $'y\ny'
  assert_release_tag_matches_remote_head "$work"

  local commit_count subject
  commit_count=$(git -C "$work" rev-list --count origin/main)
  subject=$(git -C "$work" show -s --format=%s origin/main)
  if [[ "$commit_count" -ne 2 ]]; then
    echo "expected amended release-prep commit to keep two commits, got $commit_count" >&2
    exit 1
  fi
  if [[ "$subject" != "chore: prepare release" ]]; then
    echo "expected demo GIF to amend release-prep commit, got subject: $subject" >&2
    exit 1
  fi
  if ! git -C "$work" show --name-only --format= origin/main | grep -qx 'docs/assets/omni-demo.gif'; then
    echo "amended release-prep commit does not include demo GIF" >&2
    exit 1
  fi
}

scenario_origin_advance_aborts_before_tag() {
  local work origin output status
  work=$(setup_repo origin-advance)
  origin="$TMPDIR/origin-advance-origin.git"

  set +e
  output=$(
    cd "$work" &&
      OMNI_RELEASE_TEST_ADVANCE_ORIGIN="$origin" PATH="$FAKEBIN:$PATH" bash ./release.sh patch <<<'y' 2>&1
  )
  status=$?
  set -e

  if [[ "$status" -eq 0 ]]; then
    echo "release succeeded despite concurrent origin advance" >&2
    exit 1
  fi
  if ! grep -q 'origin/main changed while preparing the release' <<< "$output"; then
    echo "release did not report concurrent origin advance:" >&2
    echo "$output" >&2
    exit 1
  fi
  if git -C "$work" rev-parse -q --verify refs/tags/v0.0.1 >/dev/null; then
    echo "release created local tag despite concurrent origin advance" >&2
    exit 1
  fi
  git -C "$work" fetch origin main >/dev/null 2>&1
  if [[ "$(git -C "$work" show -s --format=%s origin/main)" != "chore: concurrent change" ]]; then
    echo "origin did not advance to the expected concurrent commit" >&2
    exit 1
  fi
}

scenario_published_head_gets_new_demo_commit
assert_goreleaser_declares_stow_dependencies
scenario_unpushed_release_commit_gets_amended
scenario_origin_advance_aborts_before_tag

echo "release.sh regressions passed"
