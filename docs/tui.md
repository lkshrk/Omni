# TUI

The Agents view reports APM state and exposes onboarding preview/confirmation
(`O`), sync/install (`S`), update (`U`), and dependency-list inspection (`R`).
Onboarding renders each item and assigns numeric keys to the target options in
that reviewed item (`a` selects all); no fixed client names live in the TUI.
It supports secret mapping (`m`), moving a dots-owned item to APM (`M`),
keeping it in dots (`d`), and keeping any item unmanaged (`x`) before
confirmation. `T`, `V`, and `X` show local status, resume recovery, and
preview/confirm cleanup.

Native filesystem items can move to APM or remain unmanaged; they cannot be
labeled as dots-owned. Target selections shown in the item detail come from
APM, including targets added after Omni was built.

Agent desired/runtime state is owned by APM (`~/.apm/apm.yml`, lockfile, and
`~/.apm/marketplaces.json`). Omni discovers and converts legacy config, dots
state, and recognizable native filesystem primitives; ordinary APM install and
audit own the resulting runtime state.
