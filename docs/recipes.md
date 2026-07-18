# Recipes

These recipes are copy-paste starting points. Replace hostnames, group names,
tool names, and paths with your real values.

## Set Up A New Laptop From An Existing Host

On the old machine:

```sh
omni hosts list
```

On the new machine:

```sh
omni bootstrap
omni hosts copy old-host new-laptop
omni doctor
omni reconcile
```

Expected result: the new host has its own protected host group plus the reusable
groups copied from `old-host`.

## Add A Tool

```sh
omni groups create dev
omni tools set ripgrep --provider brew
omni groups move-tool dev ripgrep
omni tools sync
```

Expected result: Omni installs `ripgrep` through Homebrew.

## Add A Tool Whose Package Name Differs

```sh
omni tools set node --provider apt --package nodejs
omni groups move-tool dev node
omni tools sync
```

Expected result: the logical tool is `node`, but the package manager receives
`nodejs`.

## Use A Specific Provider For One Tool

```sh
omni tools set typescript --provider pnpm --package typescript
omni groups move-tool dev typescript
omni tools sync
```

Expected result: this one tool uses `pnpm`.

## Grok: Homebrew On macOS, x.ai Script On Linux

Use Homebrew when the formula is available, and fall back to the official x.ai
installer everywhere else (Linux, or macOS when you installed outside Homebrew):

Set `"version": 17` (or let Omni migrate on load). Multi-provider `providers[]`
entries require config version 6 or newer — without a version, the v5→v6
migration can drop hand-authored provider lists that only used the new shape.

```json
"grok": {
  "providers": [
    { "provider": "brew", "package": "grok" },
    {
      "provider": "script",
      "options": {
        "install": "curl -fsSL https://x.ai/cli/install.sh | bash",
        "check": "command -v grok || test -x $HOME/.grok/bin/grok",
        "uninstall": "rm -f \"$HOME/.grok/bin/grok\" \"$HOME/.grok/bin/agent\" \"$HOME/.local/bin/grok\" \"$HOME/.local/bin/agent\" && rm -f \"$HOME/.grok/downloads\"/grok-* && rm -rf \"$HOME/.grok/completions\"",
        "upgrade": "grok update 2>/dev/null || curl -fsSL https://x.ai/cli/install.sh | bash",
        "version": "grok --version"
      }
    }
  ],
  "git": "https://github.com/xai-org/grok-cli"
}
```

On Linux, Omni skips unavailable `brew` and installs through the `script`
candidate. Delete with the script owner when Omni sees grok as installed:

```sh
omni tools delete grok --provider script
```

## Move Python Tools To uv

Preview first:

```sh
omni tools consolidate python uv --dry-run
```

Apply:

```sh
omni tools consolidate python uv
```

Expected result: Python tools move to the `uv` manager and stale installs are
removed best-effort from the old manager.

## Use Node Via nvm

Use nvm for the Node runtime. Let omni manage JS global CLIs through `pnpm`,
`npm`, or `bun` — not a system package manager (`brew`, `apt`, `dnf`, …).

Pin your JS package manager:

```sh
omni settings set ecosystems.node.manager pnpm
```

Preview migration off the system provider:

```sh
omni consolidate --to pnpm --dry-run
```

Apply:

```sh
omni consolidate --to pnpm
omni tools refresh
```

For the Node runtime itself, do not keep `node` as a system-managed tool if nvm
owns it. Remove it from the manifest or stop syncing it after uninstalling the
system package (for example `brew uninstall node` on macOS).

Migrate tools still owned by a system provider:

```sh
omni tools migrate-nvm --all
```

Check for drift:

```sh
omni doctor
```

Expected result: JS globals are configured for `pnpm` (or your chosen manager),
`doctor` reports no system-vs-nvm drift, and nvm remains responsible for Node
versions.

## Adopt Neovim Config Into Dotfiles

```sh
omni settings set dots_repo ~/dotfiles
omni groups create dev
omni dots add ~/.config/nvim --group dev --adopt
omni dots sync
omni dots status nvim
```

Expected result: local Neovim files are copied into the dotfiles repo and the
target path is linked through Stow.

## Ignore Machine-Local Dotfile Noise

```sh
omni dots ignore nvim 'cache/'
omni dots ignore nvim '*.log'
omni dots sync
```

Expected result: cache and log files stop appearing as dotfile drift.

## Use Host-Specific Dotfile Content

```sh
omni dots variant add nvim --host workstation --package nvim@workstation --sync
omni dots variant list nvim
```

Expected result: the logical dot entry stays `nvim`, but `workstation` uses the
`nvim@workstation` Stow package.

## Recover From A Dotfile Conflict

Inspect:

```sh
omni dots status nvim
```

Keep the repo version:

```sh
omni dots resolve nvim --use-repo
```

Keep the local version:

```sh
omni dots resolve nvim --use-local
```

Expected result: the conflict is resolved explicitly, and the entry can sync
again.

## Rebuild Stale Tool State

```sh
omni settings reset-cache
omni tools refresh
omni tools list
```

Expected result: Omni discards local cache rows and rebuilds observed tool state
from providers.

## Headless Health Check

```sh
omni doctor
omni reconcile
```

Expected result: `doctor` reports issues without changing state; `reconcile`
then applies the normal repair pass.
