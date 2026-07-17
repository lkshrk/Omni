// Package pip implements the pip (Python) provider.
package pip

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type Provider struct {
	exec       executor.Executor
	bin        string // "pip3" by default
	httpClient *http.Client
	pypiURL    string // default "https://pypi.org"
}

func New(exec executor.Executor) *Provider {
	return &Provider{
		exec:       exec,
		bin:        "pip3",
		httpClient: &http.Client{Timeout: 15 * time.Second},
		pypiURL:    "https://pypi.org",
	}
}

// newWithPyPI creates a Provider with a custom PyPI base URL and HTTP client.
// Intended for use in tests only.
func newWithPyPI(exec executor.Executor, pypiURL string, client *http.Client) *Provider {
	return &Provider{exec: exec, bin: "pip3", httpClient: client, pypiURL: pypiURL}
}

func (p *Provider) Name() string        { return "pip" }
func (p *Provider) Description() string { return "pip — Python package installer" }

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

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, p.bin, "--version")
	return err == nil, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	_, stderr, err := p.exec.Run(ctx, p.bin, "install", tool.EffectivePackage())
	if err != nil {
		if provider.IsExternallyManagedPythonOutput(stderr) {
			return provider.NewExternallyManagedPythonError(p.bin, "install", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
		}
		return fmt.Errorf("pip install %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	_, stderr, err := p.exec.Run(ctx, p.bin, "uninstall", "-y", tool.EffectivePackage())
	if err != nil {
		if provider.IsExternallyManagedPythonOutput(stderr) {
			return provider.NewExternallyManagedPythonError(p.bin, "uninstall", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
		}
		return fmt.Errorf("pip uninstall %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

// SelfPackageName implements provider.SelfPackageUpgradeChecker. The pip
// package represents the manager itself.
func (p *Provider) SelfPackageName() string { return "pip" }

// externallyManagedProbe asks the Python interpreter whether the environment is
// PEP 668 externally managed (the same `<stdlib>/EXTERNALLY-MANAGED` marker pip
// itself checks before refusing to mutate the environment).
const externallyManagedProbe = `import sysconfig,os;print('1' if os.path.exists(os.path.join(sysconfig.get_path('stdlib'),'EXTERNALLY-MANAGED')) else '0')`

// SelfPackageUpgradeable implements provider.SelfPackageUpgradeChecker. It
// reports whether pip can upgrade its own package: false under PEP 668
// externally-managed Python (where `pip install --upgrade pip` is refused),
// true otherwise. When no interpreter can be probed it assumes upgradeable so
// the tool is not silently hidden on systems we cannot classify.
func (p *Provider) SelfPackageUpgradeable(ctx context.Context) bool {
	for _, py := range []string{"python3", "python"} {
		stdout, _, err := p.exec.Run(ctx, py, "-c", externallyManagedProbe)
		if err != nil {
			continue
		}
		return strings.TrimSpace(stdout) != "1"
	}
	return true
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	_, stderr, err := p.exec.Run(ctx, p.bin, "install", "--upgrade", tool.EffectivePackage())
	if err != nil {
		if provider.IsExternallyManagedPythonOutput(stderr) {
			return provider.NewExternallyManagedPythonError(p.bin, "upgrade", tool, err, stderr, p.ErrorSolutions(provider.ErrorExternallyManagedPython, tool))
		}
		return fmt.Errorf("pip install --upgrade %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	stdout, _, err := p.exec.Run(ctx, p.bin, "show", tool.EffectivePackage())
	if err != nil {
		return false, "", nil // non-zero exit → not installed
	}
	return true, parsePipShowVersion(stdout), nil
}

// pipListEntry is one entry from `pip list --not-required --format=json`.
type pipListEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// cliToolSetScript returns a JSON object mapping lowercase package name → true
// for every pip package that installs a console_scripts or scripts entry point.
// Used by CLIToolSet to decide which imported packages need auto-ignore.
// Distributions with broken/partial dist-info metadata (seen on some Ubuntu
// apt-managed installs) are skipped individually instead of aborting the scan.
const cliToolSetScript = `import importlib.metadata,json
seen={}
def has_cli(d):
	try:
		return any(e.group in ("console_scripts","scripts") for e in d.entry_points)
	except Exception:
		return False
for d in importlib.metadata.distributions():
	try:
		name=d.metadata["Name"]
	except Exception:
		continue
	if not name:
		continue
	if has_cli(d):
		seen[name.lower()]=1
print(json.dumps(seen))`

// pipOwnedPackageSetScript returns distributions whose INSTALLER metadata says
// pip. Distro packages are visible to `pip list` too, but pip does not own them
// and Omni must not import or offer to upgrade them.
const pipOwnedPackageSetScript = `import importlib.metadata,json
owned={}
for d in importlib.metadata.distributions():
	try:
		installer=(d.read_text("INSTALLER") or "").strip().casefold()
		name=d.metadata["Name"]
	except Exception:
		continue
	if installer=="pip" and name:
		owned[name.lower()]=1
print(json.dumps(owned))`

func (p *Provider) pipOwnedPackageSet(ctx context.Context) (map[string]bool, error) {
	stdout, _, err := p.exec.Run(ctx, "python3", "-c", pipOwnedPackageSetScript)
	if err != nil {
		return nil, fmt.Errorf("pip ownership probe: %w", err)
	}
	var raw map[string]int
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &raw); err != nil {
		return nil, fmt.Errorf("parsing pip ownership probe: %w", err)
	}
	owned := make(map[string]bool, len(raw))
	for name := range raw {
		owned[strings.ToLower(name)] = true
	}
	return owned, nil
}

// CLIToolSet returns the set of lowercase pip package names that install at
// least one CLI entry point.  Used by Import to mark library packages as
// auto-ignored.
func (p *Provider) CLIToolSet(ctx context.Context) (map[string]bool, error) {
	stdout, _, err := p.exec.Run(ctx, "python3", "-c", cliToolSetScript)
	if err != nil {
		return nil, fmt.Errorf("pip cli tool set: %w", err)
	}
	var m map[string]int
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &m); err != nil {
		return nil, fmt.Errorf("parsing cli tool set: %w", err)
	}
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out, nil
}

// ListInstalled returns top-level (non-transitive), pip-owned packages,
// including pure libraries. Distro-owned packages exposed by pip are excluded.
func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	stdout, _, err := p.exec.Run(ctx, p.bin, "list", "--not-required", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("pip list --not-required: %w", err)
	}
	var entries []pipListEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entries); err != nil {
		return nil, fmt.Errorf("parsing pip list output: %w", err)
	}
	owned, err := p.pipOwnedPackageSet(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]provider.InstalledTool, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(e.Name)
		if !owned[name] {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "pip", Package: name},
			Version: e.Version,
		})
	}
	return tools, nil
}

// InstalledMap returns top-level, pip-owned packages as lowercase-name→version.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	stdout, _, err := p.exec.Run(ctx, p.bin, "list", "--not-required", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("pip list --not-required: %w", err)
	}
	var entries []pipListEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &entries); err != nil {
		return nil, fmt.Errorf("parsing pip list output: %w", err)
	}
	owned, err := p.pipOwnedPackageSet(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.ToLower(e.Name)
		if owned[name] {
			m[name] = e.Version
		}
	}
	return m, nil
}

// pipOutdatedEntry is one entry from `pip list --outdated --format=json`.
type pipOutdatedEntry struct {
	Name          string `json:"name"`
	LatestVersion string `json:"latest_version"`
}

// OutdatedMap returns pip-owned lowercase package name → latest available version.
func (p *Provider) OutdatedMap(ctx context.Context) (map[string]string, error) {
	stdout, _, err := p.exec.Run(ctx, p.bin, "list", "--outdated", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("pip list --outdated: %w", err)
	}
	var entries []pipOutdatedEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, fmt.Errorf("parsing pip outdated: %w", err)
	}
	owned, err := p.pipOwnedPackageSet(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.ToLower(e.Name)
		if owned[name] {
			m[name] = e.LatestVersion
		}
	}
	return m, nil
}

// OutdatedInfoMap returns outdated package versions plus PyPI upload timestamps
// for the latest version when PyPI exposes them.
func (p *Provider) OutdatedInfoMap(ctx context.Context) (map[string]provider.OutdatedInfo, error) {
	outdated, err := p.OutdatedMap(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]provider.OutdatedInfo, len(outdated))
	for name, latest := range outdated {
		info := provider.OutdatedInfo{LatestVersion: latest}
		if availableAt, err := p.pypiVersionAvailableAt(ctx, name, latest); err == nil && availableAt != nil {
			info.AvailableAt = availableAt
			info.DateSource = "pypi_upload_time"
		}
		result[name] = info
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// BulkDescribe fetches descriptions for multiple tools via a single `pip show` call.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	args := make([]string, 0, len(tools)+1)
	args = append(args, "show")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	stdout, _, err := p.exec.Run(ctx, p.bin, args...)
	if err != nil {
		return nil, fmt.Errorf("pip show: %w", err)
	}
	return parsePipShowDescriptions(stdout), nil
}

// parsePipShowDescriptions extracts name→summary from multi-package `pip show` output.
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

// Describe fetches a one-line description from PyPI.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	var payload pypiInfoResponse
	status, err := provider.FetchJSON(ctx, p.httpClient, p.pypiURL+"/pypi/"+url.PathEscape(tool.EffectivePackage())+"/json", &payload)
	if status == 0 {
		return "", fmt.Errorf("PyPI describe: %w", err)
	}
	if err != nil || status != http.StatusOK {
		return "", nil
	}
	return payload.Info.Summary, nil
}

// parsePipShowVersion extracts the version from `pip show` output.
// Input: "Name: black\nVersion: 23.12.1\n..." → "23.12.1"
func parsePipShowVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	return ""
}

// pypiInfoResponse is the relevant subset of GET /pypi/<name>/json.
type pypiInfoResponse struct {
	Info struct {
		Name        string            `json:"name"`
		Version     string            `json:"version"`
		Summary     string            `json:"summary"`
		HomePage    string            `json:"home_page"`
		ProjectURLs map[string]string `json:"project_urls"`
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

// Search looks up an exact package name on PyPI.
// Returns one result on a match, nil on 404.
// PyPI's full-text search API was removed in 2021; exact lookup is the only
// option that doesn't involve scraping HTML.
func (p *Provider) Search(ctx context.Context, query string) ([]provider.SearchResult, error) {
	var payload pypiInfoResponse
	status, err := provider.FetchJSON(ctx, p.httpClient, p.pypiURL+"/pypi/"+url.PathEscape(query)+"/json", &payload)
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status == 0 {
		return nil, fmt.Errorf("PyPI lookup: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("decoding PyPI response: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("PyPI returned HTTP %d", status)
	}
	return []provider.SearchResult{{
		Name:        payload.Info.Name,
		Version:     payload.Info.Version,
		Description: payload.Info.Summary,
		Provider:    "pip",
		Source:      provider.GitHubSourceHint(pypiSourceCandidates(payload.Info.ProjectURLs, payload.Info.HomePage)...),
	}}, nil
}

// pypiSourceCandidates returns project URLs likely to point at the upstream
// source repo, repository/source-labelled keys first, then any remaining URLs,
// then the home page.
func pypiSourceCandidates(projectURLs map[string]string, homePage string) []string {
	candidates := make([]string, 0, len(projectURLs)+1)
	preferred := make([]string, 0, len(projectURLs))
	rest := make([]string, 0, len(projectURLs))
	for label, u := range projectURLs {
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "source", "source code", "repository", "code", "github":
			preferred = append(preferred, u)
		default:
			rest = append(rest, u)
		}
	}
	candidates = append(candidates, preferred...)
	candidates = append(candidates, rest...)
	candidates = append(candidates, homePage)
	return candidates
}
