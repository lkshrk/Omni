#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ]; then
	echo "usage: run-docker-safe.sh <docker> <args...>" >&2
	exit 2
fi

docker_bin="$1"
shift

case "${DOCKER_HOST:-}" in
"" | unix://* | npipe://* | tcp://localhost:* | tcp://127.0.0.1:* | tcp://\[::1\]:*) ;;
*)
	echo "refusing non-local Docker daemon: $DOCKER_HOST" >&2
	exit 2
	;;
esac

docker_config="$(mktemp -d /tmp/omni-docker.XXXXXX)"
docker_config="$(cd "$docker_config" && pwd -P)"
builder=""

cleanup() {
	local status=$?
	trap - EXIT HUP INT TERM
	if [ -n "$builder" ]; then
		DOCKER_CONFIG="$docker_config" DOCKER_CONTEXT= DOCKER_AUTH_CONFIG= \
			DOCKER_CERT_PATH= DOCKER_TLS_VERIFY= \
			"$docker_bin" buildx rm -f "$builder" >/dev/null 2>&1 || true
	fi
	case "$docker_config" in /tmp/omni-docker.* | /private/tmp/omni-docker.*) find "$docker_config" -depth -delete ;; esac
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

export DOCKER_CONFIG="$docker_config"
unset DOCKER_CONTEXT DOCKER_AUTH_CONFIG DOCKER_CERT_PATH DOCKER_TLS_VERIFY BUILDX_CONFIG BUILDKIT_HOST
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy

if [ "${1:-}" = buildx ] && [ "${2:-}" = build ]; then
	shift 2
	for arg in "$@"; do
		case "$arg" in *type=gha*) builder="omni-test-$$-${RANDOM:-0}" ;; esac
	done
	if [ -n "$builder" ]; then
		"$docker_bin" buildx create --name "$builder" --driver docker-container --use >/dev/null
		"$docker_bin" buildx build --builder "$builder" "$@"
	else
		"$docker_bin" buildx build "$@"
	fi
else
	"$docker_bin" "$@"
fi
