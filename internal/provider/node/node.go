// Package node delegates to bun, then pnpm, then npm; pinnable via settings.ecosystems.node.manager.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type mgr struct {
	binary               string
	install              []string // global install args — package name appended by caller
	uninstall            []string
	upgrade              []string
	upgradeWithLatestTag bool
	listGlobal           []string // lists all global packages
	filterByPkg          bool     // whether listGlobal accepts a package name to narrow output
	emptyExitNonZero     bool     // listGlobal exits non-zero when no globals are installed (bun)
}

var exactNPMVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// supported is ordered by auto-detect preference (bun → pnpm → npm).
var supported = []mgr{
	{
		binary:           "bun",
		install:          []string{"add", "-g"},
		uninstall:        []string{"remove", "-g"},
		upgrade:          []string{"update", "-g", "--latest"},
		listGlobal:       []string{"pm", "ls", "-g"},
		filterByPkg:      false, // bun pm ls -g does not accept a package filter
		emptyExitNonZero: true,  // bun pm ls -g exits 1 when the global dir is empty
	},
	{
		binary:           "pnpm",
		install:          []string{"add", "-g"},
		uninstall:        []string{"remove", "-g"},
		upgrade:          []string{"update", "-g", "--latest"},
		listGlobal:       []string{"ls", "-g", "--depth=0"}, // "ls" outputs tree format in all pnpm versions; "list" outputs JSON by default in pnpm v10+
		filterByPkg:      true,
		emptyExitNonZero: true, // pnpm ls -g exits 1 when no globals are installed or global bin dir is empty
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
		registryURL: defaultRegistryURL(),
	}
}

func defaultRegistryURL() string {
	if os.Getenv("OMNI_TEST_ISOLATED") == "1" {
		if raw := strings.TrimSpace(os.Getenv("OMNI_TEST_NPM_REGISTRY_URL")); raw != "" {
			return strings.TrimRight(raw, "/")
		}
	}
	return "https://registry.npmjs.org"
}

// Intended for use in tests only.
func newWithRegistry(exec executor.Executor, hint, registryURL string, client *http.Client) *Provider {
	return &Provider{exec: exec, hint: hint, httpClient: client, registryURL: registryURL}
}

func (p *Provider) Name() string { return "node" }
func (p *Provider) Description() string {
	return "Node.js packages via npm registry (bun · pnpm · npm)"
}

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

// ResolvedName — Returns the binary name of the active manager ("bun", "pnpm", or "npm").
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

func (p *Provider) IsInstalledWithManager(ctx context.Context, tool provider.Tool, manager string) (bool, string, error) {
	m := managerByName(manager)
	if m == nil {
		return false, "", nil
	}
	return p.isInstalledWith(ctx, tool, m)
}

func (p *Provider) isInstalledWith(ctx context.Context, tool provider.Tool, m *mgr) (bool, string, error) {
	pkg, requiredVersion := splitPackageSpec(tool.EffectivePackage())
	args := m.listGlobal
	if m.filterByPkg {
		args = with(m.listGlobal, pkg)
	}
	stdout, _, err := p.exec.Run(ctx, m.binary, args...)
	if err != nil {
		return false, "", nil // non-zero exit = not installed
	}
	ver := parseVersion(stdout, pkg)
	return ver != "" && (requiredVersion == "" || ver == requiredVersion), ver, nil
}

func splitPackageSpec(spec string) (name, version string) {
	idx := strings.LastIndex(spec, "@")
	if idx <= 0 || idx == len(spec)-1 || !exactNPMVersion.MatchString(spec[idx+1:]) {
		return spec, ""
	}
	return spec[:idx], spec[idx+1:]
}

func (p *Provider) ExactVersionPin(tool provider.Tool) (string, string, bool) {
	name, version := splitPackageSpec(tool.EffectivePackage())
	return name, version, version != ""
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
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

// InstalledMap — Lowercase-name to version for the effective manager only; use InstalledByManager for attribution.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	m, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := p.exec.Run(ctx, m.binary, m.listGlobal...)
	if err != nil {
		if m.emptyExitNonZero && isEmptyGlobalListError(err) {
			return map[string]string{}, nil // empty global dir; nothing installed
		}
		return nil, executor.WrapError(err, cmdStr(m.binary, m.listGlobal), stdout, stderr)
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

// InstalledByManager — The effective manager is probed first so it wins when a tool exists under several managers.
func (p *Provider) InstalledByManager(ctx context.Context) (map[string]provider.InstalledEntry, error) {
	var effectiveBinary string
	if m, err := p.resolve(ctx); err == nil {
		effectiveBinary = m.binary
	}

	result := make(map[string]provider.InstalledEntry)

	// First writer wins: caller controls priority by ordering calls.
	probeManager := func(m *mgr) error {
		if _, _, err := p.exec.Run(ctx, m.binary, "--version"); err != nil {
			return nil // not available
		}
		stdout, stderr, err := p.exec.Run(ctx, m.binary, m.listGlobal...)
		if err != nil {
			if m.emptyExitNonZero && isEmptyGlobalListError(err) {
				return nil // empty global dir; nothing to attribute
			}
			return executor.WrapError(err, cmdStr(m.binary, m.listGlobal), stdout, stderr)
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

	for i := range supported {
		if supported[i].binary == effectiveBinary {
			if err := probeManager(&supported[i]); err != nil {
				return nil, err
			}
			break
		}
	}
	// A fill-in manager that fails to list (pnpm's global bin dir off PATH exits 1) must not abort detection.
	for i := range supported {
		if supported[i].binary != effectiveBinary {
			if err := probeManager(&supported[i]); err != nil {
				continue
			}
		}
	}

	return result, nil
}

// bun and pnpm exit 1 on an empty global install dir; that is benign, not a failure.
func isEmptyGlobalListError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == 1
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "exit status 1")
}

// npmOutdatedEntry is one entry from `npm/pnpm outdated -g --json`.
type npmOutdatedEntry struct {
	Latest string `json:"latest"`
}

// OutdatedMap — Probes every manager; npm/pnpm exit 1 when anything is outdated, so stdout is parsed regardless of the error.
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

// OutdatedByManager — Preserves manager attribution for callers updating cache rows by InstalledWith.
func (p *Provider) OutdatedByManager(ctx context.Context) (map[string]map[string]string, error) {
	effective, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]map[string]string)
	var errs []error
	anyOK := false
	probe := func(m *mgr) {
		if _, _, err := p.exec.Run(ctx, m.binary, "--version"); err != nil {
			return
		}
		outdated, err := p.outdatedMapForManager(ctx, m)
		if err != nil {
			errs = append(errs, err)
			return
		}
		anyOK = true // succeeded, even if it found nothing outdated
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
	// One failing manager makes the aggregate incomplete; error out so callers keep prior state.
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if anyOK {
		return result, nil
	}
	return nil, nil
}

// OutdatedInfoMap — Enriches outdated versions with npm registry publish timestamps when available.
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

// OutdatedInfoByManager — Preserves manager attribution and adds npm registry time[version] metadata.
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
	if stdout == "" || stdout == "{}" {
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
	if err != nil && !isExpectedNPMOutdatedExit(err) {
		return nil, fmt.Errorf("%s: %w\n%s", cmdStr(m.binary, []string{"outdated", "-g", "--json"}), err, strings.TrimSpace(stderr))
	}
	result, parseErr := parseNPMOutdatedMap(stdout)
	if parseErr != nil {
		return nil, fmt.Errorf("parsing %s outdated output: %w", m.binary, parseErr)
	}
	return result, nil
}

func isExpectedNPMOutdatedExit(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == 1
	}
	switch strings.ToLower(strings.TrimSpace(err.Error())) {
	case "exit 1", "exit status 1":
		return true
	default:
		return false
	}
}

func parseNPMOutdatedMap(stdout string) (map[string]string, error) {
	if !json.Valid([]byte(stdout)) {
		return nil, fmt.Errorf("invalid JSON")
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}
	result := make(map[string]string)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected package name")
		}
		var info npmOutdatedEntry
		if err := dec.Decode(&info); err != nil {
			return nil, err
		}
		key := strings.ToLower(strings.TrimSpace(name))
		latest := strings.TrimSpace(info.Latest)
		if key == "" || latest == "" {
			continue
		}
		if _, exists := result[key]; !exists {
			result[key] = latest
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
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

// Scoped packages force splitting on the last "@", not the first.
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

// Root-level rows only: indented rows are dependencies of a listed package, not global tools.
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
			Links       struct {
				Repository string `json:"repository"`
				Homepage   string `json:"homepage"`
			} `json:"links"`
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
			Source:      provider.GitHubSourceHint(obj.Package.Links.Repository, obj.Package.Links.Homepage),
		})
	}
	return results, nil
}
