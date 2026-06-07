package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	Name           string
	Version        string
	Description    string
	Provider       string // provider suitable for config/install after app-layer normalization
	SourceProvider string // provider that produced the registry result
	Source         SourceMetadata
	Options        map[string]string
	Privilege      PrivilegePlan
}

const SourceTypeGitHub = "github"

// SourceMetadata describes an upstream/source repository hint reported by a provider.
type SourceMetadata struct {
	Type  string
	Owner string
	Repo  string
	URL   string
}

// GitHubSourceHint derives a GitHub SourceMetadata from the first of the given
// values that parses as a GitHub repository URL/spec. It accepts https/http/ssh
// and git+ prefixed forms. Returns the zero value when none parse.
func GitHubSourceHint(values ...string) SourceMetadata {
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		value = strings.TrimPrefix(value, "git+")
		value = strings.TrimPrefix(value, "https://")
		value = strings.TrimPrefix(value, "http://")
		value = strings.TrimPrefix(value, "git@")
		value = strings.TrimPrefix(value, "www.")
		switch {
		case strings.HasPrefix(value, "github.com:"):
			value = strings.TrimPrefix(value, "github.com:")
		case strings.HasPrefix(value, "github.com/"):
			value = strings.TrimPrefix(value, "github.com/")
		default:
			continue
		}
		parts := strings.Split(value, "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		repo := strings.TrimSuffix(parts[1], ".git")
		if repo == "" {
			continue
		}
		return SourceMetadata{
			Type:  SourceTypeGitHub,
			Owner: parts[0],
			Repo:  repo,
			URL:   "https://github.com/" + parts[0] + "/" + repo,
		}
	}
	return SourceMetadata{}
}

// Searcher is implemented by providers that support registry search.
// It is optional — providers that do not implement it are silently skipped.
type Searcher interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
}

// ConcreteResolver is optionally implemented by provider families that delegate
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

// InstalledMetadata pairs the installed version with provider-specific metadata
// learned during a bulk installed scan.
type InstalledMetadata struct {
	Version   string
	Privilege PrivilegePlan
	Source    SourceMetadata
}

// MetadataBulkChecker is optionally implemented by providers whose installed
// scan can also report metadata that should be cached with the tool row.
type MetadataBulkChecker interface {
	InstalledMetadataMap(ctx context.Context) (map[string]InstalledMetadata, error)
}

// InstalledEntry pairs a tool version with the concrete backend that reports it.
// Returned by MultiManagerBulkChecker. ConcreteManager is empty when not installed.
type InstalledEntry struct {
	Version         string
	ConcreteManager string // e.g. "uv", "pip3", "pip"
}

// OutdatedInfo describes a PM-reported update, including optional PM metadata
// for the latest version.
type OutdatedInfo struct {
	LatestVersion string
	AvailableAt   *time.Time
	DateSource    string
}

// MultiManagerBulkChecker is optionally implemented by provider families that
// delegate to multiple concrete backends (e.g. python → uv/pip3/pip).
// Unlike BulkChecker (which only probes the currently-active backend),
// InstalledByManager probes ALL available backends and records which concrete
// manager actually owns each tool, enabling accurate per-tool InstalledWith
// attribution and correct WrongProv detection in the TUI.
type MultiManagerBulkChecker interface {
	InstalledByManager(ctx context.Context) (map[string]InstalledEntry, error)
}

// SelfPackageUpgradeChecker is optionally implemented by providers that manage a
// package representing the manager itself (e.g. pip's own "pip" package). Some
// environments forbid the manager from upgrading itself (PEP 668 externally
// managed Python), so the package must not be presented as upgradeable.
type SelfPackageUpgradeChecker interface {
	// SelfPackageName is the package name representing the manager itself.
	SelfPackageName() string
	// SelfPackageUpgradeable reports whether the manager can upgrade its own
	// package in the current environment.
	SelfPackageUpgradeable(ctx context.Context) bool
}

// MetadataRefresher is optionally implemented by providers whose outdated
// detection relies on a locally-cached package index that can go stale (e.g.
// Homebrew taps). Refreshing pulls the latest index so OutdatedMap sees newly
// published versions. Callers invoke it only for user-initiated refreshes, not
// passive background scans, because it incurs network latency.
type MetadataRefresher interface {
	RefreshMetadata(ctx context.Context) error
}

// OutdatedChecker is optionally implemented by providers that can return all
// outdated tools in a single call.
type OutdatedChecker interface {
	// OutdatedMap returns a lowercase-name→latestVersion map of outdated tools.
	OutdatedMap(ctx context.Context) (map[string]string, error)
}

// OutdatedInfoChecker is optionally implemented by providers that can attach
// package-manager metadata to outdated results.
type OutdatedInfoChecker interface {
	OutdatedInfoMap(ctx context.Context) (map[string]OutdatedInfo, error)
}

// ManagerOutdatedChecker is optionally implemented by provider families that
// can attribute outdated packages to their concrete managers.
type ManagerOutdatedChecker interface {
	// OutdatedByManager returns manager→lowercase-name→latestVersion.
	OutdatedByManager(ctx context.Context) (map[string]map[string]string, error)
}

// ManagerOutdatedInfoChecker is optionally implemented by provider families
// that can preserve manager attribution and PM update metadata.
type ManagerOutdatedInfoChecker interface {
	OutdatedInfoByManager(ctx context.Context) (map[string]map[string]OutdatedInfo, error)
}

// ManagerUpgrader is optionally implemented by provider families that can upgrade
// a tool using the concrete manager that owns the current installation.
type ManagerUpgrader interface {
	UpgradeWithManager(ctx context.Context, tool Tool, manager string) error
}

// ManagerUninstaller is optionally implemented by provider families that can
// uninstall a tool from a caller-selected concrete manager.
type ManagerUninstaller interface {
	UninstallFrom(ctx context.Context, tool Tool, manager string) error
}

// ManagerInstaller is optionally implemented by provider families that can
// install a tool using a caller-selected concrete manager.
type ManagerInstaller interface {
	InstallWithManager(ctx context.Context, tool Tool, manager string) error
}

// ManagerInstalledChecker is optionally implemented by provider families that can
// check a tool using a concrete manager rather than the currently resolved one.
type ManagerInstalledChecker interface {
	IsInstalledWithManager(ctx context.Context, tool Tool, manager string) (bool, string, error)
}

// ErrorAdvisor is optionally implemented by providers that can attach
// actionable remedies to explicit provider errors.
type ErrorAdvisor interface {
	ErrorSolutions(code ErrorCode, tool Tool) []ErrorSolution
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
// CLI tools from pure library packages (e.g. pip). Import uses it to mark
// non-CLI packages with Ignore:true so app-level tool processing can skip them.
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
