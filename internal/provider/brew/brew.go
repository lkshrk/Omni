// Package brew implements the Homebrew provider.
package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type Provider struct {
	exec executor.Executor
	mu   sync.RWMutex
}

func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "brew" }
func (p *Provider) Description() string { return "Homebrew — macOS/Linux package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if checker, ok := p.exec.(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable("brew"), nil
	}
	_, _, err := p.exec.Run(ctx, "brew", "--version")
	if err != nil {
		return false, nil // binary not found; not an error from our perspective
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "install", tool.EffectivePackage())
	if err != nil {
		return fmt.Errorf("brew install %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "uninstall", tool.EffectivePackage())
	if err != nil {
		return fmt.Errorf("brew uninstall %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "upgrade", tool.EffectivePackage())
	if err != nil {
		return fmt.Errorf("brew upgrade %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

// IsInstalled checks whether a formula or cask is installed.
// Tap packages like "hashicorp/tap/terraform" must be stripped to "terraform" —
// brew list --versions does not accept the tap-qualified form.
// Modern brew auto-detects formula vs cask for install/uninstall/upgrade, but
// `brew list --versions` only searches formulae by default, so we try the cask
// flag as a fallback.
func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	name := formulaName(tool.EffectivePackage())
	for _, args := range [][]string{
		{"list", "--versions", name},
		{"list", "--versions", "--cask", name},
	} {
		stdout, _, err := p.exec.Run(ctx, "brew", args...)
		if err == nil {
			if out := strings.TrimSpace(stdout); out != "" {
				return true, parseBrewVersion(out), nil
			}
		}
	}
	return false, "", nil
}

// brewInfoOutput is the shape of `brew info --json=v2`.
type brewInfoOutput struct {
	Formulae []brewFormulaInfo `json:"formulae"`
	Casks    []brewCaskInfo    `json:"casks"`
}

type brewFormulaInfo struct {
	Name      string `json:"name"`
	FullName  string `json:"full_name"`
	Desc      string `json:"desc"`
	Installed []struct {
		Version            string `json:"version"`
		InstalledOnRequest bool   `json:"installed_on_request"`
	} `json:"installed"`
}

type brewCaskInfo struct {
	Token     string                       `json:"token"`
	Desc      string                       `json:"desc"`
	Installed string                       `json:"installed"` // installed version string
	Artifacts []map[string]json.RawMessage `json:"artifacts"`
}

// ListInstalled returns explicitly installed formulae and all installed casks.
// It avoids `brew info --json=v2 --installed` because that command can emit
// megabytes of JSON and dominate startup refreshes on cask-heavy systems.
func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.listInstalled(ctx)
}

func (p *Provider) listInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	formulae, err := p.installedFormulae(ctx)
	if err != nil {
		return nil, err
	}
	casks, err := p.installedCasks(ctx)
	if err != nil {
		return nil, err
	}
	var tools []provider.InstalledTool
	tools = append(tools, formulae...)
	tools = append(tools, casks...)
	return tools, nil
}

// InstalledMap returns explicitly installed formulae and casks as lowercase-name→version map.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	tools, err := p.listInstalled(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(tools))
	for _, t := range tools {
		m[strings.ToLower(t.Name)] = t.Version
	}
	return m, nil
}

func (p *Provider) installedFormulae(ctx context.Context) ([]provider.InstalledTool, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "leaves", "--installed-on-request")
	if err != nil {
		return nil, fmt.Errorf("brew leaves --installed-on-request: %w", err)
	}
	packages := strings.Fields(stdout)
	if len(packages) == 0 {
		return nil, nil
	}
	args := append([]string{"list", "--versions"}, packages...)
	stdout, _, err = p.exec.Run(ctx, "brew", args...)
	if err != nil {
		return nil, fmt.Errorf("brew list --versions: %w", err)
	}
	versions := parseBrewListVersions(stdout)
	tools := make([]provider.InstalledTool, 0, len(packages))
	for _, pkg := range packages {
		name := formulaName(pkg)
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "brew", Package: pkg},
			Version: lookupBrewListVersion(versions, pkg),
		})
	}
	return tools, nil
}

func (p *Provider) installedCasks(ctx context.Context) ([]provider.InstalledTool, error) {
	casks, err := p.installedCaskInfos(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]provider.InstalledTool, 0, len(casks))
	for _, c := range casks {
		if c.Token == "" {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: c.Token, Provider: "brew", Package: c.Token},
			Version: c.Installed,
		})
	}
	return tools, nil
}

func (p *Provider) installedCaskInfos(ctx context.Context) ([]brewCaskInfo, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "list", "--cask")
	if err != nil {
		return nil, fmt.Errorf("brew list --cask: %w", err)
	}
	tokens := strings.Fields(stdout)
	if len(tokens) == 0 {
		return nil, nil
	}

	args := append([]string{"info", "--json=v2", "--cask"}, tokens...)
	out, err := p.info(ctx, args...)
	if err != nil {
		return nil, err
	}
	infoByToken := make(map[string]brewCaskInfo, len(out.Casks))
	for _, c := range out.Casks {
		infoByToken[strings.ToLower(c.Token)] = c
	}
	casks := make([]brewCaskInfo, 0, len(tokens))
	for _, token := range tokens {
		if c, ok := infoByToken[strings.ToLower(token)]; ok {
			casks = append(casks, c)
			continue
		}
		casks = append(casks, brewCaskInfo{Token: token})
	}
	return casks, nil
}

func parseBrewListVersions(output string) map[string]string {
	versions := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		version := ""
		if len(fields) > 1 {
			version = strings.Join(fields[1:], " ")
		}
		pkg := strings.ToLower(fields[0])
		versions[pkg] = version
		versions[strings.ToLower(formulaName(fields[0]))] = version
	}
	return versions
}

func lookupBrewListVersion(versions map[string]string, pkg string) string {
	if version, ok := versions[strings.ToLower(pkg)]; ok {
		return version
	}
	return versions[strings.ToLower(formulaName(pkg))]
}

// InstalledMetadataMap returns explicitly installed formulae and casks with
// provider metadata learned from Homebrew's JSON. Some casks install via a pkg
// artifact and uninstall through pkgutil, which may require sudo even though
// normal brew formula operations do not.
func (p *Provider) InstalledMetadataMap(ctx context.Context) (map[string]provider.InstalledMetadata, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	formulae, err := p.installedFormulae(ctx)
	if err != nil {
		return nil, err
	}
	casks, err := p.installedCaskInfos(ctx)
	if err != nil {
		return nil, err
	}
	metadata := make(map[string]provider.InstalledMetadata, len(formulae)+len(casks))
	for _, f := range formulae {
		metadata[strings.ToLower(f.Name)] = provider.InstalledMetadata{Version: f.Version}
	}
	for _, c := range casks {
		entry := provider.InstalledMetadata{Version: c.Installed}
		if plan := c.privilegePlan(provider.PrivilegeActionUninstall); plan.RequiresPrivilege() {
			entry.Privilege = plan
		}
		metadata[strings.ToLower(c.Token)] = entry
	}
	return metadata, nil
}

func (p *Provider) info(ctx context.Context, args ...string) (brewInfoOutput, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", args...)
	if err != nil {
		return brewInfoOutput{}, fmt.Errorf("brew info: %w", err)
	}
	var out brewInfoOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return brewInfoOutput{}, fmt.Errorf("parsing brew info output: %w", err)
	}
	return out, nil
}

func (p *Provider) PrivilegePlan(ctx context.Context, action provider.PrivilegeAction, tool provider.Tool) (provider.PrivilegePlan, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out, err := p.info(ctx, "info", "--json=v2", "--cask", tool.EffectivePackage())
	if err != nil {
		return provider.PrivilegePlan{}, nil
	}
	for _, cask := range out.Casks {
		if !strings.EqualFold(cask.Token, tool.EffectivePackage()) {
			continue
		}
		return cask.privilegePlan(action), nil
	}
	return provider.PrivilegePlan{}, nil
}

func (c brewCaskInfo) privilegePlan(action provider.PrivilegeAction) provider.PrivilegePlan {
	if len(c.Artifacts) == 0 {
		return provider.PrivilegePlan{}
	}
	var reason string
	switch action {
	case provider.PrivilegeActionInstall:
		if c.hasArtifact("pkg") {
			reason = "brew cask " + c.Token + " uses a pkg installer"
		}
	case provider.PrivilegeActionUninstall:
		switch {
		case c.hasUninstallKey("pkgutil"):
			reason = "brew cask " + c.Token + " uses pkgutil uninstall"
		case c.hasArtifact("pkg"):
			reason = "brew cask " + c.Token + " uses a pkg installer"
		}
	case provider.PrivilegeActionUpgrade:
		switch {
		case c.hasArtifact("pkg"):
			reason = "brew cask " + c.Token + " uses a pkg installer"
		case c.hasUninstallKey("pkgutil"):
			reason = "brew cask " + c.Token + " uses pkgutil uninstall"
		}
	}
	if reason == "" {
		return provider.PrivilegePlan{}
	}
	return provider.PrivilegePlan{Requirement: provider.PrivilegeMaybe, Reason: reason}
}

func (c brewCaskInfo) hasArtifact(name string) bool {
	for _, artifact := range c.Artifacts {
		if _, ok := artifact[name]; ok {
			return true
		}
	}
	return false
}

func (c brewCaskInfo) hasUninstallKey(name string) bool {
	for _, artifact := range c.Artifacts {
		raw, ok := artifact["uninstall"]
		if !ok {
			continue
		}
		if rawObjectHasKey(raw, name) {
			return true
		}
	}
	return false
}

func rawObjectHasKey(raw json.RawMessage, key string) bool {
	var object any
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	return objectHasKey(object, key)
}

func objectHasKey(v any, key string) bool {
	switch typed := v.(type) {
	case map[string]any:
		for k, value := range typed {
			if k == key || objectHasKey(value, key) {
				return true
			}
		}
	case []any:
		for _, value := range typed {
			if objectHasKey(value, key) {
				return true
			}
		}
	}
	return false
}

// brewOutdatedOutput is the shape of `brew outdated --json=v2`.
type brewOutdatedOutput struct {
	Formulae []struct {
		Name           string `json:"name"`
		CurrentVersion string `json:"current_version"` // confusingly, this is the LATEST available version
	} `json:"formulae"`
	Casks []struct {
		Name           string `json:"name"`
		CurrentVersion string `json:"current_version"` // latest available version
	} `json:"casks"`
}

// OutdatedMap returns lowercase name → latest available version for outdated formulae and casks.
func (p *Provider) OutdatedMap(ctx context.Context) (map[string]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stdout, _, err := p.exec.Run(ctx, "brew", "outdated", "--json=v2")
	if err != nil {
		return nil, fmt.Errorf("brew outdated: %w", err)
	}
	var out brewOutdatedOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return nil, fmt.Errorf("parsing brew outdated: %w", err)
	}
	m := make(map[string]string, len(out.Formulae)+len(out.Casks))
	for _, f := range out.Formulae {
		m[strings.ToLower(formulaName(f.Name))] = f.CurrentVersion
	}
	for _, c := range out.Casks {
		m[strings.ToLower(formulaName(c.Name))] = c.CurrentVersion
	}
	return m, nil
}

// Describe fetches a one-line description via `brew info --json=v2`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out, err := p.info(ctx, "info", "--json=v2", tool.EffectivePackage())
	if err != nil {
		return "", fmt.Errorf("brew info %s: %w", tool.EffectivePackage(), err)
	}
	if len(out.Formulae) > 0 {
		return out.Formulae[0].Desc, nil
	}
	if len(out.Casks) > 0 {
		return out.Casks[0].Desc, nil
	}
	return "", nil
}

// BulkDescribe fetches descriptions for multiple tools via a single `brew info --json=v2` call.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	args := make([]string, 0, len(tools)+2)
	args = append(args, "info", "--json=v2")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	out, err := p.info(ctx, args...)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.Formulae)+len(out.Casks))
	for _, f := range out.Formulae {
		if f.Desc != "" {
			m[strings.ToLower(f.Name)] = f.Desc
		}
	}
	for _, c := range out.Casks {
		if c.Desc != "" {
			m[strings.ToLower(c.Token)] = c.Desc
		}
	}
	return m, nil
}

func (p *Provider) Tap(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "tap", name)
	if err != nil {
		return fmt.Errorf("brew tap %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Untap(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "untap", name)
	if err != nil {
		return fmt.Errorf("brew untap %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) ListTaps(ctx context.Context) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stdout, _, err := p.exec.Run(ctx, "brew", "tap")
	if err != nil {
		return nil, fmt.Errorf("brew tap: %w", err)
	}
	var taps []string
	for _, line := range strings.Split(stdout, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			taps = append(taps, t)
		}
	}
	return taps, nil
}

func (p *Provider) IsTapped(ctx context.Context, name string) (bool, error) {
	taps, err := p.ListTaps(ctx)
	if err != nil {
		return false, err
	}
	for _, t := range taps {
		if t == name {
			return true, nil
		}
	}
	return false, nil
}

func (p *Provider) Search(ctx context.Context, query string) ([]provider.SearchResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stdout, stderr, err := p.exec.Run(ctx, "brew", "search", query)
	if err != nil {
		if isSearchNotFound(stdout) || isSearchNotFound(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("brew search: %w", err)
	}
	var results []provider.SearchResult
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "==>") {
			continue
		}
		// Each output line may contain multiple space-separated formula names.
		for _, name := range strings.Fields(line) {
			results = append(results, provider.SearchResult{Name: name, Provider: "brew"})
		}
	}
	p.enrichSearchResults(ctx, results)
	return results, nil
}

func isSearchNotFound(output string) bool {
	msg := strings.ToLower(output)
	return strings.Contains(msg, "no formulae or casks found") ||
		strings.Contains(msg, "no formulae found") ||
		strings.Contains(msg, "no casks found")
}

func (p *Provider) enrichSearchResults(ctx context.Context, results []provider.SearchResult) {
	if len(results) == 0 {
		return
	}
	args := make([]string, 0, len(results)+2)
	args = append(args, "info", "--json=v2")
	for _, result := range results {
		args = append(args, result.Name)
	}
	out, err := p.info(ctx, args...)
	if err != nil {
		return
	}
	descriptions := make(map[string]string, len(out.Formulae)+len(out.Casks))
	privileges := make(map[string]provider.PrivilegePlan, len(out.Casks))
	for _, f := range out.Formulae {
		name := f.Name
		if name == "" {
			name = formulaName(f.FullName)
		}
		if name != "" && f.Desc != "" {
			descriptions[strings.ToLower(name)] = f.Desc
		}
	}
	for _, c := range out.Casks {
		if c.Token == "" {
			continue
		}
		key := strings.ToLower(c.Token)
		if c.Desc != "" {
			descriptions[key] = c.Desc
		}
		if plan := c.privilegePlan(provider.PrivilegeActionInstall); plan.RequiresPrivilege() {
			privileges[key] = plan
		}
	}
	for i := range results {
		key := strings.ToLower(results[i].Name)
		if desc := descriptions[key]; desc != "" {
			results[i].Description = desc
		}
		if plan := privileges[key]; plan.RequiresPrivilege() {
			results[i].Privilege = plan
		}
	}
}

// formulaName returns the bare formula name from a package path.
// "hashicorp/tap/terraform" → "terraform", "git" → "git"
func formulaName(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// parseBrewVersion extracts the installed version from `brew list --versions` output.
// Input: "ripgrep 14.1.1" → "14.1.1"
func parseBrewVersion(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return ""
}
