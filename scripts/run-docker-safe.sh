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

# Docker Desktop and Homebrew ship buildx as a per-user CLI plugin the isolated config would otherwise hide.
for plugin_dir in "${HOME:-}/.docker/cli-plugins" /opt/homebrew/lib/docker/cli-plugins; do
	[ -d "$plugin_dir" ] || continue
	mkdir -p "$docker_config/cli-plugins"
	for plugin in "$plugin_dir"/docker-*; do
		[ -e "$plugin" ] && [ ! -e "$docker_config/cli-plugins/$(basename "$plugin")" ] && ln -s "$plugin" "$docker_config/cli-plugins/"
	done
done

# Dropping DOCKER_CONTEXT falls back to /var/run/docker.sock, which Docker Desktop on macOS does not create by default.
if [ -z "${DOCKER_HOST:-}" ] && [ ! -S /var/run/docker.sock ] && [ -S "${HOME:-}/.docker/run/docker.sock" ]; then
	export DOCKER_HOST="unix://$HOME/.docker/run/docker.sock"
fi

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
