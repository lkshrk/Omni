package apm_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/executor"
)

func TestClientCommands(t *testing.T) {
	tests := []struct {
		name  string
		scope apm.Scope
		run   func(*apm.Client) (apm.Result, error)
		args  []string
	}{
		{"uninstall project", apm.Project, func(c *apm.Client) (apm.Result, error) { return c.Uninstall(context.Background(), "owner/pkg") }, []string{"uninstall", "owner/pkg"}},
		{"uninstall global", apm.Global, func(c *apm.Client) (apm.Result, error) { return c.Uninstall(context.Background(), "owner/pkg") }, []string{"uninstall", "--global", "owner/pkg"}},
		{"install only packages refreshing refs", apm.Global, func(c *apm.Client) (apm.Result, error) {
			return c.InstallOnly(context.Background(), apm.SurfacePackages, []string{"claude"}, apm.InstallOptions{Update: true})
		}, []string{"install", "-g", "--only", "apm", "--update", "--target", "claude"}},
		{"install only mcp refreshing refs as a dry run", apm.Global, func(c *apm.Client) (apm.Result, error) {
			return c.InstallOnly(context.Background(), apm.SurfaceMcp, []string{"codex"}, apm.InstallOptions{Update: true, DryRun: true})
		}, []string{"install", "-g", "--only", "mcp", "--update", "--dry-run", "--target", "codex"}},
		{"install only packages targeted", apm.Global, func(c *apm.Client) (apm.Result, error) {
			return c.InstallOnly(context.Background(), apm.SurfacePackages, []string{"claude", "codex"}, apm.InstallOptions{})
		}, []string{"install", "-g", "--only", "apm", "--target", "claude,codex"}},
		{"install only mcp dry run no targets", apm.Global, func(c *apm.Client) (apm.Result, error) {
			return c.InstallOnly(context.Background(), apm.SurfaceMcp, nil, apm.InstallOptions{DryRun: true})
		}, []string{"install", "-g", "--only", "mcp", "--dry-run"}},
		{"install only frozen replays a surface", apm.Global, func(c *apm.Client) (apm.Result, error) {
			return c.InstallOnly(context.Background(), apm.SurfaceMcp, []string{"claude"}, apm.InstallOptions{Frozen: true})
		}, []string{"install", "-g", "--frozen", "--only", "mcp", "--target", "claude"}},
		{"search", apm.Project, func(c *apm.Client) (apm.Result, error) {
			return c.Search(context.Background(), "security@skills")
		}, []string{"search", "security@skills"}},
		{"audit", apm.Project, func(c *apm.Client) (apm.Result, error) { return c.Audit(context.Background()) }, []string{"audit", "--ci", "--format", "json"}},
		{"targets", apm.Project, func(c *apm.Client) (apm.Result, error) { return c.Targets(context.Background()) }, []string{"targets", "--json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "out\n", Stderr: "warning\n"}}}
			result, err := tt.run(apm.New(mock, tt.scope))
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			if result.Stdout != "out\n" || result.Stderr != "warning\n" {
				t.Fatalf("result = %#v", result)
			}
			if len(mock.Calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(mock.Calls))
			}
			if mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, tt.args) {
				t.Fatalf("call = %s %v, want apm %v", mock.Calls[0].Name, mock.Calls[0].Args, tt.args)
			}
		})
	}
}

func TestClientRejectsEmptyPackageLists(t *testing.T) {
	for _, run := range []func(*apm.Client) error{
		func(c *apm.Client) error { _, err := c.Uninstall(context.Background()); return err },
		func(c *apm.Client) error {
			_, err := c.InstallOnly(context.Background(), "", nil, apm.InstallOptions{})
			return err
		},
	} {
		mock := &executor.MockExecutor{}
		err := run(apm.New(mock, apm.Project))
		if err == nil {
			t.Fatal("expected error")
		}
		if len(mock.Calls) != 0 {
			t.Fatalf("calls = %d, want 0", len(mock.Calls))
		}
	}
}

func TestClientRejectsUnsupportedGlobalReadCommands(t *testing.T) {
	tests := []func(*apm.Client) error{
		func(c *apm.Client) error { _, err := c.Audit(context.Background()); return err },
		func(c *apm.Client) error { _, err := c.Targets(context.Background()); return err },
	}

	for _, run := range tests {
		mock := &executor.MockExecutor{}
		err := run(apm.New(mock, apm.Global))
		if !errors.Is(err, apm.ErrUnsupportedScope) {
			t.Fatalf("error = %v, want ErrUnsupportedScope", err)
		}
		if len(mock.Calls) != 0 {
			t.Fatalf("calls = %d, want 0", len(mock.Calls))
		}
	}
}

func TestInstallOnlyRejectsProjectScope(t *testing.T) {
	for _, surface := range []string{apm.SurfacePackages, apm.SurfaceMcp} {
		mock := &executor.MockExecutor{}
		_, err := apm.New(mock, apm.Project).InstallOnly(context.Background(), surface, []string{"claude"}, apm.InstallOptions{})
		if !errors.Is(err, apm.ErrUnsupportedScope) {
			t.Fatalf("error = %v, want ErrUnsupportedScope", err)
		}
		if len(mock.Calls) != 0 {
			t.Fatalf("calls = %d, want 0", len(mock.Calls))
		}
	}
}

func TestClientMapsMissingExecutable(t *testing.T) {
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Err: &exec.Error{Name: "apm", Err: exec.ErrNotFound}}}}
	_, err := apm.New(mock, apm.Global).InstallOnly(context.Background(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if !errors.Is(err, apm.ErrNotInstalled) {
		t.Fatalf("error = %v, want ErrNotInstalled", err)
	}
	if !strings.Contains(err.Error(), "apm executable not found") {
		t.Fatalf("unclear error: %v", err)
	}
}

func TestClientMapsMissingExecutableFromPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := apm.New(executor.New(), apm.Global).InstallOnly(t.Context(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if !errors.Is(err, apm.ErrNotInstalled) {
		t.Fatalf("error = %v, want ErrNotInstalled", err)
	}
}

// APM is a click CLI and reports most failures on stdout with an empty stderr.
func TestClientNonzeroExitFallsBackToStdoutDetail(t *testing.T) {
	sentinel := errors.New("exit status 1")
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{
		Stdout: "No apm.yml found in ~/.apm/. Run 'apm install -g <org/repo>' to create one.\n",
		Err:    sentinel,
	}}}
	_, err := apm.New(mock, apm.Global).InstallOnly(context.Background(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error does not wrap executor error: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "apm install failed") || !strings.Contains(got, "No apm.yml found") {
		t.Fatalf("unclear error: %v", err)
	}
}

func TestClientNonzeroExitTruncatesLongDetailToTail(t *testing.T) {
	long := strings.Repeat("progress line\n", 500) + "final error: ref not found\n"
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: long, Err: errors.New("exit status 1")}}}
	_, err := apm.New(mock, apm.Global).InstallOnly(context.Background(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "final error: ref not found") {
		t.Fatalf("tail lost: %v", err)
	}
	if len(err.Error()) > 3000 {
		t.Fatalf("error too long: %d bytes", len(err.Error()))
	}
}

func TestClientNonzeroExitPreservesResultAndErrorDetail(t *testing.T) {
	sentinel := errors.New("exit status 1")
	mock := &executor.MockExecutor{Responses: []executor.MockCall{{
		Stdout: "partial output\n",
		Stderr: "  package not found\n",
		Err:    sentinel,
	}}}
	result, err := apm.New(mock, apm.Global).InstallOnly(context.Background(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if result.Stdout != "partial output\n" || result.Stderr != "  package not found\n" {
		t.Fatalf("result = %#v", result)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error does not wrap executor error: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "apm install failed") || !strings.Contains(got, "package not found") {
		t.Fatalf("unclear error: %v", err)
	}
}

// An `apm update` invocation always redeploys the MCP surface into every target of the run, and an install
// without --only does the same, so no client method may build either.
func TestClientNeverInvokesAnUnscopedDeploy(t *testing.T) {
	source, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("reading client source: %v", err)
	}
	for _, forbidden := range []string{`"update"`, `"--yes"`} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("client.go still builds an apm update invocation (%s)", forbidden)
		}
	}
	if !bytes.Contains(source, []byte(`"--only", surface`)) {
		t.Fatal("client.go no longer scopes its install to a single manifest surface")
	}
}
