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

for tool in git stow; do
  if command -v "$tool" >/dev/null 2>&1; then
    ln -sf "$(command -v "$tool")" "$root/bin/$tool"
  fi
done

mkdir -p \
  "$root/dotfiles/nvim/.config/nvim" \
  "$root/dotfiles/zsh" \
  "$root/dotfiles/git" \
  "$root/dotfiles/starship/.config" \
  "$root/dotfiles/alacritty/.config" \
  "$root/dotfiles/wezterm" \
  "$root/dotfiles/tmux"

cat > "$root/dotfiles/nvim/.config/nvim/init.lua" <<'EOF'
vim.opt.number = true
vim.opt.termguicolors = true
EOF

cat > "$root/dotfiles/zsh/.zshrc" <<'EOF'
export EDITOR=nvim
alias ll='ls -lah'
EOF

cat > "$root/dotfiles/git/.gitconfig" <<'EOF'
[user]
  name = Demo User
[pull]
  rebase = true
EOF

cat > "$root/dotfiles/starship/.config/starship.toml" <<'EOF'
[character]
success_symbol = "[➜](bold green)"
EOF

cat > "$root/dotfiles/alacritty/.config/alacritty.toml" <<'EOF'
[font]
size = 12.0

[window]
opacity = 0.98
EOF

cat > "$root/dotfiles/wezterm/wezterm.lua" <<'EOF'
local wezterm = require 'wezterm'
return {color_scheme = "Builtin Solarized Light"}
EOF

cat > "$root/dotfiles/tmux/.tmux.conf" <<'EOF'
set -g mouse on
EOF

cat > "$root/home/.zshrc" <<'EOF'
export EDITOR=vim
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
      "system": { "manager": "brew", "priority": ["brew", "apt", "pacman"] },
      "node": { "manager": "pnpm", "priority": ["pnpm", "bun", "npm"] },
      "python": { "manager": "uv", "priority": ["uv", "pip3", "pip"] }
    },
    "dots_repo": "$root/dotfiles",
    "dots_git": { "auto_commit": false, "auto_push": false }
  },
  "hostnames": {
    "demo-macbook": "work"
  },
  "profiles": {
    "work": { "groups": ["base", "cloud", "dev"], "ignore": ["zoom"] },
    "home": { "groups": ["base", "media"], "ignore": ["slack"] }
  },
  "tools": {
    "ripgrep": { "provider": "system", "package": "ripgrep" },
    "git": { "provider": "system", "package": "git" },
    "fd": { "provider": "system", "package": "fd" },
    "typescript": { "provider": "node", "package": "typescript", "install_with": "pnpm" },
    "eslint": { "provider": "node", "package": "eslint", "install_with": "pnpm" },
    "black": { "provider": "python", "package": "black", "install_with": "uv" },
    "pytest": { "provider": "python", "package": "pytest", "install_with": "uv" },
    "kubectl": { "provider": "system", "package": "kubectl" },
    "terraform": { "provider": "system", "package": "terraform" },
    "slack": { "provider": "system", "package": "slack" },
    "zoom": { "provider": "system", "package": "zoom" },
    "jq": { "provider": "system", "package": "jq" },
    "bat": { "provider": "system", "package": "bat" },
    "fzf": { "provider": "system", "package": "fzf" },
    "helm": { "provider": "system", "package": "helm" },
    "docker": { "provider": "system", "package": "docker" },
    "gh": { "provider": "system", "package": "gh" },
    "awscli": { "provider": "system", "package": "awscli", "install_with": "brew" },
    "go": { "provider": "system", "package": "go" },
    "starship": { "provider": "system", "package": "starship" }
  },
  "groups": [
    {
      "tools": ["ripgrep", "git", "fd", "typescript", "black"],
      "dots": [
        { "name": "nvim", "path": "~/.config/nvim" },
        { "name": "zsh", "path": "~/.zshrc" },
        { "name": "git", "path": "~/.gitconfig" }
      ]
    },
    {
      "name": "dev",
      "description": "Language servers, linters, and test runners",
      "tools": ["eslint", "pytest", "jq", "bat", "fzf", "go"],
      "dots": [
        { "name": "starship", "path": "~/.config/starship.toml" },
        { "name": "tmux", "path": "~/.tmux.conf" }
      ]
    },
    {
      "name": "cloud",
      "description": "Work infrastructure tools",
      "tools": ["kubectl", "terraform", "slack", "zoom", "helm", "docker", "gh", "awscli"],
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

OMNI_HOSTNAME=demo-macbook HOME="$root/home" \
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
  ('black', 'python', 'black', 1, 'uv', '25.1.0', 0, NULL, 'Python formatter', CURRENT_TIMESTAMP, 1),
  ('pytest', 'python', 'pytest', 0, '', NULL, 0, NULL, 'Python test runner', CURRENT_TIMESTAMP, 1),
  ('kubectl', 'system', 'kubectl', 1, 'brew', '1.32.0', 1, '1.33.1', 'Kubernetes CLI', CURRENT_TIMESTAMP, 1),
  ('terraform', 'system', 'terraform', 0, '', NULL, 0, NULL, 'Infrastructure as code', CURRENT_TIMESTAMP, 1),
  ('slack', 'system', 'slack', 1, 'apt', '4.37.101', 0, NULL, 'team chat', CURRENT_TIMESTAMP, 1),
  ('zoom', 'system', 'zoom', 1, 'brew', '6.1.0', 0, NULL, 'video meetings', CURRENT_TIMESTAMP, 1),
  ('jq', 'system', 'jq', 1, 'brew', '1.8.0', 1, '1.8.1', 'JSON processor', CURRENT_TIMESTAMP, 1),
  ('bat', 'system', 'bat', 1, 'brew', '0.24.0', 1, '0.24.1', 'better cat', CURRENT_TIMESTAMP, 1),
  ('fzf', 'system', 'fzf', 1, 'brew', '0.56.0', 0, NULL, 'fuzzy finder', CURRENT_TIMESTAMP, 1),
  ('helm', 'system', 'helm', 0, '', NULL, 0, NULL, 'Kubernetes package manager', CURRENT_TIMESTAMP, 1),
  ('docker', 'system', 'docker', 1, 'brew', '27.3.1', 1, '27.4.0', 'container runtime', CURRENT_TIMESTAMP, 1),
  ('gh', 'system', 'gh', 1, 'brew', '2.66.0', 0, NULL, 'GitHub CLI', CURRENT_TIMESTAMP, 1),
  ('awscli', 'system', 'awscli', 0, '', NULL, 0, NULL, 'AWS CLI tooling', CURRENT_TIMESTAMP, 1),
  ('go', 'system', 'go', 1, 'brew', '1.23.6', 1, '1.24.0', 'Go toolchain', CURRENT_TIMESTAMP, 1),
  ('starship', 'system', 'starship', 1, 'brew', '1.22.0', 0, NULL, 'shell prompt', CURRENT_TIMESTAMP, 1),
  ('htop', 'system', 'htop', 1, 'brew', '3.3.0', 0, NULL, 'interactive process viewer', CURRENT_TIMESTAMP, 0);
SQL

echo "$root"
