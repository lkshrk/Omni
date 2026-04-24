// Package brew implements the Homebrew provider.
package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type Provider struct {
	exec executor.Executor
}

func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "brew" }
func (p *Provider) Description() string { return "Homebrew — macOS/Linux package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "brew", "--version")
	if err != nil {
		return false, nil // binary not found; not an error from our perspective
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	_, stderr, err := p.exec.Run(ctx, "brew", "install", tool.EffectivePackage())
	if err != nil {
		return fmt.Errorf("brew install %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	_, stderr, err := p.exec.Run(ctx, "brew", "uninstall", tool.EffectivePackage())
	if err != nil {
		return fmt.Errorf("brew uninstall %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
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

// brewInfoOutput is the shape of `brew info --json=v2 --installed`.
type brewInfoOutput struct {
	Formulae []struct {
		FullName  string `json:"full_name"`
		Installed []struct {
			Version            string `json:"version"`
			InstalledOnRequest bool   `json:"installed_on_request"`
		} `json:"installed"`
	} `json:"formulae"`
	Casks []struct {
		Token     string `json:"token"`
		Installed string `json:"installed"` // installed version string
	} `json:"casks"`
}

// ListInstalled returns explicitly installed formulae (installed_on_request=true) and
// all installed casks (casks are always explicit). Excludes transitive formula deps.
// Uses `brew info --json=v2 --installed` because `--full-name` and `--versions` are
// mutually exclusive on `brew list`.
func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "info", "--json=v2", "--installed")
	if err != nil {
		return nil, fmt.Errorf("brew info --installed: %w", err)
	}
	var out brewInfoOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return nil, fmt.Errorf("parsing brew info output: %w", err)
	}
	var tools []provider.InstalledTool
	for _, f := range out.Formulae {
		if len(f.Installed) == 0 || !f.Installed[0].InstalledOnRequest {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: formulaName(f.FullName), Provider: "brew", Package: f.FullName},
			Version: f.Installed[0].Version,
		})
	}
	for _, c := range out.Casks {
		if c.Installed == "" {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: c.Token, Provider: "brew", Package: c.Token},
			Version: c.Installed,
		})
	}
	return tools, nil
}

// InstalledMap returns explicitly installed formulae and casks as lowercase-name→version map.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "info", "--json=v2", "--installed")
	if err != nil {
		return nil, fmt.Errorf("brew info --installed: %w", err)
	}
	var out brewInfoOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return nil, fmt.Errorf("parsing brew info output: %w", err)
	}
	m := make(map[string]string, len(out.Formulae)+len(out.Casks))
	for _, f := range out.Formulae {
		if len(f.Installed) == 0 || !f.Installed[0].InstalledOnRequest {
			continue
		}
		m[strings.ToLower(formulaName(f.FullName))] = f.Installed[0].Version
	}
	for _, c := range out.Casks {
		if c.Installed == "" {
			continue
		}
		m[strings.ToLower(c.Token)] = c.Installed
	}
	return m, nil
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
	if _, stderr, err := p.exec.Run(ctx, "brew", "update"); err != nil {
		return nil, fmt.Errorf("brew update: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
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
		m[strings.ToLower(f.Name)] = f.CurrentVersion
	}
	for _, c := range out.Casks {
		m[strings.ToLower(c.Name)] = c.CurrentVersion
	}
	return m, nil
}

// brewDescV2Output is the shape of `brew info --json=v2 <pkg1> [<pkg2> ...]`.
type brewDescV2Output struct {
	Formulae []struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	} `json:"formulae"`
	Casks []struct {
		Token string `json:"token"`
		Desc  string `json:"desc"`
	} `json:"casks"`
}

// Describe fetches a one-line description via `brew info --json=v2`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "info", "--json=v2", tool.EffectivePackage())
	if err != nil {
		return "", fmt.Errorf("brew info %s: %w", tool.EffectivePackage(), err)
	}
	var out brewDescV2Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return "", nil
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
	args := make([]string, 0, len(tools)+2)
	args = append(args, "info", "--json=v2")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	stdout, _, err := p.exec.Run(ctx, "brew", args...)
	if err != nil {
		return nil, fmt.Errorf("brew info --json=v2: %w", err)
	}
	var out brewDescV2Output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return nil, fmt.Errorf("parsing brew info output: %w", err)
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
	_, stderr, err := p.exec.Run(ctx, "brew", "tap", name)
	if err != nil {
		return fmt.Errorf("brew tap %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Untap(ctx context.Context, name string) error {
	_, stderr, err := p.exec.Run(ctx, "brew", "untap", name)
	if err != nil {
		return fmt.Errorf("brew untap %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) ListTaps(ctx context.Context) ([]string, error) {
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
	stdout, _, err := p.exec.Run(ctx, "brew", "search", "--formulae", query)
	if err != nil {
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
	return results, nil
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
