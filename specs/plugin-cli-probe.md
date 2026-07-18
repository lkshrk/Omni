# Live CLI Probe: Claude Code & Codex Plugin/Marketplace Commands

Captured 2026-07-02 in a fully sandboxed environment: `CLAUDE_CONFIG_DIR`,
`CODEX_HOME`, and `HOME` all pointed at fresh `mktemp -d` directories before
any invocation. Binaries invoked by absolute path
(`/opt/homebrew/bin/claude`, `/opt/homebrew/bin/codex`) to bypass shell
wrapper functions. Real marketplace used: `lkshrk/agent-marketplace`
(via SSH clone for claude, HTTPS for codex). Sandbox dirs discarded after
the run; no `~/.claude*` or `~/.codex` state was touched.

## Deviations from spec assumptions

1. **Marketplace name is NOT the full `owner/repo` source string.** Adding
   `lkshrk/agent-marketplace` produced a marketplace declared as `lkshrk`
   (the GitHub owner segment) for *both* claude and codex — not `agent-marketplace`
   and not the full `owner/repo` string. Any code that derives a default
   `Marketplace.Name` from `Source` must split on `/` and take the owner,
   or (safer) require the user/config to supply an explicit name rather than
   inferring one, since the CLIs' own inference is owner-based and easy to
   collide (e.g. two different marketplaces both owned by the same GitHub
   user would both default to the same name).
2. **`claude plugins list --json` is NOT always wrapped in `{installed, available}`.**
   Without `--available` it returns a bare JSON array of installed-plugin
   objects (`[]` or `[{id, version, scope, enabled, installPath, installedAt,
   lastUpdated}, ...]`). Only `--available --json` returns the
   `{"installed": [...], "available": [...]}` wrapper shape, and per
   `plugins list --help`, `--available` "requires --json". Parsers for claude
   must branch on whether `--available` was passed.
3. **`codex plugin list --json` is ALWAYS wrapped in `{installed, available}`**,
   regardless of `--available` — the `available` key is just an empty array
   `[]` when `--available` is omitted. This is a different shape convention
   than claude's bare-array default, so Tasks 2/3 parsers cannot share one
   JSON-shape assumption across agents.
4. **Codex identity separator matches spec (`PLUGIN@MARKETPLACE`)** for both
   `plugin add`/`plugin remove` and claude's `plugin install`/`plugin
   uninstall` — this part of the spec's ground truth held.
5. **The spec's example plugin `caveman@caveman` does not exist** in the real
   `lkshrk/agent-marketplace` marketplace; installing it via claude failed
   with exit code 1 ("Plugin \"caveman\" not found in marketplace \"caveman\"").
   The marketplace's real available plugins are `linear-ai` and
   `useful-skills` (both under marketplace name `lkshrk`, since names are
   owner-derived per point 1). The probe below uses `useful-skills@lkshrk` as
   the real, successfully-installed plugin for both agents instead.
6. **Claude's plain-text `plugins marketplace list` and `plugins list` output
   have no structured delimiter** beyond indentation and a `❯` bullet — any
   fallback non-JSON parser must be regex/whitespace based, not column-based
   like codex's plain `plugin list` table (which does have a fixed-width
   `PLUGIN  STATUS  VERSION  PATH` header).
7. Codex's `plugin marketplace add` accepts `--ref` and `--sparse` similar to
   claude's `--scope`/`--sparse`, but codex has no `--scope` concept at all
   (no per-scope marketplace declarations); scope only exists on claude's
   `install`/`uninstall`/`marketplace add`/`marketplace remove` commands.

## Raw transcript

Every command below was run in the order shown, stdout+stderr combined,
literal exit code appended as `EXIT_CODE: N`.

### claude plugins --help
```
Usage: claude plugin|plugins [options] [command]

Manage Claude Code plugins

Options:
  -h, --help                           Display help for command

Commands:
  details [options] <name>             Show a plugin's component inventory and
                                       projected token cost
  disable [options] [plugin]           Disable an enabled plugin
  enable [options] <plugin>            Enable a disabled plugin
  help [command]                       display help for command
  init|new [options] <name>            Scaffold a new plugin at
                                       ~/.claude/skills/<name>/ (auto-loads next
                                       session as <name>@skills-dir)
  install|i [options] <plugin>         Install a plugin from available
                                       marketplaces (use plugin@marketplace for
                                       specific marketplace)
  list [options]                       List installed plugins
  marketplace                          Manage Claude Code marketplaces
  prune|autoremove [options]           Remove auto-installed dependencies that
                                       are no longer needed
  tag [options] [path]                 Create a {name}--v{version} git tag for a
                                       plugin release, validating that
                                       plugin.json and any enclosing marketplace
                                       entry agree
  uninstall|remove [options] <plugin>  Uninstall an installed plugin
  update [options] <plugin>            Update a plugin to the latest version
                                       (restart required to apply)
  validate [options] <path>            Validate a plugin or marketplace manifest
EXIT_CODE: 0
```

### claude plugins list --help
```
Usage: claude plugin list [options]

List installed plugins

Options:
  --available  Include available plugins from marketplaces (requires --json)
  -h, --help   Display help for command
  --json       Output as JSON
EXIT_CODE: 0
```

### claude plugins list --json
```
[]
EXIT_CODE: 0
```

### claude plugins install --help
```
Usage: claude plugin install|i [options] <plugin>

Install a plugin from available marketplaces (use plugin@marketplace for
specific marketplace)

Options:
  --config <key=value>  Set a userConfig option declared in the plugin's
                        manifest (repeatable). Values are validated against the
                        schema and stored via the same path as the interactive
                        /plugin configure flow.
  -h, --help            Display help for command
  -s, --scope <scope>   Installation scope: user, project, or local (default:
                        "user")
EXIT_CODE: 0
```

### claude plugins uninstall --help
```
Usage: claude plugin uninstall|remove [options] <plugin>

Uninstall an installed plugin

Options:
  -h, --help           Display help for command
  --keep-data          Preserve the plugin's persistent data directory
                       (~/.claude/plugins/data/{id}/)
  --prune              Also remove auto-installed dependencies that are no
                       longer needed (requires -y in non-interactive contexts)
  -s, --scope <scope>  Uninstall from scope: user, project, or local (default:
                       "user")
  -y, --yes            Skip the --prune confirmation prompt (required when stdin
                       or stdout is not a TTY)
EXIT_CODE: 0
```

### claude plugins marketplace --help
```
Usage: claude plugin marketplace [options] [command]

Manage Claude Code marketplaces

Options:
  -h, --help                  Display help for command

Commands:
  add [options] <source>      Add a marketplace from a URL, path, or GitHub repo
  help [command]              display help for command
  list [options]              List all configured marketplaces
  remove|rm [options] <name>  Remove a configured marketplace
  update [options] [name]     Update marketplace(s) from their source - updates
                              all if no name specified
EXIT_CODE: 0
```

### claude plugins marketplace list --help
```
Usage: claude plugin marketplace list [options]

List all configured marketplaces

Options:
  -h, --help  Display help for command
  --json      Output as JSON
EXIT_CODE: 0
```

### claude plugins marketplace add --help
```
Usage: claude plugin marketplace add [options] <source>

Add a marketplace from a URL, path, or GitHub repo

Options:
  -h, --help           Display help for command
  --scope <scope>      Where to declare the marketplace: user (default),
                       project, or local
  --sparse <paths...>  Limit checkout to specific directories via git
                       sparse-checkout (for monorepos). Example: --sparse
                       .claude-plugin plugins
EXIT_CODE: 0
```

### claude plugins marketplace remove --help
```
Usage: claude plugin marketplace remove|rm [options] <name>

Remove a configured marketplace

Options:
  -h, --help       Display help for command
  --scope <scope>  Remove the marketplace declaration from a specific settings
                   scope: user, project, or local. Omit to remove it from every
                   scope.
EXIT_CODE: 0
```

### codex plugin --help
```
Manage Codex plugins

Usage: codex plugin [OPTIONS] <COMMAND>

Commands:
  add          Install a plugin from a configured marketplace snapshot
  list         List plugins available from configured marketplace snapshots
  marketplace  Add, list, upgrade, or remove configured plugin marketplaces
  remove       Remove an installed plugin from local config and cache
  help         Print this message or the help of the given subcommand(s)

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')
EXIT_CODE: 0
```

### codex plugin list --help
```
List plugins available from configured marketplace snapshots

Usage: codex plugin list [OPTIONS]

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

  -m, --marketplace <MARKETPLACE>
          Only list plugins from this configured marketplace name

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --json
          Output plugin list as JSON

      --available
          Include uninstalled marketplace plugins in the JSON output

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')

Examples:
  codex plugin list
  codex plugin list --marketplace debug
  codex plugin list --json
  codex plugin list --available --json
EXIT_CODE: 0
```

### codex plugin add --help
```
Install a plugin from a configured marketplace snapshot.

Pass either `PLUGIN@MARKETPLACE` or pass `PLUGIN` with `--marketplace MARKETPLACE`.

Usage: codex plugin add [OPTIONS] <PLUGIN[@MARKETPLACE]>

Arguments:
  <PLUGIN[@MARKETPLACE]>
          Plugin selector to install: either PLUGIN@MARKETPLACE or PLUGIN with --marketplace

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

  -m, --marketplace <MARKETPLACE>
          Configured marketplace name to use when PLUGIN does not include @MARKETPLACE

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --json
          Output install result as JSON

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')

Examples:
  codex plugin add sample@debug
  codex plugin add sample --marketplace debug
EXIT_CODE: 0
```

### codex plugin remove --help
```
Remove an installed plugin from local config and cache.

Pass either `PLUGIN@MARKETPLACE` or pass `PLUGIN` with `--marketplace MARKETPLACE`.

Usage: codex plugin remove [OPTIONS] <PLUGIN[@MARKETPLACE]>

Arguments:
  <PLUGIN[@MARKETPLACE]>
          Plugin selector to remove: either PLUGIN@MARKETPLACE or PLUGIN with --marketplace

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

  -m, --marketplace <MARKETPLACE>
          Marketplace name to use when PLUGIN does not include @MARKETPLACE

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --json
          Output remove result as JSON

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')

Examples:
  codex plugin remove sample@debug
  codex plugin remove sample --marketplace debug
EXIT_CODE: 0
```

### codex plugin marketplace --help
```
Add, list, upgrade, or remove configured plugin marketplaces

Usage: codex plugin marketplace [OPTIONS] <COMMAND>

Commands:
  add      Add a local or Git marketplace to the configured marketplace sources
  list     List plugin marketplaces Codex is currently considering and their roots
  upgrade  Refresh configured Git marketplace snapshots
  remove   Remove a configured marketplace source by name
  help     Print this message or the help of the given subcommand(s)

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')
EXIT_CODE: 0
```

### codex plugin marketplace add --help
```
Add a local or Git marketplace to the configured marketplace sources

Usage: codex plugin marketplace add [OPTIONS] <SOURCE>

Arguments:
  <SOURCE>
          Marketplace source: a local path, owner/repo[@ref], HTTPS Git URL, or SSH Git URL

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

      --ref <REF>
          Git ref to fetch for Git marketplace sources

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --sparse <PATH>
          Sparse checkout path for Git marketplace sources. Can be repeated

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

      --json
          Output add result as JSON

  -h, --help
          Print help (see a summary with '-h')

Examples:
  codex plugin marketplace add ./path/to/marketplace
  codex plugin marketplace add owner/repo --ref main
  codex plugin marketplace add https://github.com/owner/repo --sparse plugins/foo
EXIT_CODE: 0
```

### codex plugin marketplace list --help
```
List plugin marketplaces Codex is currently considering and their roots

Usage: codex plugin marketplace list [OPTIONS]

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

      --json
          Output marketplace list as JSON

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')
EXIT_CODE: 0
```

### codex plugin marketplace remove --help
```
Remove a configured marketplace source by name

Usage: codex plugin marketplace remove [OPTIONS] <MARKETPLACE_NAME>

Arguments:
  <MARKETPLACE_NAME>
          Configured marketplace name to remove

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

      --json
          Output remove result as JSON

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')

Example:
  codex plugin marketplace remove debug
EXIT_CODE: 0
```

### codex plugin marketplace upgrade --help
```
Refresh configured Git marketplace snapshots.

Omit MARKETPLACE_NAME to upgrade all configured Git marketplaces.

Usage: codex plugin marketplace upgrade [OPTIONS] [MARKETPLACE_NAME]

Arguments:
  [MARKETPLACE_NAME]
          Optional configured marketplace name to upgrade. Omit to upgrade all Git marketplaces

Options:
  -c, --config <key=value>
          Override a configuration value that would otherwise be loaded from `~/.codex/config.toml`.
          Use a dotted path (`foo.bar.baz`) to override nested values. The `value` portion is parsed
          as TOML. If it fails to parse as TOML, the raw string is used as a literal.
          
          Examples: - `-c model="o3"` - `-c 'sandbox_permissions=["disk-full-read-access"]'` - `-c
          shell_environment_policy.inherit=all`

      --json
          Output upgrade result as JSON

      --enable <FEATURE>
          Enable a feature (repeatable). Equivalent to `-c features.<name>=true`

      --disable <FEATURE>
          Disable a feature (repeatable). Equivalent to `-c features.<name>=false`

  -h, --help
          Print help (see a summary with '-h')

Examples:
  codex plugin marketplace upgrade
  codex plugin marketplace upgrade debug
EXIT_CODE: 0
```

### claude plugins marketplace add lkshrk/agent-marketplace
```
Adding marketplace…Cloning via SSH: git@github.com:lkshrk/agent-marketplace.git
Refreshing marketplace cache (timeout: 120s)…
Cloning repository (timeout: 120s): git@github.com:lkshrk/agent-marketplace.git
Clone complete, validating marketplace…
Cleaning up old marketplace cache…
✔ Successfully added marketplace: lkshrk (declared in user settings)
EXIT_CODE: 0
```

### claude plugins install caveman@caveman
```
Installing plugin "caveman@caveman"...✘ Failed to install plugin "caveman@caveman": Plugin "caveman" not found in marketplace "caveman". Your local copy may be out of date — try `claude plugin marketplace update caveman`.

EXIT_CODE: 1
```

### claude plugins list --json (after add)
```
[]
EXIT_CODE: 0
```

### claude plugins marketplace list (after add)
```
Configured marketplaces:

  ❯ lkshrk
    Source: GitHub (lkshrk/agent-marketplace)

EXIT_CODE: 0
```

### claude plugins marketplace list --json (after add)
```
[
  {
    "name": "lkshrk",
    "source": "github",
    "repo": "lkshrk/agent-marketplace",
    "installLocation": "/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/claude-config/plugins/marketplaces/lkshrk"
  }
]
EXIT_CODE: 0
```

### claude plugins list --available --json (real)
```
{
  "installed": [],
  "available": [
    {
      "pluginId": "linear-ai@lkshrk",
      "name": "linear-ai",
      "description": "Linear issue intake, setup checks, status detection, refinement, implementation, dashboard progress, and review handoff workflow skills.",
      "marketplaceName": "lkshrk",
      "version": "1.5.0",
      "source": {
        "source": "url",
        "url": "https://github.com/lkshrk/linear-ai.git",
        "ref": "v1.5.0"
      }
    },
    {
      "pluginId": "useful-skills@lkshrk",
      "name": "useful-skills",
      "description": "A collection of portable agent skills for Codex, Claude Code, and other skill runtimes.",
      "marketplaceName": "lkshrk",
      "version": "0.2.0",
      "source": {
        "source": "url",
        "url": "https://github.com/lkshrk/useful-skills.git",
        "ref": "v0.2.0"
      }
    }
  ]
}
EXIT_CODE: 0
```

### claude plugins install useful-skills@lkshrk
```
Installing plugin "useful-skills@lkshrk"...✔ Successfully installed plugin: useful-skills@lkshrk (scope: user)
EXIT_CODE: 0
```

### claude plugins list --json (installed)
```
[
  {
    "id": "useful-skills@lkshrk",
    "version": "0.2.0",
    "scope": "user",
    "enabled": true,
    "installPath": "/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/claude-config/plugins/cache/lkshrk/useful-skills/0.2.0",
    "installedAt": "2026-07-02T09:11:33.969Z",
    "lastUpdated": "2026-07-02T09:11:33.969Z"
  }
]
EXIT_CODE: 0
```

### claude plugins list (plain, installed)
```
Installed plugins:

  ❯ useful-skills@lkshrk
    Version: 0.2.0
    Scope: user
    Status: ✔ enabled

EXIT_CODE: 0
```

### claude plugins uninstall useful-skills@lkshrk --yes
```
✔ Successfully uninstalled plugin: useful-skills (scope: user)
EXIT_CODE: 0
```

### claude plugins list --json (after remove)
```
[]
EXIT_CODE: 0
```

### claude plugins marketplace remove lkshrk
```
✔ Successfully removed marketplace: lkshrk
EXIT_CODE: 0
```

### claude plugins marketplace list (after remove)
```
No marketplaces configured
EXIT_CODE: 0
```

### codex plugin marketplace add lkshrk/agent-marketplace
```
Added marketplace `lkshrk` from https://github.com/lkshrk/agent-marketplace.git.
Installed marketplace root: /private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/.tmp/marketplaces/lkshrk
EXIT_CODE: 0
```

### codex plugin marketplace list
```
MARKETPLACE  ROOT
lkshrk       /private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/.tmp/marketplaces/lkshrk
EXIT_CODE: 0
```

### codex plugin marketplace list --json
```
{
  "marketplaces": [
    {
      "name": "lkshrk",
      "root": "/private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/.tmp/marketplaces/lkshrk",
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      }
    }
  ]
}
EXIT_CODE: 0
```

### codex plugin list --json --available
```
{
  "installed": [],
  "available": [
    {
      "pluginId": "linear-ai@lkshrk",
      "name": "linear-ai",
      "marketplaceName": "lkshrk",
      "version": null,
      "installed": false,
      "enabled": false,
      "source": {
        "source": "git",
        "url": "https://github.com/lkshrk/linear-ai.git",
        "ref": "v1.5.0"
      },
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      },
      "installPolicy": "AVAILABLE",
      "authPolicy": "ON_INSTALL"
    },
    {
      "pluginId": "useful-skills@lkshrk",
      "name": "useful-skills",
      "marketplaceName": "lkshrk",
      "version": null,
      "installed": false,
      "enabled": false,
      "source": {
        "source": "git",
        "url": "https://github.com/lkshrk/useful-skills.git",
        "ref": "v0.2.0"
      },
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      },
      "installPolicy": "AVAILABLE",
      "authPolicy": "ON_INSTALL"
    }
  ]
}
EXIT_CODE: 0
```

### codex plugin add useful-skills@lkshrk
```
Added plugin `useful-skills` from marketplace `lkshrk`.
Installed plugin root: /private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/plugins/cache/lkshrk/useful-skills/0.2.0
EXIT_CODE: 0
```

### codex plugin list --json
```
{
  "installed": [
    {
      "pluginId": "useful-skills@lkshrk",
      "name": "useful-skills",
      "marketplaceName": "lkshrk",
      "version": "0.2.0",
      "installed": true,
      "enabled": true,
      "source": {
        "source": "git",
        "url": "https://github.com/lkshrk/useful-skills.git",
        "ref": "v0.2.0"
      },
      "marketplaceSource": {
        "sourceType": "git",
        "source": "https://github.com/lkshrk/agent-marketplace.git"
      },
      "installPolicy": "AVAILABLE",
      "authPolicy": "ON_INSTALL"
    }
  ],
  "available": []
}
EXIT_CODE: 0
```

### codex plugin list
```
Marketplace `lkshrk`
/private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/.tmp/marketplaces/lkshrk/.agents/plugins/marketplace.json

PLUGIN                STATUS              VERSION  PATH                                                     
linear-ai@lkshrk      not installed                https://github.com/lkshrk/linear-ai.git, ref `v1.5.0`    
useful-skills@lkshrk  installed, enabled  0.2.0    https://github.com/lkshrk/useful-skills.git, ref `v0.2.0`
EXIT_CODE: 0
```

### codex plugin remove useful-skills@lkshrk
```
Removed plugin `useful-skills` from marketplace `lkshrk`.
EXIT_CODE: 0
```

### codex plugin list --json (after remove)
```
{
  "installed": [],
  "available": []
}
EXIT_CODE: 0
```

### codex plugin marketplace remove lkshrk
```
Removed marketplace `lkshrk`.
Removed installed marketplace root: /private/var/folders/xn/60cxwtb13mq7_n0r4zwgy2j00000gn/T/tmp.SiIr1tWtUC/codex-home/.tmp/marketplaces/lkshrk
EXIT_CODE: 0
```

### codex plugin marketplace list (after remove)
```
No plugin marketplaces in scope.
EXIT_CODE: 0
```

