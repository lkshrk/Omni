# Schema Reference

`settings.json` schema version 24 covers Omni tools, dotfiles, providers,
hosts, groups, and ignore lists.

Agent resources are deliberately absent. APM owns agent desired/runtime state
and stores it in `~/.apm/apm.yml`, `~/.apm/apm.lock.yaml`, and
`~/.apm/marketplaces.json`.

The top-level `agents` field and agent-specific settings/group fields are
removed. Loading a configuration containing them fails with an actionable
message directing the user to APM; Omni does not import or migrate those
resources.
