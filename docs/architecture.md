# Architecture

Omni owns tool and dotfile configuration. APM owns all steady-state agent
desired and runtime state.

The steady-state agent boundary is a thin adapter around APM: Omni selects and
invokes APM commands, then presents their output in the CLI/TUI. APM owns
manifests, resolution, lockfiles, package installation, marketplaces, plugins,
MCP, and target deployment.

APM state lives under `~/.apm/` (`apm.yml`, `apm.lock.yaml`, and
`marketplaces.json`). Omni keeps no parallel agent manifest, ownership ledger,
or runtime deployment model.

## Agent manifest boundary

Omni's only write on the agent side is a whole-file copy: sync materializes the
optional host template `~/.config/omni/apm.yml` over `~/.apm/apm.yml`, then
invokes APM install. Omni never edits manifest fields, so the manifest stays a
dotfile-managed artifact and APM stays the sole owner of everything the install
produces.

```mermaid
sequenceDiagram
  participant User
  participant Omni
  participant APM
  participant Manifest as ~/.apm/apm.yml

  User->>Omni: omni agents sync
  Omni->>Manifest: Compare live hash with last applied
  alt Unseen or edited outside Omni
    Omni-->>User: Warn; require --force-template
  else Matches
    Omni->>Manifest: Copy host template over it
  end
  Omni->>APM: install -g
  APM-->>Omni: Resolution, lockfile, deployment
  Omni->>Omni: Record the normalized manifest hash
```

The recorded hash is Omni's whole state for this surface. A pre-APM host's old
declarations live in a read-only snapshot committed in dotfiles;
`omni agents migrate` renders them as a manifest and writes nothing.
