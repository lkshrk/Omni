# Upgrading From CLI-Managed Skills

Omni used to install agent skills by shelling out to an external `skills` CLI.
It now installs them itself: a content-addressed package store plus one symlink
per skill in each agent's skills directory. This page covers what changes on a
machine that used the old path, what a skills host has to serve for HTTP
sources, and how Omni coexists with other tools writing to the same directories.

Nothing here is a one-time script you have to run in order. Existing machines
keep working; the sections below explain what happens on their next sync.

## What Changes On An Existing Machine

Config migrates automatically. Loading a settings file written by an older
Omni upgrades it to schema version 20 in place, and `agents.packages` entries
keep their `source`, `ref`, and `agents` fields unchanged. An entry with no
`skills` selector means "every skill discovered in the source" — the selector
list is optional rather than required.

The next sync reinstalls manifest packages natively:

```sh
omni agents skills sync
```

Sync acquires each package's source, stores it under
`$XDG_DATA_HOME/omni/skills/packages/<content hash>`, and links each skill into
every target agent's skills directory. Git is a prerequisite for any package
whose source is a repository (an `owner/repo` shorthand, an `ssh://` or `git://`
URL, a GitHub or GitLab HTTPS URL, or any source pinned with `ref`). Local
directory sources need no Git unless they are pinned to a ref. `omni doctor`
reports `git: not required` or `git: not found on PATH` under its skills group
so a machine can check this before restoring.

The legacy skill lockfile is read-only, permanently. Omni reads it to find
import candidates and never writes, rewrites, or deletes it. Its location
follows the legacy CLI:

1. `$XDG_STATE_HOME/skills/.skill-lock.json` when `XDG_STATE_HOME` is set
2. `~/.agents/.skill-lock.json` otherwise

`XDG_STATE_HOME` wins outright — there is no fallback to `~/.agents` when it is
set, so a machine that exports it (common on Linux, where `~/.local/state` is
the usual value) reads a different file than the one under `~/.agents`. Check
which path applies before concluding that import found nothing; a stale
`~/.agents/.skill-lock.json` on such a machine is simply not the file Omni
reads. Removing the file Omni actually reads only removes Omni's ability to
offer those candidates.

## Adopting The Old Installation

`omni agents skills import` is the single adoption command. It folds lockfile
entries into `agents.packages` and replaces the CLI-era directories under
`~/.agents/skills` with links into the canonical package store, leaving the
lockfile itself untouched.

Sync, upgrade, and add never adopt on their own. When they find a legacy
directory for a package they would install, they leave it in place and warn
that import can adopt it.

Adoption fails safe. If a legacy directory holds content that differs from the
package Omni would install, a converge leaves that target alone and reports it:

```text
~ drift: <source>: drifted on <id> (another tool owns the skill entry; left untouched)
```

Nothing is overwritten and the package still installs on its other targets.
Resolve the difference — keep the local edits somewhere else, or delete the
directory — and run import again. An explicit `agents add` of a new package
onto the same content fails instead, with
`skill "<name>" already exists for target <id>`.

## Serving Skills Over HTTP

An HTTP or HTTPS source that is not a Git remote is resolved through a
well-known skills index. Omni requests `/.well-known/agent-skills/index.json`,
falling back to the older `/.well-known/skills/index.json` path, and accepts a
source ending in `/index.json` as the index itself.

The index must declare schema `0.2.0` and give every skill a SHA-256 digest:

```json
{
  "$schema": "https://schemas.agentskills.io/discovery/0.2.0/schema.json",
  "skills": [
    {
      "name": "code-review",
      "type": "skill-md",
      "description": "Structured code review checklist",
      "url": "/skills/code-review/SKILL.md",
      "digest": "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"
    }
  ]
}
```

`type` is `skill-md` for a bare `SKILL.md` or `archive` for a `.zip`/`.tar.gz`
holding the skill directory. Omni verifies each artifact's digest before
writing it and rejects an artifact whose `SKILL.md` name disagrees with its
index entry. Vercel's skills handler serves this format directly, so a project
deployed there needs no extra work.

Two failure modes are worth knowing apart:

- **Not a skills host.** When the probe returns 404, a login page, or any
  non-catalog response, Omni falls back to cloning the source as a Git
  repository. A host that never served an index does not block a Git-over-HTTPS
  source.
- **A skills host serving an unsupported index.** When the probe returns an
  index whose `$schema` is not `0.2.0`, Omni does not install from it — the
  only artifact path is the digest-verified `0.2.0` one. The source is still
  tried as a Git remote, and if that also fails the error names both causes,
  including `unsupported skills index at <url>: schema "<value>"`. Verification
  is never weakened: the fallback clones a repository, it does not install the
  unverified index content.

The pre-`0.2.0` file-list format — entries carrying a `files` array and no
digest — is intentionally unsupported.

??? note "Alternatives considered: trust-on-first-use for legacy hosts"
    Hashing whatever a legacy host serves on first install and pinning that
    hash would let old indexes keep working. It was deferred: the hash would
    attest only that content had not changed since Omni first fetched it, not
    that the content was what the publisher intended, and it would silently
    create two classes of package with different integrity guarantees. A host
    that wants Omni support publishes a `0.2.0` index; until then, the same
    repository usually works as a Git source.

## Coexisting With npx skills And Other Tools

Other tools install skills as real directories into the same agent skills
directories Omni links into. Ownership is per entry, not per directory: Omni
owns an entry when the path is a symlink resolving into its package store, and
treats everything else as somebody else's.

That yields three per-agent states, visible in the agents table, the dashboard
attention count, and `omni doctor`:

| State | What is at the path | What Omni does |
| --- | --- | --- |
| installed | A link into the package store, or a byte-identical copy | Nothing |
| missing | Nothing | Installs the link on the next sync |
| drifted | A directory with different content, or a foreign symlink | Skips that target and reports it; never overwrites |

Sync converges the identical case: when another tool left a copy that
matches the package byte for byte, sync moves it aside, installs the managed
link, and reports `adopted identical unmanaged copy at <path>`. The displaced
copy is deleted only after the whole sync succeeds, so a failure part-way
through rewinds it back into place.

Drift is never resolved silently, because the content genuinely differs and
Omni cannot know which side is right. `omni doctor` names the package and the
agent and points at the ways through — sync, if the copy turns out to be
identical after all, otherwise `omni agents skills resolve` or, for a package
the legacy lockfile still attributes, `omni agents skills import`. Uninstalling
a package leaves a drifted directory on disk and says so rather than deleting
content Omni did not create.

A contested entry does not fail the package. Converge verbs — `sync`,
`agents sync`, `tools sync --all`, `reconcile` and `upgrade` — skip the
contested target, link every other target the package declares, and report the
skip as a drift line; the package still counts as installed where it could be,
and the run's exit status is unaffected. This matches how MCP servers and
plugins already degrade. Verbs that record *new* intent still refuse outright:
`agents add` onto foreign content fails, because a new declaration that quietly
landed nowhere is worse than an error, and `agents skills resolve --use-managed`
fails if it cannot stage the content it is about to displace.

`omni agents skills resolve <source>[@skill]` is the explicit way to settle one:

| Side | What happens to the content | What happens to the manifest |
| --- | --- | --- |
| `--use-managed` | The foreign entry is staged aside and Omni's link takes its place; the staged copy is discarded only once the install succeeded, and restored if it failed | Unchanged |
| `--use-local` | Untouched | Narrowed so Omni stops expecting its content there |

`--use-managed` destroys local edits, so the CLI confirms before running it
(`--yes` answers the prompt) and the agents tab arms it as a two-step `u`
press. `--use-local` cannot adopt the live copy as desired state — upstream
owns a package's content — so it releases the entry instead: naming a skill
narrows the package's selectors, and naming none drops the selected agents
(`--agent <id>`, repeatable) from its target list. A narrowing that would leave
the package with no skills or no agents is refused in favour of
`omni agents skills remove`. In the TUI, a drifted skills row offers `u` (use
managed) and `l` (use local), mirroring the dots tab's conflict keys.

`omni tools sync --all` performs the import and the sync for you: it claims
unmanaged skill packages, MCP servers, and plugins into the manifest and then
reconciles all three, reporting drift in its summary instead of resolving it.
A claim it cannot make — an MCP server two agents describe differently, a
plugin from an undeclared marketplace — becomes a report line rather than a
failed run.

### MCP Servers And Plugins

The same three states apply to the other two capabilities, over different
evidence:

| Capability | installed | missing | drifted | outdated |
| --- | --- | --- | --- | --- |
| skills | Managed link or byte-identical copy | Nothing at the path | Different content, or a foreign symlink | Package behind its source |
| mcp servers | Live registration matches the manifest's transport, command and URL | No registration under that name | Registration differs on one of those identity fields | n/a — Omni does not track server versions |
| plugins | Installed from the declared marketplace or direct source | Not installed | Installed from a different marketplace | Behind the marketplace's version |

Headers are deliberately absent from the MCP identity list: they derive from
environment variables and secrets whose rotation is routine, so sync keeps
converging them from the manifest without treating a difference as a conflict.
Env is manifest-authoritative for the same reason — an agent that reports env
at all reports one merged map of resolved values in which `env` names and
inline `env_literal` pairs look alike, and Codex reports none. Adoption is the
one place that map is interrogated: a reported value that matches the ambient
environment is claimed as an `env` name with no value stored, and a value with
no match makes Omni refuse the server and name the variable, so nothing secret
reaches the manifest.

`omni agents mcp resolve <name>` and `omni agents plugins resolve <name>` take
the same `--use-managed` / `--use-local` / `--agent` / `--dry-run` flags, and
the agents tab arms them with the same two-step `u` and `l` presses. The
`--use-local` side differs from the skills one in what it can do rather than
what it means: a server definition and a plugin's marketplace are pure
configuration, so Omni adopts them into the manifest outright instead of merely
releasing the entry. Adoption still refuses to guess — agents that disagree
need `--agent`, and a plugin's marketplace must already be declared.

## Related Pages

- [State And Files](state-and-files.md) — where the package store and legacy
  lockfile live.
- [CLI Reference](cli.md) — the full `omni agents skills` command set.
- [Configuration](configuration.md) — the `agents.packages` schema.
