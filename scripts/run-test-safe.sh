#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -eq 0 ]; then
	echo "usage: run-test-safe.sh <command> [args...]" >&2
	exit 2
fi

created_root=""
if [ -n "${OMNI_TEST_ROOT:-}" ]; then
	root="${OMNI_TEST_ROOT}"
else
	root="$(mktemp -d "${TMPDIR:-/tmp}/omni-test.XXXXXX")"
	created_root="1"
fi

cleanup() {
	if [ -n "$created_root" ]; then
		chmod -R u+w "$root" 2>/dev/null || true
		rm -rf "$root"
	fi
}
trap cleanup EXIT

mkdir -p \
	"$root/home" \
	"$root/xdg-config/omni" \
	"$root/xdg-cache" \
	"$root/omni-cache"

export HOME="$root/home"
export XDG_CONFIG_HOME="$root/xdg-config"
export XDG_CACHE_HOME="$root/xdg-cache"
export OMNI_CACHE_DIR="$root/omni-cache"
export OMNI_CONFIG="$root/xdg-config/omni/settings.json"

go telemetry off >/dev/null

"$@"
