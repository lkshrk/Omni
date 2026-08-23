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

## Agent onboarding boundary

Onboarding is a one-time migration owned by Omni because its inputs include
Omni's legacy config, dotfile ownership model, and recognizable native resources
under APM-reported deploy roots. Omni inventories without writing to the user's
APM home, records reviewed decisions, stages ordinary APM packages, and invokes
normal APM install/audit operations. Omni commits schema v24 last.

```mermaid
sequenceDiagram
  participant User
  participant Omni
  participant APM
  participant Config as Omni config

  User->>Omni: Preview onboarding
  Omni->>Omni: Inventory legacy, dots, and native resources
  Omni->>APM: Read target catalog in isolated HOME
  Omni-->>User: Reviewed items and blockers
  User->>Omni: Apply decisions
  Omni->>APM: Install ordinary packages and MCP
  Omni->>APM: Audit global state
  APM-->>Omni: Verified deployment
  Omni->>Config: Commit schema v24
  Omni-->>User: Complete; cleanup remains optional
```

Recovery journals and private preimages exist only for this migration under
Omni's state directory. Confirmed cleanup deletes them, while the durable APM
packages, manifest, lockfile, and completion marker remain.
