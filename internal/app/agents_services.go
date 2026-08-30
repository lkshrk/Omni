package app

import (
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
)

// LSP deploys only to these targets; APM records no per-entry target for either surface.
var agentsLSPTargets = []string{"claude", "copilot"}

type AgentsServiceRow struct {
	Name           string
	Detail         string
	Command        string
	URLHost        string
	Harnesses      []string
	Targets        []string
	Status         AgentsPackageStatus
	SyncActionable bool
}

type AgentsStatus struct {
	Packages       []AgentsPackageRow
	MCP            []AgentsServiceRow
	LSP            []AgentsServiceRow
	Notices        []string
	SyncActionable int
}

// AgentsStatus reports every declared, locked, and harness-deployed agent surface; it reads files only, never the APM CLI.
func (a *App) AgentsStatus() (AgentsStatus, error) {
	manifest, lock, err := readAPMWorkspace()
	if err != nil {
		return AgentsStatus{}, err
	}
	var harness harnessDeployments
	if home, err := os.UserHomeDir(); err == nil {
		harness = readHarnessDeployments(home)
	}
	resolves := memoizedLookPath()
	mcp := mcpServiceInput(manifest, lock)
	mcp.deployed, mcp.configsOnClaude, mcp.resolves = harness.MCP, harness.MCPConfigs, resolves
	lsp := lspServiceInput(manifest, lock)
	lsp.deployed, lsp.resolves = harness.LSP, resolves
	packages := joinAPMPackages(manifest, lock)
	var ownership agentsOwnershipEvidence
	if dir, err := apm.GlobalWorkspaceDir(); err == nil {
		ownership = readAPMModuleManifests(dir, packages)
	}
	mcpRows, lspRows := reconcileAgentsOwnedChildren(packages, manifest, ownership, mcp, lsp)
	syncActionable := 0
	for i := range packages {
		packages[i].SyncActionable = packages[i].Status == AgentsPackageMissing
		if packages[i].SyncActionable {
			syncActionable++
		}
	}
	for _, rows := range [][]AgentsServiceRow{mcpRows, lspRows} {
		for i := range rows {
			rows[i].SyncActionable = rows[i].Status == AgentsPackageMissing
			if rows[i].SyncActionable {
				syncActionable++
			}
		}
	}
	return AgentsStatus{
		Packages:       packages,
		MCP:            mcpRows,
		LSP:            lspRows,
		Notices:        harness.Notices,
		SyncActionable: syncActionable,
	}, nil
}

type agentsServiceInput struct {
	declared        []agentsServiceDecl
	locked          []string
	configs         map[string]apmServiceConfig
	targets         []string
	deployed        map[string][]string
	configsOnClaude map[string]harnessMCPConfig
	resolves        func(string) bool
}

type agentsServiceDecl struct {
	name    string
	detail  string
	command string
	url     string
	remote  bool
}

func mcpServiceInput(manifest apmManifest, lock apmLockfile) agentsServiceInput {
	declared := make([]agentsServiceDecl, 0, len(manifest.Dependencies.MCP))
	for _, dep := range manifest.Dependencies.MCP {
		declared = append(declared, agentsServiceDecl{
			name:    dep.Name,
			detail:  dep.Transport,
			command: dep.Command,
			url:     dep.URL,
			remote:  dep.URL != "",
		})
	}
	return agentsServiceInput{declared: declared, locked: lock.MCPServers, configs: lock.MCPConfigs, targets: manifest.Targets}
}

func lspServiceInput(manifest apmManifest, lock apmLockfile) agentsServiceInput {
	declared := make([]agentsServiceDecl, 0, len(manifest.Dependencies.LSP))
	for _, dep := range manifest.Dependencies.LSP {
		declared = append(declared, agentsServiceDecl{
			name:    dep.Name,
			detail:  path.Base(dep.Command),
			command: dep.Command,
		})
	}
	// nil = defaults (all targets); empty non-nil = declared targets, none of which LSP deploys to.
	var targets []string
	if len(manifest.Targets) > 0 {
		targets = make([]string, 0, len(agentsLSPTargets))
		for _, target := range manifest.Targets {
			if slices.Contains(agentsLSPTargets, target) {
				targets = append(targets, target)
			}
		}
	}
	return agentsServiceInput{declared: declared, locked: lock.LSPServers, configs: lock.LSPConfigs, targets: targets}
}

func joinAPMServices(in agentsServiceInput) []AgentsServiceRow {
	locked := make(map[string]bool, len(in.locked))
	for _, name := range in.locked {
		locked[name] = true
	}

	rows := make([]AgentsServiceRow, 0, len(in.declared)+len(in.locked))
	declared := make(map[string]bool, len(in.declared))
	for _, dep := range in.declared {
		if dep.name == "" {
			continue
		}
		declared[dep.name] = true
		cfg := in.configs[dep.name]
		row := AgentsServiceRow{
			Name:           dep.name,
			Detail:         dep.detail,
			Command:        path.Base(firstNonEmpty(dep.command, cfg.Command)),
			URLHost:        apmURLHost(firstNonEmpty(dep.url, cfg.URL)),
			Harnesses:      in.deployed[dep.name],
			Targets:        in.targets,
			Status:         AgentsPackageMissing,
			SyncActionable: true,
		}
		if row.Command == "." {
			row.Command = ""
		}
		if locked[dep.name] {
			row.Status = agentsServiceJoinedStatus(dep, cfg, in.configsOnClaude, in.resolves)
		}
		if row.Detail == "" {
			row.Detail = agentsServiceDetail(cfg)
		}
		rows = append(rows, row)
	}

	for _, name := range in.locked {
		if name == "" || declared[name] {
			continue
		}
		cfg := in.configs[name]
		rows = append(rows, AgentsServiceRow{
			Name:      name,
			Detail:    agentsServiceDetail(cfg),
			Command:   path.Base(cfg.Command),
			URLHost:   apmURLHost(cfg.URL),
			Harnesses: in.deployed[name],
			Targets:   in.targets,
			Status:    AgentsPackageOrphaned,
		})
	}

	// apm only ever prunes names it locked itself, so a hand-added harness entry is invisible to every apm command.
	for _, name := range slices.Sorted(maps.Keys(in.deployed)) {
		if declared[name] || locked[name] {
			continue
		}
		rows = append(rows, AgentsServiceRow{
			Name:      name,
			Detail:    "unmanaged",
			Harnesses: in.deployed[name],
			Targets:   in.deployed[name],
			Status:    AgentsPackageOrphaned,
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := agentsPackageStatusRank(rows[i].Status), agentsPackageStatusRank(rows[j].Status); ri != rj {
			return ri < rj
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func agentsServiceJoinedStatus(dep agentsServiceDecl, cfg apmServiceConfig, deployedConfigs map[string]harnessMCPConfig, resolves func(string) bool) AgentsPackageStatus {
	if deployed, ok := deployedConfigs[dep.name]; ok && agentsServiceDrifted(cfg, deployed) {
		return AgentsPackageDrifted
	}
	return agentsServiceInstalledStatus(dep, cfg, resolves)
}

// One PATH walk per command per refresh: the MCP and LSP joins routinely name the same binaries.
func memoizedLookPath() func(string) bool {
	seen := map[string]bool{}
	return func(command string) bool {
		resolved, ok := seen[command]
		if !ok {
			_, err := exec.LookPath(command)
			resolved = err == nil
			seen[command] = resolved
		}
		return resolved
	}
}

// apm neither detects nor repairs a deployed value that no longer matches the lock, so the tab is the only place it can surface.
func agentsServiceDrifted(cfg apmServiceConfig, deployed harnessMCPConfig) bool {
	command, ok := expandAPMEnv(cfg.Command)
	if !ok {
		return false
	}
	url, ok := expandAPMEnv(cfg.URL)
	if !ok {
		return false
	}
	if command != deployed.Command || url != deployed.URL {
		return true
	}
	if len(cfg.Args) != len(deployed.Args) {
		return true
	}
	for i, arg := range cfg.Args {
		expanded, ok := expandAPMEnv(arg)
		if !ok {
			return false
		}
		if expanded != deployed.Args[i] {
			return true
		}
	}
	return false
}

// The lock keeps ${env:VAR} verbatim while claude.json stores it expanded; an unset var means no comparable value, never drift.
func expandAPMEnv(value string) (string, bool) {
	const open = "${env:"
	var out strings.Builder
	for {
		start := strings.Index(value, open)
		if start < 0 {
			out.WriteString(value)
			return out.String(), true
		}
		end := strings.Index(value[start:], "}")
		if end < 0 {
			out.WriteString(value)
			return out.String(), true
		}
		name := value[start+len(open) : start+end]
		resolved, present := os.LookupEnv(name)
		if !present {
			return "", false
		}
		out.WriteString(value[:start])
		out.WriteString(resolved)
		value = value[start+end+1:]
	}
}

// APM writes an entry into the lock without ever checking that its binary exists, so the tab checks it here.
func agentsServiceInstalledStatus(dep agentsServiceDecl, cfg apmServiceConfig, resolves func(string) bool) AgentsPackageStatus {
	if dep.remote || cfg.URL != "" {
		return AgentsPackageInstalled
	}
	command := dep.command
	if command == "" {
		command = cfg.Command
	}
	if strings.TrimSpace(command) == "" {
		return AgentsPackageInstalled
	}
	if resolves == nil {
		resolves = memoizedLookPath()
	}
	if !resolves(command) {
		return AgentsPackageUnavailable
	}
	return AgentsPackageInstalled
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// Only the host is shown: a url can carry credentials or a token path.
func apmURLHost(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Hostname()
}

func agentsServiceDetail(cfg apmServiceConfig) string {
	if cfg.Transport != "" {
		return cfg.Transport
	}
	if cfg.Command != "" {
		return path.Base(cfg.Command)
	}
	return ""
}
