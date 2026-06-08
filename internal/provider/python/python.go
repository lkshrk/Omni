// Package python implements the abstract Python tools provider.
// Delegates to whichever Python package manager is available
// (uv preferred, then pip3, then pip). The active manager can be pinned via
// settings.ecosystems.python.manager in the config file.
package python

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// backend describes one Python package manager.
type backend struct {
	binary       string
	pythonBinary string
	usesTool     bool // true for uv: commands become "uv tool <sub> …"
}

// supported is ordered by auto-detect preference.
var supported = []backend{
	{binary: "uv", usesTool: true},
	{binary: "pip3", pythonBinary: "python3"},
	{binary: "pip", pythonBinary: "python"},
}

// Provider is the abstract Python tools provider.
type Provider struct {
	exec       executor.Executor
	hint       string // "", "uv", "pip3", "pip" — from settings.ecosystems.python.manager
	httpClient *http.Client
	pypiURL    string
}

// New creates a Provider. Pass hint="" to auto-detect from PATH.
func New(exec executor.Executor, hint string) *Provider {
	return &Provider{
		exec:       exec,
		hint:       hint,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		pypiURL:    "https://pypi.org",
	}
}

// newWithPyPI creates a Provider with a custom PyPI URL and HTTP client.
// Intended for use in tests only.
func newWithPyPI(exec executor.Executor, hint, pypiURL string, client *http.Client) *Provider {
	return &Provider{exec: exec, hint: hint, httpClient: client, pypiURL: pypiURL}
}

func (p *Provider) Name() string        { return "python" }
func (p *Provider) Description() string { return "Python tools via uv · pip3" }

func (p *Provider) ErrorSolutions(code provider.ErrorCode, tool provider.Tool) []provider.ErrorSolution {
	if code != provider.ErrorExternallyManagedPython {
		return nil
	}
	fromProvider := tool.Provider
	if fromProvider == "" {
		fromProvider = p.Name()
	}
	command := ""
	if tool.Name != "" {
		command = fmt.Sprintf("omni reinstall %s --from %s --to uv", tool.Name, fromProvider)
	}
	return []provider.ErrorSolution{
		{
			Label:          "Reinstall this tool with uv",
			Command:        command,
			Detail:         "uv installs Python CLI tools into isolated tool environments instead of modifying the externally managed Python.",
			Action:         provider.ErrorSolutionActionSwitchProvider,
			TargetProvider: "uv",
		},
	}
}

// resolve returns the active backend, honouring hint when set.
func (p *Provider) resolve(ctx context.Context) (*backend, error) {
	if p.hint != "" {
		for i := range supported {
			if supported[i].binary == p.hint {
				if _, _, err := p.exec.Run(ctx, supported[i].binary, "--version"); err != nil {
					return nil, fmt.Errorf("python manager %q not found on PATH", p.hint)
				}
				return &supported[i], nil
			}
		}
		return nil, fmt.Errorf("unknown python manager %q (supported: uv, pip3, pip)", p.hint)
	}
	for i := range supported {
		if _, _, err := p.exec.Run(ctx, supported[i].binary, "--version"); err == nil {
			return &supported[i], nil
		}
	}
	return nil, fmt.Errorf("no Python package manager found — install uv or pip3")
}

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, err := p.resolve(ctx)
	return err == nil, nil
}

// ResolvedName implements provider.ConcreteResolver.
// Returns the binary name of the active backend ("uv", "pip3", or "pip").
func (p *Provider) ResolvedName(ctx context.Context) (string, error) {
	b, err := p.resolve(ctx)
	if err != nil {
		return "", err
	}
	return b.binary, nil
}

// ─── command helpers ──────────────────────────────────────────────────────────

func (b *backend) installArgs(pkg string) []string {
	if b.usesTool {
		return []string{"tool", "install", pkg}
	}
	return []string{"install", pkg}
}

func (b *backend) uninstallArgs(pkg string) []string {
	if b.usesTool {
		return []string{"tool", "uninstall", pkg}
	}
	return []string{"uninstall", "-y", pkg}
}

func (b *backend) upgradeArgs(pkg string) []string {
	if b.usesTool {
		return []string{"tool", "install", uvLatestToolSpec(pkg)}
	}
	return []string{"install", "--upgrade", pkg}
}

func uvLatestToolSpec(pkg string) string {
	if pkg == "" || strings.Contains(pkg, "@") || strings.ContainsAny(pkg, "<>=") || strings.Contains(pkg, "~=") || strings.Contains(pkg, "!=") {
		return pkg
	}
	return pkg + "@latest"
}

// ─── Provider interface ──────────────────────────────────────────────────────

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	b, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return p.installWith(ctx, tool, b)
}

// InstallWithManager installs a package using the selected concrete Python manager.
func (p *Provider) InstallWithManager(ctx context.Context, tool provider.Tool, manager string) error {
	b := backendByName(manager)
	if b == nil {
		return fmt.Errorf("unknown python manager %q (supported: uv, pip3, pip)", manager)
	}
	return p.installWith(ctx, tool, b)
}

func (p *Provider) installWith(ctx context.Context, tool provider.Tool, b *backend) error {
	args := b.installArgs(tool.EffectivePackage())
	_, stderr, err := p.exec.Run(ctx, b.binary, args...)
	if err != nil {
		if provider.IsExternallyManagedPythonOutput(stderr) {
			return provider.NewExternallyManagedPythonError(b.binary, "install", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
		}
		return fmt.Errorf("%s %s: %w\n%s", b.binary, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	b, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	args := b.uninstallArgs(tool.EffectivePackage())
	_, stderr, err := p.exec.Run(ctx, b.binary, args...)
	if err != nil {
		if provider.IsExternallyManagedPythonOutput(stderr) {
			return provider.NewExternallyManagedPythonError(b.binary, "uninstall", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
		}
		return fmt.Errorf("%s %s: %w\n%s", b.binary, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	return nil
}

// UninstallFrom uninstalls the package using a specific binary, regardless of
// the currently configured python ecosystem manager. Used during provider migration to
// clean up from the old manager's environment (e.g. uv→pip3 leaves the tool
// in uv's isolated env; this removes it explicitly).
// Returns nil if the binary is not recognised or the tool wasn't in that env.
func (p *Provider) UninstallFrom(ctx context.Context, tool provider.Tool, binary string) error {
	for i := range supported {
		if supported[i].binary == binary {
			args := supported[i].uninstallArgs(tool.EffectivePackage())
			_, stderr, err := p.exec.Run(ctx, binary, args...)
			if err != nil {
				if provider.IsExternallyManagedPythonOutput(stderr) {
					return provider.NewExternallyManagedPythonError(binary, "uninstall", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
				}
				return fmt.Errorf("%s %s: %w\n%s", binary, strings.Join(args, " "), err, strings.TrimSpace(stderr))
			}
			return nil
		}
	}
	return nil // unknown binary — nothing to do
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	b, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return p.upgradeWith(ctx, tool, b)
}

// UpgradeWithManager upgrades a package using the concrete Python manager that owns it.
func (p *Provider) UpgradeWithManager(ctx context.Context, tool provider.Tool, manager string) error {
	b := backendByName(manager)
	if b == nil {
		return fmt.Errorf("unknown python manager %q (supported: uv, pip3, pip)", manager)
	}
	return p.upgradeWith(ctx, tool, b)
}

func (p *Provider) upgradeWith(ctx context.Context, tool provider.Tool, b *backend) error {
	args := b.upgradeArgs(tool.EffectivePackage())
	_, stderr, err := p.exec.Run(ctx, b.binary, args...)
	if err != nil {
		if provider.IsExternallyManagedPythonOutput(stderr) {
			return provider.NewExternallyManagedPythonError(b.binary, "upgrade", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
		}
		return fmt.Errorf("%s %s: %w\n%s", b.binary, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	b, err := p.resolve(ctx)
	if err != nil {
		return false, "", nil
	}
	return p.isInstalledWith(ctx, tool, b)
}

// IsInstalledWithManager checks package status using a specific concrete manager.
func (p *Provider) IsInstalledWithManager(ctx context.Context, tool provider.Tool, manager string) (bool, string, error) {
	b := backendByName(manager)
	if b == nil {
		return false, "", nil
	}
	return p.isInstalledWith(ctx, tool, b)
}

func (p *Provider) isInstalledWith(ctx context.Context, tool provider.Tool, b *backend) (bool, string, error) {
	if b.usesTool {
		m, err := p.uvInstalledMap(ctx, b)
		if err != nil {
			return false, "", nil
		}
		ver, ok := m[strings.ToLower(tool.EffectivePackage())]
		return ok, ver, nil
	}
	stdout, _, err := p.exec.Run(ctx, b.binary, "show", tool.EffectivePackage())
	if err != nil {
		return false, "", nil
	}
	return true, parsePipShowVersion(stdout), nil
}

func backendByName(name string) *backend {
	for i := range supported {
		if supported[i].binary == name {
			return &supported[i]
		}
	}
	return nil
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	b, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if b.usesTool {
		stdout, _, err := p.exec.Run(ctx, b.binary, "tool", "list")
		if err != nil {
			return nil, fmt.Errorf("uv tool list: %w", err)
		}
		return parseUVToolList(stdout), nil
	}
	stdout, _, err := p.exec.Run(ctx, b.binary, "list", "--not-required", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("pip list --not-required: %w", err)
	}
	return parsePipList(stdout)
}

// InstalledByManager implements provider.MultiManagerBulkChecker.
// Probes every available backend (uv, pip3, pip) and returns per-tool attribution.
// The effective backend (from resolve()) is probed first so it takes priority when
// the same tool appears in multiple environments, then remaining backends fill in
// tools that the effective manager doesn't own.
func (p *Provider) InstalledByManager(ctx context.Context) (map[string]provider.InstalledEntry, error) {
	var effectiveBinary string
	if b, err := p.resolve(ctx); err == nil {
		effectiveBinary = b.binary
	}

	result := make(map[string]provider.InstalledEntry)

	// probeBackend fetches the installed map for b and adds entries to result.
	// First writer wins: caller controls priority by ordering calls.
	probeBackend := func(b *backend) error {
		if _, _, err := p.exec.Run(ctx, b.binary, "--version"); err != nil {
			return nil // not available
		}
		var m map[string]string
		if b.usesTool {
			var err error
			m, err = p.uvInstalledMap(ctx, b)
			if err != nil {
				return err
			}
		} else {
			stdout, _, err := p.exec.Run(ctx, b.binary, "list", "--not-required", "--format=json")
			if err != nil {
				return fmt.Errorf("%s list --not-required --format=json: %w", b.binary, err)
			}
			var parseErr error
			m, parseErr = parsePipInstalledMap(stdout)
			if parseErr != nil {
				return fmt.Errorf("parsing %s list output: %w", b.binary, parseErr)
			}
		}
		for name, ver := range m {
			if _, exists := result[name]; !exists {
				result[name] = provider.InstalledEntry{Version: ver, ConcreteManager: b.binary}
			}
		}
		return nil
	}

	// Effective backend first (highest priority).
	for i := range supported {
		if supported[i].binary == effectiveBinary {
			if err := probeBackend(&supported[i]); err != nil {
				return nil, err
			}
			break
		}
	}
	// Remaining backends fill in tools not owned by the effective manager.
	// A fill-in backend that fails to list must not abort detection: skip it and
	// keep the effective backend's attribution plus whatever else reports.
	for i := range supported {
		if supported[i].binary != effectiveBinary {
			if err := probeBackend(&supported[i]); err != nil {
				continue
			}
		}
	}

	return result, nil
}

// InstalledMap implements provider.BulkChecker.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	b, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if b.usesTool {
		return p.uvInstalledMap(ctx, b)
	}
	stdout, _, err := p.exec.Run(ctx, b.binary, "list", "--not-required", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("pip list --not-required: %w", err)
	}
	return parsePipInstalledMap(stdout)
}

// cliToolSetScript returns a JSON object mapping lowercase package name → 1
// for every pip package that installs a console_scripts or scripts entry point.
const cliToolSetScript = `import importlib.metadata,json,sys` +
	`;seen={}` +
	`;[seen.update({d.metadata["Name"].lower():1})` +
	` for d in importlib.metadata.distributions()` +
	` if any(e.group in ("console_scripts","scripts") for e in d.entry_points)]` +
	`;print(json.dumps(seen))`

// CLIToolSet implements provider.CLIToolProvider.
// For uv: all installed tools are CLI tools by definition (uv tool only installs CLI apps).
// For pip3/pip: uses importlib.metadata to detect packages with entry points.
func (p *Provider) CLIToolSet(ctx context.Context) (map[string]bool, error) {
	b, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if b.usesTool {
		m, err := p.uvInstalledMap(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("python cli tool set: %w", err)
		}
		out := make(map[string]bool, len(m))
		for name := range m {
			out[name] = true
		}
		return out, nil
	}
	stdout, _, err := p.exec.Run(ctx, b.pythonBinary, "-c", cliToolSetScript)
	if err != nil {
		return nil, fmt.Errorf("python cli tool set: %w", err)
	}
	var raw map[string]int
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &raw); err != nil {
		return nil, fmt.Errorf("parsing python cli tool set: %w", err)
	}
	out := make(map[string]bool, len(raw))
	for k := range raw {
		out[k] = true
	}
	return out, nil
}

// OutdatedMap implements provider.OutdatedChecker.
// For uv, `uv tool list --outdated` is attempted; if the flag is not supported
// by the installed version the call is silently skipped (returns nil, nil).
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

// OutdatedByManager probes Python managers in effective-manager priority order
// and preserves manager attribution for cache rows with InstalledWith set.
func (p *Provider) OutdatedByManager(ctx context.Context) (map[string]map[string]string, error) {
	var effectiveBinary string
	b, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	effectiveBinary = b.binary

	result := make(map[string]map[string]string)
	probeBackend := func(b *backend) error {
		if _, _, err := p.exec.Run(ctx, b.binary, "--version"); err != nil {
			return nil
		}
		var outdated map[string]string
		var err error
		if b.usesTool {
			stdout, _, runErr := p.exec.Run(ctx, b.binary, "tool", "list", "--outdated")
			if runErr != nil {
				return nil // flag not supported or no outdated tools
			}
			outdated = parseUVOutdatedList(stdout)
		} else {
			stdout, _, runErr := p.exec.Run(ctx, b.binary, "list", "--outdated", "--format=json")
			if runErr != nil {
				return fmt.Errorf("%s list --outdated --format=json: %w", b.binary, runErr)
			}
			outdated, err = parsePipOutdatedMap(stdout)
			if err != nil {
				return fmt.Errorf("parsing %s outdated output: %w", b.binary, err)
			}
		}
		if len(outdated) > 0 {
			result[b.binary] = outdated
		}
		return nil
	}

	for i := range supported {
		if supported[i].binary == effectiveBinary {
			if err := probeBackend(&supported[i]); err != nil {
				return nil, err
			}
			break
		}
	}
	for i := range supported {
		if supported[i].binary != effectiveBinary {
			// A broken fill-in backend (e.g. an externally-managed pip) must not
			// fail the whole outdated check when the effective backend succeeded.
			if err := probeBackend(&supported[i]); err != nil {
				continue
			}
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// OutdatedInfoMap returns outdated package versions plus PyPI upload timestamps
// when available.
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

// OutdatedInfoByManager preserves uv/pip attribution and enriches latest
// versions with PyPI release upload metadata.
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
			if availableAt, err := p.pypiVersionAvailableAt(ctx, name, latest); err == nil && availableAt != nil {
				info.AvailableAt = availableAt
				info.DateSource = "pypi_upload_time"
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

// Describe fetches a one-line description from PyPI.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.pypiURL+"/pypi/"+url.PathEscape(tool.EffectivePackage())+"/json", nil)
	if err != nil {
		return "", fmt.Errorf("building PyPI request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PyPI describe: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	var payload struct {
		Info struct {
			Summary string `json:"summary"`
		} `json:"info"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", nil
	}
	return payload.Info.Summary, nil
}

type pypiInfoResponse struct {
	Info struct {
		Summary string `json:"summary"`
	} `json:"info"`
	Releases map[string][]struct {
		UploadTime        string `json:"upload_time"`
		UploadTimeISO8601 string `json:"upload_time_iso_8601"`
	} `json:"releases"`
}

func (p *Provider) pypiVersionAvailableAt(ctx context.Context, pkg, version string) (*time.Time, error) {
	if pkg == "" || version == "" {
		return nil, nil
	}
	var payload pypiInfoResponse
	status, err := provider.FetchJSON(ctx, p.httpClient, p.pypiURL+"/pypi/"+url.PathEscape(pkg)+"/json", &payload)
	if status == 0 {
		return nil, fmt.Errorf("PyPI release metadata: %w", err)
	}
	if err != nil || status != http.StatusOK {
		return nil, nil
	}
	return pypiReleaseAvailableAt(payload.Releases[version]), nil
}

func pypiReleaseAvailableAt(files []struct {
	UploadTime        string `json:"upload_time"`
	UploadTimeISO8601 string `json:"upload_time_iso_8601"`
}) *time.Time {
	var earliest *time.Time
	for _, file := range files {
		uploadedAt, ok := parsePyPIUploadTime(file.UploadTimeISO8601)
		if !ok {
			uploadedAt, ok = parsePyPIUploadTime(file.UploadTime)
		}
		if !ok {
			continue
		}
		if earliest == nil || uploadedAt.Before(*earliest) {
			t := uploadedAt
			earliest = &t
		}
	}
	return earliest
}

func parsePyPIUploadTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			if layout == "2006-01-02T15:04:05" {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// BulkDescribe fetches descriptions for multiple tools via a single show command.
// For uv: runs `uv pip show pkg1 pkg2 ...`.
// For pip3/pip: runs `pip3 show pkg1 pkg2 ...`.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	b, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	pkgs := make([]string, len(tools))
	for i, t := range tools {
		pkgs[i] = t.EffectivePackage()
	}
	var args []string
	if b.usesTool {
		args = append([]string{"pip", "show"}, pkgs...)
	} else {
		args = append([]string{"show"}, pkgs...)
	}
	stdout, _, err := p.exec.Run(ctx, b.binary, args...)
	if err != nil {
		return nil, fmt.Errorf("%s show: %w", b.binary, err)
	}
	return parsePipShowDescriptions(stdout), nil
}

// parsePipShowDescriptions extracts name→summary from multi-package `pip show` / `uv pip show` output.
// Stanzas are separated by "---" lines; each stanza has "Name:" and "Summary:" fields.
func parsePipShowDescriptions(output string) map[string]string {
	m := make(map[string]string)
	var name, summary string
	flush := func() {
		if name != "" && summary != "" {
			m[strings.ToLower(name)] = summary
		}
		name, summary = "", ""
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "---" {
			flush()
			continue
		}
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			name = strings.TrimSpace(val)
		case "Summary":
			summary = strings.TrimSpace(val)
		}
	}
	flush()
	return m
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// uvInstalledMap fetches `uv tool list` and returns lowercase-name→version.
func (p *Provider) uvInstalledMap(ctx context.Context, b *backend) (map[string]string, error) {
	stdout, _, err := p.exec.Run(ctx, b.binary, "tool", "list")
	if err != nil {
		return nil, fmt.Errorf("uv tool list: %w", err)
	}
	tools := parseUVToolList(stdout)
	m := make(map[string]string, len(tools))
	for _, t := range tools {
		m[strings.ToLower(t.Tool.Name)] = t.Version
	}
	return m, nil
}

// parseUVToolList parses `uv tool list` output.
// Non-indented lines match "<name> v?<version>"; indented lines (binaries) are skipped.
func parseUVToolList(stdout string) []provider.InstalledTool {
	var tools []provider.InstalledTool
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		ver := ""
		if len(fields) >= 2 {
			ver = strings.TrimPrefix(fields[1], "v")
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "python", Package: fields[0]},
			Version: ver,
		})
	}
	return tools
}

// parseUVOutdatedList parses `uv tool list --outdated` output.
// Each outdated tool is shown as:
//
//	tool-name v<installed> (update available: v<latest>)
//
// The returned map contains lowercase-name → latestVersion.
// Tools without an "update available" annotation are skipped.
func parseUVOutdatedList(stdout string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
			continue
		}
		// Look for the "(update available: vX.Y.Z)" suffix.
		const marker = "(update available: "
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(marker):]
		// rest is "vX.Y.Z)" — trim the closing paren.
		rest = strings.TrimSuffix(strings.TrimSpace(rest), ")")
		latestVer := strings.TrimPrefix(rest, "v")

		// Extract the tool name from the beginning of the line.
		fields := strings.Fields(line[:idx])
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if name != "" && latestVer != "" {
			m[name] = latestVer
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// pipListEntry is one entry from `pip list --not-required --format=json`.
type pipListEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// parsePipList parses `pip list --not-required --format=json`.
func parsePipList(stdout string) ([]provider.InstalledTool, error) {
	var entries []pipListEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entries); err != nil {
		return nil, err
	}
	tools := make([]provider.InstalledTool, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(e.Name)
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "python", Package: name},
			Version: e.Version,
		})
	}
	return tools, nil
}

// parsePipInstalledMap parses `pip list --not-required --format=json` into lowercase-name→version.
func parsePipInstalledMap(stdout string) (map[string]string, error) {
	var entries []pipListEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entries); err != nil {
		return nil, err
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[strings.ToLower(e.Name)] = e.Version
	}
	return m, nil
}

// pipOutdatedEntry is one entry from `pip list --outdated --format=json`.
type pipOutdatedEntry struct {
	Name          string `json:"name"`
	LatestVersion string `json:"latest_version"`
}

// parsePipOutdatedMap parses `pip list --outdated --format=json`.
func parsePipOutdatedMap(stdout string) (map[string]string, error) {
	var entries []pipOutdatedEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entries); err != nil {
		return nil, err
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[strings.ToLower(e.Name)] = e.LatestVersion
	}
	return m, nil
}

// parsePipShowVersion extracts "Version: x.y.z" from `pip show` output.
func parsePipShowVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	return ""
}
