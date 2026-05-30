// Package node implements the Node.js provider.
// Installs are delegated to whichever JS package manager is available
// (pnpm preferred, then npm). The active manager can be pinned via
// settings.ecosystems.node.manager in the config file.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// mgr holds the command shape for one JS package manager.
type mgr struct {
	binary               string
	install              []string // global install args — package name appended by caller
	uninstall            []string
	upgrade              []string
	upgradeWithLatestTag bool
	listGlobal           []string // lists all global packages
	filterByPkg          bool     // whether listGlobal accepts a package name to narrow output
}

// supported is ordered by auto-detect preference (bun → pnpm → npm).
var supported = []mgr{
	{
		binary:      "bun",
		install:     []string{"add", "-g"},
		uninstall:   []string{"remove", "-g"},
		upgrade:     []string{"update", "-g", "--latest"},
		listGlobal:  []string{"pm", "ls", "-g"},
		filterByPkg: false, // bun pm ls -g does not accept a package filter
	},
	{
		binary:      "pnpm",
		install:     []string{"add", "-g"},
		uninstall:   []string{"remove", "-g"},
		upgrade:     []string{"update", "-g", "--latest"},
		listGlobal:  []string{"ls", "-g", "--depth=0"}, // "ls" outputs tree format in all pnpm versions; "list" outputs JSON by default in pnpm v10+
		filterByPkg: true,
	},
	{
		binary:               "npm",
		install:              []string{"install", "-g"},
		uninstall:            []string{"uninstall", "-g"},
		upgrade:              []string{"install", "-g"},
		upgradeWithLatestTag: true,
		listGlobal:           []string{"list", "-g", "--depth=0"},
		filterByPkg:          true,
	},
}

type Provider struct {
	exec        executor.Executor
	hint        string // preferred binary ("pnpm", "npm", …); "" = auto-detect
	httpClient  *http.Client
	registryURL string // default "https://registry.npmjs.org"
}

// New creates a node Provider. Pass hint="" to auto-detect from PATH.
func New(exec executor.Executor, hint string) *Provider {
	return &Provider{
		exec:        exec,
		hint:        hint,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		registryURL: "https://registry.npmjs.org",
	}
}

// newWithRegistry creates a Provider with a custom registry URL and HTTP client.
// Intended for use in tests only.
func newWithRegistry(exec executor.Executor, hint, registryURL string, client *http.Client) *Provider {
	return &Provider{exec: exec, hint: hint, httpClient: client, registryURL: registryURL}
}

func (p *Provider) Name() string { return "node" }
func (p *Provider) Description() string {
	return "Node.js packages via npm registry (bun · pnpm · npm)"
}

// resolve returns the active manager, honouring hint if set.
func (p *Provider) resolve(ctx context.Context) (*mgr, error) {
	if p.hint != "" {
		for i := range supported {
			if supported[i].binary == p.hint {
				if _, _, err := p.exec.Run(ctx, supported[i].binary, "--version"); err != nil {
					return nil, fmt.Errorf("node manager %q not found on PATH", p.hint)
				}
				return &supported[i], nil
			}
		}
		return nil, fmt.Errorf("unknown node manager %q (supported: bun, pnpm, npm)", p.hint)
	}
	for i := range supported {
		if _, _, err := p.exec.Run(ctx, supported[i].binary, "--version"); err == nil {
			return &supported[i], nil
		}
	}
	return nil, fmt.Errorf("no Node.js package manager found — install bun, npm, or pnpm")
}

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, err := p.resolve(ctx)
	return err == nil, nil
}

// ResolvedName implements provider.ConcreteResolver.
// Returns the binary name of the active manager ("bun", "pnpm", or "npm").
func (p *Provider) ResolvedName(ctx context.Context) (string, error) {
	m, err := p.resolve(ctx)
	if err != nil {
		return "", err
	}
	return m.binary, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	m, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return p.installWith(ctx, tool, m)
}

// InstallWithManager installs a package using the selected concrete manager.
func (p *Provider) InstallWithManager(ctx context.Context, tool provider.Tool, manager string) error {
	m := managerByName(manager)
	if m == nil {
		return fmt.Errorf("unknown node manager %q (supported: bun, pnpm, npm)", manager)
	}
	return p.installWith(ctx, tool, m)
}

func (p *Provider) installWith(ctx context.Context, tool provider.Tool, m *mgr) error {
	args := with(m.install, tool.EffectivePackage())
	_, stderr, err := p.exec.Run(ctx, m.binary, args...)
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", cmdStr(m.binary, args), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	m, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return p.uninstallWith(ctx, tool, m)
}

// UninstallFrom uninstalls a package using the selected concrete manager.
func (p *Provider) UninstallFrom(ctx context.Context, tool provider.Tool, manager string) error {
	m := managerByName(manager)
	if m == nil {
		return fmt.Errorf("unknown node manager %q (supported: bun, pnpm, npm)", manager)
	}
	return p.uninstallWith(ctx, tool, m)
}

func (p *Provider) uninstallWith(ctx context.Context, tool provider.Tool, m *mgr) error {
	args := with(m.uninstall, tool.EffectivePackage())
	_, stderr, err := p.exec.Run(ctx, m.binary, args...)
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", cmdStr(m.binary, args), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	m, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return p.upgradeWith(ctx, tool, m)
}

// UpgradeWithManager upgrades a package using the concrete manager that owns it.
func (p *Provider) UpgradeWithManager(ctx context.Context, tool provider.Tool, manager string) error {
	m := managerByName(manager)
	if m == nil {
		return fmt.Errorf("unknown node manager %q (supported: bun, pnpm, npm)", manager)
	}
	return p.upgradeWith(ctx, tool, m)
}

func (p *Provider) upgradeWith(ctx context.Context, tool provider.Tool, m *mgr) error {
	pkg := tool.EffectivePackage()
	if m.upgradeWithLatestTag {
		pkg = npmLatestSpec(pkg)
	}
	args := with(m.upgrade, pkg)
	_, stderr, err := p.exec.Run(ctx, m.binary, args...)
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", cmdStr(m.binary, args), err, strings.TrimSpace(stderr))
	}
	return nil
}

func npmLatestSpec(pkg string) string {
	if pkg == "" {
		return pkg
	}
	if strings.HasPrefix(pkg, "@") {
		if strings.LastIndex(pkg, "@") > 0 {
			return pkg
		}
		return pkg + "@latest"
	}
	if strings.Contains(pkg, "@") {
		return pkg
	}
	return pkg + "@latest"
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	m, err := p.resolve(ctx)
	if err != nil {
		return false, "", err
	}
	return p.isInstalledWith(ctx, tool, m)
}

// IsInstalledWithManager checks package status using a specific concrete manager.
func (p *Provider) IsInstalledWithManager(ctx context.Context, tool provider.Tool, manager string) (bool, string, error) {
	m := managerByName(manager)
	if m == nil {
		return false, "", nil
	}
	return p.isInstalledWith(ctx, tool, m)
}

func (p *Provider) isInstalledWith(ctx context.Context, tool provider.Tool, m *mgr) (bool, string, error) {
	pkg := tool.EffectivePackage()
	args := m.listGlobal
	if m.filterByPkg {
		args = with(m.listGlobal, pkg)
	}
	stdout, _, err := p.exec.Run(ctx, m.binary, args...)
	if err != nil {
		return false, "", nil // non-zero exit = not installed
	}
	ver := parseVersion(stdout, pkg)
	return ver != "", ver, nil
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	// Ensure at least one manager is available.
	if _, err := p.resolve(ctx); err != nil {
		return nil, err
	}
	entries, err := p.InstalledByManager(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]provider.InstalledTool, 0, len(entries))
	for name, e := range entries {
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "node", Package: name},
			Version: e.Version,
		})
	}
	return tools, nil
}

// InstalledMap returns all globally installed packages as lowercase-name→version.
// Uses only the effective manager (implements provider.BulkChecker).
// For cross-manager attribution use InstalledByManager instead.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	m, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	stdout, _, err := p.exec.Run(ctx, m.binary, m.listGlobal...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cmdStr(m.binary, m.listGlobal), err)
	}
	result := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		name, ver := parseListLine(line)
		if name != "" {
			result[strings.ToLower(name)] = ver
		}
	}
	return result, nil
}

// InstalledByManager implements provider.MultiManagerBulkChecker.
// Probes every available manager (bun, pnpm, npm) and returns per-tool attribution.
// The effective manager (from resolve()) is probed first so it takes priority when
// the same tool appears in multiple environments; remaining managers fill in the rest.
func (p *Provider) InstalledByManager(ctx context.Context) (map[string]provider.InstalledEntry, error) {
	var effectiveBinary string
	if m, err := p.resolve(ctx); err == nil {
		effectiveBinary = m.binary
	}

	result := make(map[string]provider.InstalledEntry)

	// probeManager fetches the global package list for m and adds entries to result.
	// First writer wins: caller controls priority by ordering calls.
	probeManager := func(m *mgr) error {
		if _, _, err := p.exec.Run(ctx, m.binary, "--version"); err != nil {
			return nil // not available
		}
		stdout, _, err := p.exec.Run(ctx, m.binary, m.listGlobal...)
		if err != nil {
			return fmt.Errorf("%s: %w", cmdStr(m.binary, m.listGlobal), err)
		}
		for _, line := range strings.Split(stdout, "\n") {
			name, ver := parseListLine(line)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, exists := result[key]; !exists {
				result[key] = provider.InstalledEntry{Version: ver, ConcreteManager: m.binary}
			}
		}
		return nil
	}

	// Effective manager first (highest priority).
	for i := range supported {
		if supported[i].binary == effectiveBinary {
			if err := probeManager(&supported[i]); err != nil {
				return nil, err
			}
			break
		}
	}
	// Remaining managers fill in tools not owned by the effective manager.
	for i := range supported {
		if supported[i].binary != effectiveBinary {
			if err := probeManager(&supported[i]); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

// npmOutdatedEntry is one entry from `npm/pnpm outdated -g --json`.
type npmOutdatedEntry struct {
	Latest string `json:"latest"`
}

// OutdatedMap returns lowercase package name → latest version for outdated globals.
// It probes every available manager so tools installed outside the active
// manager still get update status. npm/pnpm exit 1 when outdated packages
// exist, so stdout is parsed regardless of the process error.
func (p *Provider) OutdatedMap(ctx context.Context) (map[string]string, error) {
	byManager, err := p.OutdatedByManager(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, m := range byManager {
		for name, latest := range m {
			if _, exists := result[name]; !exists {
				result[name] = latest
			}
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// OutdatedByManager probes every available manager and preserves manager
// attribution for callers that need to update cache rows by InstalledWith.
func (p *Provider) OutdatedByManager(ctx context.Context) (map[string]map[string]string, error) {
	effective, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]map[string]string)
	var errs []error
	probe := func(m *mgr) {
		if _, _, err := p.exec.Run(ctx, m.binary, "--version"); err != nil {
			return
		}
		outdated, err := p.outdatedMapForManager(ctx, m)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if len(outdated) > 0 {
			result[m.binary] = outdated
		}
	}

	probe(effective)
	for i := range supported {
		if supported[i].binary != effective.binary {
			probe(&supported[i])
		}
	}
	if len(result) > 0 {
		return result, nil
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, nil
}

// OutdatedInfoMap returns outdated package versions plus npm registry
// publish timestamps when available.
func (p *Provider) OutdatedInfoMap(ctx context.Context) (map[string]provider.OutdatedInfo, error) {
	byManager, err := p.OutdatedInfoByManager(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]provider.OutdatedInfo)
	for _, m := range byManager {
		for name, info := range m {
			if _, exists := result[name]; !exists {
				result[name] = info
			}
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// OutdatedInfoByManager preserves concrete manager attribution and enriches
// latest versions with npm registry time[version] metadata.
func (p *Provider) OutdatedInfoByManager(ctx context.Context) (map[string]map[string]provider.OutdatedInfo, error) {
	byManager, err := p.OutdatedByManager(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]map[string]provider.OutdatedInfo, len(byManager))
	for manager, outdated := range byManager {
		infos := make(map[string]provider.OutdatedInfo, len(outdated))
		for name, latest := range outdated {
			info := provider.OutdatedInfo{LatestVersion: latest}
			if availableAt, err := p.npmVersionPublishedAt(ctx, name, latest); err == nil && availableAt != nil {
				info.AvailableAt = availableAt
				info.DateSource = "npm_registry_time"
			}
			infos[name] = info
		}
		if len(infos) > 0 {
			result[manager] = infos
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func (p *Provider) outdatedMapForManager(ctx context.Context, m *mgr) (map[string]string, error) {
	stdout, stderr, err := p.exec.Run(ctx, m.binary, "outdated", "-g", "--json")
	stdout = strings.TrimSpace(stdout)
	if stdout == "" || stdout == "{}" || stdout == "null" {
		if err != nil {
			return nil, fmt.Errorf("%s: %w\n%s", cmdStr(m.binary, []string{"outdated", "-g", "--json"}), err, strings.TrimSpace(stderr))
		}
		return nil, nil
	}
	if m.binary == "bun" {
		if err != nil {
			return nil, fmt.Errorf("%s: %w\n%s", cmdStr(m.binary, []string{"outdated", "-g", "--json"}), err, strings.TrimSpace(stdout+"\n"+stderr))
		}
		return parseBunOutdatedMap(stdout), nil
	}
	var payload map[string]npmOutdatedEntry
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil, fmt.Errorf("parsing %s outdated output: %w", m.binary, err)
	}
	result := make(map[string]string, len(payload))
	for name, info := range payload {
		if info.Latest != "" {
			result[strings.ToLower(name)] = info.Latest
		}
	}
	return result, nil
}

func parseBunOutdatedMap(stdout string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cols := strings.Split(line, "|")
		if len(cols) < 5 {
			continue
		}
		name := strings.TrimSpace(cols[1])
		latest := strings.TrimSpace(cols[4])
		if name == "" || latest == "" || strings.EqualFold(name, "package") {
			continue
		}
		result[strings.ToLower(name)] = latest
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// npmPackageResponse is the relevant subset of GET /registry.npmjs.org/<pkg>.
type npmPackageResponse struct {
	Description string            `json:"description"`
	Readme      string            `json:"readme"`
	Time        map[string]string `json:"time"`
}

func (p *Provider) npmVersionPublishedAt(ctx context.Context, pkg, version string) (*time.Time, error) {
	if pkg == "" || version == "" {
		return nil, nil
	}
	endpoint := p.registryURL + "/" + url.PathEscape(pkg)
	var payload npmPackageResponse
	status, err := provider.FetchJSON(ctx, p.httpClient, endpoint, &payload)
	if status == 0 {
		return nil, fmt.Errorf("npm registry time: %w", err)
	}
	if err != nil || status != http.StatusOK {
		return nil, nil
	}
	raw := strings.TrimSpace(payload.Time[version])
	if raw == "" {
		return nil, nil
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, nil
	}
	return &publishedAt, nil
}

// Describe fetches a one-line description from the npm registry.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	endpoint := p.registryURL + "/" + url.PathEscape(tool.EffectivePackage())
	var payload npmPackageResponse
	status, err := provider.FetchJSON(ctx, p.httpClient, endpoint, &payload)
	if status == 0 {
		return "", fmt.Errorf("npm registry describe: %w", err)
	}
	if err != nil || status != http.StatusOK {
		return "", nil
	}
	if desc := strings.TrimSpace(payload.Description); desc != "" {
		return desc, nil
	}
	return readmeIntro(payload.Readme), nil
}

func readmeIntro(readme string) string {
	inFence := false
	var paragraph []string
	for _, line := range strings.Split(readme, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if len(paragraph) == 0 && (strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[!") || strings.HasPrefix(line, "![") || strings.HasPrefix(line, "<")) {
			continue
		}
		paragraph = append(paragraph, line)
	}
	return strings.TrimSpace(strings.Join(paragraph, " "))
}

// parseVersion finds "pkg@version" in `list -g <pkg> --depth=0` output.
// Both npm and pnpm emit tree-format lines: "├── typescript@5.3.3"
func parseVersion(output, pkg string) string {
	for _, line := range strings.Split(output, "\n") {
		name, ver := parseListLine(line)
		if name == pkg {
			return ver
		}
	}
	return ""
}

// parseListLine extracts (name, version) from one `list -g --depth=0` line.
// Scoped packages "@scope/pkg@version" → ("@scope/pkg", "version") require
// splitting on the last "@" rather than the first.
func parseListLine(line string) (name, version string) {
	line = stripTopLevelTreePrefix(line)
	if line == "" {
		return "", ""
	}
	if !strings.Contains(line, "@") {
		return "", ""
	}
	idx := strings.LastIndex(line, "@")
	if idx <= 0 {
		return "", ""
	}
	return line[:idx], line[idx+1:]
}

// stripTopLevelTreePrefix removes npm/pnpm tree prefixes only from root-level
// package rows. Indented rows are dependencies of a listed package, not global
// tools, and must be ignored.
func stripTopLevelTreePrefix(line string) string {
	line = strings.TrimRight(line, " \t\r")
	for _, prefix := range []string{"├── ", "└── ", "+-- ", "`-- "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// with appends pkg to a copy of args, leaving the original slice untouched.
func with(args []string, pkg string) []string {
	out := make([]string, len(args)+1)
	copy(out, args)
	out[len(args)] = pkg
	return out
}

func managerByName(name string) *mgr {
	for i := range supported {
		if supported[i].binary == name {
			return &supported[i]
		}
	}
	return nil
}

func cmdStr(binary string, args []string) string {
	return binary + " " + strings.Join(args, " ")
}

// npmSearchResponse is the relevant subset of the npm registry /-/v1/search response.
type npmSearchResponse struct {
	Objects []struct {
		Package struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
		} `json:"package"`
	} `json:"objects"`
}

func (p *Provider) Search(ctx context.Context, query string) ([]provider.SearchResult, error) {
	endpoint := p.registryURL + "/-/v1/search?text=" + url.QueryEscape(query) + "&size=20"
	var payload npmSearchResponse
	status, err := provider.FetchJSON(ctx, p.httpClient, endpoint, &payload)
	if status == 0 {
		return nil, fmt.Errorf("npm registry search: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("decoding npm search response: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("npm registry returned HTTP %d", status)
	}
	results := make([]provider.SearchResult, 0, len(payload.Objects))
	for _, obj := range payload.Objects {
		results = append(results, provider.SearchResult{
			Name:        obj.Package.Name,
			Version:     obj.Package.Version,
			Description: obj.Package.Description,
			Provider:    "node",
		})
	}
	return results, nil
}
