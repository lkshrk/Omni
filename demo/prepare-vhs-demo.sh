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
{"formulae":[{"name":"ripgrep","full_name":"ripgrep","desc":"fast recursive search","installed":[{"version":"14.1.1","installed_on_request":true}]},{"name":"git","full_name":"git","desc":"distributed version control","installed":[{"version":"2.49.0","installed_on_request":true}]},{"name":"fd","full_name":"fd","desc":"friendly find replacement","installed":[]},{"name":"kubectl","full_name":"kubectl","desc":"Kubernetes CLI","installed":[{"version":"1.32.0","installed_on_request":true}]},{"name":"terraform","full_name":"terraform","desc":"Infrastructure as code","installed":[]},{"name":"jq","full_name":"jq","desc":"JSON processor","installed":[{"version":"1.8.0","installed_on_request":true}]},{"name":"bat","full_name":"bat","desc":"better cat","installed":[{"version":"0.24.0","installed_on_request":true}]},{"name":"fzf","full_name":"fzf","desc":"fuzzy finder","installed":[{"version":"0.56.0","installed_on_request":true}]},{"name":"helm","full_name":"helm","desc":"Kubernetes package manager","installed":[]},{"name":"docker","full_name":"docker","desc":"container runtime","installed":[{"version":"27.3.1","installed_on_request":true}]},{"name":"gh","full_name":"gh","desc":"GitHub CLI","installed":[{"version":"2.66.0","installed_on_request":true}]},{"name":"awscli","full_name":"awscli","desc":"AWS CLI tooling","installed":[]},{"name":"go","full_name":"go","desc":"Go toolchain","installed":[{"version":"1.23.6","installed_on_request":true}]},{"name":"htop","full_name":"htop","desc":"interactive process viewer","installed":[{"version":"3.3.0","installed_on_request":true}]},{"name":"starship","full_name":"starship","desc":"shell prompt","installed":[{"version":"1.22.0","installed_on_request":true}]}],"casks":[{"token":"slack","desc":"team chat","installed":"4.37.101"},{"token":"parsec","desc":"low-latency remote desktop","installed":"150-95","artifacts":[{"pkg":["Parsec.pkg"]},{"uninstall":{"pkgutil":"com.parsec.Parsec"}}]},{"token":"zoom","desc":"video meetings","installed":"6.1.0"}]}
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
    name="${@: -1}"
    case "$name" in
      ripgrep) echo "ripgrep 14.1.1" ;;
      git) echo "git 2.49.0" ;;
      kubectl) echo "kubectl 1.32.0" ;;
      jq) echo "jq 1.8.0" ;;
      bat) echo "bat 0.24.0" ;;
      fzf) echo "fzf 0.56.0" ;;
      docker) echo "docker 27.3.1" ;;
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

mkdir -p \
  "$root/dotfiles/dotfiles/nvim/.config/nvim" \
  "$root/dotfiles/dotfiles/zsh" \
  "$root/dotfiles/dotfiles/git" \
  "$root/dotfiles/dotfiles/starship/.config" \
  "$root/dotfiles/dotfiles/alacritty/.config/alacritty" \
  "$root/dotfiles/dotfiles/wezterm/.config/wezterm" \
  "$root/dotfiles/dotfiles/tmux"

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

git -C "$root/dotfiles" init -q
git -C "$root/dotfiles" add .
git -C "$root/dotfiles" \
  -c user.name="Demo User" \
  -c user.email="demo@example.com" \
  -c commit.gpgSign=false \
  commit -q -m "Seed dotfiles"

cat > "$root/settings.json" <<EOF
{
  "\$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.schema.json",
  "settings": {
    "auto_import": true,
    "ecosystems": {
      "system": { "manager": "brew", "priority": ["brew"] },
      "node": { "manager": "pnpm", "priority": ["pnpm", "bun", "npm"] },
      "python": { "manager": "uv", "priority": ["uv", "pip3", "pip"] }
    },
    "dots_repo": "$root/dotfiles",
    "dots_git": { "auto_commit": false, "auto_push": false }
  },
  "ignore": {
    "tools": ["zoom"]
  },
  "hosts": {
    "demo-macbook": ["core", "cloud", "dev"],
    "home-desktop": ["core", "media"]
  },
  "tools": {
    "ripgrep": { "provider": "system", "package": "ripgrep" },
    "git": { "provider": "system", "package": "git" },
    "fd": { "provider": "system", "package": "fd" },
    "typescript": { "provider": "node", "package": "typescript", "install_with": "pnpm" },
    "eslint": { "provider": "node", "package": "eslint", "install_with": "pnpm" },
    "vitest": { "provider": "node", "package": "vitest", "install_with": "pnpm" },
    "prettier": { "provider": "node", "package": "prettier", "install_with": "npm" },
    "wrangler": { "provider": "node", "package": "wrangler", "install_with": "npm" },
    "@aisuite/chub": { "provider": "node", "package": "@aisuite/chub", "install_with": "bun" },
    "black": { "provider": "python", "package": "black", "install_with": "uv" },
    "ruff": { "provider": "python", "package": "ruff", "install_with": "uv" },
    "poetry": { "provider": "python", "package": "poetry", "install_with": "uv" },
    "ansible": { "provider": "python", "package": "ansible", "install_with": "uv" },
    "httpie": { "provider": "python", "package": "httpie", "install_with": "pip3" },
    "awscli": { "provider": "python", "package": "awscli", "install_with": "pip3" },
    "pytest": { "provider": "python", "package": "pytest", "install_with": "uv" },
    "pre-commit": { "provider": "python", "package": "pre-commit", "install_with": "pip3" },
    "kubectl": { "provider": "system", "package": "kubectl" },
    "slack": { "provider": "system", "package": "slack", "install_with": "brew" },
    "parsec": { "provider": "system", "package": "parsec" },
    "zoom": { "provider": "system", "package": "zoom" },
    "jq": { "provider": "system", "package": "jq" },
    "bat": { "provider": "system", "package": "bat" },
    "fzf": { "provider": "system", "package": "fzf" },
    "docker": { "provider": "system", "package": "docker" },
    "gh": { "provider": "system", "package": "gh" },
    "go": { "provider": "system", "package": "go" },
    "starship": { "provider": "system", "package": "starship" }
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
        { "name": "alacritty", "path": "~/.config/alacritty" }
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
  "\$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.schema.json",
  "settings": {
    "ecosystems": {
      "system": { "manager": "brew", "priority": ["brew"] },
      "node": { "manager": "pnpm", "priority": ["pnpm", "bun", "npm"] },
      "python": { "manager": "uv", "priority": ["uv", "pip3", "pip"] }
    }
  },
  "tools": {},
  "hosts": {},
  "groups": []
}
EOF

OMNI_HOSTNAME=demo-macbook HOME="$root/home" PATH="$root/bin" \
  "$omni_bin" --config "$root/settings.json" --cache-dir "$root/cache" providers >/dev/null

db="$root/cache/omni.db"

sqlite3 "$db" <<SQL
DELETE FROM tool_cache;
INSERT INTO tool_cache
  (name, provider, package, installed, installed_with, version, outdated, latest_version, description, last_checked, tracked)
VALUES
  ('ripgrep', 'system', 'ripgrep', 1, 'brew', '14.1.1', 1, '14.2.0', 'fast recursive search', CURRENT_TIMESTAMP, 1),
  ('git', 'system', 'git', 1, 'brew', '2.49.0', 0, NULL, 'distributed version control', CURRENT_TIMESTAMP, 1),
  ('fd', 'system', 'fd', 0, '', NULL, 0, NULL, 'friendly find replacement', CURRENT_TIMESTAMP, 1),
  ('typescript', 'node', 'typescript', 1, 'pnpm', '5.8.3', 1, '5.9.3', 'TypeScript compiler', CURRENT_TIMESTAMP, 1),
  ('eslint', 'node', 'eslint', 1, 'pnpm', '9.25.1', 0, NULL, 'JavaScript linting', CURRENT_TIMESTAMP, 1),
  ('vitest', 'node', 'vitest', 1, 'pnpm', '3.1.4', 0, NULL, 'Vite-native test runner', CURRENT_TIMESTAMP, 1),
  ('prettier', 'node', 'prettier', 1, 'npm', '3.5.3', 1, '3.6.2', 'opinionated code formatter', CURRENT_TIMESTAMP, 1),
  ('wrangler', 'node', 'wrangler', 1, 'npm', '4.14.1', 0, NULL, 'Cloudflare developer platform CLI', CURRENT_TIMESTAMP, 1),
  ('@aisuite/chub', 'node', '@aisuite/chub', 1, 'bun', '0.1.4', 0, NULL, 'AI tooling workspace helpers', CURRENT_TIMESTAMP, 1),
  ('black', 'python', 'black', 1, 'uv', '25.1.0', 0, NULL, 'Python formatter', CURRENT_TIMESTAMP, 1),
  ('ruff', 'python', 'ruff', 1, 'uv', '0.11.8', 0, NULL, 'Python linter and formatter', CURRENT_TIMESTAMP, 1),
  ('poetry', 'python', 'poetry', 1, 'uv', '2.1.3', 0, NULL, 'Python packaging and dependency manager', CURRENT_TIMESTAMP, 1),
  ('ansible', 'python', 'ansible', 1, 'uv', '11.5.0', 0, NULL, 'automation for infrastructure and apps', CURRENT_TIMESTAMP, 1),
  ('httpie', 'python', 'httpie', 1, 'pip3', '3.2.3', 0, NULL, 'friendly HTTP client', CURRENT_TIMESTAMP, 1),
  ('awscli', 'python', 'awscli', 1, 'pip3', '1.38.0', 0, NULL, 'AWS CLI tooling', CURRENT_TIMESTAMP, 1),
  ('pytest', 'python', 'pytest', 0, '', NULL, 0, NULL, 'Python test runner', CURRENT_TIMESTAMP, 1),
  ('pre-commit', 'python', 'pre-commit', 1, 'pip3', '4.1.0', 1, '4.2.0', 'Git hook manager', CURRENT_TIMESTAMP, 1),
  ('kubectl', 'system', 'kubectl', 1, 'brew', '1.32.0', 1, '1.33.1', 'Kubernetes CLI', CURRENT_TIMESTAMP, 1),
  ('slack', 'system', 'slack', 1, 'brew', '4.37.101', 0, NULL, 'team chat', CURRENT_TIMESTAMP, 1),
  ('parsec', 'system', 'parsec', 1, 'brew', '150-95', 0, NULL, 'low-latency remote desktop', CURRENT_TIMESTAMP, 1),
  ('zoom', 'system', 'zoom', 1, 'brew', '6.1.0', 0, NULL, 'video meetings', CURRENT_TIMESTAMP, 1),
  ('jq', 'system', 'jq', 1, 'brew', '1.8.0', 0, NULL, 'JSON processor', CURRENT_TIMESTAMP, 1),
  ('bat', 'system', 'bat', 1, 'brew', '0.24.0', 0, NULL, 'better cat', CURRENT_TIMESTAMP, 1),
  ('fzf', 'system', 'fzf', 1, 'brew', '0.56.0', 0, NULL, 'fuzzy finder', CURRENT_TIMESTAMP, 1),
  ('docker', 'system', 'docker', 1, 'brew', '27.3.1', 0, NULL, 'container runtime', CURRENT_TIMESTAMP, 1),
  ('gh', 'system', 'gh', 1, 'brew', '2.66.0', 0, NULL, 'GitHub CLI', CURRENT_TIMESTAMP, 1),
  ('go', 'system', 'go', 1, 'brew', '1.23.6', 0, NULL, 'Go toolchain', CURRENT_TIMESTAMP, 1),
  ('starship', 'system', 'starship', 1, 'brew', '1.22.0', 0, NULL, 'shell prompt', CURRENT_TIMESTAMP, 1),
  ('htop', 'system', 'htop', 1, 'brew', '3.3.0', 0, NULL, 'interactive process viewer', CURRENT_TIMESTAMP, 0);

UPDATE tool_cache
SET privilege = 'maybe',
    privilege_reason = 'brew cask parsec uses pkgutil uninstall',
    privilege_at = CURRENT_TIMESTAMP
WHERE name = 'parsec' AND provider = 'system' AND package = 'parsec';
SQL

echo "$root"
