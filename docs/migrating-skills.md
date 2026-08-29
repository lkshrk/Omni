# Agent migration

Agent desired and runtime state is now owned by APM. Omni no longer stores
agent declarations, so a machine that still ran the pre-APM schema migrates by
rendering its old declarations as an APM manifest.

The pre-migration configuration is preserved as a snapshot directory committed
in the dotfiles repo, next to the real `settings.json` that Omni's config path
symlinks to.

## Render the manifest

```sh
omni agents migrate --host workstation
```

This reads the snapshot, prints the apm.yml those declarations map to, and
writes nothing. Trailing `# apm marketplace add` comment lines cover the
marketplace registrations apm.yml cannot express.

Omni finds the snapshot by resolving the loaded config path through its symlink
and globbing `.omni-apm-migration-backup-*` next to it. If there is no such
directory, or more than one, pass `--snapshot /path/to/backup-dir`.

Only the groups active for that host are rendered: the groups assigned to the
host, plus a group named after the host itself. See
[Agents](agents.md) for the field-by-field mapping, including how per-entry
`agents` lists become `targets:` and how `${VAR}` becomes `${env:VAR}`.

## Adopt it

Review the output, then save it as the host template and sync:

```sh
omni agents migrate --host workstation > ~/.config/omni/apm.yml
omni agents sync --force-template
```

Keep the printed `# apm marketplace add` comment lines in the template: sync
registers any marketplace they declare that APM does not know yet, so no manual
registration step is needed.

`--force-template` is required here: the first sync that finds both a template
and an existing `~/.apm/apm.yml` refuses to overwrite the live manifest and
warns instead, so the two can be compared before the template wins.

Adapter-specific state that cannot be represented losslessly—such as some
client databases or native MCP formats—is not rendered. Move it by hand or
leave it unmanaged; migration never guesses it into an APM dependency.
