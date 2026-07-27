#!/usr/bin/env sh
# Browser-driveable isolated TUI QA rig; usage: scripts/tui-e2e.sh [up|down].
# docker cp also works with rootless or remote daemons that cannot resolve host bind mounts.
set -eu

CONTAINER=omni-tui-e2e
PORT="${OMNI_TUI_E2E_PORT:-7681}"
WORKDIR="${TMPDIR:-/tmp}/omni-tui-e2e"
PIDFILE="$WORKDIR/ttyd.pid"
REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

down() {
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
    # Use the PID file so another ttyd on the same port pattern is never killed.
    if [ -f "$PIDFILE" ]; then
        kill "$(cat "$PIDFILE")" 2>/dev/null || true
        rm -f "$PIDFILE"
    fi
}

up() {
    command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
    command -v ttyd >/dev/null || { echo "ttyd is required (https://github.com/tsl0922/ttyd)" >&2; exit 1; }
    down
    mkdir -p "$WORKDIR/seed/skills-src/skills/alpha" \
        "$WORKDIR/seed/skills-src/skills/beta" \
        "$WORKDIR/seed/legacy-src/skills/legacy-one" \
        "$WORKDIR/seed/catalog"

    printf -- '---\nname: alpha\ndescription: first demo skill\n---\n\nalpha body\n' \
        > "$WORKDIR/seed/skills-src/skills/alpha/SKILL.md"
    printf -- '---\nname: beta\ndescription: second demo skill\n---\n\nbeta body\n' \
        > "$WORKDIR/seed/skills-src/skills/beta/SKILL.md"
    printf -- '---\nname: legacy-one\ndescription: legacy cli-managed skill\n---\n\nlegacy body\n' \
        > "$WORKDIR/seed/legacy-src/skills/legacy-one/SKILL.md"
    cat > "$WORKDIR/seed/catalog/search.json" <<'EOF'
{"skills":[{"source":"vercel-labs/skills","skillId":"find-skills","name":"Find Skills","installs":1200},{"source":"anthropics/skills","skillId":"pdf","name":"PDF","installs":800}]}
EOF
    cat > "$WORKDIR/seed/settings.json" <<'EOF'
{
  "version": 20,
  "settings": {},
  "tools": {},
  "groups": [],
  "hosts": {},
  "agents": { "packages": [ { "source": "/seed/skills-src" } ] }
}
EOF
    cat > "$WORKDIR/seed/skill-lock.json" <<'EOF'
{"skills":{"legacy-one":{"source":"/seed/legacy-src","skillFolderHash":"deadbeef","installedAt":"2026-07-01T00:00:00Z"}}}
EOF

    echo "building static omni…"
    (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux go build -o "$WORKDIR/omni" ./cmd/omni)

    docker run -d --name "$CONTAINER" alpine:3.20 sleep infinity >/dev/null
    docker cp "$WORKDIR/omni" "$CONTAINER:/usr/local/bin/omni"
    docker cp "$WORKDIR/seed" "$CONTAINER:/seed"
    docker exec "$CONTAINER" sh -c '
        chmod +x /usr/local/bin/omni &&
        apk add --no-cache busybox-extras git >/tmp/apk.log 2>&1 &&
        httpd -p 8080 -h /seed/catalog &&
        mkdir -p /root/.claude /root/.codex /root/.agents/skills /root/.config/omni &&
        cp /seed/settings.json /root/.config/omni/settings.json &&
        cp /seed/skill-lock.json /root/.agents/.skill-lock.json &&
        cp -r /seed/legacy-src/skills/legacy-one /root/.agents/skills/legacy-one &&
        for b in claude codex; do
            printf "#!/bin/sh\necho []\n" > /usr/local/bin/$b && chmod +x /usr/local/bin/$b
        done'
    docker exec -e HOME=/root "$CONTAINER" omni agents skills sync >/dev/null 2>&1 || true
    docker exec "$CONTAINER" sh -c '
        rm -rf /root/.codex/skills/beta && mkdir -p /root/.codex/skills/beta &&
        printf -- "---\nname: beta\ndescription: second demo skill\n---\n\nDRIFTED local edit\n" \
            > /root/.codex/skills/beta/SKILL.md'

    # Wait for the browser's PTY size before first paint to avoid a garbled frame.
    # Bind ttyd to loopback because writable mode exposes a root shell in the container.
    ttyd -p "$PORT" -i 127.0.0.1 --writable -t fontSize=14 \
        docker exec -it -e HOME=/root -e TERM=xterm-256color -e NO_EMOJI=1 \
        -e OMNI_SKILLS_CATALOG_URL=http://127.0.0.1:8080/search.json \
        "$CONTAINER" sh -c 'sleep 0.5 && exec omni' >"$WORKDIR/ttyd.log" 2>&1 &
    ttyd_pid=$!
    echo "$ttyd_pid" > "$PIDFILE"
    sleep 1
    if ! kill -0 "$ttyd_pid" 2>/dev/null; then
        echo "ttyd failed to start (port $PORT already bound, or docker exec failed):" >&2
        cat "$WORKDIR/ttyd.log" >&2
        rm -f "$PIDFILE"
        exit 1
    fi
    echo "TUI at http://127.0.0.1:$PORT/ (each connection = fresh TUI, state persists in container)"
    echo "reset: scripts/tui-e2e.sh down"
}

case "${1:-up}" in
    up) up ;;
    down) down ;;
    *) echo "usage: $0 [up|down]" >&2; exit 1 ;;
esac
