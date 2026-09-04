#!/usr/bin/env bash
# One-shot APM migration and cleanup for a fleet host. Dry-run unless --apply.
set -euo pipefail

usage() {
  cat <<'EOF'
usage: apm-host-migrate.sh [--apply] [--drop-unmanaged] [--remove-duplicates] [--skip-pull]

  --apply              execute deletions (default: print them)
  --drop-unmanaged     delete native items the template does not cover
                       (default: stop and ask you to add them to the template first)
  --remove-duplicates  also uninstall native plugins whose repo APM already installs
  --skip-pull          do not run `omni dots pull`

Flow: dots pull -> drop dangling dot links -> ensure template link -> omni doctor --fix
-> omni agents sync -> omni agents migrate -> cleanup -> omni agents sync -> final check.
EOF
}

APPLY=0 DROP_UNMANAGED=0 REMOVE_DUPES=0 SKIP_PULL=0
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    --drop-unmanaged) DROP_UNMANAGED=1 ;;
    --remove-duplicates) REMOVE_DUPES=1 ;;
    --skip-pull) SKIP_PULL=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; usage >&2; exit 2 ;;
  esac
done

HOST="${OMNI_HOSTNAME:-$(hostname -s)}"
DOTFILES="${DOTFILES_DIR:-$HOME/dotfiles}"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/omni"
TEMPLATE="$CONFIG_DIR/apm.yml"
REPO_TEMPLATE="$DOTFILES/dotfiles/omni-apm@$HOST/.config/omni/apm.yml"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

unset -f claude codex omni 2>/dev/null || true
CLAUDE="$(command -v claude || true)"
CODEX="$(command -v codex || true)"
export OMNI_HOSTNAME="$HOST"

step() { printf '\n==> %s\n' "$*"; }
run() { printf '   $ %s\n' "$*"; "$@"; }
section() { awk -v h="$1" '$0 ~ "^"h{f=1;next} /^[^ ]/{f=0} f' "$2"; }
col() { awk -F'  +' -v n="$1" '{print $n}'; }
queue() { printf '%s\n' "$*" >> "$WORK/cleanup.txt"; }

native_plugin_marketplaces() {
  {
    [ -n "$CLAUDE" ] && "$CLAUDE" plugin list --json 2>/dev/null | python3 -c 'import json,sys
for p in json.load(sys.stdin): print("claude/"+p["id"].split("@",1)[1])' || true
    [ -n "$CODEX" ] && "$CODEX" plugin list --json 2>/dev/null | python3 -c 'import json,sys
for p in json.load(sys.stdin).get("installed",[]): print("codex/"+p["marketplaceName"])' || true
  } | sort -u
}

step "host $HOST, dotfiles $DOTFILES, template $TEMPLATE"

if [ "$SKIP_PULL" -eq 0 ]; then
  step "pull dotfiles and re-link"
  run omni dots pull
fi

step "dangling dot links under ~/.claude ~/.codex ~/.agents"
DANGLING="$(find "$HOME/.claude" "$HOME/.codex" "$HOME/.agents" -maxdepth 4 -xtype l 2>/dev/null || true)"
if [ -n "$DANGLING" ]; then
  printf '%s\n' "$DANGLING" | sed 's/^/   /'
  if [ "$APPLY" -eq 1 ]; then
    printf '%s\n' "$DANGLING" | while IFS= read -r l; do rm -f "$l"; done
    echo "   removed"
  else
    echo "   (dry-run)"
  fi
else
  echo "   none"
fi

step "apm template"
if [ ! -e "$TEMPLATE" ]; then
  [ -f "$REPO_TEMPLATE" ] || { echo "   no template for host $HOST at $REPO_TEMPLATE" >&2; exit 1; }
  mkdir -p "$CONFIG_DIR"
  run ln -s "$REPO_TEMPLATE" "$TEMPLATE"
fi
ls -l "$TEMPLATE" | sed 's/^/   /'

step "apm toolchain"
run omni doctor --fix
apm --version | sed 's/^/   /'

step "sync from template"
run omni agents sync

step "inventory"
omni agents migrate --host "$HOST" > "$WORK/preview.txt"
section 'Replaced by this manifest' "$WORK/preview.txt" > "$WORK/replaced.txt"
section 'Retained \(not migrated\)' "$WORK/preview.txt" > "$WORK/retained.txt"
section 'Already managed by APM' "$WORK/preview.txt" > "$WORK/managed.txt"

if [ -s "$WORK/retained.txt" ]; then
  echo "   retained, left untouched (needs a manual decision):"
  sed 's/^/   /' "$WORK/retained.txt"
fi

if [ -s "$WORK/replaced.txt" ]; then
  echo "   native items the template does not cover (the preview proposes them):"
  sed 's/^/   /' "$WORK/replaced.txt"
  if [ "$DROP_UNMANAGED" -eq 0 ]; then
    cat <<EOF

   Either add them to $REPO_TEMPLATE (see the manifest above 'Replaced' in
   'omni agents migrate --host $HOST'), commit, 'omni agents sync', and rerun;
   or rerun with --drop-unmanaged to delete them natively.
EOF
    exit 1
  fi
fi

: > "$WORK/cleanup.txt"
if [ "$DROP_UNMANAGED" -eq 1 ]; then
  while IFS= read -r line; do
    t="$(printf '%s\n' "$line" | col 2)"; k="$(printf '%s\n' "$line" | col 3)"; id="$(printf '%s\n' "$line" | col 4)"
    case "$t/$k" in
      claude/mcp)    queue "$CLAUDE" mcp remove -s user "$id" ;;
      claude/plugin) queue "$CLAUDE" plugin uninstall "$id" ;;
      codex/mcp)     queue "$CODEX" mcp remove "$id" ;;
      codex/plugin)  queue "$CODEX" plugin remove "$id" ;;
    esac
  done < "$WORK/replaced.txt"
fi
if [ "$REMOVE_DUPES" -eq 1 ]; then
  while IFS= read -r line; do
    t="$(printf '%s\n' "$line" | col 2)"; k="$(printf '%s\n' "$line" | col 3)"; id="$(printf '%s\n' "$line" | col 4)"
    case "$t/$k" in
      claude/plugin) queue "$CLAUDE" plugin uninstall "$id" ;;
      codex/plugin)  queue "$CODEX" plugin remove "$id" ;;
    esac
  done < "$WORK/managed.txt"
fi

if [ ! -s "$WORK/cleanup.txt" ]; then
  echo "   nothing to delete"
  exit 0
fi

step "cleanup commands"
sed 's/^/   $ /' "$WORK/cleanup.txt"
if [ "$APPLY" -eq 0 ]; then
  echo "   (dry-run: rerun with --apply)"
  exit 0
fi
while IFS= read -r cmd; do
  printf '   $ %s\n' "$cmd"
  eval "$cmd" || echo "   (failed, continuing)"
done < "$WORK/cleanup.txt"

step "marketplaces no native plugin references any more"
native_plugin_marketplaces > "$WORK/still-used.txt"
cat "$WORK/replaced.txt" "$WORK/managed.txt" | while IFS= read -r line; do
  t="$(printf '%s\n' "$line" | col 2)"; k="$(printf '%s\n' "$line" | col 3)"; id="$(printf '%s\n' "$line" | col 4)"
  [ "$k" = marketplace ] || continue
  if grep -qx "$t/$id" "$WORK/still-used.txt"; then
    echo "   keep $t marketplace $id (still referenced by a native plugin)"
    continue
  fi
  case "$t" in
    claude) printf '   $ %s\n' "$CLAUDE plugin marketplace remove $id"; "$CLAUDE" plugin marketplace remove "$id" || echo "   (failed, continuing)" ;;
    codex)  printf '   $ %s\n' "$CODEX plugin marketplace remove $id";  "$CODEX" plugin marketplace remove "$id"  || echo "   (failed, continuing)" ;;
  esac
done

step "second sync (re-deploys anything the cleanup removed)"
run omni agents sync

step "final check"
omni agents migrate --host "$HOST" > "$WORK/final.txt"
if section 'Replaced by this manifest' "$WORK/final.txt" | grep -q .; then
  echo "   still not covered:"
  section 'Replaced by this manifest' "$WORK/final.txt" | sed 's/^/   /'
  exit 1
fi
echo "   clean"
awk '/^Already managed by APM/{f=1} f' "$WORK/final.txt" | sed 's/^/   /'
