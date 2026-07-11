# Release Notes

Omni does not ship a separate `CHANGELOG` file. This page summarizes user-facing
changes since the last tagged release. For day-to-day behavior, prefer the
guides and reference pages linked from each section.

## v0.9.0

### Tools and providers

- **Script provider** — install tools with user-authored shell commands when no
  package manager carries them. See [Providers — Script Provider](providers.md#script-provider)
  and [Recipes — Grok](recipes.md#grok-homebrew-on-macos-xai-script-on-linux).
- **`omni tools migrate-nvm`** — move system-provider tools whose active
  binaries resolve through nvm onto the configured node manager, or remove the
  Node runtime from config when nvm owns it. See [Tools — NVM migration](tools.md#nvm-managed-provider-drift)
  and [Troubleshooting](troubleshooting.md#node-or-pnpm-still-listed-under-a-system-provider-after-switching-to-nvm).
- **Pip discovery filter** — provider scans skip non-CLI Python libraries during
  discovery so libraries such as `asyncpg` stay suppressible without becoming
  managed tools. See [FAQ](faq.md#why-did-a-python-library-get-imported-as-ignored).
- **Orphan tool ignore** — discovered packages can be suppressed from the Tools
  tab with host-scoped ignore (`ignore.tools`) without creating a logical tool
  spec. Tracked tools still support tool-wide, per-group, and host scopes.

### Agents

- **Grok agent** — Omni detects the Grok CLI (`grok` binary and `~/.grok`) and
  can restore Grok plugins and MCP servers through the agents adapters.
- **Agents tab v3** — per-agent rows, `c` to claim orphans, two-step ignore
  confirm, marketplace rows, and plugin-provided skill/MCP shadowing (`via plugin`).
  See [TUI — Agents Tab](tui.md#agents-tab).
- **Agent feature toggles** — per-host `agents_disabled`, `skills_disabled`,
  `mcp_disabled`, and `plugins_disabled`. See [Configuration](configuration.md#host-settings).
- **Config v14** — one-time migration drops dot entries that tracked agent
  config directories; discovery no longer surfaces those paths. See
  [Schema Reference](schema-reference.md#config-version-migrations).

### Dashboard and TUI

- **Command palette** — `tools migrate-nvm` is available from the `:` palette
  (search `migrate` or `mig`).
- **Health Check row order** — Agents attention appears before Services.
- **Dashboard vs Tools tab** — tool ignore/unignore lives on the Tools tab only;
  the dashboard is read/repair oriented.
- **Dashboard Data rows** — Tools, Dotfiles, Agents, then Services (stats only).
- **NVM fix-all** — dashboard Tool Sync and doctor findings can route nvm drift
  through migrate-nvm confirmation from the Tools tab (`r` on drift rows).

### Dotfiles

- **Agent config dirs** — dots discovery ignores agent config trees by default;
  `.agents/.skill-lock.json` remains trackable.
- **Symlink self-heal** — dots sync repairs wrong-shape symlinks stow refuses to
  own.

### Docs

- **Demo GIF** — refreshed the README/TUI demo to showcase the v0.9 dashboard,
  Agents tab, migrate-nvm palette entry, and richer row selection details.

### Config format

- Current settings version is **16**. Omni migrates older files on load. Hand-edited
  `providers[]` arrays require `version` ≥ 6 so the v5→v6 migration does not strip
  multi-provider specs. See [Schema Reference](schema-reference.md).