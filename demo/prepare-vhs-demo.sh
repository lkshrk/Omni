#!/usr/bin/env bash
set -euo pipefail

root="${1:-/tmp/omni-vhs-demo}"
omni_bin="${2:-$root/omni}"

case "$root" in
  /tmp/*|/private/tmp/*) ;;
  *) echo "refusing to prepare demo outside a temp directory: $root" >&2; exit 1 ;;
esac

if [[ ! -x "$omni_bin" ]]; then
  echo "omni binary is missing or not executable: $omni_bin" >&2
  exit 1
fi

rm -rf "$root"
mkdir -p "$root/bin" "$root/cache" "$root/home/.config" "$root/dotfiles"

for tool in bash sh cat sleep basename mkdir ln rm mv readlink git tr; do
  if command -v "$tool" >/dev/null 2>&1; then
    ln -sf "$(command -v "$tool")" "$root/bin/$tool"
  fi
done

cat > "$root/bin/stow" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--version" ]]; then
  echo "stow (GNU Stow) 2.4.1"
  exit 0
fi

mode=""
repo=""
target=""
simulate=0
packages=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -R|-D|--adopt)
      mode="$1"
      shift
      ;;
    --simulate)
      simulate=1
      shift
      ;;
    -d)
      repo="${2:-}"
      shift 2
      ;;
    -t)
      target="${2:-}"
      shift 2
      ;;
    *)
      packages+=("$1")
      shift
      ;;
  esac
done

if [[ -z "$mode" || -z "$repo" || -z "$target" ]]; then
  echo "fake stow: missing mode, repo, or target" >&2
  exit 1
fi

shopt -s dotglob nullglob

link_tree() {
  local src="$1"
  local dst="$2"

  if [[ -d "$src" && ! -L "$src" && -d "$dst" && ! -L "$dst" ]]; then
    local child
    for child in "$src"/*; do
      link_tree "$child" "$dst/${child##*/}"
    done
    return
  fi

  mkdir -p "${dst%/*}"
  rm -rf "$dst"
  ln -s "$src" "$dst"
}

unlink_tree() {
  local src="$1"
  local dst="$2"

  if [[ -L "$dst" ]]; then
    local link
    link="$(readlink "$dst" || true)"
    if [[ "$link" == "$src" ]]; then
      rm "$dst"
    fi
    return
  fi

  if [[ -d "$src" && -d "$dst" && ! -L "$dst" ]]; then
    local child
    for child in "$src"/*; do
      unlink_tree "$child" "$dst/${child##*/}"
    done
  fi
}

for pkg in "${packages[@]}"; do
  pkg_dir="$repo/$pkg"
  [[ -d "$pkg_dir" ]] || continue

  if [[ "$simulate" == "1" ]]; then
    continue
  fi

  case "$mode" in
    -R)
      for child in "$pkg_dir"/*; do
        link_tree "$child" "$target/${child##*/}"
      done
      ;;
    -D)
      for child in "$pkg_dir"/*; do
        unlink_tree "$child" "$target/${child##*/}"
      done
      ;;
    --adopt)
      for child in "$pkg_dir"/*; do
        dst="$target/${child##*/}"
        if [[ -e "$dst" || -L "$dst" ]]; then
          mkdir -p "${child%/*}"
          mv "$dst" "$child"
        fi
        link_tree "$child" "$dst"
      done
      ;;
  esac
done
EOF
chmod +x "$root/bin/stow"

cat > "$root/bin/brew" <<'EOF'
#!/usr/bin/env bash
slow_probe() {
  if [[ "${OMNI_VHS_SLOW_PROBES:-}" == "1" ]]; then
    sleep 1.2
  fi
}
cmd="${1:-}"
case "$cmd" in
  --version)
    slow_probe
    echo "Homebrew 4.4.0"
    ;;
  update|install|upgrade|uninstall|tap|untap)
    exit 0
    ;;
  outdated)
    cat <<'JSON'
{"formulae":[{"name":"ripgrep","current_version":"14.2.0"},{"name":"kubectl","current_version":"1.33.1"}],"casks":[]}
JSON
    ;;
  info)
    cat <<'JSON'
{"formulae":[{"name":"ripgrep","full_name":"ripgrep","desc":"fast recursive search","installed":[{"version":"14.1.1","installed_on_request":true}]},{"name":"git","full_name":"git","desc":"distributed version control","installed":[{"version":"2.49.0","installed_on_request":true}]},{"name":"fd","full_name":"fd","desc":"friendly find replacement","installed":[]},{"name":"kubectl","full_name":"kubectl","desc":"Kubernetes CLI","installed":[{"version":"1.32.0","installed_on_request":true}]},{"name":"terraform","full_name":"terraform","desc":"Infrastructure as code","installed":[]},{"name":"jq","full_name":"jq","desc":"JSON processor","installed":[{"version":"1.8.0","installed_on_request":true}]},{"name":"bat","full_name":"bat","desc":"better cat","installed":[{"version":"0.24.0","installed_on_request":true}]},{"name":"fzf","full_name":"fzf","desc":"fuzzy finder","installed":[{"version":"0.56.0","installed_on_request":true}]},{"name":"helm","full_name":"helm","desc":"Kubernetes package manager","installed":[]},{"name":"gh","full_name":"gh","desc":"GitHub CLI","installed":[{"version":"2.66.0","installed_on_request":true}]},{"name":"awscli","full_name":"awscli","desc":"AWS CLI tooling","installed":[]},{"name":"go","full_name":"go","desc":"Go toolchain","installed":[{"version":"1.23.6","installed_on_request":true}]},{"name":"htop","full_name":"htop","desc":"interactive process viewer","installed":[{"version":"3.3.0","installed_on_request":true}]},{"name":"starship","full_name":"starship","desc":"shell prompt","installed":[{"version":"1.22.0","installed_on_request":true}]}],"casks":[{"token":"slack","desc":"team chat","installed":"4.37.101"},{"token":"parsec","desc":"low-latency remote desktop","installed":"150-95","artifacts":[{"pkg":["Parsec.pkg"]},{"uninstall":{"pkgutil":"com.parsec.Parsec"}}]},{"token":"zoom","desc":"video meetings","installed":"6.1.0"},{"token":"docker-desktop","desc":"Docker Desktop","installed":"4.76.0"}]}
JSON
    ;;
  search)
    query="$(printf '%s' "${*: -1}" | tr '[:upper:]' '[:lower:]')"
    case "$query" in
      pre-commit*) echo "pre-commit" ;;
      pre*) echo "pre-commit prettier presenterm prettyping" ;;
      term*) echo "presenterm termshark terminal-notifier" ;;
      jq*) echo "jq jaq gojq" ;;
      lint*) echo "eslint shellcheck markdownlint ruff" ;;
      rip*) echo "ripgrep ripgrep-all" ;;
      *) echo "ripgrep fd fzf jq go git gh docker kubectl starship htop parsec" ;;
    esac
    ;;
  list)
    if [ "${2:-}" = "--cask" ]; then
      echo "slack"
      echo "parsec"
      echo "zoom"
      echo "docker-desktop"
      exit 0
    fi
    name="${@: -1}"
    case "$name" in
      ripgrep) echo "ripgrep 14.1.1" ;;
      git) echo "git 2.49.0" ;;
      kubectl) echo "kubectl 1.32.0" ;;
      jq) echo "jq 1.8.0" ;;
      bat) echo "bat 0.24.0" ;;
      fzf) echo "fzf 0.56.0" ;;
      docker-desktop) echo "docker-desktop 4.76.0" ;;
      gh) echo "gh 2.66.0" ;;
      go) echo "go 1.23.6" ;;
      htop) echo "htop 3.3.0" ;;
      starship) echo "starship 1.22.0" ;;
      parsec) echo "parsec 150-95" ;;
    esac
    ;;
esac
EOF

cat > "$root/bin/pnpm" <<'EOF'
#!/usr/bin/env bash
slow_probe() {
  if [[ "${OMNI_VHS_SLOW_PROBES:-}" == "1" ]]; then
    sleep 1.2
  fi
}
binary="$(basename "$0")"
if [[ "${1:-}" == "--version" ]]; then
  slow_probe
  case "$binary" in
    bun) echo "1.2.0" ;;
    npm) echo "11.0.0" ;;
    *) echo "10.9.0" ;;
  esac
  exit 0
fi
if [[ "$binary" == "pnpm" && ( "${1:-}" == "ls" || "${1:-}" == "list" ) ]]; then
  cat <<'EOF_LIST'
├── typescript@5.8.3
├── eslint@9.25.1
└── vitest@3.1.4
EOF_LIST
  exit 0
fi
if [[ "$binary" == "npm" && ( "${1:-}" == "ls" || "${1:-}" == "list" ) ]]; then
  cat <<'EOF_LIST'
├── prettier@3.5.3
├── markdownlint-cli@0.44.0
└── wrangler@4.14.1
EOF_LIST
  exit 0
fi
if [[ "$binary" == "bun" && ( "${1:-}" == "pm" || "${1:-}" == "ls" || "${1:-}" == "list" ) ]]; then
  echo "└── @aisuite/chub@0.1.4"
  exit 0
fi
if [[ "${1:-}" == "outdated" ]]; then
  if [[ "$binary" == "pnpm" ]]; then
    echo '{"typescript":{"latest":"5.9.3"}}'
  elif [[ "$binary" == "npm" ]]; then
    echo '{"prettier":{"latest":"3.6.2"}}'
  else
    echo '{}'
  fi
  exit 0
fi
exit 0
EOF
ln -sf pnpm "$root/bin/bun"
ln -sf pnpm "$root/bin/npm"

cat > "$root/bin/uv" <<'EOF'
#!/usr/bin/env bash
slow_probe() {
  if [[ "${OMNI_VHS_SLOW_PROBES:-}" == "1" ]]; then
    sleep 1.2
  fi
}
if [[ "${1:-}" == "--version" ]]; then
  slow_probe
  echo "uv 0.7.0"
  exit 0
fi
uv_state="${OMNI_CACHE_DIR:-/tmp}/fake-uv-tools"
if [[ "${1:-}" == "tool" ]]; then
  case "${2:-}" in
    list)
      if [[ "${3:-}" == "--outdated" ]]; then
        echo ""
      else
        echo "black v25.1.0"
        echo "ruff v0.11.8"
        echo "poetry v2.1.3"
        echo "ansible v11.5.0"
        if [[ -e "$uv_state/pre-commit" ]]; then
          echo "pre-commit v4.2.0"
        fi
      fi
      exit 0
      ;;
    install|upgrade)
      mkdir -p "$uv_state"
      : > "$uv_state/${3:-}"
      exit 0
      ;;
    uninstall)
      rm -f "$uv_state/${3:-}"
      exit 0
      ;;
  esac
fi
if [[ "${1:-}" == "pip" && "${2:-}" == "show" ]]; then
  shift 2
  for pkg in "$@"; do
    case "$pkg" in
      black) printf 'Name: black\nVersion: 25.1.0\nSummary: Python formatter\n---\n' ;;
      pytest) printf 'Name: pytest\nVersion: 8.3.5\nSummary: Python test runner\n---\n' ;;
      ruff) printf 'Name: ruff\nVersion: 0.11.8\nSummary: Python linter and formatter\n---\n' ;;
      poetry) printf 'Name: poetry\nVersion: 2.1.3\nSummary: Python packaging and dependency manager\n---\n' ;;
      ansible) printf 'Name: ansible\nVersion: 11.5.0\nSummary: Automation for infrastructure and apps\n---\n' ;;
    esac
  done
  exit 0
fi
exit 0
EOF

cat > "$root/bin/pip3" <<'EOF'
#!/usr/bin/env bash
slow_probe() {
  if [[ "${OMNI_VHS_SLOW_PROBES:-}" == "1" ]]; then
    sleep 1.2
  fi
}
if [[ "${1:-}" == "--version" ]]; then
  slow_probe
  echo "pip 25.0"
elif [[ "${1:-}" == "list" ]]; then
  echo '[{"name":"pre-commit","version":"4.1.0"},{"name":"httpie","version":"3.2.3"},{"name":"awscli","version":"1.38.0"}]'
elif [[ "${1:-}" == "install" ]]; then
  echo 'error: externally-managed-environment' >&2
  echo 'This Python installation is managed by the system package manager.' >&2
  exit 1
elif [[ "${1:-}" == "uninstall" ]]; then
  exit 0
elif [[ "${1:-}" == "show" ]]; then
  shift
  for pkg in "$@"; do
    case "$pkg" in
      pre-commit) printf 'Name: pre-commit\nVersion: 4.1.0\nSummary: Git hook manager\n---\n' ;;
      httpie) printf 'Name: httpie\nVersion: 3.2.3\nSummary: Friendly HTTP client\n---\n' ;;
      awscli) printf 'Name: awscli\nVersion: 1.38.0\nSummary: AWS CLI tooling\n---\n' ;;
      *) exit 1 ;;
    esac
  done
fi
EOF
ln -sf pip3 "$root/bin/pip"
chmod +x "$root/bin/brew" "$root/bin/pnpm" "$root/bin/uv" "$root/bin/pip3"

for agent_bin in claude grok; do
  ln -sf agent-stub "$root/bin/$agent_bin"
done

cat > "$root/bin/agent-stub" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

agent="$(basename "$0")"
args="$*"

case "$agent" in
claude)
  case "$args" in
  "plugins list --json --available")
    printf '%s\n' '{"installed":[{"id":"caveman@lkshrk","version":"1.0.0","scope":"user","enabled":true}],"available":[{"name":"caveman","marketplaceName":"lkshrk","latestVersion":"2.0.0"}]}'
    ;;
  "plugins list --json")
    printf '%s\n' '[{"id":"caveman@lkshrk","version":"1.0.0","scope":"user","enabled":true}]'
    ;;
  "plugins marketplace list --json")
    printf '%s\n' '[{"name":"lkshrk","source":"github","repo":"lkshrk/agent-marketplace"}]'
    ;;
  "mcp list")
    printf 'grafana: https://mcp.example.com (HTTP) - ✔ Connected\n'
    ;;
  *)
    echo "agent-stub: unsupported claude args: $args" >&2
    exit 1
    ;;
  esac
  ;;
grok)
  case "$args" in
  "mcp list --json")
    # Deliberate URL drift lets the Agents screen demonstrate managed/local resolution.
    printf '%s\n' '[{"name":"grafana","url":"https://mcp.example.com/mcp","enabled":true,"scope":"user"}]'
    ;;
  "plugin list --json --available")
    printf '%s\n' '[]'
    ;;
  "plugin list --json")
    printf '%s\n' '[]'
    ;;
  "plugin marketplace list --json")
    printf '%s\n' '[]'
    ;;
  *)
    echo "agent-stub: unsupported grok args: $args" >&2
    exit 1
    ;;
  esac
  ;;
*)
  echo "agent-stub: unknown agent binary: $agent" >&2
  exit 1
  ;;
esac
EOF
chmod +x "$root/bin/agent-stub"

skill_package_id="7327b3821ba34d6bff5c64321da6c5c73399c11f11cd174746e4b15b5c94c020"
skill_package_root="$root/home/.local/share/omni/skills/packages/$skill_package_id"

mkdir -p \
  "$skill_package_root/skills/frontend-design" \
  "$root/home/.claude/skills" \
  "$root/home/.grok/skills"

cat > "$skill_package_root/skills/frontend-design/SKILL.md" <<'EOF'
---
name: frontend-design
description: Opinionated UI craft guidance for agent-generated interfaces and design systems.
---
EOF

cat > "$skill_package_root/.omni-package.json" <<'EOF'
{
  "source": "vercel-labs/agent-skills",
  "ref": "main",
  "selectors": ["frontend-design"],
  "all_skills": false
}
EOF

ln -s \
  "../../.local/share/omni/skills/packages/$skill_package_id/skills/frontend-design" \
  "$root/home/.claude/skills/frontend-design"
ln -s \
  "../../.local/share/omni/skills/packages/$skill_package_id/skills/frontend-design" \
  "$root/home/.grok/skills/frontend-design"

cat > "$root/home/.claude.json" <<'EOF'
{
  "mcpServers": {
    "grafana": {
      "type": "http",
      "url": "https://mcp.example.com"
    }
  }
}
EOF

mkdir -p "$root/home/.claude/plugins/marketplaces/lkshrk/.claude-plugin"
cat > "$root/home/.claude/plugins/marketplaces/lkshrk/.claude-plugin/marketplace.json" <<'EOF'
{
  "plugins": [
    {
      "name": "caveman",
      "description": "Caveman-style commit messages and token discipline for agent sessions.",
      "version": "2.0.0"
    }
  ]
}
EOF

mkdir -p \
  "$root/dotfiles/dotfiles/nvim/.config/nvim" \
  "$root/dotfiles/dotfiles/zsh" \
  "$root/dotfiles/dotfiles/git" \
  "$root/dotfiles/dotfiles/starship/.config" \
  "$root/dotfiles/dotfiles/alacritty/.config/alacritty" \
  "$root/dotfiles/dotfiles/wezterm/.config/wezterm" \
  "$root/dotfiles/dotfiles/tmux" \
  "$root/dotfiles/dotfiles/grok/.grok"

cat > "$root/dotfiles/dotfiles/nvim/.config/nvim/init.lua" <<'EOF'
vim.opt.number = true
vim.opt.termguicolors = true
EOF

cat > "$root/dotfiles/dotfiles/zsh/.zshrc" <<'EOF'
export EDITOR=nvim
alias ll='ls -lah'
EOF

cat > "$root/dotfiles/dotfiles/git/.gitconfig" <<'EOF'
[user]
  name = Demo User
[pull]
  rebase = true
EOF

cat > "$root/dotfiles/dotfiles/starship/.config/starship.toml" <<'EOF'
[character]
success_symbol = "[➜](bold green)"
EOF

cat > "$root/dotfiles/dotfiles/alacritty/.config/alacritty/alacritty.toml" <<'EOF'
[font]
size = 12.0

[window]
opacity = 0.98
EOF

cat > "$root/dotfiles/dotfiles/wezterm/.config/wezterm/wezterm.lua" <<'EOF'
local wezterm = require 'wezterm'
return {color_scheme = "Builtin Solarized Light"}
EOF

cat > "$root/dotfiles/dotfiles/tmux/.tmux.conf" <<'EOF'
set -g mouse on
EOF

cat > "$root/dotfiles/dotfiles/grok/.grok/config.toml" <<'EOF'
default_model = "grok-3"
EOF

git -C "$root/dotfiles" init -q
git -C "$root/dotfiles" add .
git -C "$root/dotfiles" \
  -c user.name="Demo User" \
  -c user.email="demo@example.com" \
  -c commit.gpgSign=false \
  commit -q -m "Seed dotfiles"

cat >> "$root/dotfiles/dotfiles/starship/.config/starship.toml" <<'EOF'
add_newline = false
EOF

cat > "$root/settings.json" <<EOF
{
  "\$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.v20.schema.json",
  "version": 20,
  "settings": {
    "auto_import": true,
    "provider_priority": ["brew", "pnpm", "bun", "npm", "uv", "pip"],
    "dots_repo": "$root/dotfiles",
    "dots_git": { "auto_commit": false, "auto_push": false }
  },
  "agents": {
    "packages": [
      {
        "source": "vercel-labs/agent-skills",
        "ref": "main",
        "skills": ["frontend-design"],
        "agents": ["claude-code", "grok"]
      }
    ],
    "mcp_servers": [
      {
        "name": "grafana",
        "transport": "http",
        "url": "https://mcp.example.com",
        "agents": ["claude-code", "grok"]
      }
    ],
    "marketplaces": [
      {
        "name": "lkshrk",
        "source": "lkshrk/agent-marketplace",
        "agents": ["claude-code"]
      }
    ],
    "plugins": [
      {
        "name": "caveman",
        "marketplace": "lkshrk",
        "agents": ["claude-code"]
      }
    ]
  },
  "ignore": {
    "tools": ["zoom"]
  },
  "hosts": {
    "demo-macbook": ["core", "cloud", "dev"],
    "home-desktop": ["core", "media"]
  },
  "tools": {
    "ripgrep": { "providers": [{ "provider": "brew", "package": "ripgrep" }] },
    "git": { "providers": [{ "provider": "brew", "package": "git" }] },
    "fd": { "providers": [{ "provider": "brew", "package": "fd" }] },
    "typescript": { "providers": [{ "provider": "pnpm", "package": "typescript" }] },
    "eslint": { "providers": [{ "provider": "pnpm", "package": "eslint" }] },
    "vitest": { "providers": [{ "provider": "pnpm", "package": "vitest" }] },
    "prettier": { "providers": [{ "provider": "npm", "package": "prettier" }] },
    "wrangler": { "providers": [{ "provider": "npm", "package": "wrangler" }] },
    "@aisuite/chub": { "providers": [{ "provider": "bun", "package": "@aisuite/chub" }] },
    "black": { "providers": [{ "provider": "uv", "package": "black" }] },
    "ruff": { "providers": [{ "provider": "uv", "package": "ruff" }] },
    "poetry": { "providers": [{ "provider": "uv", "package": "poetry" }] },
    "ansible": { "providers": [{ "provider": "uv", "package": "ansible" }] },
    "httpie": { "providers": [{ "provider": "pip", "package": "httpie" }] },
    "awscli": { "providers": [{ "provider": "pip", "package": "awscli" }] },
    "pytest": { "providers": [{ "provider": "uv", "package": "pytest" }] },
    "pre-commit": { "providers": [{ "provider": "pip", "package": "pre-commit" }] },
    "kubectl": { "providers": [{ "provider": "brew", "package": "kubectl" }] },
    "slack": { "providers": [{ "provider": "brew", "package": "slack" }] },
    "parsec": { "providers": [{ "provider": "brew", "package": "parsec" }] },
    "zoom": { "providers": [{ "provider": "brew", "package": "zoom" }] },
    "jq": { "providers": [{ "provider": "brew", "package": "jq" }] },
    "bat": { "providers": [{ "provider": "brew", "package": "bat" }] },
    "fzf": { "providers": [{ "provider": "brew", "package": "fzf" }] },
    "docker": { "providers": [{ "provider": "brew", "package": "docker-desktop" }] },
    "gh": { "providers": [{ "provider": "brew", "package": "gh" }] },
    "go": { "providers": [{ "provider": "brew", "package": "go" }] },
    "starship": { "providers": [{ "provider": "brew", "package": "starship" }] }
  },
  "groups": [
    {
      "name": "demo-macbook",
      "special": "host",
      "description": "Local demo machine defaults",
      "tools": ["fd", "typescript", "black", "poetry"],
      "dots": [
        { "name": "nvim", "path": "~/.config/nvim" },
        { "name": "zsh", "path": "~/.zshrc" },
        { "name": "git", "path": "~/.gitconfig" }
      ]
    },
    {
      "name": "home-desktop",
      "special": "host",
      "description": "Home machine defaults",
      "tools": []
    },
    {
      "name": "core",
      "description": "Shared shell and source-control tools",
      "tools": ["ripgrep", "git", "starship"]
    },
    {
      "name": "dev",
      "description": "Language servers, linters, and test runners",
      "tools": ["eslint", "vitest", "prettier", "ruff", "httpie", "pytest", "pre-commit", "jq", "bat", "fzf", "go", "@aisuite/chub"],
      "skills": ["vercel-labs/agent-skills"],
      "mcp_servers": ["grafana"],
      "plugins": ["caveman"],
      "marketplaces": ["lkshrk"],
      "dots": [
        { "name": "starship", "path": "~/.config/starship.toml" },
        { "name": "tmux", "path": "~/.tmux.conf" }
      ]
    },
    {
      "name": "cloud",
      "description": "Work infrastructure tools",
      "tools": ["kubectl", "slack", "parsec", "zoom", "docker", "gh", "awscli", "ansible", "wrangler"],
      "dots": [
        { "name": "wezterm", "path": "~/.config/wezterm" },
        { "name": "alacritty", "path": "~/.config/alacritty" },
        { "name": "grok", "path": "~/.grok" }
      ]
    },
    {
      "name": "media",
      "description": "Home-machine tools",
      "tools": []
    }
  ]
}
EOF

cat > "$root/onboarding-settings.json" <<EOF
{
  "\$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.v20.schema.json",
  "version": 20,
  "settings": {
    "provider_priority": ["brew", "pnpm", "bun", "npm", "uv", "pip"]
  },
  "tools": {},
  "hosts": {},
  "groups": []
}
EOF

db="$root/cache/omni.db"

# Let Omni own its cache schema so the fixture cannot drift from migrations.
OMNI_CONFIG="$root/onboarding-settings.json" \
OMNI_CACHE_DIR="$root/cache" \
OMNI_HOSTNAME="demo-macbook" \
HOME="$root/home" \
"$omni_bin" trace list >/dev/null
config_path="$root/settings.json"
if command -v shasum >/dev/null 2>&1; then
  hash_line="$(printf '%s' "$config_path" | shasum -a 256)"
elif command -v sha256sum >/dev/null 2>&1; then
  hash_line="$(printf '%s' "$config_path" | sha256sum)"
elif command -v openssl >/dev/null 2>&1; then
  hash_line="$(printf '%s' "$config_path" | openssl dgst -sha256 -r)"
else
  echo "missing sha256 helper for bootstrap demo marker" >&2
  exit 1
fi
config_hash="${hash_line%% *}"
bootstrap_key="bootstrap.completed.demo-macbook.${config_hash:0:32}"

sqlite3 "$db" <<SQL
INSERT INTO local_state (key, value, updated_at)
VALUES ('$bootstrap_key', 'complete', CURRENT_TIMESTAMP)
ON CONFLICT (key) DO UPDATE SET
  value = EXCLUDED.value,
  updated_at = EXCLUDED.updated_at;

INSERT INTO local_state (key, value, updated_at)
VALUES ('migration.provider_list_cache_cleared', '1', CURRENT_TIMESTAMP)
ON CONFLICT (key) DO UPDATE SET
  value = EXCLUDED.value,
  updated_at = EXCLUDED.updated_at;

INSERT INTO local_state (key, value, updated_at)
VALUES ('migration.tool_metadata_migrated', '1', CURRENT_TIMESTAMP)
ON CONFLICT (key) DO UPDATE SET
  value = EXCLUDED.value,
  updated_at = EXCLUDED.updated_at;

DELETE FROM tool_metadata;
INSERT INTO tool_metadata (name, provider, package, description, updated_at)
VALUES
  ('ripgrep', 'brew', 'ripgrep', 'fast recursive search', CURRENT_TIMESTAMP),
  ('git', 'brew', 'git', 'distributed version control', CURRENT_TIMESTAMP),
  ('fd', 'brew', 'fd', 'friendly find replacement', CURRENT_TIMESTAMP),
  ('typescript', 'pnpm', 'typescript', 'TypeScript compiler', CURRENT_TIMESTAMP),
  ('eslint', 'pnpm', 'eslint', 'JavaScript linting', CURRENT_TIMESTAMP),
  ('vitest', 'pnpm', 'vitest', 'Vite-native test runner', CURRENT_TIMESTAMP),
  ('prettier', 'npm', 'prettier', 'opinionated code formatter', CURRENT_TIMESTAMP),
  ('wrangler', 'npm', 'wrangler', 'Cloudflare developer platform CLI', CURRENT_TIMESTAMP),
  ('@aisuite/chub', 'bun', '@aisuite/chub', 'AI tooling workspace helpers', CURRENT_TIMESTAMP),
  ('black', 'uv', 'black', 'Python formatter', CURRENT_TIMESTAMP),
  ('ruff', 'uv', 'ruff', 'Python linter and formatter', CURRENT_TIMESTAMP),
  ('poetry', 'uv', 'poetry', 'Python packaging and dependency manager', CURRENT_TIMESTAMP),
  ('ansible', 'uv', 'ansible', 'automation for infrastructure and apps', CURRENT_TIMESTAMP),
  ('httpie', 'pip', 'httpie', 'friendly HTTP client', CURRENT_TIMESTAMP),
  ('awscli', 'pip', 'awscli', 'AWS CLI tooling', CURRENT_TIMESTAMP),
  ('pytest', 'uv', 'pytest', 'Python test runner', CURRENT_TIMESTAMP),
  ('pre-commit', 'pip', 'pre-commit', 'Git hook manager', CURRENT_TIMESTAMP),
  ('kubectl', 'brew', 'kubectl', 'Kubernetes CLI', CURRENT_TIMESTAMP),
  ('slack', 'brew', 'slack', 'team chat', CURRENT_TIMESTAMP),
  ('parsec', 'brew', 'parsec', 'low-latency remote desktop', CURRENT_TIMESTAMP),
  ('zoom', 'brew', 'zoom', 'video meetings', CURRENT_TIMESTAMP),
  ('jq', 'brew', 'jq', 'JSON processor', CURRENT_TIMESTAMP),
  ('bat', 'brew', 'bat', 'better cat', CURRENT_TIMESTAMP),
  ('fzf', 'brew', 'fzf', 'fuzzy finder', CURRENT_TIMESTAMP),
  ('docker', 'brew', 'docker-desktop', 'Docker Desktop', CURRENT_TIMESTAMP),
  ('gh', 'brew', 'gh', 'GitHub CLI', CURRENT_TIMESTAMP),
  ('go', 'brew', 'go', 'Go toolchain', CURRENT_TIMESTAMP),
  ('starship', 'brew', 'starship', 'shell prompt', CURRENT_TIMESTAMP),
  ('htop', 'brew', 'htop', 'interactive process viewer', CURRENT_TIMESTAMP);

DELETE FROM tool_cache;
INSERT INTO tool_cache
  (name, provider, package, installed, installed_with, version, outdated, latest_version, description, last_checked, tracked)
VALUES
  ('ripgrep', 'brew', 'ripgrep', 1, 'brew', '14.1.1', 1, '14.2.0', 'fast recursive search', CURRENT_TIMESTAMP, 1),
  ('git', 'brew', 'git', 1, 'brew', '2.49.0', 0, NULL, 'distributed version control', CURRENT_TIMESTAMP, 1),
  ('fd', 'brew', 'fd', 0, '', NULL, 0, NULL, 'friendly find replacement', CURRENT_TIMESTAMP, 1),
  ('typescript', 'pnpm', 'typescript', 1, 'pnpm', '5.8.3', 1, '5.9.3', 'TypeScript compiler', CURRENT_TIMESTAMP, 1),
  ('eslint', 'pnpm', 'eslint', 1, 'pnpm', '9.25.1', 0, NULL, 'JavaScript linting', CURRENT_TIMESTAMP, 1),
  ('vitest', 'pnpm', 'vitest', 1, 'pnpm', '3.1.4', 0, NULL, 'Vite-native test runner', CURRENT_TIMESTAMP, 1),
  ('prettier', 'npm', 'prettier', 1, 'npm', '3.5.3', 1, '3.6.2', 'opinionated code formatter', CURRENT_TIMESTAMP, 1),
  ('wrangler', 'npm', 'wrangler', 1, 'npm', '4.14.1', 0, NULL, 'Cloudflare developer platform CLI', CURRENT_TIMESTAMP, 1),
  ('@aisuite/chub', 'bun', '@aisuite/chub', 1, 'bun', '0.1.4', 0, NULL, 'AI tooling workspace helpers', CURRENT_TIMESTAMP, 1),
  ('black', 'uv', 'black', 1, 'uv', '25.1.0', 0, NULL, 'Python formatter', CURRENT_TIMESTAMP, 1),
  ('ruff', 'uv', 'ruff', 1, 'uv', '0.11.8', 0, NULL, 'Python linter and formatter', CURRENT_TIMESTAMP, 1),
  ('poetry', 'uv', 'poetry', 1, 'uv', '2.1.3', 0, NULL, 'Python packaging and dependency manager', CURRENT_TIMESTAMP, 1),
  ('ansible', 'uv', 'ansible', 1, 'uv', '11.5.0', 0, NULL, 'automation for infrastructure and apps', CURRENT_TIMESTAMP, 1),
  ('httpie', 'pip', 'httpie', 1, 'pip', '3.2.3', 0, NULL, 'friendly HTTP client', CURRENT_TIMESTAMP, 1),
  ('awscli', 'pip', 'awscli', 1, 'pip', '1.38.0', 0, NULL, 'AWS CLI tooling', CURRENT_TIMESTAMP, 1),
  ('pytest', 'uv', 'pytest', 0, '', NULL, 0, NULL, 'Python test runner', CURRENT_TIMESTAMP, 1),
  ('pre-commit', 'pip', 'pre-commit', 1, 'pip', '4.1.0', 1, '4.2.0', 'Git hook manager', CURRENT_TIMESTAMP, 1),
  ('kubectl', 'brew', 'kubectl', 1, 'brew', '1.32.0', 1, '1.33.1', 'Kubernetes CLI', CURRENT_TIMESTAMP, 1),
  ('slack', 'brew', 'slack', 1, 'brew', '4.37.101', 0, NULL, 'team chat', CURRENT_TIMESTAMP, 1),
  ('parsec', 'brew', 'parsec', 1, 'brew', '150-95', 0, NULL, 'low-latency remote desktop', CURRENT_TIMESTAMP, 1),
  ('zoom', 'brew', 'zoom', 1, 'brew', '6.1.0', 0, NULL, 'video meetings', CURRENT_TIMESTAMP, 1),
  ('jq', 'brew', 'jq', 1, 'brew', '1.8.0', 0, NULL, 'JSON processor', CURRENT_TIMESTAMP, 1),
  ('bat', 'brew', 'bat', 1, 'brew', '0.24.0', 0, NULL, 'better cat', CURRENT_TIMESTAMP, 1),
  ('fzf', 'brew', 'fzf', 1, 'brew', '0.56.0', 0, NULL, 'fuzzy finder', CURRENT_TIMESTAMP, 1),
  ('docker', 'brew', 'docker-desktop', 1, 'brew', '4.76.0', 0, NULL, 'Docker Desktop', CURRENT_TIMESTAMP, 1),
  ('gh', 'brew', 'gh', 1, 'brew', '2.66.0', 0, NULL, 'GitHub CLI', CURRENT_TIMESTAMP, 1),
  ('go', 'brew', 'go', 1, 'brew', '1.23.6', 0, NULL, 'Go toolchain', CURRENT_TIMESTAMP, 1),
  ('starship', 'brew', 'starship', 1, 'brew', '1.22.0', 0, NULL, 'shell prompt', CURRENT_TIMESTAMP, 1),
  ('htop', 'brew', 'htop', 1, 'brew', '3.3.0', 0, NULL, 'interactive process viewer', CURRENT_TIMESTAMP, 0);

UPDATE tool_cache
SET privilege = 'maybe',
    privilege_reason = 'brew cask parsec uses pkgutil uninstall',
    privilege_at = CURRENT_TIMESTAMP
WHERE name = 'parsec' AND provider = 'brew' AND package = 'parsec';

DELETE FROM command_traces;
INSERT INTO command_traces
  (started_at, finished_at, duration_ms, reason, command, status, exit_code, error, stderr)
VALUES
  (datetime('now', '-2 minutes'), datetime('now', '-119 seconds'), 320,
   'upgrading ripgrep (brew)', 'brew upgrade ripgrep', 'success', 0, '', ''),
  (datetime('now', '-90 seconds'), datetime('now', '-89 seconds'), 180,
   'syncing dotfiles', 'stow -R -d $root/dotfiles/dotfiles -t $root/home nvim zsh git',
   'success', 0, '', ''),
  (datetime('now', '-45 seconds'), datetime('now', '-44 seconds'), 95,
   'checking fallback pre-commit (uv)', 'uv tool list', 'success', 0, '', ''),
  (datetime('now', '-20 seconds'), datetime('now', '-19 seconds'), 140,
   'restoring agent skills', 'install vercel-labs/agent-skills at ref main for configured targets', 'success', 0, '', '');
SQL

echo "$root"
