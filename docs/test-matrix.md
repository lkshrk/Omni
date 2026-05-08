# Test Matrix

This matrix tracks representative coverage for every user-visible action in
`internal/actions/catalog.go`.

Status meanings:

- `yes`: representative happy path and important routing/error behavior exist.
- `partial`: some coverage exists, but a realistic use-case or error branch is missing.
- `gap`: no meaningful coverage at this layer yet.
- `n/a`: this layer intentionally does not expose the action.

Use this as a release checklist. App/shared tests should own durable behavior;
CLI integration should cover realistic command flows; TUI tests should cover
key routing, modal state, rendering, and async result handling.

| Action ID | App/shared | CLI unit | CLI integration | TUI flow/render | Gap/next fixture |
| --- | --- | --- | --- | --- | --- |
| `tools.sync` | yes | yes | yes | yes | - |
| `tools.install` | yes | yes | yes | yes | - |
| `tools.delete` | yes | yes | yes | yes | - |
| `tools.update` | yes | yes | yes | yes | - |
| `tools.update_all` | yes | yes | yes | yes | - |
| `tools.sync_all` | yes | yes | yes | yes | - |
| `tools.claim` | yes | yes | yes | yes | - |
| `tools.ignore` | yes | yes | yes | yes | - |
| `tools.change_group` | yes | yes | yes | yes | - |
| `tools.pin_provider` | yes | yes | yes | yes | - |
| `tools.reinstall_default` | yes | yes | yes | yes | - |
| `tools.refresh` | yes | yes | yes | yes | - |
| `tools.consolidate` | yes | yes | yes | yes | - |
| `tools.set_spec` | yes | yes | yes | yes | - |
| `tools.delete_spec` | yes | yes | yes | yes | - |
| `tools.normalize_provider_overrides` | yes | yes | yes | n/a | - |
| `tools.import` | yes | yes | yes | yes | - |
| `tools.switch_provider` | yes | yes | yes | n/a | - |
| `dots.sync` | yes | yes | yes | yes | - |
| `dots.discover` | yes | yes | yes | yes | - |
| `dots.add` | yes | yes | yes | yes | - |
| `dots.edit_groups` | yes | yes | yes | yes | - |
| `dots.delete` | yes | yes | yes | yes | - |
| `dots.ignore` | yes | yes | yes | yes | - |
| `dots.enable` | yes | yes | yes | yes | - |
| `dots.disable` | yes | yes | yes | yes | - |
| `dots.pull` | yes | yes | yes | n/a | - |
| `dots.push` | yes | yes | yes | n/a | - |
| `groups.create` | yes | yes | yes | yes | - |
| `groups.rename` | yes | yes | yes | yes | - |
| `groups.delete` | yes | yes | yes | yes | - |
| `groups.edit_tools` | yes | yes | yes | yes | - |
| `groups.edit_dots` | yes | yes | yes | yes | - |
| `hosts.create` | yes | yes | yes | n/a | - |
| `hosts.delete` | yes | yes | yes | yes | - |
| `hosts.edit_groups` | yes | yes | yes | yes | - |
| `settings.set` | yes | yes | yes | yes | - |
| `settings.provider` | yes | yes | yes | yes | - |
| `settings.reset` | yes | yes | yes | yes | - |
| `settings.reset_cache` | yes | yes | yes | yes | - |
| `setup.init` | yes | yes | yes | yes | - |
