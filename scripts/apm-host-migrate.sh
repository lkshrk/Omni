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
-> omni agents sync -> sweep stale unmanaged copies -> omni agents migrate -> cleanup
-> omni agents sync -> final check.
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
queue() {
  local cmd="$*"
  if [ -f "$WORK/cleanup.txt" ] && grep -qxF "$cmd" "$WORK/cleanup.txt"; then return 0; fi
  printf '%s\n' "$cmd" >> "$WORK/cleanup.txt"
}

plugin_keep_reason() {
  case "$2" in
    *@skills-dir) echo 'apm-generated, marketplace skills-dir'; return 0 ;;
  esac
  awk -F'\t' -v t="$1" -v id="$2" '$1==t && $2==id {print $3; f=1; exit} END{exit !f}' "$WORK/plugin-keep.txt"
}

queue_plugin_uninstall() {
  local reason
  if reason="$(plugin_keep_reason "$1" "$2")"; then
    if ! grep -qxF "$1/$2" "$WORK/kept.txt" 2>/dev/null; then
      printf '%s/%s\n' "$1" "$2" >> "$WORK/kept.txt"
      printf '   keep: %s (%s)\n' "$2" "$reason"
    fi
    return 0
  fi
  case "$1" in
    claude) queue "$CLAUDE" plugin uninstall "$2" ;;
    codex)  queue "$CODEX" plugin remove "$2" ;;
  esac
}

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
doctor_rc=0
run omni doctor --fix || doctor_rc=$?
if [ "$doctor_rc" -ne 0 ]; then
  printf '   omni doctor --fix exited %s; checking whether apm is usable\n' "$doctor_rc"
  if ! apm --version >/dev/null 2>&1; then
    echo "   apm is missing and omni doctor --fix could not install it; aborting" >&2
    exit 1
  fi
fi
apm --version | sed 's/^/   /'

step "sync from template"
run omni agents sync

step "stale unmanaged copies"
python3 - > "$WORK/stale.txt" <<'PY'
import os, re

home = os.environ["HOME"]
lock = os.path.join(home, ".apm", "apm.lock.yaml")
modules = os.path.join(home, ".apm", "apm_modules")
STAGING = ".apm-resolution-staging"
KINDS = ("agents", "commands", "skills", "hooks")


def managed_values():
    if not os.path.exists(lock):
        return []
    try:
        import yaml
    except ImportError:
        yaml = None
    if yaml is not None:
        data = yaml.safe_load(open(lock)) or {}
        return [str(d["value"]) for d in (data.get("deployments") or [])
                if isinstance(d, dict) and d.get("kind") == "project-relative" and d.get("value")]
    out, inblock = [], False
    for line in open(lock):
        s = line.rstrip("\n")
        if re.match(r"^-\s+kind:\s*project-relative\s*$", s):
            inblock = True
        elif s.startswith("- ") or (s and not s.startswith(" ")):
            inblock = False
        elif inblock:
            m = re.match(r"^\s+value:\s*(.+?)\s*$", s)
            if m:
                out.append(m.group(1).strip("'\""))
    return out


managed = set(os.path.join(home, v) for v in managed_values())


def is_managed(p):
    return p in managed or any(m.startswith(p + os.sep) for m in managed)


def package_roots():
    roots = set()
    if not os.path.isdir(modules):
        return roots
    for owner in os.listdir(modules):
        odir = os.path.join(modules, owner)
        if owner == STAGING or not os.path.isdir(odir):
            continue
        for pkg in os.listdir(odir):
            if os.path.isdir(os.path.join(odir, pkg)):
                roots.add(os.path.join(odir, pkg))
    for dirpath, dirnames, filenames in os.walk(modules):
        if STAGING in dirnames:
            dirnames.remove(STAGING)
        if "apm.yml" in filenames or os.path.isfile(os.path.join(dirpath, ".claude-plugin", "plugin.json")):
            roots.add(dirpath)
    return roots


def package_names(root):
    names = [os.path.basename(root)]
    manifest = os.path.join(root, "apm.yml")
    if os.path.isfile(manifest):
        for line in open(manifest):
            m = re.match(r"^name:\s*(\S+)\s*$", line)
            if m:
                names.append(m.group(1).strip("'\""))
                break
    return names


def sources():
    for root in sorted(package_roots()):
        names = package_names(root)
        for kind in KINDS:
            for src in (os.path.join(root, kind), os.path.join(root, ".claude", kind)):
                if os.path.isdir(src):
                    yield src, kind, names
        codex = os.path.join(root, ".codex", "agents")
        if os.path.isdir(codex):
            yield codex, "codex-agents", names


def destinations(src, kind, names):
    if kind == "hooks":
        for n in names:
            yield os.path.join(home, ".claude", "hooks", n)
            yield os.path.join(home, ".codex", "hooks", n)
        return
    for n in sorted(os.listdir(src)):
        if n.startswith("."):
            continue
        isdir = os.path.isdir(os.path.join(src, n))
        if kind == "codex-agents":
            if not isdir and n.endswith(".toml"):
                yield os.path.join(home, ".codex", "agents", n)
        elif kind == "skills":
            if isdir:
                yield os.path.join(home, ".agents", "skills", n)
                yield os.path.join(home, ".claude", "skills", n)
        elif not isdir:
            yield os.path.join(home, ".claude", kind, n)
            if kind == "agents" and n.endswith(".md"):
                yield os.path.join(home, ".codex", "agents", n[:-3] + ".toml")


seen = set()
for src, kind, names in sources():
    for dest in destinations(src, kind, names):
        if dest in seen:
            continue
        seen.add(dest)
        if not os.path.lexists(dest) or is_managed(dest):
            continue
        if os.path.islink(dest):
            print("keep\t%s\tsymlink (dotfiles override)" % dest)
        else:
            print("remove\t%s" % dest)
PY

STALE_REMOVED=0
awk -F'\t' '$1=="keep"{print $2"  -- "$3}' "$WORK/stale.txt" > "$WORK/stale-keep.txt"
awk -F'\t' '$1=="remove"{print $2}' "$WORK/stale.txt" > "$WORK/stale-remove.txt"
if [ -s "$WORK/stale-keep.txt" ]; then
  echo "   keep:"
  sed 's/^/     /' "$WORK/stale-keep.txt"
fi
if [ ! -s "$WORK/stale-remove.txt" ]; then
  echo "   remove: none"
elif [ "$APPLY" -eq 0 ]; then
  echo "   remove (shadows APM package content, sync reports these as skipped):"
  sed 's/^/     /' "$WORK/stale-remove.txt"
  echo "   (dry-run)"
else
  echo "   remove:"
  sed 's/^/     /' "$WORK/stale-remove.txt"
  BACKUP="$HOME/.cache/omni/apm-stale-backup-$(date +%Y%m%d-%H%M%S)"
  while IFS= read -r p; do
    rel="${p#$HOME/}"
    mkdir -p "$WORK/stale-backup/$(dirname "$rel")" "$BACKUP/$(dirname "$rel")"
    cp -R "$p" "$WORK/stale-backup/$rel"
    cp -R "$p" "$BACKUP/$rel"
    rm -rf "$p"
  done < "$WORK/stale-remove.txt"
  echo "   removed; backup: $BACKUP"
  STALE_REMOVED=1
fi

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

step "cleanup queue"
: > "$WORK/claude-plugins.json"
: > "$WORK/codex-plugins.json"
if [ -n "$CLAUDE" ]; then "$CLAUDE" plugin list --json > "$WORK/claude-plugins.json" 2>/dev/null || true; fi
if [ -n "$CODEX" ]; then "$CODEX" plugin list --json > "$WORK/codex-plugins.json" 2>/dev/null || true; fi
python3 - "$WORK/claude-plugins.json" "$WORK/codex-plugins.json" > "$WORK/plugin-keep.txt" <<'PY'
import json, os, sys

home = os.environ["HOME"]
PROTECTED = (os.path.join(home, ".claude", "skills"), os.path.join(home, ".apm"))


def under(path):
    path = os.path.expanduser(path or "")
    return any(path == p or path.startswith(p + os.sep) for p in PROTECTED)


def installed(path):
    try:
        with open(path) as fh:
            data = json.load(fh)
    except (OSError, ValueError):
        return []
    if isinstance(data, dict):
        data = data.get("installed") or []
    return [e for e in data if isinstance(e, dict)]


for target, path, field in (("claude", sys.argv[1], "installPath"), ("codex", sys.argv[2], "source.url")):
    for entry in installed(path):
        ident = entry.get("id") or ""
        if ident:
            name, _, market = ident.partition("@")
        else:
            name, market = entry.get("name") or "", entry.get("marketplaceName") or ""
        if not name:
            continue
        if field == "installPath":
            root = entry.get("installPath") or ""
        else:
            root = (entry.get("source") or {}).get("url") or ""
        if under(root):
            print("%s\t%s@%s\tapm-generated, %s %s" % (target, name, market, field, root))
PY

: > "$WORK/cleanup.txt"
: > "$WORK/kept.txt"
if [ "$DROP_UNMANAGED" -eq 1 ]; then
  while IFS= read -r line; do
    t="$(printf '%s\n' "$line" | col 2)"; k="$(printf '%s\n' "$line" | col 3)"; id="$(printf '%s\n' "$line" | col 4)"
    case "$t/$k" in
      claude/mcp)    queue "$CLAUDE" mcp remove -s user "$id" ;;
      claude/plugin) queue_plugin_uninstall claude "$id" ;;
      codex/mcp)     queue "$CODEX" mcp remove "$id" ;;
      codex/plugin)  queue_plugin_uninstall codex "$id" ;;
    esac
  done < "$WORK/replaced.txt"
fi
if [ "$REMOVE_DUPES" -eq 1 ]; then
  while IFS= read -r line; do
    t="$(printf '%s\n' "$line" | col 2)"; k="$(printf '%s\n' "$line" | col 3)"; id="$(printf '%s\n' "$line" | col 4)"
    case "$t/$k" in
      claude/plugin) queue_plugin_uninstall claude "$id" ;;
      codex/plugin)  queue_plugin_uninstall codex "$id" ;;
    esac
  done < "$WORK/managed.txt"
fi

if [ ! -s "$WORK/cleanup.txt" ]; then
  echo "   nothing to delete"
  [ "$STALE_REMOVED" -eq 1 ] || exit 0
else
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
fi

step "marketplaces no native plugin references any more"
native_plugin_marketplaces > "$WORK/still-used.txt"
if [ -s "$WORK/cleanup.txt" ]; then cat "$WORK/replaced.txt" "$WORK/managed.txt"; fi | while IFS= read -r line; do
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
