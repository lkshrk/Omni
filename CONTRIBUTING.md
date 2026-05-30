# Contributing to omni

This project keeps durable behavior behind `internal/app`, then exposes it
through both CLI and TUI surfaces. Keep changes small, covered, and aligned with
the existing JSON config model.

## Development Loop

```sh
make build
make lint
make test
```

Integration coverage must use the isolated target:

```sh
make test-integration
```

Do not run integration fixtures directly against your real home directory or
real package-manager state. For docs-only changes, use the docs checks in
[docs/documentation-maintenance.md](docs/documentation-maintenance.md).

## Code Style

- Use standard `gofmt` and `goimports` formatting.
- Keep user-visible durable behavior in `internal/app` first.
- Add or update focused tests at the app boundary before CLI/TUI routing tests.
- Wrap errors with context, usually `fmt.Errorf("context: %w", err)`.
- Do not silently discard errors with `_ = someFunc()` or `_, _ = someFunc()`.
  The only acceptable ignored errors are deferred cleanup/no-op cases where the
  code makes that intent explicit.
- Prefer existing helpers and project patterns before adding new abstractions or
  dependencies.

## Config Model

Omni uses JSON config in `settings.json`; it does not use TOML config.

Common shape:

```json
{
  "version": 1,
  "settings": {},
  "tools": {
    "ripgrep": { "provider": "system" }
  },
  "groups": [
    { "name": "dev", "tools": ["ripgrep"] }
  ],
  "hosts": {
    "workstation": ["dev"]
  }
}
```

Prefer portable providers in config:

- `system`
- `node`
- `python`

Use concrete managers such as `brew`, `apt`, `uv`, or `pip3` as
`install_with` pins only when a specific manager is required.

## Adding Or Changing Providers

Provider registration lives in `internal/app.App.initProviderRegistry`. Concrete
providers are always registered so Omni can inspect installed state. Ecosystem
providers are registered unless disabled for the current host.

When adding a provider:

1. Implement the provider package under `internal/provider/<name>/`.
2. Add provider tests next to the package.
3. Register it in `internal/app.App.initProviderRegistry`.
4. Add provider metadata in `internal/provider/catalog.go`.
5. Update docs under `docs/providers.md`, `docs/architecture.md`, and the
   schema/docs pages if config values change.

Concrete providers should implement only the capabilities they can support
reliably. If a manager can bulk-list installed, outdated, or described packages,
prefer the bulk interface over serial command calls.

## CLI/TUI Parity

For user-visible mutations:

1. Add the shared operation in `internal/app`.
2. Add app-level tests for config, cache, provider calls, and filesystem
   effects.
3. Wire the CLI command or flag.
4. Wire the TUI action to the same app operation.
5. Keep CLI/TUI tests focused on routing, confirmation, key handling, output,
   and rendering.

TUI-only behavior is acceptable for presentation, navigation, onboarding, and
interactive affordances. Durable actions that mutate config, providers, the DB,
or the filesystem need a CLI equivalent or a documented exception.

## Documentation

Docs live under `docs/` and are served with MkDocs Material on GitHub Pages.
Use `uv` for docs dependencies:

```sh
python3 -m venv .tmp/docs-venv
uv pip install --python .tmp/docs-venv/bin/python -r docs/requirements.txt
.tmp/docs-venv/bin/mkdocs build --strict
```

When command behavior changes, update both the task guide and reference page:

- task guide: `docs/tools.md`, `docs/dotfiles.md`, `docs/providers.md`, or
  another user-facing guide
- reference: `docs/cli.md` and `docs/command-matrix.md`
- config shape: `docs/configuration.md`, `docs/schema-reference.md`, and
  `spec/omni.settings.v1.schema.json`

## Commit Discipline

Use Conventional Commit subjects and keep unrelated changes in separate commits.
Do not commit local agent notes, scratch plans, generated private state, or
machine-specific caches.
