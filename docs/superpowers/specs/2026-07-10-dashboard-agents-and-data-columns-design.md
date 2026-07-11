# Dashboard — Agents Rows + Data-Section Stat Columns (Design)

User feedback: the dashboard's Data section must display data/stats only,
while Health Check owns state + proposed actions. Agents get first-class rows
in both. The Data rows' middle column (dim prose summaries) duplicates Health
Check — repurpose it.

## Decisions

| Question | Decision |
|---|---|
| Data section rows | Tools, Dotfiles, Automation, **Agents** — "Tool Sync" overview row removed (its attention twin in Health Check stays) |
| Data middle column | Stat breakdowns instead of prose (user-confirmed); value column stays right-aligned (unchanged renderer). Prose summaries live only in Health Check rows and selection-expanded details |
| Agents in Health Check | `statusAgentsAttentionRow` mirroring the tools/dots conditional pattern exactly (user: "align all behavior with tools/dots, consistency is key"): appears only when agents are out of sync; icon/severity/action semantics copied from the Tool Sync attention row |
| Agents out-of-sync signal | Cheap only (no exec, mirrors doctor policy): skills manifest-missing + skills lock-only (unmanaged) counts. mcp/plugins installed-state needs adapter List → excluded from the dashboard signal |
| Agents attention action | `restore skills` when missing > 0 and not busy (mirrors Tool Sync's `sync tools`); otherwise `open agents` (mirrors `open tools`/`open sync data`) |
| Agents Data row | label `Agents`, middle `N skills · M mcp · K plugins` (manifest counts), value `T managed`, details: per-feature lines incl. skills installed/missing/unmanaged, marketplaces count; action `open agents` |
| Data source | New `app.DashboardAgentsSummary` computed from config + skills lockfile only; carried on the startup snapshot like `DashboardToolSummary`, refreshed by the same messages that already reload skills state (agents toggles, skill add/adopt/restore msgs) |

## Middle-column breakdowns (Data rows)

Derived ONLY from data already on the model — no new scans:
- Tools: from `DashboardToolSummary` fields (e.g. `X installed · Y available · Z ignored` — implementer picks the 2–3 most informative existing fields).
- Dotfiles: from `DotFileCounts` (e.g. `X linked · Y ignored`).
- Automation: compact service stats (`reminder 12h · watch 2s`) from the existing service status the row already reads; `[ON/OFF/WARN]` value unchanged.
- Agents: manifest counts as above.
- Loading/activity states keep the existing spinner/stale-summary behavior (activity text replaces the breakdown while working — same mechanism as today).

## Out of scope

Attention-row changes for tools/dots/automation beyond leaving them as-is;
mcp/plugin installed-state probing; restore-all-agents action; layout/width
changes to the three-column row renderer.

## Testing

- App: DashboardAgentsSummary counts (manifest+lock fixtures), snapshot carry.
- TUI (tui-tester): Data section shows 4 rows (no Tool Sync), each middle
  breakdown renders, agents attention row appears/disappears on the signal,
  restore-vs-open action selection, agents Data row details expansion.
