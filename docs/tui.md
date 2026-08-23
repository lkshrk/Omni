# TUI

The Agents view reports APM state and exposes onboarding preview/confirmation
(`O` globally, `P` for the current project), sync/install (`S`), update (`U`),
and dependency-list inspection (`R`).
Onboarding renders each item and assigns numeric keys to the target options in
that reviewed item (`a` selects all); no fixed client names live in the TUI.
It also supports origin, executable, secret binding, and exclusion resolutions
before confirmation. `T`, `V`, and `X`
show joined status, resume recovery, and preview/confirm cleanup.

Agent desired/runtime state is owned by APM (`~/.apm/apm.yml`, lockfile, and
`~/.apm/marketplaces.json`). The TUI delegates discovery, adoption, resolution,
deployment, and audit to APM; Omni only coordinates legacy v24 commit last.
Project onboarding binds execution and recovery to its reviewed workspace root.
