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
make -j2 test
```

This runs shell regressions and `go test -race -trimpath ./...` concurrently.
Every process gets a unique disposable sandbox with isolated HOME, XDG, Omni,
temporary, Git, package-manager, and language-tool roots. The runner sanitizes
credentials and network proxy settings; tests fail before mutation when a
writable path escapes the sandbox. Use `scripts/run-test-safe.sh` for focused
local `go test` commands that can touch state.

Integration tests must use the Docker-isolated target:

```sh
make test-integration
```

This runs only integration-tagged coverage with the race detector; the full
unit suite remains in `make test`. Containers provide controlled external
tools, permissions, HOME/config roots, Git, and real-binary/PTY execution.
They use disposable filesystems with no host HOME, Docker socket, source mount,
or network access during the test run. The host-side Docker wrapper rejects
non-local daemons and strips Docker, Buildx, BuildKit, proxy, certificate, and
credential configuration before building or launching containers.

Do not run integration tests directly against the local machine. Dots and
package-manager flows intentionally mutate files and package-manager state in
their isolated environments.

`test/flows.json` is the machine-readable capability catalog. `make check-flows`
validates command/action coverage and declared test selectors;
`make gen-flows` refreshes the generated capability and gap tables in the
[test matrix](test-matrix.md). CI accepts required evidence only from the exact
declared lane, OS, tags, package, and test/subtest after an uncached passing run.

`make test-canary` is reserved for opt-in live APM contract checks. It runs only
`TestCanary*` tests behind the `canary` build tag in `internal/agent` and
`internal/app`, keeping them out of every other test target. The APM hard
migration removed the former pre-APM canaries, so no tests currently match
this target; the scheduled CI invocation performs no external checks.

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

Keep README as the minimal entry point and update the focused guide plus its
reference page:

| Change | Review |
| --- | --- |
| CLI command or flag | [CLI Reference](cli.md), [Command Matrix](command-matrix.md) |
| Configuration or state | [Configuration](configuration.md), [State And Files](state-and-files.md) |
| Tool/provider behavior | [Tools](tools.md), [Providers](providers.md), [Troubleshooting](troubleshooting.md) |
| Dotfile behavior | [Dotfiles](dotfiles.md), [Safety Model](safety.md), [Runbooks](runbooks.md) |
| Agent/APM behavior | [Agents](agents.md), [CLI Reference](cli.md), [TUI](tui.md) when interactive |

Use `omni <command> --help` as the final syntax source. `make docs-build` is the
local strict-build check. The [Docs workflow](https://github.com/lkshrk/omni/blob/main/.github/workflows/docs.yml)
also checks links, anchors, headings, and placeholder markers. Prefer
cross-links over copying long explanations between pages.

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
- [Test matrix](test-matrix.md)
- [Contributing guide](https://github.com/lkshrk/omni/blob/main/CONTRIBUTING.md)
- [Settings schema](https://github.com/lkshrk/omni/blob/main/spec/omni.settings.schema.json)
