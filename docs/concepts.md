# Concepts

## The Three Stores

Everything Omni manages exists in up to three places at once, and every verb is
a movement between them:

- **Desired** — what `settings.json` declares. Portable, and the only store you
  edit directly.
- **Managed** — the copy Omni owns on your behalf: the dotfiles repo, the skill
  package store, the local cache.
- **Live** — what the machine or an agent actually runs: installed packages,
  files at their real paths, registered MCP servers.

```text
                 add ─────────────┐
                                  v
   ┌────────────────┐  import   ┌──────────────────┐
   │  DESIRED       │ <──────── │  LIVE            │
   │  settings.json │           │  machine/agents  │
   └────────────────┘ ────────> └──────────────────┘
        ^      ^         sync      ^            ^
        │      │                   │            │
     remove    │ status            │ upgrade    │ resolve
   (--purge ───┼───────────────────┘            │
    also       │            ┌──────────────┐    │
    cleans     └────────────│  MANAGED     │────┘
    live)         status    │  store/repo  │
                            └──────────────┘
```

| Verb | Movement |
| --- | --- |
| `add` | Declare in DESIRED, then converge this host to it. |
| `sync` | DESIRED → LIVE. Install what is missing, repair what drifted, leave what matches. |
| `import` | LIVE → DESIRED. Adopt something the machine already has. |
| `upgrade` | Refresh MANAGED from upstream, then relink LIVE. DESIRED is unchanged. |
| `remove` | Undeclare from DESIRED. LIVE stays; `--purge` cleans it too. |
| `resolve` | Settle a LIVE ≠ MANAGED conflict with `--use-managed` or `--use-local`. Confirmed before it overwrites. |
| `status` | Report one thing's position across all three stores. |

Each surface names the three stores differently:

| Surface | Desired | Managed | Live |
| --- | --- | --- | --- |
| Tools | `tools` + group membership in `settings.json` | the local cache of installed/outdated state | packages the provider has installed |
| Dots | `groups[].dots` entries | the dots repo's stow packages | the real paths in `$HOME` and their symlinks |
| Agent skills | `agents.packages` | Omni's package store under the data dir | the skill entries in each agent's skills directory |
| Agent MCP servers | `agents.mcp_servers` | — (the manifest is the only copy) | registrations inside each agent CLI |
| Agent plugins | `agents.plugins` + `agents.marketplaces` | — (the agent owns the installed copy) | plugins installed in each agent CLI |
| Hosts | `hosts` assignments | — | — (a host is config only) |

The two surfaces with no managed store are why `agents mcp remove` and
`agents plugins remove` take the live side with them and have no `--purge`:
there is no third copy to keep.

## Logical Tools

A logical tool is the name you want to manage, independent of the concrete
package manager that installs it.

```json
{
  "tools": {
    "typescript": { "providers": [{ "provider": "npm", "package": "typescript" }] },
    "black": { "providers": [{ "provider": "pip" }] },
    "ripgrep": { "providers": [{ "provider": "brew" }] }
  }
}
```

Groups reference logical tool names. Omni resolves each logical tool to an
install spec when it syncs a host.

## Providers

Providers are the package managers or registries that install a tool:

- `brew`, `apt`, `apk`, `dnf`, `pacman`, `zypper`
- `bun`, `pnpm`, `npm`
- `uv`, `pip`
- `cargo`

Each logical tool stores one or more concrete provider candidates in
`providers[]`. The first entry is the default for normal install/sync. Search
and bootstrap flows can discover candidates from configured providers and add
high-confidence matches to this list.

See [Providers](providers.md) for provider priority, import behavior, fallback,
and concrete ownership rules.

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
settings can override provider priority, disabled providers, and the dotfiles
repo without changing global config for every machine.

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
- `sync`, `reconcile`, `add`, `remove`, `upgrade`, and dotfile conflict
  resolution can mutate tools, config, local files, or the dotfiles repo.
- Privileged package operations use explicit action-time prompts in the TUI
  instead of hidden password prompts.
