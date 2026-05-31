# Concepts

## Logical Tools

A logical tool is the name you want to manage, independent of the concrete
package manager that installs it.

```json
{
  "tools": {
    "typescript": { "provider": "node", "package": "typescript" },
    "black": { "provider": "python" },
    "ripgrep": { "provider": "system" }
  }
}
```

Groups reference logical tool names. Omni resolves each logical tool to an
install spec when it syncs a host.

## Ecosystem Providers

Ecosystem providers are portable identities:

- `system`
- `node`
- `python`

Concrete managers are the actual package managers:

- `brew`, `apt`, `apk`, `dnf`, `pacman`, `zypper`
- `bun`, `pnpm`, `npm`
- `uv`, `pip3`

Prefer ecosystem providers in config. Use `install_with` only when one tool
must use a specific concrete manager.

See [Providers](providers.md) for concrete resolution order, manager defaults,
import normalization, and `install_with` decision rules.

## Groups

Groups collect tools and dotfiles. Reusable groups are assigned to hosts:

```json
{
  "hosts": {
    "laptop": ["dev", "work"]
  }
}
```

Each host also has a protected special host group. That group is the local inbox
for machine-specific tools and dotfiles.

## Hosts

The active host is the short hostname, or `OMNI_HOSTNAME` when set. Host
settings can override manager choices, disabled providers, and the dotfiles repo
without changing global config for every machine.

## Dot Entries

A dot entry names one managed path:

```json
{
  "name": "nvim",
  "path": "~/.config/nvim",
  "package": "nvim"
}
```

The default package is the dot name. Host variants can map the same logical
entry to different Stow packages on different machines.

## Cache

Omni stores observed tool state in a local SQLite cache under the cache
directory. It makes the TUI fast and records useful local evidence such as
installed versions, ownership, and privilege metadata. The cache is still
derived state: the portable source of truth is `settings.json` plus the
dotfiles repo.

If cache state looks stale:

```sh
omni settings reset-cache
omni tools refresh
```

See [State And Files](state-and-files.md) for exact path resolution, environment
variables, and source-of-truth rules.

## Safety Model

- `doctor` is read-only.
- `dots status`, `dots list`, and `tools list` inspect state.
- `sync`, `reconcile`, `add`, `delete`, `upgrade`, and dotfile conflict
  resolution can mutate tools, config, local files, or the dotfiles repo.
- Privileged package operations use explicit action-time prompts in the TUI
  instead of hidden password prompts.
