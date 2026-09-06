# TUI

## Agent status

The Agents view is a navigable per-package list: one row per declared or locked
package with its source, version, targets, and status (installed, drifted,
unavailable, missing, or orphaned). Rows come from reading `~/.apm/apm.yml` and
`~/.apm/apm.lock.yaml` directly, never from parsing APM's table output.
`installed` means the entry is present in the lockfile. A package can also be
`drifted` when a bundled MCP/LSP child conflicts, has multiple owners, or has
degraded runtime health.

Below the packages, two more sections list the manifest's `mcp` and `lsp`
servers, joined to the lockfile's `mcp_servers` / `lsp_servers` by name. Their
targets come from the manifest's top-level `targets:` (LSP intersected with the
only targets APM deploys it to, `claude` and `copilot`) — never from the
lockfile, whose `mcp_target_servers` records only the last install. A locked
entry whose command binary is not on `PATH` is reported `unavailable`: APM writes
such entries to the lockfile without ever checking that they can run. An empty
section is omitted.

## Ownership and runtime health

Package-owned MCP/LSP children render under the selected package as
`provides:`, sorted by kind and name, instead of appearing again as top-level
service rows. `issues:` shows duplicate, conflict, ambiguity, unavailable
ownership evidence, or child-health problems and degrades the package row.
Unavailable ownership evidence appears only when standalone MCP/LSP entries
make ownership relevant; a package-only workspace is not degraded. Search
matches provided child names and issue text.

An exact standalone duplicate is hidden from the top-level service section and
reported on its owner package. A conflicting standalone declaration remains
visible with `conflicts with package <owner>` while the package shows the same
problem. Multi-owner ambiguity marks every involved package. Independent and
unmanaged services remain top-level. Package-owned child health never improves
an already missing, orphaned, unavailable, or drifted package.

Those two sections also read the deployed harness files — `~/.claude.json`
(`mcpServers`, `lspServers`) and `~/.codex/config.toml` (`[mcp_servers.<name>]`)
— which is the one documented exception to "the TUI hard-codes no client names":
APM records no per-entry deployment anywhere, so nothing else can see them. A
name in a harness file that APM neither declares nor locks is `orphaned` with
detail `unmanaged` and the harnesses it was found in as its targets; APM prunes
only names it locked itself, so nothing else will ever report it. A codex entry
with `enabled = false` still counts — it is deployed config APM cannot see.

An MCP row whose lockfile `mcp_configs` entry disagrees with what
`~/.claude.json` actually deploys is `drifted`. Only `command`, `url`, and `args`
are compared; headers and env are not, because they carry secrets and the two
harnesses store them differently. Lock values are expanded through the
environment first, and an entry referencing an unset variable is skipped rather
than reported as drift. Drift is a status, never a value diff — no deployed
value is ever rendered. LSP entries never drift: APM rewrites them from
`lsp_configs` on every install. Codex-side value drift is not detected; codex
gets orphan detection only.

Sync fails closed on two APM 0.29.0 defects, before APM is invoked at all
(dry runs included, so a preview that would fail for real says so): a `--frozen`
sync whose manifest declares an LSP server missing from the lockfile is refused,
because `--frozen` does not check LSP entries and would silently install and lock
it; and declaring `lsp` entries whose `targets:` intersect neither `claude` nor
`copilot` is refused, because APM aborts the entire install — MCP included — on
that combination.

Sync also fails closed on exact package-child duplicates, differing
definitions, multi-owner ambiguity, and unavailable package evidence when the
same template declares standalone MCP/LSP services. These checks run before the
live manifest is materialized, including for dry-run.

## Items APM does not manage

A final section, `Not managed by APM`, lists the native Claude and Codex
plugins, MCP servers and marketplaces this host has installed outside APM. It
is the same inventory `omni agents drift` prints, so a row here means the
artifact exists on the host but no APM manifest declares it. Rows read
`unavailable`, except an ignored one, which reads `orphaned`: it is deliberately
outside APM, not damaged. The tab summary counts them as `N native`. The
section is omitted when there is nothing to report and when the clients cannot
be read.

The row's detail block carries the client, the kind, the state, the file or CLI
record it was read from, and its install root. The state is one of
`not declared in the host template`, `retained` with the classifier's reason
(the migration classifier will not import it — a marketplace with no APM
source, say), or `ignored` with the recorded reason.

Three keys act on the selected native row, and the ones a row can offer appear
in its own detail block:

| Key | Action |
| --- | --- |
| `x` | Ignore the artifact, or unignore it when it is already ignored. Writes only `agents.ignored` in `settings.json`; the host template is never touched. |
| `A` | Adopt: declare the artifact in the host template. Offered only for a row the classifier can import, and refused while APM is running, because adopt writes the template sync reads. |
| `d` | Remove the artifact through its own client CLI, after a second press. |

`x` and `A` report `select a row under Not managed by APM first` when the
selection is elsewhere; `d` falls through to the package uninstall it also
serves. An ignored row offers neither adopt nor remove — the entry exists to
say this one stays — and says to press `x` first.

Removal runs the client's own command by identity (`claude plugin uninstall`,
`claude mcp remove -s user`, `claude plugin marketplace remove`, and the Codex
equivalents), so no filesystem path is ever derived from a package-supplied
name. Adoption writes the manifest entry only: the artifact is deployed by the
next `omni agents sync`, and the status line says so. Neither key runs APM.

## Navigation and package actions

`/` filters every section by name and by source or transport, hides the
sections it empties, and reports how many rows are showing; the arrow keys keep
moving the selection while the query has focus. The selected row always carries
its own detail block beneath it, as the tools and dots rows do: the package's
description first, then source, license, author, originating marketplace,
deployed-file count, provided children, and issues; for a service, transport,
command basename, URL host, and the harnesses it is deployed to. Descriptions
come from the package's own `apm.yml` under `~/.apm/apm_modules/`, so a package
without one says `no description available`. Only a URL's host and a command's
basename are shown, so credentials, header values, and install paths never reach
the screen.

`u` updates the selected package and `d` uninstalls it after a second press;
both keys, and the reason a row cannot offer them, appear in that row's own
detail block rather than the footer, which carries only tab-wide actions. A
failed row op reports between the row and its details.
Rows APM cannot act on report why instead of failing: a pinned row has no
version picker yet (change the pin in the host template), a local path is never
part of an update plan, an orphan is not declared in `apm.yml` for APM to
select, and `mcp`/`lsp` entries are removed by editing `~/.apm/apm.yml` and
re-running `S`. Both keys act on the live workspace only, so the footer shows
the host-template hint: a package the template still declares comes back on the
next sync, and a package installed without being declared is lost at the next
one. An uninstall re-deploys the surviving packages and can drop their trusted
`bin/` executables; the repair is `apm install -g --trust-bin <pkg>`. A row op
still runs APM's scope-wide passes, so its workspace-wide summary counts are
dropped from the footer — the full output stays in the trace log.

## Registry and tab actions

`i` opens the registry: every plugin the registered marketplaces offer, read
from `~/.apm/marketplaces.json` and the catalogs under
`~/.apm/cache/marketplace/`. APM files a catalog under the registered
marketplace name, except a URL marketplace, whose filename it hashes, so a
catalog that does not match by name is resolved by the name it carries inside.
Type to filter and use the arrow keys (or `ctrl-n`/`ctrl-p`, `home`/`end`) to
move the selection while typing continues — `j` and `k` stay text. `enter`
installs the selected plugin after a confirm, `esc` leaves. Entries already
present in the lockfile are marked and cannot be re-installed. Sync never
refreshes an existing cache, so a marketplace with no resolvable catalog says
to run `apm marketplace update`.

It also exposes sync/install (`S`), update (`U`), refresh (`R`), and the trace
log (`e`). `S` runs the same lifecycle as `omni agents sync`, so it materializes
the host template before installing and reports the divergence warning below
the list. The TUI takes no flags; use the CLI for `--frozen`, `--dry-run`, or
`--force-template`. `U` dispatches APM. The view checks for package updates on
startup; `R` reloads the manifest and lockfile and runs that check again.
Available versions appear in a separate `Updates Available` section. A failed
check leaves the package rows usable and offers `R` to retry. Full APM output
goes to the trace log, not the pane.

Agent desired/runtime state is owned by APM (`~/.apm/apm.yml`, lockfile, and
`~/.apm/marketplaces.json`). Targets come from APM, including targets added after
Omni was built; the harness readers above are the only place a client name is
hard-coded, and every one of them is read-only.
