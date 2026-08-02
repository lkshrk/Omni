# Configuration

Omni stores configuration in JSON:

```text
~/.config/omni/settings.json
```

Override the path with:

```sh
omni --config /path/to/settings.json
```

See [State And Files](state-and-files.md) for config path priority, cache path
priority, environment variables, backups, and disposable cache behavior.

The current schema lives at
[`spec/omni.settings.schema.json`](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.schema.json).
Omni migrates older files to the current version on load.

## Smallest Valid File

The smallest legal file is:

```json
{
  "version": 22
}
```

`bootstrap` normally creates the host and group scaffolding around that minimum
shape. `settings` is optional when every setting should use the default. The
`$schema` key is optional for Omni, but useful for editors and written
automatically by Omni config writes.

## Minimal Example

```json
{
  "$schema": "https://raw.githubusercontent.com/lkshrk/omni/main/spec/omni.settings.schema.json",
  "version": 22,
  "settings": {
    "fallback_bin_dir": "~/.local/share/omni/fallback/bin",
    "provider_priority": ["brew", "apt", "dnf", "zypper", "pacman", "apk", "npm", "pip"]
  },
  "tools": {
    "ripgrep": { "providers": [{ "provider": "brew" }] },
    "typescript": { "providers": [{ "provider": "npm", "package": "typescript" }] },
    "black": { "providers": [{ "provider": "pip" }] }
  },
  "hosts": {
    "workstation": ["dev"]
  },
  "groups": [
    {
      "name": "dev",
      "tools": ["ripgrep", "typescript", "black"],
      "dots": [
        { "name": "nvim", "path": "~/.config/nvim" }
      ]
    },
    { "name": "workstation", "special": "host" }
  ]
}
```

## Top-Level Keys

| Key | Purpose |
| --- | --- |
| `$schema` | Editor schema URI. Omni injects it on writes. |
| `version` | Settings format version. |
| `settings` | Global defaults for providers, dotfiles, Git behavior, and imports. |
| `host_settings` | Per-host overrides for selected settings. |
| `tools` | Logical tool specs keyed by logical tool name. |
| `groups` | Reusable and special host groups. |
| `hosts` | Host to reusable-group assignments. |
| `ignore` | Global tool and dotfile ignore lists. |
| `agents` | Agent skill packages, MCP servers, marketplaces, plugins, and agent-specific ignores. |

## Settings

```json
{
  "settings": {
    "auto_import": false,
    "update_quarantine": "2d",
    "provider_update_quarantine": {
      "npm": "1d",
      "pip": "3d"
    },
    "provider_priority": ["brew", "apt", "dnf", "zypper", "pacman", "apk", "npm", "pip"],
    "dots_repo": "~/dotfiles",
    "dots_git": {
      "auto_commit": false,
      "auto_push": false
    },
    "providers": [
      { "name": "uv", "provider": "brew" }
    ]
  }
}
```

Common keys:

| Key | Description |
| --- | --- |
| `auto_import` | Add newly discovered installed tools during scoped plain sync. Defaults to `false`. |
| `update_quarantine` | Defer upgrades until the PM-reported latest-version availability date is older than this duration. Empty or omitted disables quarantine. |
| `provider_update_quarantine` | Duration overrides keyed by provider (`npm`, `pip`, `brew`). |
| `provider_priority` | Preferred provider order for search/bootstrap choices and high-confidence auto selection. |
| `dots_repo` | Local path to the Git-backed dotfiles repo. |
| `dots_git.auto_commit` | Commit dotfile repo changes after add/remove flows. |
| `dots_git.auto_push` | Push dotfile repo changes after add/remove flows. |
| `fallback_bin_dir` | Default directory for fallback-installed binaries. Omni warns if it is not on `PATH`; it does not edit shell files automatically. |
| `providers` | Bootstrap provider tools installed before dependent tools. |
| `agents_use` | Agent IDs managed on this machine. Omit to manage every detected agent; an explicit empty list manages none. |

Use CLI settings commands when possible:

```sh
omni settings show
omni settings set dots_repo ~/dotfiles
```

See [Providers](providers.md) for provider priority, candidate selection, and
concrete ownership.

`auto_import` does not control `omni tools sync --all` or `omni reconcile`.
Those broad commands explicitly claim discovered installed tools into the
machine group. Leave `auto_import` at `false` when you want discovered tools to
stay visible until you choose a claim path.

## Host Settings

Host settings override selected global settings for one machine:

```json
{
  "host_settings": {
    "workstation": {
      "dots_repo": "~/src/dotfiles",
      "provider_priority": ["apt", "brew", "npm", "pip"],
      "disabled_providers": ["pip"],
      "agents_disabled": false,
      "skills_disabled": false,
      "mcp_disabled": false,
      "plugins_disabled": false,
      "agents_use": ["claude-code", "codex"]
    }
  }
}
```

Host-specific fields include `provider_priority`, `dots_repo`, `dots_disabled`,
`disabled_providers`, `agents_disabled`, `skills_disabled`, `mcp_disabled`,
`plugins_disabled`, and `agents_use`. Global fields such as `auto_import`,
`update_quarantine`, `provider_update_quarantine`, and `dots_git` are not host
overrides.

`agents_disabled`, `skills_disabled`, `mcp_disabled`, and `plugins_disabled`
are per-host pointer-to-bool fields: absent (`nil`) means enabled by default,
and only an explicit `true` turns the feature off. `agents_disabled` is the
master switch for the agent skills/mcp/plugins features — when it disables
agents for a host, skills, mcp, and plugins are all disabled too regardless of
their own individual flags.

`agents_use` is also host-scoped: an absent host value inherits the global
list, while a present list replaces it. An explicit empty list disables every
agent target on that host.

## Tool Specs

```json
{
  "tools": {
    "node": {
      "providers": [{ "provider": "apt", "package": "nodejs" }]
    },
    "typescript": {
      "providers": [{ "provider": "npm", "package": "typescript" }]
    },
    "rg": {
      "providers": [{ "provider": "apt", "package": "ripgrep" }],
      "git": "https://github.com/BurntSushi/ripgrep",
      "fallback": {
        "source": {
          "type": "github",
          "owner": "BurntSushi",
          "repo": "ripgrep",
          "url": "https://github.com/BurntSushi/ripgrep"
        },
        "status": "unverified",
        "binary": "rg",
        "commands": {
          "install": "install rg",
          "check": "command -v rg",
          "uninstall": "rm -f ~/.local/share/omni/fallback/bin/rg"
        }
      }
    },
    "black": {
      "providers": [{ "provider": "pip" }]
    }
  }
}
```

Fields:

| Field | Description |
| --- | --- |
| `providers` | Ordered concrete provider candidates for this logical tool. |
| `providers[].provider` | Concrete provider such as `brew`, `apt`, `npm`, `pip`, or `script`. |
| `providers[].package` | Package name when it differs from the logical name. |
| `providers[].bin` | Optional binary name when the command differs from the package. |
| `providers[].options` | Provider-specific options. For `script`, requires `install` plus `check` or `detect`. |
| `git` | Upstream git repository URL. Brew metadata refresh/import/install and install-from-search may populate GitHub URLs here for later fallback setup. |
| `quarantine` | Tool-specific update quarantine override. Use `2d`/`48h`, `0`, or `exempt`. |
| `taps` | Homebrew taps required before install. |
| `variants` | Alternate install candidates tried in order. |
| `hosts` | Host-specific install overrides. |
| `ignore` | Keep the spec but skip management. |
| `fallback` | GitHub fallback recipe used when no configured native provider can provide the package. |

Fallback fields:

| Field | Description |
| --- | --- |
| `source.type` | Currently `github`. |
| `source.owner`, `source.repo`, `source.url` | Source repository provenance. |
| `status` | `unresolved`, `unsupported`, `unverified`, `verified`, or `failed`. |
| `binary` | Expected command name after install. |
| `bin_dir` | Optional per-tool override for `settings.fallback_bin_dir`. |
| `release_channel` | Optional release channel metadata for future resolver/editor use. |
| `recipe` and `platforms` | Structured release asset metadata when available. |
| `recipe.release_id`, `recipe.tag_name`, `recipe.published_at` | Generated GitHub release provenance and update-detection metadata. Leave alone unless editing manually. |
| `recipe.asset_id`, `recipe.asset_name`, `recipe.asset_download_url` | Generated current-platform asset provenance and download metadata. Leave alone unless editing manually. |
| `commands.install` | Shell command used by fallback install. Required for usable fallbacks. |
| `commands.check` | Shell command used to verify install. Required unless status is `unresolved` or `unsupported`. |
| `commands.uninstall` | Optional shell command for fallback uninstall. If absent, uninstall is unavailable. |
| `commands.upgrade` | Optional shell command for fallback upgrade. If absent, Omni reuses `install`. |

`omni tools fallback <tool> --from-github owner/repo` writes a usable fallback
after resolving latest stable release metadata and a supported asset for the
current platform. If `--from-github` is omitted, Omni uses the tool's `git`
value when it is a GitHub URL. If the release exists but has no current-platform
asset, Omni saves an `unsupported` draft with source and release provenance for
later editing. Resolver/API failures leave the existing config unchanged. The
command is config-only; install, sync, and upgrade decide later whether to use
the saved GitHub fallback recipe.
Accepted GitHub repo forms are `owner/repo`, `github.com/owner/repo`, `https://github.com/owner/repo`,
`https://github.com/owner/repo.git`, and `git@github.com:owner/repo.git`.
Browser URLs with extra paths, queries, or fragments are rejected.

## Groups And Hosts

```json
{
  "hosts": {
    "workstation": ["dev"]
  },
  "groups": [
    {
      "name": "dev",
      "description": "Daily development tools",
      "tools": ["ripgrep", "typescript"],
      "dots": [{ "name": "nvim", "path": "~/.config/nvim" }]
    },
    { "name": "workstation", "special": "host" }
  ]
}
```

Manage group and host assignments through the CLI:

```sh
omni groups create dev
omni groups move-tool dev ripgrep
omni hosts add-group workstation dev
omni hosts copy workstation laptop
```

Dot entries use two similarly named fields:

| Field | Meaning |
| --- | --- |
| `ignored` | Skip the whole dot entry while keeping it visible. |
| `ignore` | Child path patterns to skip inside that entry. |

Groups can also scope agent resources. Values reference entries declared under
the top-level `agents` object:

| Field | Reference value |
| --- | --- |
| `skills` | `agents.packages[].source` |
| `mcp_servers` | `agents.mcp_servers[].name` |
| `marketplaces` | `agents.marketplaces[].name` |
| `plugins` | `agents.plugins[].name` |

Each field also accepts its corresponding expansion token:
`@agents.packages`, `@agents.mcp_servers`, `@agents.marketplaces`, or
`@agents.plugins`.

## Global Ignore

```json
{
  "ignore": {
    "tools": ["python-library"],
    "dots": ["scratch"]
  }
}
```

Ignore state keeps noisy or intentionally unmanaged items out of normal sync
and refresh flows.

## Agent Resources

See [Agents](agents.md) for normal management, sync order, dry-run projection,
feature gates, and plugin shadowing.

`agents` contains four desired-state lists:

| Field | Identity | Purpose |
| --- | --- | --- |
| `packages` | `source` | Skill packages installed through Omni's native skill store. |
| `mcp_servers` | `name` | MCP registrations restored through agent adapters. |
| `marketplaces` | `name` | Plugin catalogs added before their items. |
| `plugins` | `name` | Marketplace or direct-source plugins. |

Every entry accepts an optional `agents` list that narrows the enabled target
agents for that resource. An omitted or empty resource-level list uses all
enabled targets for the host.

### Skill Packages

`agents.packages` is the durable desired-state manifest for skills:

```json
{
  "agents": {
    "packages": [
      {
        "source": "vercel-labs/agent-skills",
        "ref": "main",
        "skills": ["frontend-design"],
        "agents": ["claude-code", "codex"]
      }
    ]
  }
}
```

`source` accepts GitHub shorthand, GitHub/GitLab/enterprise or generic Git/SSH
URLs, repository subpaths, well-known HTTP catalogs, `file://` URLs, and local
directories. `ref` is an optional branch, tag, or commit. Omitting `skills`
installs every discovered skill; otherwise only the named skills are installed.
Omitting `agents` uses the host's enabled Agent Targets.

CLI source shorthand can combine these fields:
`owner/repo#ref@skill`. Omni stores the normalized locator, ref, and selectors
separately. Re-adding selected skills merges unique selectors; re-adding the
source without a selector resets the package to all skills.

Direct standalone `SKILL.md` HTTP URLs are not supported. HTTP publishers must
provide `/.well-known/agent-skills/index.json`; the legacy
`/.well-known/skills/index.json` location is still probed when the first
returns 404. Both locations must serve a discovery index whose `$schema` is
`https://schemas.agentskills.io/discovery/0.2.0/schema.json`, and every entry
needs a `sha256:` `digest` that Omni verifies before writing anything to disk.
An index in any other format — including the pre-0.2 flat file list — is
rejected rather than installed. When neither location answers with a
recognized index, an HTTP source falls back to Git acquisition.

Beyond the conventional `skills/` layouts, Omni also harvests skill paths
declared by a plugin manifest at `.claude-plugin/plugin.json`,
`.claude-plugin/marketplace.json`, or `.plugin/plugin.json`. Every `skills`
array found in those manifests is followed; a declared path is used directly
when it holds a `SKILL.md`, otherwise its immediate subdirectories are scanned.
Paths that escape the repository are rejected. Directories whose `SKILL.md`
frontmatter is unusable are skipped with a warning rather than failing the
whole package.

Legacy `agents.skills` entries migrate into `agents.packages[].skills`.
Legacy `.skill-lock.json` state is never written automatically. Sync and
update leave legacy CLI-managed skill directories in place and warn about
them; only `omni agents skills import` adopts those installations into the
canonical package store.

### MCP Servers, Marketplaces, And Plugins

MCP servers require `name` and `transport`. `stdio` requires `command`; `http`
and `sse` require `url`. `env` stores environment variable names,
`env_literal` stores literal non-secret values, and `headers` stores remote
headers.

Marketplaces require `name` and `source`. Plugins require `name` and exactly
one of `marketplace` or `source`; a `marketplace` value must name a declared
marketplace. Sync adds a missing marketplace before installing its plugins and
does not re-add one already present.

## Agents Ignore

```json
{
  "agents": {
    "ignore": {
      "skills": ["vercel-labs/agent-skills"],
      "mcp_servers": ["context7"],
      "plugins": ["my-plugin"],
      "marketplaces": ["noisy-marketplace"]
    }
  }
}
```

`agents.ignore` lists managed skill packages, MCP servers, plugins, and
marketplaces by name, skipped during sync — the agents equivalent of
the top-level `ignore` list for tools and dots. Ignored items still render,
dimmed, under the Ignored section of the Agents tab, and are excluded from the
dashboard's Agents attention count.
