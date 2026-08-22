# TUI

The Agents view reports APM state and exposes onboarding preview/confirmation
(`O`), sync/install (`S`), update (`U`), and dependency-list inspection (`R`).
Onboarding renders each item and supports target, origin, executable, secret
binding, and exclusion resolutions before confirmation. `T`, `V`, and `X`
show joined status, resume recovery, and preview/confirm cleanup.

Agent desired/runtime state is owned by APM (`~/.apm/apm.yml`, lockfile, and
`~/.apm/marketplaces.json`). The TUI delegates discovery, adoption, resolution,
deployment, and audit to APM; Omni only coordinates legacy v24 commit last. MCP
is host-global for APM-supported user-global targets.
