# Agent Skills Restore — Subsystem Spec

**Date:** 2026-06-15
**Parent vision:** [agent-environment-restore](./2026-06-15-agent-environment-restore-design.md) — subsystem 4 (bundles), narrowed to **skills only**, first slice built.
**Intake:** [HCL-25](https://linear.app/h-cloud/issue/HCL-25/add-omni-managed-restore-for-claudecodex-agent-environment) — "shared skills from lockfiles".

## Goal

Restore a machine's agent skills from tracked desired state, so local and Coder
machines converge on the same skill set with one Omni command. Same skill set on
every machine (no per-host divergence).

## Approach: omni manifest authority + CLI executor

Omni owns a small **skills manifest** as its declarative source of truth, and
uses the upstream [`vercel-labs/skills`](https://github.com/vercel-labs/skills)
CLI (`npx`/`bunx skills`) only as the *executor* that fetches and places skills.
Three operations:

- **declare** — list a skill in the omni manifest (edit omni config).
- **import** — ingest CLI/UI-added skills from the live `.skill-lock.json` into
  the manifest, so drift is captured deliberately.
- **restore** — drive the CLI to install every manifest skill onto the host.

Omni still does not fetch or model skill internals — that stays with the CLI.
What changed from the earlier draft: authority moved from the CLI's lockfile to
the omni manifest, because the user wants both to *declare* skills in omni and to
*import* CLI-added ones. The CLI's `.skill-lock.json` becomes a derived runtime
record, not the synced source of truth.

**Why not hand-author `.skill-lock.json`:** it carries runtime-derived fields
omni cannot fake — `skillFolderHash` (GitHub tree API), `installedAt`,
`updatedAt` — and the CLI wipes the file on malformed/old-version content. So the
manifest is omni's *own* format; the lockfile is read (for import + drift) but
never written by omni.

Rejected alternative (B, full model): omni reconciles each skill's installed
state natively (remove/replace, per-file). More control, re-implements lock
semantics. Not needed — restore is install-to-desired, not full reconcile.

## Upstream facts (verified against source)

- **CLI:** `npx skills` — commands `add`, `use`, `list`, `find`, `remove`,
  `update`, `init`, `sync`. `sync` only crawls `node_modules`; **there is no
  install-from-global-lock command.**
- **`add` flags:** `-g/--global`, `-a/--agent <agents...>` (e.g. `claude-code`,
  `codex`), `-s/--skill <names...>`, `-y/--yes`, `--all`, `--copy`.
- **Global lockfile:** `$XDG_STATE_HOME/skills/.skill-lock.json` if set, else
  `~/.agents/.skill-lock.json`. Schema version 3. Written by `src/skill-lock.ts`.

```ts
SkillLockFile {
  version: number;
  skills: Record<string /*name*/, SkillLockEntry>;
  dismissed?: ...;
  lastSelectedAgents?: string[];
}
SkillLockEntry {
  source: string;          // "owner/repo"
  sourceType: string;      // github | mintlify | huggingface | local
  sourceUrl: string;       // original URL, used to re-fetch
  ref?: string;            // branch/tag
  skillPath?: string;      // subpath within source
  skillFolderHash: string; // GitHub tree SHA — drift detection, NOT a pinned checkout
  installedAt: string;
  updatedAt: string;
  pluginName?: string;
}
```

Three consequences:

1. **No bulk install** → Omni replays per entry via `skills add`.
2. **Lockfile agent targeting is not per-skill** — the entry has no `agents`
   field, only a global `lastSelectedAgents`. So `import` cannot recover a
   per-skill agent set; imported skills default to manifest default / detected.
   The omni manifest *can* carry per-skill `agents` when declared by hand.
3. **Restore is ref-level, not hash-level** — `ref` is a branch/tag;
   `skillFolderHash` detects drift but is not a checkout pin. Replaying `add`
   fetches the latest of that ref, which may differ from the locked hash. True
   reproducibility = compare post-restore hash vs locked and warn.

## Manifest

The manifest is omni's own declarative list, stored in omni config (synced by
omni's normal config mechanism — not Stow, not the lockfile). Shared across
machines; one desired set. Each entry carries only what omni can author and
replay:

```
ManifestSkill {
  name:      string    // the skill name (CLI -s <name>, lockfile map key)
  source:    string    // "owner/repo"
  ref:       string?   // branch/tag → appended as "#ref"
  skillPath: string?   // subpath within source
  agents:    []string? // optional per-skill targets; default = manifest default / detected
}
```

Runtime-only fields (`skillFolderHash`, `installedAt`, `updatedAt`,
`sourceType`, `sourceUrl`) are deliberately absent — omni reads them from the
live lockfile for import + drift but never stores or authors them.

## Sync mechanism

The manifest rides omni's **existing config sync** (it is omni config). No Stow
of the lockfile, no JSON merge, no per-host overlay. The CLI's
`.skill-lock.json` (XDG path, else `~/.agents/.skill-lock.json`) is read-only to
omni: consulted on `import` and on the post-restore drift check.

## Operations

### restore

1. Ensure a runner is available — `npx` (Node) or `bunx` (Bun), per
   `settings.node_manager`. No install step required; clear error if no runner
   is on `PATH`.
2. Read the omni manifest.
3. Resolve target agents per skill: the entry's `agents`, else the manifest
   default, else machine-detected (CLI auto-detects claude-code/codex).
4. For each manifest skill, call the installer. The `npxInstaller` runs
   `<runner> skills add <source> -s <name> -g -a <agents...> -y`, where
   `<source> = source[#ref]` (see Reconstructing the source).
5. Drift check: read the live lockfile, diff `skillFolderHash` per skill against
   the prior value, report mismatches as warnings (non-fatal).
6. Aggregate per-skill results; report installed / skipped / failed. One failed
   skill must not abort the rest.

### import

1. Read the live `.skill-lock.json` (XDG path, else `~/.agents`).
2. For each lock entry, derive a `ManifestSkill` (`name`, `source`, `ref`,
   `skillPath`) — drop runtime fields.
3. Upsert into the manifest: add new skills, update changed `source`/`ref`,
   leave existing untouched unless changed. Report added / updated / unchanged.
4. `--dry-run` prints the diff without writing the manifest.

## Omni surface

Follow Omni's CLI / app / TUI split.

- **CLI:** new `agents` command group:
  - `omni agents restore skills` (`--dry-run` prints the `skills add`
    invocations without running them).
  - `omni agents import skills` (`--dry-run` prints the manifest diff without
    writing). Ingests CLI/UI-added skills from the live lockfile.
  - `omni agents list skills` (optional) — show the manifest.
  Command shape may fold into a broader `omni agents …` later; scoped to
  `skills` now.
- **App layer:** orchestration in `internal/app` — read/write the manifest, read
  the live lockfile (import + drift), drive an installer, collect results. No
  skill logic beyond replay.
- **TUI:** mirror restore + import (progress per skill, drift warnings, import
  diff), consistent with existing flows.
- **Provider reuse:** `skills` is not modeled as a tool entry; it is invoked via
  `npx`/`bunx` on demand. (Revisit if pinning the CLI version becomes
  necessary.)

### Installer boundary (wrap now, port-ready later)

Decision: **wrap `npx skills` now**, but isolate the install step behind a small
interface so a Go reimplementation can replace it later without touching the
CLI/TUI/orchestration. Restore depends on the interface, not on `npx`.

```go
// internal/app (or a focused sub-package)
type SkillInstaller interface {
    // Install materializes one manifest skill onto the host for the given agents.
    Install(ctx, skill ManifestSkill, agents []string, opts InstallOpts) error
}
```

- `npxInstaller` is the only implementation now: builds and runs
  `<runner> skills add <source> -s <name> -g -a <agents...> -y`, where
  `<source> = skill.source[#skill.ref]` (see Reconstructing the source).
  `<runner>` is `npx` or `bunx` — selected from `settings.node_manager`
  (`bun` → `bunx`, otherwise `npx`), falling back to whichever is on `PATH`.
- A future `goInstaller` (clone + GitHub tree fetch + placement, exact-hash pin)
  can drop in behind the same interface — start with the GitHub-source path
  only, fall back to `npxInstaller` for other source types.
- The orchestration loop, dry-run, drift check, and result aggregation are
  installer-agnostic.

### Reconstructing the source

A manifest skill maps to an `add` argument the same way upstream `update`
reconstructs from its lock entry: `source` + optional `#ref` → `owner/repo` or
`owner/repo#ref`, optionally carrying `skillPath`. This is the documented refresh
form (`npx skills add owner/repo#ref -y`), so ref syntax is settled — not an open
question. Import derives these same fields *from* the lockfile.

## Out of scope

- Per-host / per-profile skill sets (manifest is shared; per-skill `agents` is
  the only targeting).
- Plugins, marketplaces, MCP servers (later subsystems).
- Removing skills on restore (restore is install-to-desired, not full reconcile;
  no prune of skills the host has but the manifest lacks).
- Omni writing `.skill-lock.json` (read-only to omni — import reads it, restore
  diffs it).
- Pinning skills to an exact `skillFolderHash` checkout (upstream doesn't
  support it via `add`).

## Testing

- Unit (`internal/app`, `package *_test`): manifest read/write, lockfile path
  resolution (XDG vs `~/.agents`), parse of v3 lock schema, import upsert (add /
  update / unchanged, runtime fields dropped), command construction per manifest
  skill, agent resolution (per-skill → default → detected), drift diff,
  partial-failure aggregation. Mock the CLI invocation — do not hit the network.
- Integration (`integration_tests/`, txtar): `omni agents restore skills
  --dry-run` against a fixture manifest (assert emitted `skills add` lines) and
  `omni agents import skills --dry-run` against a fixture lockfile (assert the
  manifest diff). Delegate fixture authoring to the **txtar-writer** agent.
- TUI tests via the **tui-tester** agent.
- ≥80% coverage per project convention.

## Open questions

- Manifest location in omni config: a new top-level section (e.g.
  `agents.skills`) in the existing config JSON, or a separate tracked file?
  Decide in the plan against the current config schema.
- `--copy` vs symlink: which does Omni want on restore? Symlink is the CLI
  default; Coder workspaces may prefer `--copy` for self-containment.
- Missing/empty manifest on restore, or missing lockfile on import: no-op
  success or guided error?
