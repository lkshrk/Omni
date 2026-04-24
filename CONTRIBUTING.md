# Contributing to omni

## Adding a New Provider

A provider is any package manager you want `omni` to manage (e.g. `cargo`, `apt`, `winget`).

### Step 1 — Implement the interface

Create a new package under `internal/provider/<name>/`:

```go
// internal/provider/cargo/cargo.go
package cargo

import (
    "context"
    "fmt"
    "strings"

    "github.com/lkshrk/omni/internal/executor"
    "github.com/lkshrk/omni/internal/provider"
)

type Cargo struct {
    exec executor.Executor
}

func New(exec executor.Executor) *Cargo { return &Cargo{exec: exec} }

func (c *Cargo) Name() string        { return "cargo" }
func (c *Cargo) Description() string { return "Rust package manager (cargo install)" }

func (c *Cargo) Available(ctx context.Context) (bool, error) {
    _, _, err := c.exec.Run(ctx, "cargo", "--version")
    return err == nil, nil
}

func (c *Cargo) Install(ctx context.Context, t provider.Tool) error {
    pkg := t.Package
    if pkg == "" { pkg = t.Name }
    _, stderr, err := c.exec.Run(ctx, "cargo", "install", pkg)
    if err != nil {
        return fmt.Errorf("cargo install %s: %s: %w", pkg, stderr, err)
    }
    return nil
}

func (c *Cargo) Uninstall(ctx context.Context, t provider.Tool) error {
    pkg := t.Package
    if pkg == "" { pkg = t.Name }
    _, stderr, err := c.exec.Run(ctx, "cargo", "uninstall", pkg)
    if err != nil {
        return fmt.Errorf("cargo uninstall %s: %s: %w", pkg, stderr, err)
    }
    return nil
}

func (c *Cargo) Upgrade(ctx context.Context, t provider.Tool) error {
    return c.Install(ctx, t) // cargo install upgrades in-place
}

func (c *Cargo) IsInstalled(ctx context.Context, t provider.Tool) (bool, string, error) {
    pkg := t.Package
    if pkg == "" { pkg = t.Name }
    stdout, _, err := c.exec.Run(ctx, "cargo", "install", "--list")
    if err != nil {
        return false, "", fmt.Errorf("cargo install --list: %w", err)
    }
    for _, line := range strings.Split(stdout, "\n") {
        if strings.HasPrefix(line, pkg+" ") || strings.HasPrefix(line, pkg+"@") {
            parts := strings.Fields(line)
            if len(parts) >= 2 {
                return true, strings.Trim(parts[1], "v:"), nil
            }
            return true, "", nil
        }
    }
    return false, "", nil
}

func (c *Cargo) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
    stdout, _, err := c.exec.Run(ctx, "cargo", "install", "--list")
    if err != nil {
        return nil, fmt.Errorf("cargo install --list: %w", err)
    }
    var tools []provider.InstalledTool
    for _, line := range strings.Split(stdout, "\n") {
        if line == "" || strings.HasPrefix(line, " ") { continue }
        parts := strings.Fields(line)
        if len(parts) < 1 { continue }
        name := parts[0]
        ver := ""
        if len(parts) >= 2 { ver = strings.Trim(parts[1], "v:") }
        tools = append(tools, provider.InstalledTool{
            Tool:    provider.Tool{Name: name, Provider: "cargo", Package: name},
            Version: ver,
        })
    }
    return tools, nil
}
```

### Step 2 — Write tests

Create `internal/provider/cargo/cargo_test.go` using `MockExecutor`:

```go
package cargo_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/lkshrk/omni/internal/executor"
    "github.com/lkshrk/omni/internal/provider"
    "github.com/lkshrk/omni/internal/provider/cargo"
)

func TestAvailable_True(t *testing.T) {
    mock := executor.NewMockExecutor()
    mock.AddRule("cargo", []string{"--version"}, "cargo 1.78.0", "", nil)
    c := cargo.New(mock)
    ok, err := c.Available(context.Background())
    require.NoError(t, err)
    assert.True(t, ok)
}

func TestIsInstalled_Found(t *testing.T) {
    mock := executor.NewMockExecutor()
    mock.AddRule("cargo", []string{"install", "--list"}, "ripgrep v14.1.0:\n    rg\n", "", nil)
    c := cargo.New(mock)
    ok, ver, err := c.IsInstalled(context.Background(), provider.Tool{Name: "ripgrep"})
    require.NoError(t, err)
    assert.True(t, ok)
    assert.Equal(t, "14.1.0", ver)
}
```

### Step 3 — Register the provider

In `cmd/omni/main.go`, add two lines:

```go
import "github.com/lkshrk/omni/internal/provider/cargo"

// inside main(), after the other Register calls:
registry.Register(cargo.New(exec))
```

That's it — `omni` will now recognise `provider = "cargo"` in `tools.toml`.

---

## Running Tests

```bash
# Unit tests
make test

# Integration tests (isolated Docker only)
make test-integration

# Both: unit tests locally, integration tests isolated in Docker
make test-all

# Lint
make lint
```

## Code Style

- Standard `gofmt` + `goimports` formatting
- Accept interfaces, return structs
- Wrap errors with `fmt.Errorf("context: %w", err)`
- Table-driven tests; `-race` flag always on
