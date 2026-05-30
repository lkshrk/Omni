# Installation

## Requirements

Omni is a Go CLI/TUI application.

- Release archives do not require Go.
- `go install` requires the Go version declared in `go.mod`.
- Package managers that Omni should use must be available on `PATH`.
- Dotfile sync requires GNU Stow. Homebrew and Linux packages install the
  `stow` package as a dependency; release archives and `go install` users need
  `stow` on `PATH`.

## Dependency Handling By Channel

| Install channel | GNU Stow handling |
| --- | --- |
| Homebrew | The generated formula depends on `stow`; `brew install omni` installs it when needed. |
| Linux packages | The `.deb`, `.rpm`, `.apk`, and Arch packages declare `stow` as a dependency. Use the distro package manager so dependencies are resolved. |
| Release archives | Archives contain only the `omni` binary. Install `stow` separately before using dotfile sync. |
| `go install` | Go installs only the `omni` binary. Install `stow` separately before using dotfile sync. |

Low-level package tools such as `dpkg -i` and `rpm -i` may report a missing
`stow` dependency instead of fetching it. Prefer commands that resolve
dependencies, such as `apt install ./omni.deb`, `dnf install ./omni.rpm`,
`zypper install ./omni.rpm`, `apk add ./omni.apk`, or `pacman -U <package>`.

## Platform Support

| Platform | Status | Notes |
| --- | --- | --- |
| macOS | supported | Homebrew is the expected system package manager. User services use launchd where available. |
| Linux | supported | Native managers include `apt`, `apk`, `dnf`, `zypper`, and `pacman`. User services use systemd where available. |
| Windows | not documented | Windows support is not part of the current tested contract. |

Supported built-in providers:

| Ecosystem | Portable provider | Concrete managers |
| --- | --- | --- |
| System packages | `system` | `apt`, `apk`, `dnf`, `zypper`, `pacman`, `brew` |
| Node packages | `node` | `bun`, `pnpm`, `npm` |
| Python packages | `python` | `uv`, `pip3` |

## Install With Go

```sh
go install github.com/lkshrk/omni/cmd/omni@latest
```

Make sure `$GOPATH/bin` or `$GOBIN` is on `PATH`.

## Install With Homebrew

```sh
brew tap lkshrk/tap
brew install omni
```

## Install From Releases

GitHub releases publish macOS/Linux archives and Linux packages, including
`.deb`, `.rpm`, `.apk`, and Arch Linux packages.

Download the artifact for your platform from:

```text
https://github.com/lkshrk/omni/releases
```

## Upgrade

Use the same channel you installed from:

```sh
go install github.com/lkshrk/omni/cmd/omni@latest
brew upgrade omni
```

Then verify the local state:

```sh
omni doctor
omni reconcile
```

## Shell Completions

Omni uses Cobra shell completions:

```sh
omni completion zsh > "${fpath[1]}/_omni"
omni completion bash > ~/.local/share/bash-completion/completions/omni
omni completion fish > ~/.config/fish/completions/omni.fish
```

Restart the shell after installing completions.
