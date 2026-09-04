package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

const snapshotGlob = ".omni-apm-migration-backup-*"

const agentsMigrationMarker = "# omni:agents-migration:v1"

type apmManifest struct {
	Name         string          `yaml:"name"`
	Version      string          `yaml:"version"`
	Dependencies apmDependencies `yaml:"dependencies"`
	Targets      []string        `yaml:"targets,omitempty"`
}

type apmDependencies struct {
	APM []apmPackageDep `yaml:"apm,omitempty"`
	MCP []apmMCPDep     `yaml:"mcp,omitempty"`
	LSP []apmLSPDep     `yaml:"lsp,omitempty"`
}

type apmLSPDep struct {
	Name                string            `yaml:"name"`
	Transport           string            `yaml:"transport,omitempty"`
	Command             string            `yaml:"command,omitempty"`
	Args                []string          `yaml:"args,omitempty"`
	Env                 map[string]string `yaml:"env,omitempty"`
	Cwd                 string            `yaml:"cwd,omitempty"`
	ExtensionToLanguage map[string]string `yaml:"extensionToLanguage,omitempty"`
	Initialization      any               `yaml:"initializationOptions,omitempty"`
	WorkspaceFolder     string            `yaml:"workspaceFolder,omitempty"`
	StartupTimeout      int               `yaml:"startupTimeout,omitempty"`
	ShutdownTimeout     int               `yaml:"shutdownTimeout,omitempty"`
	RestartOnCrash      bool              `yaml:"restartOnCrash,omitempty"`
	MaxRestarts         int               `yaml:"maxRestarts,omitempty"`
	Raw                 map[string]any    `yaml:"-" json:"-"`
}

type apmPackageDep struct {
	Git         string   `yaml:"git,omitempty"`
	Path        string   `yaml:"path,omitempty"`
	Name        string   `yaml:"name,omitempty"`
	Marketplace string   `yaml:"marketplace,omitempty"`
	Ref         string   `yaml:"ref,omitempty"`
	Targets     []string `yaml:"targets,omitempty"`
}

type apmMCPDep struct {
	Name      string            `yaml:"name"`
	Registry  bool              `yaml:"registry"`
	Transport string            `yaml:"transport"`
	Version   string            `yaml:"version,omitempty"`
	Package   string            `yaml:"package,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Cwd       string            `yaml:"cwd,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Tools     []string          `yaml:"tools,omitempty"`
	Raw       map[string]any    `yaml:"-" json:"-"`
}

type legacyEntry struct {
	Name        string            `json:"name"`
	Source      string            `json:"source"`
	Ref         string            `json:"ref"`
	Marketplace string            `json:"marketplace"`
	Owner       string            `json:"owner"`
	Transport   string            `json:"transport"`
	URL         string            `json:"url"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Cwd         string            `json:"cwd"`
	Headers     map[string]string `json:"headers"`
	Env         []string          `json:"env"`
	EnvLiteral  map[string]string `json:"env_literal"`
	Agents      []string          `json:"agents"`
}

var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// apmPlaceholders rewrites omni's ${VAR} into apm's ${env:VAR}, the only form apm expands.
func apmPlaceholders(s string) string {
	return envPlaceholder.ReplaceAllString(s, "${env:$1}")
}

func apmTargets(agents []string) []string {
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		if agent == "claude-code" {
			agent = "claude"
		}
		if !slices.Contains(out, agent) {
			out = append(out, agent)
		}
	}
	sort.Strings(out)
	return out
}

// RenderAPMTemplate renders an apm.yml; marketplaces come back separately because apm.yml cannot express them.
func RenderAPMTemplate(decls config.LegacyAgentDecls) (string, []string, error) {
	return renderAPMTemplate(decls, nil)
}

func renderAPMTemplatePlan(plan agentBundlePlan) (string, []string, error) {
	deps := make([]apmPackageDep, 0, len(plan.Owners))
	for _, owner := range plan.Owners {
		deps = append(deps, owner.Dependency)
	}
	return renderAPMTemplate(plan.Decls, deps)
}

func renderAPMTemplate(decls config.LegacyAgentDecls, ownerDeps []apmPackageDep) (string, []string, error) {
	manifest := apmManifest{Name: "omni-migrated", Version: "1.0.0"}
	var reach []string
	note := func(targets []string) {
		for _, t := range targets {
			if !slices.Contains(reach, t) {
				reach = append(reach, t)
			}
		}
	}
	for _, dep := range ownerDeps {
		manifest.Dependencies.APM = append(manifest.Dependencies.APM, dep)
		note(dep.Targets)
	}

	for _, source := range slices.Sorted(maps.Keys(decls.Packages)) {
		entry, err := decodeLegacyEntry(decls.Packages[source], "package", source)
		if err != nil {
			return "", nil, err
		}
		targets := apmTargets(entry.Agents)
		note(targets)
		manifest.Dependencies.APM = append(manifest.Dependencies.APM, apmPackageDep{
			Git:     apmPlaceholders(entry.Source),
			Ref:     entry.Ref,
			Targets: targets,
		})
	}

	for _, name := range slices.Sorted(maps.Keys(decls.Plugins)) {
		entry, err := decodeLegacyEntry(decls.Plugins[name], "plugin", name)
		if err != nil {
			return "", nil, err
		}
		targets := apmTargets(entry.Agents)
		note(targets)
		dep := apmPackageDep{Targets: targets}
		switch {
		case entry.Marketplace != "":
			dep.Name, dep.Marketplace = entry.Name, entry.Marketplace
		case entry.Source != "":
			dep.Git = apmPlaceholders(entry.Source)
		default:
			return "", nil, fmt.Errorf("plugin %q has neither a marketplace nor a source", name)
		}
		manifest.Dependencies.APM = append(manifest.Dependencies.APM, dep)
	}

	for _, name := range slices.Sorted(maps.Keys(decls.MCPServers)) {
		entry, err := decodeLegacyEntry(decls.MCPServers[name], "mcp server", name)
		if err != nil {
			return "", nil, err
		}
		note(apmTargets(entry.Agents))
		dep := apmMCPDep{
			Name:      entry.Name,
			Transport: entry.Transport,
			URL:       apmPlaceholders(entry.URL),
			Cwd:       apmPlaceholders(entry.Cwd),
		}
		dep.Command = apmPlaceholders(entry.Command)
		for _, arg := range entry.Args {
			dep.Args = append(dep.Args, apmPlaceholders(arg))
		}
		for header, value := range entry.Headers {
			if dep.Headers == nil {
				dep.Headers = map[string]string{}
			}
			dep.Headers[header] = apmPlaceholders(value)
		}
		for _, envName := range entry.Env {
			if dep.Env == nil {
				dep.Env = map[string]string{}
			}
			dep.Env[envName] = "${env:" + envName + "}"
		}
		for key, value := range entry.EnvLiteral {
			if dep.Env == nil {
				dep.Env = map[string]string{}
			}
			dep.Env[key] = apmPlaceholders(value)
		}
		manifest.Dependencies.MCP = append(manifest.Dependencies.MCP, dep)
	}

	sort.Strings(reach)
	manifest.Targets = reach

	var buf strings.Builder
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(manifest); err != nil {
		return "", nil, fmt.Errorf("render apm.yml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", nil, fmt.Errorf("render apm.yml: %w", err)
	}

	var cmds []string
	for _, name := range slices.Sorted(maps.Keys(decls.Marketplaces)) {
		entry, err := decodeLegacyEntry(decls.Marketplaces[name], "marketplace", name)
		if err != nil {
			return "", nil, err
		}
		cmds = append(cmds, marketplaceDecl{name: entry.Name, source: entry.Source}.Render())
	}
	return buf.String(), cmds, nil
}

func decodeLegacyEntry(raw json.RawMessage, kind, name string) (legacyEntry, error) {
	var entry legacyEntry
	if unsafeMigrationScalar(name) {
		return entry, fmt.Errorf("%s identifier contains CR/LF/NUL", kind)
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return entry, fmt.Errorf("decode %s %q: %w", kind, name, err)
	}
	for field, value := range map[string]string{"name": entry.Name, "source": entry.Source, "ref": entry.Ref, "marketplace": entry.Marketplace, "owner": entry.Owner} {
		if unsafeMigrationScalar(value) {
			return entry, fmt.Errorf("%s %q field %q contains CR/LF/NUL", kind, name, field)
		}
	}
	splitJoinedLegacyCommand(&entry)
	return entry, nil
}

// Pre-migration snapshots store argv as one joined string; structured Args always win.
func splitJoinedLegacyCommand(entry *legacyEntry) {
	if len(entry.Args) > 0 {
		return
	}
	if fields := strings.Fields(entry.Command); len(fields) > 1 {
		entry.Command, entry.Args = fields[0], fields[1:]
	}
}

func unsafeMigrationScalar(value string) bool { return strings.ContainsAny(value, "\r\n\x00") }

// AgentsMigrate previews a host's pre-migration declarations; it writes nothing and runs no apm command.
func (a *App) AgentsMigrate(ctx context.Context, host, snapshotDir string) (string, error) {
	return a.agentsMigrate(ctx, host, snapshotDir, false, nil)
}

// AgentsMigrateWrite publishes required wrappers and the canonical migration-owned template.
func (a *App) AgentsMigrateWrite(ctx context.Context, host, snapshotDir string) (string, error) {
	return a.agentsMigrate(ctx, host, snapshotDir, true, writeAgentsMigrationTemplate)
}

func (a *App) agentsMigrate(ctx context.Context, host, snapshotDir string, write bool, writeTemplate func(string, []byte) (string, error)) (string, error) {
	plan, rendered, err := a.planAgentsMigration(ctx, host, snapshotDir)
	if err != nil {
		return "", err
	}
	if !write {
		return rendered, nil
	}
	templatePath, err := AgentsTemplatePath()
	if err != nil {
		return "", err
	}
	identity, err := inspectAgentsMigrationTemplate(templatePath)
	if err != nil {
		return "", err
	}
	prepared, err := prepareAgentBundleWrappers(plan)
	if err != nil {
		return "", fmt.Errorf("prepare migration wrappers: %w", err)
	}
	defer discardPreparedAgentBundleWrappers(prepared)
	warning, err := commitAgentMigration(templatePath, a.StateDir, plan, prepared, identity, rendered, writeTemplate)
	if err != nil {
		return "", err
	}
	if warning != "" {
		rendered += "# warning: " + warning + "\n"
	}
	return rendered, nil
}

func (a *App) planAgentsMigration(ctx context.Context, host, snapshotDir string) (agentBundlePlan, string, error) {
	if host == "" {
		return agentBundlePlan{}, "", fmt.Errorf("host is required")
	}
	if snapshotDir == "" {
		found, err := a.defaultSnapshotDir()
		if err != nil {
			return agentBundlePlan{}, "", err
		}
		snapshotDir = found
	}
	if snapshotDir == "" {
		observations, err := a.inventoryNativeAgents(ctx)
		if err != nil {
			return agentBundlePlan{}, "", err
		}
		plan, rendered := nativeAgentPlan(resolveAgentDispositions(observations))
		return plan, rendered, nil
	}
	decls, evidence, err := config.LegacyAgentsFromSnapshot(snapshotDir, host)
	if err != nil {
		return agentBundlePlan{}, "", err
	}
	if a.StateDir == "" {
		if err := a.resolveStateDir(); err != nil {
			return agentBundlePlan{}, "", err
		}
	}
	plan, err := planAgentBundles(decls, evidence, a.StateDir)
	if err != nil {
		return agentBundlePlan{}, "", err
	}
	manifest, cmds, err := renderAPMTemplatePlan(plan)
	if err != nil {
		return agentBundlePlan{}, "", err
	}
	var out strings.Builder
	out.WriteString(agentsMigrationMarker + "\n")
	out.WriteString(manifest)
	for _, cmd := range cmds {
		out.WriteString("# " + cmd + "\n")
	}
	for _, suppressed := range plan.Suppressed {
		out.WriteString("# suppressed: " + suppressed + "\n")
	}
	rendered := out.String()
	return plan, rendered, nil
}

type agentsMigrationTemplateIdentity struct {
	exists bool
	hash   [sha256.Size]byte
}

func inspectAgentsMigrationTemplate(path string) (agentsMigrationTemplateIdentity, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return agentsMigrationTemplateIdentity{}, nil
	}
	if err != nil {
		return agentsMigrationTemplateIdentity{}, fmt.Errorf("inspect agents template: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return agentsMigrationTemplateIdentity{}, fmt.Errorf("agents template %s is a symlink; refusing to overwrite it", path)
	}
	if !info.Mode().IsRegular() {
		return agentsMigrationTemplateIdentity{}, fmt.Errorf("agents template %s is not a regular file; refusing to overwrite it", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentsMigrationTemplateIdentity{}, fmt.Errorf("read agents template: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line == agentsMigrationMarker {
			return agentsMigrationTemplateIdentity{exists: true, hash: sha256.Sum256(raw)}, nil
		}
		break
	}
	return agentsMigrationTemplateIdentity{}, fmt.Errorf("agents template %s is not migration-owned; refusing to overwrite it", path)
}

func commitAgentMigration(templatePath, stateDir string, plan agentBundlePlan, prepared []preparedAgentBundleWrapper, identity agentsMigrationTemplateIdentity, rendered string, writeTemplate func(string, []byte) (string, error)) (string, error) {
	lock, err := config.AcquireWriteLock(templatePath)
	if err != nil {
		return "", fmt.Errorf("lock agents migration: %w", err)
	}
	defer func() { _ = lock.Close() }()
	return commitAgentMigrationLocked(templatePath, stateDir, plan, prepared, identity, rendered, writeTemplate)
}

func commitAgentMigrationLocked(templatePath, stateDir string, plan agentBundlePlan, prepared []preparedAgentBundleWrapper, identity agentsMigrationTemplateIdentity, rendered string, writeTemplate func(string, []byte) (string, error)) (string, error) {
	_ = stateDir
	_ = plan
	current, err := inspectAgentsMigrationTemplate(templatePath)
	if err != nil {
		return "", err
	}
	if current != identity {
		return "", fmt.Errorf("agents template changed during migration; retry")
	}
	if err := publishPreparedAgentBundleWrappers(prepared); err != nil {
		return "", fmt.Errorf("publish migration wrappers: %w", err)
	}
	warning, err := writeTemplate(templatePath, []byte(rendered))
	if err != nil {
		return "", fmt.Errorf("write agents migration template: %w", err)
	}
	return warning, nil
}

func writeAgentsMigrationTemplate(path string, data []byte) (string, error) {
	return writeAgentsMigrationTemplateWithSync(path, data, syncDir)
}

func writeAgentsMigrationTemplateWithSync(path string, data []byte, syncDirectory func(string) error) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".omni-agents-migrate-*")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		_ = os.Remove(temp)
	}()
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	closed = true
	if err := os.Rename(temp, path); err != nil {
		return "", err
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Sprintf("template committed but directory sync failed: %v", err), nil
	}
	return "", nil
}

func (a *App) defaultSnapshotDir() (string, error) {
	if a.ConfigPath == "" {
		return "", fmt.Errorf("no config path: pass --snapshot")
	}
	// The snapshot sits next to the real settings.json, which is often a symlink into a dotfiles repo.
	resolved, err := filepath.EvalSymlinks(a.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(resolved), snapshotGlob))
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		// An absent snapshot is a native-only migration, not an error.
		return "", nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("%d %s directories next to %s: pass --snapshot to pick one", len(matches), snapshotGlob, resolved)
	}
}
