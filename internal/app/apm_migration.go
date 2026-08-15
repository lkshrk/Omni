package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

type APMMigrationResult struct {
	Path               string
	AlreadyExists      bool
	MigratedPackages   int
	MigratedMCPServers int
	Warnings           []string
}

type apmMigrationManifest struct {
	Name         string                   `yaml:"name"`
	Version      string                   `yaml:"version"`
	Targets      []string                 `yaml:"targets,omitempty"`
	Dependencies apmMigrationDependencies `yaml:"dependencies"`
}

type apmMigrationDependencies struct {
	APM []apmMigrationPackage `yaml:"apm,omitempty"`
	MCP []apmMigrationMCP     `yaml:"mcp,omitempty"`
}

type apmMigrationPackage struct {
	Git     string   `yaml:"git,omitempty"`
	Path    string   `yaml:"path,omitempty"`
	Ref     string   `yaml:"ref,omitempty"`
	Skills  []string `yaml:"skills,omitempty"`
	Targets []string `yaml:"targets,omitempty"`
}

type apmMigrationMCP struct {
	Name      string            `yaml:"name"`
	Registry  bool              `yaml:"registry"`
	Transport string            `yaml:"transport"`
	Command   string            `yaml:"command,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	Targets   []string          `yaml:"targets,omitempty"`
}

// MigrateAgentsToAPM moves only losslessly representable legacy agent entries into APM's global manifest.
func (a *App) MigrateAgentsToAPM() (APMMigrationResult, error) {
	path, err := a.globalAPMManifestPath()
	if err != nil {
		return APMMigrationResult{}, err
	}
	result := APMMigrationResult{Path: path}
	cfg, err := a.loadConfig()
	if err != nil {
		return result, fmt.Errorf("loading legacy agent config: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		result.AlreadyExists = true
		if hasLegacyAgents(cfg.Agents) {
			return result, fmt.Errorf("existing APM manifest preserved; merge legacy Omni agent entries into %s before syncing", path)
		}
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("checking APM manifest: %w", err)
	}
	if groupAgentConfigPresent(cfg.Groups) {
		return result, fmt.Errorf("legacy group-scoped agent configuration cannot be migrated automatically; remove group agent references before syncing with APM")
	}
	use := a.effectiveSettings(cfg).AgentsUse
	if use != nil && len(use) == 0 && hasLegacyAgents(cfg.Agents) {
		return result, fmt.Errorf("legacy agent configuration cannot be migrated losslessly: agents_use explicitly disables every target")
	}
	legacyTargets := a.effectiveAgentTargets(cfg)
	targets, unsupported := migrateAPMTargets(legacyTargets)
	if len(unsupported) > 0 {
		return result, fmt.Errorf("legacy agent configuration cannot be migrated losslessly: unsupported APM targets %s", strings.Join(unsupported, ", "))
	}
	manifest := apmMigrationManifest{Name: "omni-agents", Version: "0.1.0"}
	migratedPackages := make([]config.SkillPackage, 0, len(cfg.Agents.Packages))
	deploymentTargets := make([]string, 0)
	unscopedWithoutTargets := false
	for _, pkg := range cfg.Agents.Packages {
		candidate := pkg
		if use != nil {
			candidate.Agents = effectiveSkillAgents(use, pkg)
			if len(candidate.Agents) == 0 {
				result.Warnings = append(result.Warnings, fmt.Sprintf("package %q remains in Omni: its targets are disabled by agents_use", pkg.Source))
				continue
			}
		} else if len(candidate.Agents) == 0 {
			candidate.Agents = slices.Clone(legacyTargets)
			unscopedWithoutTargets = len(candidate.Agents) == 0
		}
		dependency, warning, ok := a.migrateAPMPackage(candidate)
		if !ok {
			result.Warnings = append(result.Warnings, warning)
			continue
		}
		manifest.Dependencies.APM = append(manifest.Dependencies.APM, dependency)
		dependencyTargets := dependency.Targets
		if len(dependencyTargets) == 0 {
			dependencyTargets = targets
		}
		manifest.Targets = appendUnique(manifest.Targets, dependencyTargets...)
		migratedPackages = append(migratedPackages, pkg)
		if len(candidate.Agents) > 0 {
			deploymentTargets = append(deploymentTargets, candidate.Agents...)
		} else {
			deploymentTargets = append(deploymentTargets, legacyTargets...)
		}
	}
	migratedMCP := make([]config.McpServer, 0, len(cfg.Agents.McpServers))
	for _, server := range cfg.Agents.McpServers {
		dependency, warning, ok := migrateAPMMCP(server)
		if !ok {
			result.Warnings = append(result.Warnings, warning)
			continue
		}
		manifest.Dependencies.MCP = append(manifest.Dependencies.MCP, dependency)
		manifest.Targets = appendUnique(manifest.Targets, targets...)
		migratedMCP = append(migratedMCP, server)
	}
	for _, marketplace := range cfg.Agents.Marketplaces {
		result.Warnings = append(result.Warnings, fmt.Sprintf("marketplace %q remains in Omni: APM has no lossless equivalent for the legacy source definition", marketplace.Name))
	}
	for _, plugin := range cfg.Agents.Plugins {
		result.Warnings = append(result.Warnings, fmt.Sprintf("plugin %q remains in Omni: its legacy marketplace/source semantics are not losslessly representable", plugin.Name))
	}
	if !agentsIgnoreEmpty(cfg.Agents.Ignore) {
		result.Warnings = append(result.Warnings, "agent ignore rules remain in Omni: APM has no equivalent ignore manifest")
	}
	if len(migratedPackages) != len(cfg.Agents.Packages) || len(migratedMCP) != len(cfg.Agents.McpServers) ||
		len(cfg.Agents.Marketplaces) > 0 || len(cfg.Agents.Plugins) > 0 || !agentsIgnoreEmpty(cfg.Agents.Ignore) {
		return result, fmt.Errorf("legacy agent configuration cannot be migrated losslessly; resolve the reported entries before syncing with APM")
	}
	if len(migratedMCP) > 0 {
		for _, target := range manifest.Targets {
			if !slices.Contains(targets, target) {
				return result, fmt.Errorf("legacy agent configuration cannot be migrated losslessly: package-only targets cannot share an APM manifest with unscoped MCP servers")
			}
		}
	}
	if unscopedWithoutTargets && len(manifest.Targets) > 0 {
		return result, fmt.Errorf("legacy agent configuration cannot be migrated losslessly: an unscoped package has no effective targets beside target-specific packages")
	}
	if len(migratedPackages) == 0 && len(migratedMCP) == 0 {
		return result, nil
	}
	if len(migratedPackages) > 0 {
		home := filepath.Dir(filepath.Dir(path))
		if deployment, err := a.legacySkillDeployment(home, deploymentTargets); err != nil {
			return result, err
		} else if deployment != "" {
			return result, fmt.Errorf("legacy skill deployments remain under %s; remove or adopt them before syncing with APM", deployment)
		}
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		return result, fmt.Errorf("encoding APM manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return result, fmt.Errorf("creating APM home: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		result.AlreadyExists = true
		return result, fmt.Errorf("existing APM manifest preserved; merge legacy Omni agent entries into %s before syncing", path)
	}
	if err != nil {
		return result, fmt.Errorf("creating APM manifest: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return result, fmt.Errorf("writing APM manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return result, fmt.Errorf("closing APM manifest: %w", err)
	}

	if err := a.withConfig(func(updated *config.RootConfig) error {
		updated.Agents.Packages = removeMigrated(updated.Agents.Packages, migratedPackages)
		updated.Agents.McpServers = removeMigrated(updated.Agents.McpServers, migratedMCP)
		return nil
	}); err != nil {
		removeErr := os.Remove(path)
		if removeErr != nil {
			return result, fmt.Errorf("removing migrated legacy agent config: %w (also could not roll back %s: %v)", err, path, removeErr)
		}
		return result, fmt.Errorf("removing migrated legacy agent config: %w", err)
	}
	result.MigratedPackages = len(migratedPackages)
	result.MigratedMCPServers = len(migratedMCP)
	return result, nil
}

func (a *App) globalAPMManifestPath() (string, error) {
	home := strings.TrimSpace(a.lookupEnv("HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home for APM manifest: %w", err)
		}
		home = userHome
	}
	return filepath.Join(home, ".apm", "apm.yml"), nil
}

func (a *App) migrateAPMPackage(pkg config.SkillPackage) (apmMigrationPackage, string, bool) {
	targets, unsupported := migrateAPMTargets(pkg.Agents)
	if len(unsupported) > 0 {
		return apmMigrationPackage{}, fmt.Sprintf("package %q remains in Omni: unsupported APM targets %s", pkg.Source, strings.Join(unsupported, ", ")), false
	}
	if strings.TrimSpace(pkg.Source) == "" {
		return apmMigrationPackage{}, "agent package with an empty source remains in Omni", false
	}
	dependency := apmMigrationPackage{Ref: pkg.Ref, Skills: slices.Clone(pkg.Skills), Targets: targets}
	if isLocalAPMSource(pkg.Source) {
		if strings.TrimSpace(pkg.Ref) != "" {
			return apmMigrationPackage{}, fmt.Sprintf("package %q remains in Omni: APM local paths cannot preserve a Git ref", pkg.Source), false
		}
		dependency.Path = pkg.Source
		if !filepath.IsAbs(dependency.Path) {
			if strings.HasPrefix(dependency.Path, "~/") || strings.HasPrefix(dependency.Path, `~\`) {
				home := strings.TrimSpace(a.lookupEnv("HOME"))
				dependency.Path = filepath.Join(home, dependency.Path[2:])
			} else {
				dependency.Path = filepath.Join(filepath.Dir(a.ConfigPath), dependency.Path)
			}
		}
		dependency.Path = filepath.Clean(dependency.Path)
	} else {
		if strings.HasPrefix(pkg.Source, "file:") || strings.HasPrefix(pkg.Source, "http://") || strings.HasPrefix(pkg.Source, "https://") {
			return apmMigrationPackage{}, fmt.Sprintf("package %q remains in Omni: APM migration requires a local path or Git source", pkg.Source), false
		}
		dependency.Git = pkg.Source
	}
	return dependency, "", true
}

func migrateAPMMCP(server config.McpServer) (apmMigrationMCP, string, bool) {
	if !validAPMName(server.Name) {
		return apmMigrationMCP{}, fmt.Sprintf("MCP server %q remains in Omni: its name is not valid in an APM manifest", server.Name), false
	}
	if len(server.Agents) > 0 {
		return apmMigrationMCP{}, fmt.Sprintf("MCP server %q remains in Omni: APM cannot losslessly scope one server to selected global targets", server.Name), false
	}
	targets, unsupported := migrateAPMTargets(server.Agents)
	if len(unsupported) > 0 {
		return apmMigrationMCP{}, fmt.Sprintf("MCP server %q remains in Omni: unsupported APM targets %s", server.Name, strings.Join(unsupported, ", ")), false
	}
	dependency := apmMigrationMCP{
		Name: server.Name, Registry: false, Transport: server.Transport, URL: server.URL,
		Env: mapsClone(server.EnvLiteral), Headers: mapsClone(server.Headers), Targets: targets,
	}
	if server.Transport == "stdio" {
		if len(strings.Fields(server.Command)) != 1 || strings.Contains(server.Command, "\x00") || hasParentPathSegment(server.Command) {
			return apmMigrationMCP{}, fmt.Sprintf("MCP server %q remains in Omni: its stdio command contains arguments APM stores separately", server.Name), false
		}
		dependency.Command = server.Command
	} else {
		parsed, err := url.Parse(server.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return apmMigrationMCP{}, fmt.Sprintf("MCP server %q remains in Omni: APM target integrations require an absolute HTTPS URL", server.Name), false
		}
	}
	for name, value := range dependency.Headers {
		if strings.ContainsAny(value, "\r\n") {
			return apmMigrationMCP{}, fmt.Sprintf("MCP server %q remains in Omni: header %q contains a newline APM rejects", server.Name, name), false
		}
	}
	if dependency.Env == nil && len(server.Env) > 0 {
		dependency.Env = make(map[string]string, len(server.Env))
	}
	for _, name := range server.Env {
		if _, exists := dependency.Env[name]; !exists {
			dependency.Env[name] = "${env:" + name + "}"
		}
	}
	return dependency, "", true
}

func (a *App) effectiveAgentTargets(cfg *config.RootConfig) []string {
	if use := a.effectiveSettings(cfg).AgentsUse; use != nil {
		return use
	}
	home := strings.TrimSpace(a.lookupEnv("HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	agents := a.installedAgents(home)
	ids := make([]string, 0, len(agents))
	for _, agent := range agents {
		ids = append(ids, agent.ID)
	}
	return ids
}

func validAPMName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func hasParentPathSegment(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func (a *App) legacySkillDeployment(home string, targetIDs []string) (string, error) {
	seen := make(map[string]bool)
	for _, id := range targetIDs {
		target, ok := a.agentRegistry().ByID(id)
		if !ok {
			continue
		}
		for _, dir := range target.SkillDirs(home) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			info, err := os.Lstat(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("checking legacy skill deployments: %w", err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return dir, nil
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", fmt.Errorf("checking legacy skill deployments: %w", err)
			}
			if len(entries) > 0 {
				return dir, nil
			}
		}
	}
	return "", nil
}

func groupAgentConfigPresent(groups []*config.GroupConfig) bool {
	for _, group := range groups {
		if group != nil && (len(group.Skills) > 0 || len(group.McpServers) > 0 || len(group.Plugins) > 0 || len(group.Marketplaces) > 0) {
			return true
		}
	}
	return false
}

func migrateAPMTargets(legacy []string) ([]string, []string) {
	targets := make([]string, 0, len(legacy))
	unsupported := make([]string, 0)
	for _, target := range legacy {
		mapped := map[string]string{
			"antigravity":    "antigravity",
			"claude-code":    "claude",
			"codex":          "codex",
			"cursor":         "cursor",
			"gemini-cli":     "gemini",
			"github-copilot": "copilot",
			"grok":           "grok-build",
			"kiro-cli":       "kiro",
			"opencode":       "opencode",
			"windsurf":       "windsurf",
		}[target]
		if mapped == "" {
			unsupported = append(unsupported, target)
			continue
		}
		if !slices.Contains(targets, mapped) {
			targets = append(targets, mapped)
		}
	}
	return targets, unsupported
}

func removeMigrated[T any](all, migrated []T) []T {
	remaining := slices.Clone(all)
	for _, item := range migrated {
		for i := range remaining {
			if reflect.DeepEqual(remaining[i], item) {
				remaining = slices.Delete(remaining, i, i+1)
				break
			}
		}
	}
	return remaining
}

func mapsClone(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func appendUnique(all []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(all, value) {
			all = append(all, value)
		}
	}
	return all
}

func isLocalAPMSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~/") || strings.HasPrefix(source, `.\`) || strings.HasPrefix(source, `..\`) || strings.HasPrefix(source, `~\`)
}

func agentsIgnoreEmpty(ignore config.AgentsIgnore) bool {
	return len(ignore.Skills) == 0 && len(ignore.McpServers) == 0 && len(ignore.Plugins) == 0 && len(ignore.Marketplaces) == 0
}

func hasLegacyAgents(agents config.AgentsConfig) bool {
	return len(agents.Packages) > 0 || len(agents.McpServers) > 0 || len(agents.Marketplaces) > 0 || len(agents.Plugins) > 0 || !agentsIgnoreEmpty(agents.Ignore)
}
