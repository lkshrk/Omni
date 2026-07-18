package cargo_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/cargo"
)

func newCargo(responses ...executor.MockCall) (*cargo.Provider, *executor.MockExecutor) {
	mock := &executor.MockExecutor{Responses: responses}
	return cargo.New(mock), mock
}

func tool(name string) provider.Tool {
	return provider.Tool{Name: name, Provider: "cargo", Package: name}
}

func TestProvider(t *testing.T) {
	t.Run("availability", func(t *testing.T) {
		p, _ := newCargo(executor.MockCall{Stdout: "cargo 1.88.0"})
		available, err := p.Available(context.Background())
		if err != nil || !available {
			t.Fatalf("Available() = (%v, %v), want (true, nil)", available, err)
		}

		p, _ = newCargo(executor.MockCall{Err: errors.New("not found")})
		available, err = p.Available(context.Background())
		if err != nil || available {
			t.Fatalf("Available() = (%v, %v), want (false, nil)", available, err)
		}
	})

	t.Run("mutations", func(t *testing.T) {
		tests := []struct {
			name string
			run  func(*cargo.Provider) error
			args []string
		}{
			{name: "install", run: func(p *cargo.Provider) error { return p.Install(context.Background(), tool("ripgrep")) }, args: []string{"install", "ripgrep"}},
			{name: "package override", run: func(p *cargo.Provider) error {
				return p.Install(context.Background(), provider.Tool{Name: "rg", Provider: "cargo", Package: "ripgrep"})
			}, args: []string{"install", "ripgrep"}},
			{name: "uninstall package override", run: func(p *cargo.Provider) error {
				return p.Uninstall(context.Background(), provider.Tool{Name: "rg", Provider: "cargo", Package: "ripgrep"})
			}, args: []string{"uninstall", "ripgrep"}},
			{name: "upgrade", run: func(p *cargo.Provider) error { return p.Upgrade(context.Background(), tool("ripgrep")) }, args: []string{"install", "ripgrep"}},
			{name: "uninstall", run: func(p *cargo.Provider) error { return p.Uninstall(context.Background(), tool("ripgrep")) }, args: []string{"uninstall", "ripgrep"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p, mock := newCargo(executor.MockCall{})
				if err := tt.run(p); err != nil {
					t.Fatal(err)
				}
				if call := mock.Calls[0]; call.Name != "cargo" || !reflect.DeepEqual(call.Args, tt.args) {
					t.Fatalf("call = %s %v, want cargo %v", call.Name, call.Args, tt.args)
				}
			})
		}
	})

	const installed = `cargo-edit v0.13.6:
    cargo-add
    cargo-rm
ripgrep v14.1.1 (registry+https://github.com/rust-lang/crates.io-index):
    rg
warning: ignored line
`

	t.Run("installed", func(t *testing.T) {
		p, _ := newCargo(executor.MockCall{Stdout: installed})
		tools, err := p.ListInstalled(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(tools) != 2 || tools[0].Name != "cargo-edit" || tools[0].Version != "0.13.6" || tools[1].Name != "ripgrep" || tools[1].Version != "14.1.1" {
			t.Fatalf("ListInstalled() = %#v", tools)
		}

		p, _ = newCargo(executor.MockCall{Stdout: installed})
		found, version, err := p.IsInstalled(context.Background(), tool("ripgrep"))
		if err != nil || !found || version != "14.1.1" {
			t.Fatalf("IsInstalled() = (%v, %q, %v), want (true, 14.1.1, nil)", found, version, err)
		}

		p, _ = newCargo(executor.MockCall{Stdout: installed})
		found, version, err = p.IsInstalled(context.Background(), provider.Tool{Name: "rg", Provider: "cargo", Package: "ripgrep"})
		if err != nil || !found || version != "14.1.1" {
			t.Fatalf("IsInstalled(package override) = (%v, %q, %v), want (true, 14.1.1, nil)", found, version, err)
		}

		p, _ = newCargo(executor.MockCall{Stdout: installed})
		found, version, err = p.IsInstalled(context.Background(), provider.Tool{Name: "rg", Provider: "cargo", Package: "ripgrep@14.1.1"})
		if err != nil || !found || version != "14.1.1" {
			t.Fatalf("IsInstalled(versioned package) = (%v, %q, %v), want (true, 14.1.1, nil)", found, version, err)
		}

		p, _ = newCargo(executor.MockCall{Stdout: installed})
		found, version, err = p.IsInstalled(context.Background(), tool("missing"))
		if err != nil || found || version != "" {
			t.Fatalf("IsInstalled() = (%v, %q, %v), want (false, empty, nil)", found, version, err)
		}
	})

	t.Run("search", func(t *testing.T) {
		output := "ripgrep = \"14.1.1\" # Fast line-oriented search tool\nnot a result\n"
		p, mock := newCargo(executor.MockCall{Stdout: output})
		results, err := p.Search(context.Background(), "ripgrep")
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Name != "ripgrep" || results[0].Version != "14.1.1" || results[0].Description != "Fast line-oriented search tool" {
			t.Fatalf("Search() = %#v", results)
		}
		want := []string{"search", "--color", "never", "--limit", "20", "ripgrep"}
		if call := mock.Calls[0]; call.Name != "cargo" || !reflect.DeepEqual(call.Args, want) {
			t.Fatalf("call = %s %v, want cargo %v", call.Name, call.Args, want)
		}
	})

	t.Run("errors", func(t *testing.T) {
		p, _ := newCargo(executor.MockCall{Err: errors.New("exit 101"), Stderr: "compile failed"})
		if err := p.Install(context.Background(), tool("broken")); err == nil {
			t.Fatal("Install() error = nil")
		}

		p, _ = newCargo(executor.MockCall{Err: errors.New("exit 101")})
		if _, err := p.ListInstalled(context.Background()); err == nil {
			t.Fatal("ListInstalled() error = nil")
		}
	})
}
