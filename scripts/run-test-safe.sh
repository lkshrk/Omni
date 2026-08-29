#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -eq 0 ]; then
	echo "usage: run-test-safe.sh <command> [args...]" >&2
	exit 2
fi

approved_tools="${OMNI_TEST_APPROVED_TOOLS:-}"
approved_tool_names=()
if [ -n "$approved_tools" ]; then
	case "$approved_tools" in ,* | *, | *,,*)
		echo "invalid OMNI_TEST_APPROVED_TOOLS value: $approved_tools" >&2
		exit 2
		;;
	esac
	IFS=',' read -r -a approved_tool_names <<<"$approved_tools"
	seen=,
	for tool in "${approved_tool_names[@]}"; do
		case "$tool" in apm | claude | codex | grok | cowsay) ;; *)
			echo "unknown OMNI_TEST_APPROVED_TOOLS tool: $tool" >&2
			exit 2
		;;
		esac
		case "$seen" in *,"$tool",*)
			echo "duplicate OMNI_TEST_APPROVED_TOOLS tool: $tool" >&2
			exit 2
		;;
		esac
		seen="$seen$tool,"
	done
fi

go_root="${GOROOT:-$(go env GOROOT)}"
build_go_cache="${OMNI_TEST_BUILD_GOCACHE:-$(go env GOCACHE)}"
build_go_modcache="${OMNI_TEST_BUILD_GOMODCACHE:-$(go env GOMODCACHE)}"
for build_cache in "$build_go_cache" "$build_go_modcache"; do
	case "$build_cache" in
	/)
		echo "refusing filesystem root as Go build cache" >&2
		exit 2
		;;
	/*) ;;
	*)
		echo "refusing relative Go build cache: $build_cache" >&2
		exit 2
		;;
	esac
done
root="$(mktemp -d /tmp/omni-test.XXXXXX)"
chmod 700 "$root"
root="$(cd "$root" && pwd -P)"
nonce="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
marker="$root/.omni-test-sandbox"
printf '%s\n' "$nonce" >"$marker"
chmod 600 "$marker"

cleanup() {
	local expected actual resolved
	expected="$nonce"
	if [ ! -f "$marker" ] || [ -L "$marker" ]; then
		echo "refusing unsafe test cleanup: invalid sandbox marker at $marker" >&2
		return 1
	fi
	actual="$(<"$marker")"
	resolved="$(cd "$root" 2>/dev/null && pwd -P)" || return 1
	if [ "$root" != "$resolved" ] || [ "$actual" != "$expected" ]; then
		echo "refusing unsafe test cleanup: sandbox identity mismatch at $root" >&2
		return 1
	fi
	case "$root" in
	/tmp/omni-test.* | /private/tmp/omni-test.*) ;;
	*)
		echo "refusing unsafe test cleanup outside a canonical temporary root: $root" >&2
		return 1
		;;
	esac
	find "$root" -type d -exec chmod u+rwx {} + 2>/dev/null || true
	find "$root" -type f -exec chmod u+rw {} + 2>/dev/null || true
	find "$root" -depth -delete
}

on_exit() {
	local status=$?
	trap - EXIT
	cleanup || status=1
	exit "$status"
}
trap on_exit EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

home="$root/home"
config="$root/config"
data="$root/data"
cache="$root/cache"
state="$root/state"
tmp="$root/tmp"
bin="$root/bin"
mkdir -p \
	"$home" \
	"$home/AppData/Roaming" \
	"$home/AppData/Local" \
	"$config/omni" \
	"$config/git" \
	"$config/gnupg" \
	"$config/docker" \
	"$config/kube" \
	"$data/go" \
	"$data/cargo" \
	"$data/npm-global" \
	"$data/rustup" \
	"$cache/go-build" \
	"$cache/go-mod" \
	"$cache/npm" \
	"$cache/omni" \
	"$state/omni" \
	"$tmp" \
	"$bin" \
	"$root/work"

link_tool() {
	local name="$1" path
	path="$(type -P "$name" || true)"
	if [ -n "$path" ]; then
		case "$path" in
		/*) ln -s "$path" "$bin/$name" ;;
		esac
	fi
}

ln -s "$go_root/bin/go" "$bin/go"
for tool in bash sh git stow python3 node npm \
	awk basename cat chmod cmp cp cut date dirname du echo env find grep head ln ls make mkdir mktemp mv \
	od printf printenv pwd readlink realpath rm sed seq sleep sort stat tail tee test touch tr uname wc which xargs \
	cc gcc clang as ld pkg-config; do
	link_tool "$tool"
done
for tool in "${approved_tool_names[@]}"; do
	link_tool "$tool"
	if [ ! -x "$bin/$tool" ]; then
		echo "approved optional test tool is unavailable: $tool" >&2
		exit 2
	fi
done

for required in go bash sh git; do
	if [ ! -x "$bin/$required" ]; then
		echo "required test tool is unavailable: $required" >&2
		exit 2
	fi
done

safe_env=(
	"PATH=$bin"
	"SHELL=$bin/sh"
	"HOME=$home"
	"USERPROFILE=$home"
	"APPDATA=$home/AppData/Roaming"
	"LOCALAPPDATA=$home/AppData/Local"
	"XDG_CONFIG_HOME=$config"
	"XDG_DATA_HOME=$data"
	"XDG_CACHE_HOME=$cache"
	"XDG_STATE_HOME=$state"
	"OMNI_CONFIG=$config/omni/settings.json"
	"OMNI_CACHE_DIR=$cache/omni"
	"OMNI_STATE_DIR=$state/omni"
	"OMNI_TEST_ISOLATED=1"
	"OMNI_TEST_ROOT=$root"
	"OMNI_TEST_NONCE=$nonce"
	"TMPDIR=$tmp"
	"TMP=$tmp"
	"TEMP=$tmp"
	"GOPATH=$data/go"
	"GOCACHE=$cache/go-build"
	"GOMODCACHE=$cache/go-mod"
	"GOROOT=$go_root"
	"CARGO_HOME=$data/cargo"
	"RUSTUP_HOME=$data/rustup"
	"NPM_CONFIG_USERCONFIG=$config/npmrc"
	"NPM_CONFIG_CACHE=$cache/npm"
	"NPM_CONFIG_PREFIX=$data/npm-global"
	"GIT_CONFIG_GLOBAL=$config/git/config"
	"GIT_CONFIG_SYSTEM=/dev/null"
	"GIT_CONFIG_NOSYSTEM=1"
	"GIT_TERMINAL_PROMPT=0"
	"GIT_ASKPASS=/bin/false"
	"GNUPGHOME=$config/gnupg"
	"KUBECONFIG=$config/kube/config"
	"DOCKER_CONFIG=$config/docker"
	"HTTP_PROXY=http://127.0.0.1:1"
	"HTTPS_PROXY=http://127.0.0.1:1"
	"ALL_PROXY=http://127.0.0.1:1"
	"http_proxy=http://127.0.0.1:1"
	"https_proxy=http://127.0.0.1:1"
	"all_proxy=http://127.0.0.1:1"
	"NO_PROXY=localhost,127.0.0.1,::1"
	"no_proxy=localhost,127.0.0.1,::1"
)
if [ -n "$approved_tools" ]; then
	safe_env+=("OMNI_TEST_APPROVED_TOOLS=$approved_tools")
fi

# Preserve only non-secret build and terminal controls needed by Go/C toolchains.
for key in CI GITHUB_ACTIONS TERM COLORTERM LANG LC_ALL LC_CTYPE TZ \
	GOFLAGS GOTOOLCHAIN GOPROXY GONOPROXY GONOSUMDB GOPRIVATE GOSUMDB \
	GOEXPERIMENT CGO_ENABLED CC CXX AR PKG_CONFIG_PATH SDKROOT \
	MACOSX_DEPLOYMENT_TARGET DEVELOPER_DIR; do
	if [ -n "${!key:-}" ]; then
		safe_env+=("$key=${!key}")
	fi
done

env -i "${safe_env[@]}" go telemetry off >/dev/null
if [ "$1" = "go" ] && [ "${2:-}" = "test" ]; then
	# The Go driver may fetch only public build dependencies. Each compiled test
	# binary immediately replaces these parent-only values through testguard init.
	build_env=(
		"${safe_env[@]}"
		"GOCACHE=$build_go_cache"
		"GOMODCACHE=$build_go_modcache"
		"GOPROXY=https://proxy.golang.org"
		"GOSUMDB=sum.golang.org"
		"GOPRIVATE="
		"GONOPROXY="
		"GONOSUMDB="
		"HTTP_PROXY="
		"HTTPS_PROXY="
		"ALL_PROXY="
		"http_proxy="
		"https_proxy="
		"all_proxy="
	)
	env -i "${build_env[@]}" "$@"
else
	env -i "${safe_env[@]}" "$@"
fi
