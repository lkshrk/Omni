package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/executor"
)

func RunCmd(ctx context.Context, exec executor.Executor, errLabel, cmd string, args ...string) error {
	_, stderr, err := exec.Run(ctx, cmd, args...)
	if err != nil {
		return fmt.Errorf("%s: %w (stderr: %s)", errLabel, err, strings.TrimSpace(stderr))
	}
	return nil
}

// FetchJSON — Returns the HTTP status so callers can short-circuit; 0 means the roundtrip itself failed.
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

type Tool struct {
	Name     string
	Provider string
	Package  string            // defaults to Name if empty
	Options  map[string]string // provider-specific flags
}

func (t Tool) EffectivePackage() string {
	if t.Package != "" {
		return t.Package
	}
	return t.Name
}

type InstalledTool struct {
	Tool
	Version string
}

type Status int

const (
	StatusUnknown Status = iota //nolint:revive
	StatusInstalled
	StatusMissing
	StatusOutdated
)

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

type SourceMetadata struct {
	Type  string
	Owner string
	Repo  string
	URL   string
}

// GitHubSourceHint — Accepts https/http/ssh and git+ forms; returns the zero value when none parse.
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

// Searcher — It is optional — providers that do not implement it are silently skipped.
type Searcher interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
}

// ConcreteResolver — Optional. ResolvedName gives the active concrete delegate, or an error when none is available.
type ConcreteResolver interface {
	ResolvedName(ctx context.Context) (string, error)
}

// BulkChecker — Optional. One subprocess call for every installed tool, avoiding per-tool IsInstalled overhead.
type BulkChecker interface {
	// Keys are lowercase names; values are versions.
	InstalledMap(ctx context.Context) (map[string]string, error)
}

type InstalledMetadata struct {
	Version      string
	Privilege    PrivilegePlan
	Source       SourceMetadata
	ArtifactKind string // provider-specific artifact kind, e.g. "formula" or "cask" for brew
	SelfUpdates  bool   // cask the manager cannot upgrade (manual installer; the app self-updates)
}

type MetadataBulkChecker interface {
	InstalledMetadataMap(ctx context.Context) (map[string]InstalledMetadata, error)
}

// InstalledEntry — ConcreteManager is empty when the tool is not installed.
type InstalledEntry struct {
	Version         string
	ConcreteManager string // e.g. "uv", "pip3", "pip"
}

type OutdatedInfo struct {
	LatestVersion string
	AvailableAt   *time.Time
	DateSource    string
}

// MultiManagerBulkChecker — Optional. Unlike BulkChecker it probes ALL backends and records which one owns each tool.
type MultiManagerBulkChecker interface {
	InstalledByManager(ctx context.Context) (map[string]InstalledEntry, error)
}

// SelfPackageUpgradeChecker — Optional. Some environments (PEP 668) forbid a manager upgrading its own package.
type SelfPackageUpgradeChecker interface {
	SelfPackageName() string
	SelfPackageUpgradeable(ctx context.Context) bool
}

// MetadataRefresher — Optional. Costs network latency, so call it only for user-initiated refreshes.
type MetadataRefresher interface {
	RefreshMetadata(ctx context.Context) error
}

type OutdatedChecker interface {
	// OutdatedMap returns a lowercase-name→latestVersion map of outdated tools.
	OutdatedMap(ctx context.Context) (map[string]string, error)
}

// ErrInstalledVersionUnknown marks a permanent reporting gap, so callers discard cached verdicts and degrade to unknown.
var ErrInstalledVersionUnknown = errors.New("installed version is unknown")

// ToolOutdatedChecker — Optional. For per-tool update checks that cannot collapse into one OutdatedMap call.
type ToolOutdatedChecker interface {
	// supported is false when callers should fall through to source-based resolution.
	// An error wrapping ErrInstalledVersionUnknown reports a permanent gap rather than a transient failure.
	CheckOutdated(ctx context.Context, tool Tool, currentVersion string) (latestVersion string, outdated bool, supported bool, err error)
}

type OutdatedInfoChecker interface {
	OutdatedInfoMap(ctx context.Context) (map[string]OutdatedInfo, error)
}

type ManagerOutdatedChecker interface {
	// OutdatedByManager returns manager→lowercase-name→latestVersion.
	OutdatedByManager(ctx context.Context) (map[string]map[string]string, error)
}

type ManagerOutdatedInfoChecker interface {
	OutdatedInfoByManager(ctx context.Context) (map[string]map[string]OutdatedInfo, error)
}

// ManagerUpgrader — Optional. Upgrades through the concrete manager that owns the current installation.
type ManagerUpgrader interface {
	UpgradeWithManager(ctx context.Context, tool Tool, manager string) error
}

type ManagerUninstaller interface {
	UninstallFrom(ctx context.Context, tool Tool, manager string) error
}

type ManagerInstaller interface {
	InstallWithManager(ctx context.Context, tool Tool, manager string) error
}

// ManagerInstalledChecker — Optional. Checks against a named manager rather than the currently resolved one.
type ManagerInstalledChecker interface {
	IsInstalledWithManager(ctx context.Context, tool Tool, manager string) (bool, string, error)
}

type ErrorAdvisor interface {
	ErrorSolutions(code ErrorCode, tool Tool) []ErrorSolution
}

type Descriptor interface {
	Describe(ctx context.Context, tool Tool) (string, error)
}

// BulkDescriber — Optional. One subprocess call for many descriptions.
type BulkDescriber interface {
	// Tools not found or with no description are omitted from the result.
	BulkDescribe(ctx context.Context, tools []Tool) (map[string]string, error)
}

// CLIToolProvider — Optional. Import marks non-CLI packages Ignore:true so app-level processing skips them.
type CLIToolProvider interface {
	// Lowercase names that install at least one CLI entry point; absent means library.
	CLIToolSet(ctx context.Context) (map[string]bool, error)
}

// Provider — New provider: implement this, register the factory in register.go, blank-import it from provider/all, add a catalog entry.
type Provider interface {
	// Name returns the unique identifier used in config (e.g. "brew", "npm").
	Name() string
	// Description is shown in help output.
	Description() string
	Available(ctx context.Context) (bool, error)

	Install(ctx context.Context, tool Tool) error
	Uninstall(ctx context.Context, tool Tool) error
	Upgrade(ctx context.Context, tool Tool) error

	IsInstalled(ctx context.Context, tool Tool) (bool, string, error)
	ListInstalled(ctx context.Context) ([]InstalledTool, error)
}
