package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
)

// RunCmd runs a subprocess and wraps any error as "errLabel: <err> (stderr: <trimmed>)".
func RunCmd(ctx context.Context, exec executor.Executor, errLabel, cmd string, args ...string) error {
	_, stderr, err := exec.Run(ctx, cmd, args...)
	if err != nil {
		return fmt.Errorf("%s: %w (stderr: %s)", errLabel, err, strings.TrimSpace(stderr))
	}
	return nil
}

// FetchJSON GETs url and decodes the body into v on a 2xx response. Returns
// the HTTP status code so callers can short-circuit on 404/etc; status is 0
// when the request or roundtrip itself failed. The response body is always
// limited to 1 MiB.
func FetchJSON(ctx context.Context, client *http.Client, url string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// Tool is a tool entry from config or the DB cache.
type Tool struct {
	Name     string
	Provider string
	Package  string            // defaults to Name if empty
	Options  map[string]string // provider-specific flags
}

// EffectivePackage returns the package identifier to pass to the provider binary.
// Falls back to Name when Package is empty.
func (t Tool) EffectivePackage() string {
	if t.Package != "" {
		return t.Package
	}
	return t.Name
}

// InstalledTool is a tool found installed on the system.
type InstalledTool struct {
	Tool
	Version string
}

// Status represents the sync state of a tool.
type Status int

const (
	StatusUnknown   Status = iota //nolint:revive
	StatusInstalled               // tool is present and up-to-date
	StatusMissing                 // tool is not installed
	StatusOutdated                // tool is installed but a newer version is available
)

// SearchResult is a package found in a provider's registry.
type SearchResult struct {
	Name        string
	Version     string
	Description string
	Provider    string
}

// Searcher is implemented by providers that support registry search.
// It is optional — providers that do not implement it are silently skipped.
type Searcher interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
}

// ConcreteResolver is optionally implemented by ecosystem providers that delegate
// to one of several concrete providers. ResolvedName returns the Name() of the
// active concrete delegate, or an error when no delegate is available.
type ConcreteResolver interface {
	ResolvedName(ctx context.Context) (string, error)
}

// BulkChecker is optionally implemented by providers that can return all installed
// tools in a single subprocess call, avoiding per-tool IsInstalled overhead.
type BulkChecker interface {
	// InstalledMap returns a lowercase-name→version map for packages the provider
	// considers user-visible installed tools.
	InstalledMap(ctx context.Context) (map[string]string, error)
}

// InstalledEntry pairs a tool version with the concrete backend that reports it.
// Returned by MultiManagerBulkChecker. ConcreteManager is empty when not installed.
type InstalledEntry struct {
	Version         string
	ConcreteManager string // e.g. "uv", "pip3", "pip"
}

// MultiManagerBulkChecker is optionally implemented by ecosystem providers that
// delegate to multiple concrete backends (e.g. python → uv/pip3/pip).
// Unlike BulkChecker (which only probes the currently-active backend),
// InstalledByManager probes ALL available backends and records which concrete
// manager actually owns each tool, enabling accurate per-tool InstalledWith
// attribution and correct WrongProv detection in the TUI.
type MultiManagerBulkChecker interface {
	InstalledByManager(ctx context.Context) (map[string]InstalledEntry, error)
}

// OutdatedChecker is optionally implemented by providers that can return all
// outdated tools in a single call.
type OutdatedChecker interface {
	// OutdatedMap returns a lowercase-name→latestVersion map of outdated tools.
	OutdatedMap(ctx context.Context) (map[string]string, error)
}

// ManagerOutdatedChecker is optionally implemented by ecosystem providers that
// can attribute outdated packages to their concrete managers.
type ManagerOutdatedChecker interface {
	// OutdatedByManager returns manager→lowercase-name→latestVersion.
	OutdatedByManager(ctx context.Context) (map[string]map[string]string, error)
}

// ManagerUpgrader is optionally implemented by ecosystem providers that can upgrade
// a tool using the concrete manager that owns the current installation.
type ManagerUpgrader interface {
	UpgradeWithManager(ctx context.Context, tool Tool, manager string) error
}

// ManagerUninstaller is optionally implemented by ecosystem providers that can
// uninstall a tool from a caller-selected concrete manager.
type ManagerUninstaller interface {
	UninstallFrom(ctx context.Context, tool Tool, manager string) error
}

// ManagerInstaller is optionally implemented by ecosystem providers that can
// install a tool using a caller-selected concrete manager.
type ManagerInstaller interface {
	InstallWithManager(ctx context.Context, tool Tool, manager string) error
}

// ManagerInstalledChecker is optionally implemented by ecosystem providers that can
// check a tool using a concrete manager rather than the currently resolved one.
type ManagerInstalledChecker interface {
	IsInstalledWithManager(ctx context.Context, tool Tool, manager string) (bool, string, error)
}

// Descriptor is optionally implemented by providers that can fetch a one-line
// description for a tool.
type Descriptor interface {
	Describe(ctx context.Context, tool Tool) (string, error)
}

// BulkDescriber is optionally implemented by providers that can fetch descriptions
// for multiple tools in a single subprocess call, avoiding per-tool overhead.
type BulkDescriber interface {
	// BulkDescribe returns a map of tool name → description for the given tools.
	// Tools not found or with no description are omitted from the result.
	BulkDescribe(ctx context.Context, tools []Tool) (map[string]string, error)
}

// CLIToolProvider is optionally implemented by providers that can distinguish
// CLI tools from pure library packages (e.g. pip).  Import uses it to
// auto-mark non-CLI packages with Ignore:true in the config so they still
// appear in the ignored section for version tracking but don't clutter the
// active tools list.
type CLIToolProvider interface {
	// CLIToolSet returns the set of lowercase package names that install at
	// least one CLI entry point.  Packages absent from the set are libraries.
	CLIToolSet(ctx context.Context) (map[string]bool, error)
}

// Provider is the extensibility interface.
// Add a new package manager by implementing this interface and
// registering it in main.go — no other code changes required.
type Provider interface {
	// Name returns the unique identifier used in config (e.g. "brew", "npm").
	Name() string
	// Description is shown in help output.
	Description() string
	// Available reports whether the provider binary is present on this system.
	Available(ctx context.Context) (bool, error)

	Install(ctx context.Context, tool Tool) error
	Uninstall(ctx context.Context, tool Tool) error
	Upgrade(ctx context.Context, tool Tool) error

	// IsInstalled reports whether tool is installed and returns its current version.
	IsInstalled(ctx context.Context, tool Tool) (bool, string, error)
	ListInstalled(ctx context.Context) ([]InstalledTool, error)
}
