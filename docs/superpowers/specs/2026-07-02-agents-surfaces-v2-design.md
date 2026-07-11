# Agents Surfaces v2 — Design (sections, toggles, doctor)

Post-Phase-4 UX pass over the shipped skills/MCP/plugins features: the Agents
tab shows all three as stacked sections, each feature gets a per-host disable
toggle, and the Doctor tab gains an agents health block.

## Decisions

| Question | Decision |
|---|---|
| Agents tab layout | Stacked sections (Skills, MCP Servers, Plugins) as the DEFAULT view; existing chips remain as jump/filter to a single section |
| Feature toggles | Per-host flags `skills_disabled` / `mcp_disabled` / `plugins_disabled` in `host_settings`, beside the existing `agents_disabled` master |
| Doctor | Full health block: enabled state, runner/agent binary reachability, manifest counts, per-agent installed/missing, unmanaged counts |

## 1. Agents tab — sections + chips

- New default chip state `all` (first pill, selected on tab entry): renders
  the three sections stacked in one scrollable list, each with the existing
  section-header style (like `Installed` / `Not Installed`):
  `Skills` → current package rows (+ find results when searching),
  `MCP Servers` → managed rows + unmanaged sub-section,
  `Plugins` → managed rows + unmanaged sub-section.
- One cursor traverses all visible rows; key handling dispatches by the
  section owning the cursor row (the existing `handleSkillsKeyMsg` /
  `handleMcpKeyMsg` / `handlePluginKeyMsg` become section-scoped dispatch —
  reuse them, do not fork logic).
- Chips (`skills` / `mcp` / `plugin`) keep today's behavior: filter to that
  single section. `all` is a fourth pill state, default on activation.
- Per-key semantics in `all` view: identical to the focused-chip semantics of
  the section under the cursor (`r/i/a/d/g/n//`, two-step confirms, pickers,
  forms). `/` search applies to the section under the cursor; find/add flows
  unchanged. Empty sections render header + one dim "none" line.
- A feature disabled on this host (see toggles) drops its section from `all`
  and disables its chip (dim, non-selectable).
- Existing per-chip state fields stay; a new `agentsViewAll bool` (or
  equivalent chip-index) selects the composition. Row-index math must be
  single-sourced (one flatten function feeding both render and key dispatch —
  the mcp/plugins cursor-flattening pattern generalizes).

## 2. Per-host feature toggles

- `HostSettings` gains `SkillsDisabled`, `McpDisabled`, `PluginsDisabled`
  (`*bool`, nil = inherit, same pattern as `AgentsDisabled`).
- `EffectiveSettings` resolves them identically to `AgentsDisabled`;
  `agents_disabled` remains the master (master off ⇒ all three off).
- App layer: `requireAgentsEnabled` stays; new per-feature guards
  (`requireSkillsEnabled` etc.) wrap it and gate the respective ops —
  skills ops, mcp ops, plugin ops. CLI subcommands surface the guard error
  ("skills are disabled on this host"). Restore honors the flags (skips with
  a warning line).
- Settings tab: three toggle rows rendered beside the existing
  `agents_disabled` row, same toggle interaction/persistence path
  (`SaveAgentsDisabled` pattern → `SaveSkillsDisabled` etc.).
- Schema bump v11 → v12, additive; migration is a version-bump no-op.

## 3. Doctor — agents health block

One block appended to the existing doctor output (TUI tab and any CLI doctor
parity that exists — follow the current doctor architecture; check how
existing checks are modeled and add these as checks of the same shape):

Per feature (skills / mcp / plugins), reported per current host:
- Enabled/disabled, and which flag disabled it (feature flag vs `agents_disabled`).
- Binary reachability: skills → configured node runner (`npx`/`bunx`) on
  PATH; mcp + plugins → per agent adapter `Available()` (binary + config
  dir), e.g. `claude: ok`, `codex: binary not found on PATH`.
- Manifest counts (packages / servers / plugins; marketplaces).
- Installed vs missing per targeted agent (from the existing rows functions).
- Unmanaged counts per agent (mcp + plugins; skills lockfile-import parity
  count if cheaply available from existing import diff).
- Disabled feature ⇒ single line "disabled", no probing (no exec calls).

Doctor must not block on slow probes: reuse whatever async/spinner pattern
the doctor tab already has; note that `claude mcp list` health-checks
servers (slow) — prefer the cheaper adapter `Available()` + row status
already computed, do NOT invoke list health checks synchronously in doctor.

## Out of scope

Reordering sections, chip customization, per-feature group overrides,
enabling/disabling individual agents per feature, CLI `doctor` JSON output
changes beyond adding the block in the existing format.

## Testing

- Config: toggle resolution matrix (nil/true/false × master flag), v12
  migration, schema regen.
- App: per-feature guards gate each ops family; restore skip-with-warning.
- TUI (tui-tester): all-view render (three sections, order, empty states,
  disabled section dropped), cursor traversal across sections, per-section
  key dispatch in all-view (one action per section proves routing), chip
  jump/filter still works, settings toggles render + flip + persist call,
  doctor block render incl. unavailable-binary line.
- CLI (behavioral): disabled-feature subcommand errors; doctor block content
  if a CLI doctor exists.
- Integration (txtar-writer): disabled-feature gate fixtures; doctor fixture
  if doctor has CLI surface.
