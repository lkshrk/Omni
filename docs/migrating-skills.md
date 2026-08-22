# Agent migration

Agent skills and packages are now owned by APM. Omni no longer imports,
adopts, stores, or resolves legacy skill installations.

Use APM to declare packages, then run `omni agents sync` to invoke APM's global
install lifecycle. Legacy `.skill-lock.json` files and old agent directories
are not read.
