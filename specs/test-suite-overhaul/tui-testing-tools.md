# TUI testing tools

## Decision

Use [`github.com/charmbracelet/x/vttest`](https://github.com/charmbracelet/x/tree/main/vttest) with an explicit, matching [`github.com/charmbracelet/x/vt`](https://github.com/charmbracelet/x/tree/main/vt) pin.

It is the closest fit for omni: Go-native, from the Bubble Tea ecosystem, starts an arbitrary `exec.Cmd`, preserves per-test `Dir` and `Env`, owns the PTY and terminal dimensions, sends text/keys/mouse events, and exposes the current cell grid rather than accumulated ANSI history. Its `Close` handles the terminal; omni should retain its existing context deadline and process-exit checks. The package intentionally leaves waiting/assertion policy to the test, so the existing small polling helper remains useful. [Implementation](https://github.com/charmbracelet/x/blob/main/vttest/vttest.go)

Do not migrate to `teatest`, `tuikit-go/tuitest`, or the old `aschey/tui-tester`: they test a model or a generated Go test binary, not omni's already-built executable and persisted end-to-end state.

## Required behavior

The existing tests need all of the following:

- launch an already-built omni binary;
- set `exec.Cmd.Dir` and a complete isolated environment;
- use a deterministic terminal size;
- send text and key sequences;
- assert the currently visible screen, including cursor-driven overwrites and the alternate screen, not raw output history;
- poll with bounded timeouts;
- terminate and reap the process on success or failure;
- run headlessly on Linux CI;
- remain light enough that TUI coverage does not recreate the suite's runtime problem.

The current helper satisfies every process requirement with [`creack/pty`](https://github.com/creack/pty), but [`lockedBuffer.Text`](../../integration_tests/tui_integration_test.go) strips ANSI from accumulated output. That can retain text a real user can no longer see.

## Ranked shortlist

| Rank | Tool | Current screen | Process isolation | Wait/cleanup | Weight and activity | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | [Charm `x/vttest`](https://github.com/charmbracelet/x/tree/main/vttest) + [`x/vt`](https://pkg.go.dev/github.com/charmbracelet/x/vt) | Full cell snapshot, styles, cursor, modes, title, alternate screen | Accepts any `exec.Cmd`; `Dir`/`Env` remain native; PTY size and resize built in | `Close` and context-aware `Wait`; polling/assertions stay local | Pure Go, MIT, active but experimental/untagged; seven new module paths in the compatibility spike | Best fit; migrate the helper, not the tests |
| 2 | [`ActiveState/termtest`](https://github.com/ActiveState/termtest) | VT-emulated `Snapshot()` and processed matching | Arbitrary command, working directory, environment, configurable PTY rows/columns | Built-in expect timeouts, exit checks, kill/close | Small Go graph, BSD-3-Clause; mature but quiet and built on older forks | Best self-contained fallback if Charm's experimental API churns |
| 3 | [`creack/pty` + `rcarmo/go-te`](https://github.com/rcarmo/go-te) | `go-te` supplies a tested current screen, alternate buffer, scrollback, and SVG | Existing omni code already covers command, environment, directory, and size | Existing polling/context cleanup remains | Pure Go, MIT, tagged `v0.1.0`; active in 2026 and tested against pyte/ESCTest2 | Lowest-change fallback; less test-specific API |
| 4 | [Termless](https://termless.dev/api/terminal) | Rich current screen, cells, styles, modes, cursor, scrollback, snapshots | Arbitrary process with `cwd`, `env`, rows, and columns | Auto-retry matchers, stable waits, async cleanup | MIT and active; adds Node/Vitest and a second test ecosystem | Best feature set overall, wrong ecosystem cost for this Go suite |
| 5 | [`honeybadge-labs/virtui`](https://github.com/honeybadge-labs/virtui) | VT snapshot via Go SDK, screen waits and resize | `RunOpts` has `Dir`, `Env`, `Cols`, and `Rows` | Explicit wait options and `Kill` | Apache-2.0, active `v0.2.0`; requires a daemon and has a large gRPC/Docker module graph | Good agent/manual automation tool, too heavy for ordinary Go tests |
| 6 | [Microsoft `shell-use`](https://github.com/microsoft/shell-use) | Viewport/cells, colors, cursor, snapshots and SVG | Direct program run; cwd/env/size supported | Text/idle/exit waits, signals, close | MIT, very active, Rust binary plus daemon, beta | Strong external verifier; not an in-process Go test library |
| 7 | [`vurte/tui-td`](https://github.com/vurte/tui-td) | Current cell grid, semantic selectors, snapshots, PNG/HTML | JSON runner supports command, environment, chdir, rows, columns | Auto-wait, stable/exit waits, teardown | MIT, active `v0.2.x`; requires Ruby 3 | Capable language-neutral runner, unnecessary runtime for omni |
| 8 | [Curtaincall](https://github.com/thekevinscott/curtaincall) | pyte-backed visible viewport, cells and snapshots | Real command, env and size; public fixture docs do not expose cwd | Pytest auto-wait locators and fixture cleanup | MIT, active but requires Python 3.12, pytest, pexpect and pyte | Excellent Python choice, incomplete fit here |
| 9 | [`testty`](https://docs.rs/testty/latest/testty/) | `vt100` cell frames, regions, styles and snapshots | Real arbitrary binary in a PTY | Scenario waits and session cleanup | Apache-2.0 and active; Rust library/toolchain | Strong Rust choice, no benefit over the Charm-native option |

### `x/vttest` compatibility spike

A local spike proved the required path with a real PTY child: start process, overwrite prior output, then assert only the resulting current-screen snapshot. No Bubble Tea or ultraviolet upgrade was needed.

There is one install footgun. `go get github.com/charmbracelet/x/vttest@latest` currently selects the November 2025 `x/vt` revision from [`vttest/go.mod`](https://github.com/charmbracelet/x/blob/main/vttest/go.mod). That revision does not compile against omni's newer ultraviolet because it expects `uv.Buffer.Touched`. Explicitly selecting the current [`x/vt`](https://github.com/charmbracelet/x/tree/main/vt) revision fixed compilation and the runtime spike. An implementation must pin **both** `x/vttest` and `x/vt`; do not accept the stale transitive `x/vt` selection.

The spike added seven module paths to omni's graph: `vttest`, `vt`, `xpty`, `conpty`, `exp/ordered`, `freetype`, and `x/image`. Most terminal primitives already overlap with omni's Charm dependencies. This is materially smaller than adding a daemon or a Node/Python/Ruby test lane.

## Eliminated options

### Bubble Tea model harnesses

- [`charmbracelet/x/exp/teatest`](https://github.com/charmbracelet/x/tree/main/exp/teatest) drives a `tea.Model` and captures program output; its module depends on Bubble Tea v1, while omni uses `charm.land/bubbletea/v2`. It cannot prove the compiled CLI boundary, PTY behavior, external commands, or persisted state. [Module](https://github.com/charmbracelet/x/blob/main/exp/teatest/go.mod)
- [`moneycaringcoder/tuikit-go/tuitest`](https://pkg.go.dev/github.com/moneycaringcoder/tuikit-go/tuitest) has good virtual-screen and record/replay assertions, but `NewTestModel` still accepts a model and its module also uses Bubble Tea v1. [Module](https://github.com/moneycaringcoder/tuikit-go/blob/main/go.mod)
- [`knz/catwalk`](https://github.com/knz/catwalk) is a data-driven model/View harness, not a black-box process test.
- [`aschey/tui-tester`](https://github.com/aschey/tui-tester) does emulate a screen, but [`Suite.NewTester`](https://github.com/aschey/tui-tester/blob/main/suite.go) always builds a Go test binary with `go test -c`; it cannot simply launch omni's prepared binary. It also targets Bubble Tea `v0.22.0` and has been inactive since 2022. [Module](https://github.com/aschey/tui-tester/blob/main/go.mod)

These remain useful for cheap deterministic component tests, but they must not replace black-box flow coverage.

### PTY/expect libraries without a screen oracle

- [`Netflix/go-expect`](https://github.com/Netflix/go-expect), [`google/goexpect`](https://github.com/google/goexpect), and Rust [`expectrl`](https://github.com/zhiburt/expectrl) automate PTY input and raw-stream matching. They do not maintain the current rendered cell grid. Netflix explicitly leaves process lifecycle to callers.
- [`joeycumines/go-prompt/termtest`](https://github.com/joeycumines/go-prompt/tree/main/termtest) adds command/env/cwd/size, waits and cleanup, but its [`Snapshot`](https://github.com/joeycumines/go-prompt/blob/main/termtest/console.go) is an offset into accumulated output and conditions normalize ANSI rather than emulate the displayed screen.
- [`creack/pty`](https://github.com/creack/pty) is a good low-level primitive and is already installed, but it intentionally supplies PTY mechanics rather than terminal emulation or assertions.

Adding an expect wrapper alone would make the API nicer without fixing the actual correctness gap.

### Emulator-only alternatives

- [`rcarmo/go-te`](https://github.com/rcarmo/go-te) is the strongest fallback: pure Go, current screen and alternate buffer, tagged, MIT, and backed by pyte/ESCTest2 behavior tests.
- [`charmbracelet/x/vt`](https://pkg.go.dev/github.com/charmbracelet/x/vt) is the emulator under the recommended `vttest` package; using it alone would preserve more custom PTY code for no benefit.
- [`maximhq/vt10x`](https://github.com/maximhq/vt10x), [`cliofy/govte`](https://github.com/cliofy/govte), and [`taigrr/bubbleterm`](https://github.com/taigrr/bubbleterm) provide screen emulation, but not a stronger testing harness. The latter two are young, and vt10x's public material still points at its older upstream.
- [`charmbracelet/x/cellbuf`](https://github.com/charmbracelet/x/tree/main/cellbuf) is a cell-buffer/rendering primitive, not a black-box process harness or complete VT parser.

### External recorders and runners

- [VHS](https://github.com/charmbracelet/vhs) can type keys, wait on output, capture frames, and generate text goldens, but it requires `ttyd` and `ffmpeg` and is optimized for recordings. That is slower and less ergonomic than assertions inside the existing Go fixtures.
- [`tmux-tui-testing`](https://github.com/SirMoM/tmux-tui-testing) requires a system `tmux`, matches captured text, and introduces named-session/global-state concerns that work against parallel tests.
- `testty`, Termless, Curtaincall, `tui-td`, `shell-use`, and virtui are credible full-stack tools, but each adds a second runtime, daemon, or test framework. Adopt one only if omni later needs cross-language scripts, visual artifacts, mouse/style locators, or agent-driven exploratory testing beyond the Go CI suite.

## Minimal migration shape

1. Replace the manual `pty.StartWithSize`/ANSI-stripping capture with `vttest.NewTerminal` and `Terminal.Start(exec.Cmd)`.
2. Keep omni's existing isolated `exec.Cmd.Dir`, `exec.Cmd.Env`, context deadline, configuration polling, and explicit exit assertions.
3. Make `waitForScreen` poll `Terminal.Snapshot()` or `Terminal.Emulator.String()` so assertions see only the current screen.
4. Keep tests semantic (`Dashboard`, selected tool, persisted config) rather than adding full-screen goldens. Use cell/style snapshots only when styling or layout is the behavior under test.
5. Pin compatible `x/vttest` and `x/vt` revisions together and retain one overwrite/alternate-screen regression test for the harness itself.

This is a helper migration, not a rewrite of the TUI journeys.
