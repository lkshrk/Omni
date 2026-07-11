# Development

## Build

```sh
make build
./bin/omni --help
```

Useful run targets:

```sh
make tui-live
make tui-dev
make cli-live ARGS='tools list'
make cli-dev ARGS='tools list'
```

`*-dev` targets use isolated config and cache paths under `/private/tmp`.

## Tests

```sh
make test
```

`make test` runs shell regressions and `go test -race -trimpath ./...`.

Integration tests must use the Docker-isolated target:

```sh
make test-integration
```

Do not run integration tests directly against the local machine. Dots and
package-manager flows intentionally mutate files and package-manager state in
their isolated environments.

## Lint

```sh
make lint
```

The lint target keeps caches under `.tmp/` when cache environment variables are
not already set.

## Regenerate The Schema

```sh
make gen-schema
```

Schema version changes are explicit product decisions. Do not bump the settings
version just because generated schema output changed.

## Documentation

Strict docs builds run in the docs Docker image.

Build strictly:

```sh
make docs-build
```

If Docker Desktop is installed but `docker` is not on `PATH`, pass the binary:

```sh
make docs-build DOCKER=/Applications/Docker.app/Contents/Resources/bin/docker
```

For iterative local serving, install MkDocs into your own environment and run:

```sh
mkdocs serve
```

The `Docs` GitHub Actions workflow builds the MkDocs site on pull requests and
deploys it to GitHub Pages from `main`.

See [Documentation Maintenance](documentation-maintenance.md) for the page
ownership matrix, CLI drift checks, and docs quality gates.

## Demo GIF

```sh
make demo-gif
```

The demo uses VHS through `rtk vhs` when available, otherwise `vhs`. Rendering
may need a less restrictive local environment because VHS allocates a recorder
port.

## Releases

Releases are GoReleaser based and use Conventional Commit subjects for release
notes. Release automation is CI-gated by commit SHA.

## More References

- [Architecture](architecture.md)
- [Documentation maintenance](documentation-maintenance.md)
- [Test matrix](test-matrix.md)
- [Contributing guide](https://github.com/lkshrk/omni/blob/main/CONTRIBUTING.md)
- [Settings schema](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.v16.schema.json)
