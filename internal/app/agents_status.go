package app

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
)

type AgentsPackageStatus string

const (
	AgentsPackageInstalled   AgentsPackageStatus = "installed"
	AgentsPackageDrifted     AgentsPackageStatus = "drifted"
	AgentsPackageUnavailable AgentsPackageStatus = "unavailable"
	AgentsPackageMissing     AgentsPackageStatus = "missing"
	AgentsPackageOrphaned    AgentsPackageStatus = "orphaned"
)

// AgentsStatusOrder is worst-last: it is both the row sort order and the order counts are reported in.
var AgentsStatusOrder = []AgentsPackageStatus{
	AgentsPackageInstalled,
	AgentsPackageDrifted,
	AgentsPackageUnavailable,
	AgentsPackageMissing,
	AgentsPackageOrphaned,
}

const (
	agentsUnrecognizedSource = "unrecognized apm.yml entry"
	// APM locks a filesystem-path dependency under this synthetic repo, so its name is the only joinable identity.
	apmLocalRepo = "_local"
)

func isLocalLockDep(dep apmLockDep) bool {
	repo := apmNormalizeRepo(dep.RepoURL)
	return repo == apmLocalRepo || strings.HasPrefix(repo, apmLocalRepo+"/")
}

type AgentsPackageRow struct {
	Name            string
	Source          string
	ModuleSource    string
	Ref             string
	Version         string
	LatestVersion   string
	UpdateAvailable bool
	Commit          string
	License         string
	Marketplace     string
	LocalPath       string
	Description     string
	Author          string
	Homepage        string
	Targets         []string
	DeployedFiles   int
	Status          AgentsPackageStatus
	SyncActionable  bool
	Provides        []AgentsProvidedChild
	Issues          []string
	lockEvidence    *agentsPackageLockEvidence
}

// agentsPackageLockEvidence is private ownership input from the uniquely joined lock dependency.
// Keep exact lock values here: status rendering exposes only the existing public row fields.
type agentsPackageLockEvidence struct {
	RepoURL        string
	Name           string
	VirtualPath    string
	LocalPath      string
	PackageType    string
	ResolvedCommit string
	DeployedFiles  []string
}

type AgentsProvidedChild struct {
	Kind   string
	Name   string
	Status AgentsPackageStatus
}

// ApplyAgentsOutdated decorates installed remote rows without treating local wrappers as remotely checkable.
func ApplyAgentsOutdated(rows []AgentsPackageRow, result apm.OutdatedResult) {
	for i := range rows {
		rows[i].LatestVersion = ""
		rows[i].UpdateAvailable = false
	}
	used := make([]bool, len(rows))
	for _, update := range result.Rows {
		candidates := make([]int, 0, 1)
		for i := range rows {
			if update.Package != "" && !used[i] && rows[i].Status == AgentsPackageInstalled && !rows[i].Local() &&
				apmNormalizeRepo(update.Package) == apmNormalizeRepo(rows[i].Source) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 && !strings.ContainsAny(update.Package, `/\\`) {
			for i := range rows {
				if !used[i] && rows[i].Status == AgentsPackageInstalled && !rows[i].Local() && update.Package == rows[i].Name {
					candidates = append(candidates, i)
				}
			}
		}
		if len(candidates) != 1 {
			continue
		}
		i := candidates[0]
		used[i] = true
		rows[i].UpdateAvailable = true
		rows[i].LatestVersion = update.Latest
		if rows[i].Version == "" {
			rows[i].Version = update.Current
		}
	}
}

// Local reports a filesystem-path dependency, which apm can neither update nor address by repo.
func (r AgentsPackageRow) Local() bool {
	return r.LocalPath != "" || strings.HasPrefix(strings.ToLower(r.Source), apmLocalRepo+"/") ||
		strings.HasPrefix(r.Source, "/") || strings.HasPrefix(r.Source, ".") || strings.HasPrefix(r.Source, "~")
}

type apmLockfile struct {
	Dependencies []apmLockDep                `yaml:"dependencies"`
	MCPServers   []string                    `yaml:"mcp_servers"`
	MCPConfigs   map[string]apmServiceConfig `yaml:"mcp_configs"`
	LSPServers   []string                    `yaml:"lsp_servers"`
	LSPConfigs   map[string]apmServiceConfig `yaml:"lsp_configs"`
}

type apmServiceConfig struct {
	Transport string   `yaml:"transport"`
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args"`
	URL       string   `yaml:"url"`
}

// Models the apm.lock.yaml schema of the pinned APM version asserted by requirePinnedAPM.
type apmLockDep struct {
	RepoURL               string   `yaml:"repo_url"`
	Name                  string   `yaml:"name"`
	Version               string   `yaml:"version"`
	VirtualPath           string   `yaml:"virtual_path"`
	LocalPath             string   `yaml:"local_path"`
	ResolvedCommit        string   `yaml:"resolved_commit"`
	PackageType           string   `yaml:"package_type"`
	DeclaredLicense       string   `yaml:"declared_license"`
	DiscoveredVia         string   `yaml:"discovered_via"`
	MarketplacePluginName string   `yaml:"marketplace_plugin_name"`
	TargetSubset          []string `yaml:"target_subset"`
	DeployedFiles         []string `yaml:"deployed_files"`
}

// A bare scalar dependency is APM's shorthand for a source with no options.
func (d *apmPackageDep) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var source string
		if err := node.Decode(&source); err != nil {
			return err
		}
		*d = apmPackageDep{Git: source}
		return nil
	}
	type rawPackageDep apmPackageDep
	var raw rawPackageDep
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*d = apmPackageDep(raw)
	return nil
}

func apmTrimRepo(repo string) string {
	return strings.TrimSuffix(strings.Trim(strings.TrimSpace(repo), "/"), ".git")
}

// APM rewrites repo_url in the lockfile: lowercased, host prefix and .git suffix stripped.
func apmNormalizeRepo(repo string) string {
	repo = apmTrimRepo(repo)
	if idx := strings.Index(repo, "://"); idx >= 0 {
		repo = repo[idx+3:]
		if slash := strings.Index(repo, "/"); slash >= 0 {
			repo = repo[slash+1:]
		}
	} else if at := strings.Index(repo, "@"); at >= 0 {
		if colon := strings.Index(repo[at:], ":"); colon >= 0 {
			repo = repo[at+colon+1:]
		}
	}
	return strings.ToLower(strings.Trim(repo, "/"))
}

// Splits a git spec into source and ref: after a scheme the last @ wins, otherwise the first.
func splitAPMGitRef(spec string) (base, ref string) {
	scheme := strings.Index(spec, "://")
	if scheme < 0 {
		base, ref, _ = strings.Cut(spec, "@")
		return base, ref
	}
	at := strings.LastIndex(spec, "@")
	if at <= scheme {
		return spec, ""
	}
	return spec[:at], spec[at+1:]
}

// APMPackageSpec splits a user-typed package spec into its bare owner/repo and optional ref.
func APMPackageSpec(spec string) (repo, ref string) {
	repo = strings.TrimSuffix(spec, "/")
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:", "github.com/"} {
		repo = strings.TrimPrefix(repo, prefix)
	}
	repo, ref = splitAPMGitRef(repo)
	return strings.TrimSuffix(repo, ".git"), ref
}

func apmPackageKey(repo, path string) string {
	return apmNormalizeRepo(repo) + "\x00" + strings.ToLower(strings.Trim(path, "/"))
}

// Display keeps the scheme and casing the manifest declared; only apmPackageKey is the join identity.
func apmPackageSource(repo, sub string) string {
	repo = apmTrimRepo(repo)
	if sub = strings.Trim(sub, "/"); sub != "" {
		return repo + "/" + sub
	}
	return repo
}

func apmPackageName(dep apmPackageDep) string {
	if dep.Name != "" {
		return dep.Name
	}
	base := dep.Path
	if base == "" {
		base = apmTrimRepo(dep.Git)
	}
	return path.Base(strings.Trim(base, "/"))
}

// An absent file is the empty document: apm writes neither until the first install.
func readAPMYAML(file string, out any, label string) error {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read APM %s: %w", label, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse APM %s: %w", label, err)
	}
	return nil
}

func readAPMManifest() (apmManifest, error) {
	var manifest apmManifest
	return manifest, readAPMYAML("apm.yml", &manifest, "manifest")
}

func readAPMLockfile() (apmLockfile, error) {
	var lock apmLockfile
	return lock, readAPMYAML("apm.lock.yaml", &lock, "lockfile")
}

func readAPMWorkspace() (apmManifest, apmLockfile, error) {
	manifest, err := readAPMManifest()
	if err != nil {
		return manifest, apmLockfile{}, err
	}
	lock, err := readAPMLockfile()
	return manifest, lock, err
}

func joinAPMPackages(manifest apmManifest, lock apmLockfile) []AgentsPackageRow {
	byRepoPath := make(map[string][]int, len(lock.Dependencies))
	byLocalPath := make(map[string][]int, len(lock.Dependencies))
	byName := make(map[string][]int, len(lock.Dependencies))
	for i, dep := range lock.Dependencies {
		key := apmPackageKey(dep.RepoURL, dep.VirtualPath)
		byRepoPath[key] = append(byRepoPath[key], i)
		if dep.LocalPath != "" {
			local := filepath.Clean(strings.TrimSpace(dep.LocalPath))
			byLocalPath[local] = append(byLocalPath[local], i)
		}
		if dep.Name != "" {
			name := strings.ToLower(dep.Name)
			byName[name] = append(byName[name], i)
		}
	}
	claimed := make([]bool, len(lock.Dependencies))
	// Ambiguity is reported, never guessed: a lone unclaimed candidate joins, several leave the row missing.
	claim := func(candidates []int, eligible func(apmLockDep) bool) (apmLockDep, bool) {
		match := -1
		for _, i := range candidates {
			if claimed[i] || (eligible != nil && !eligible(lock.Dependencies[i])) {
				continue
			}
			if match >= 0 {
				return apmLockDep{}, false
			}
			match = i
		}
		if match < 0 {
			return apmLockDep{}, false
		}
		claimed[match] = true
		return lock.Dependencies[match], true
	}

	rows := make([]AgentsPackageRow, 0, len(manifest.Dependencies.APM)+len(lock.Dependencies))
	install := func(row *AgentsPackageRow, dep apmLockDep) {
		row.Status = AgentsPackageInstalled
		row.SyncActionable = false
		row.Version = dep.Version
		row.Commit = dep.ResolvedCommit
		row.License = dep.DeclaredLicense
		row.Marketplace = dep.DiscoveredVia
		row.LocalPath = dep.LocalPath
		row.ModuleSource = apmPackageSource(dep.RepoURL, dep.VirtualPath)
		row.DeployedFiles = len(dep.DeployedFiles)
		row.lockEvidence = &agentsPackageLockEvidence{
			RepoURL: dep.RepoURL, Name: dep.Name, VirtualPath: dep.VirtualPath, LocalPath: dep.LocalPath,
			PackageType: dep.PackageType, ResolvedCommit: dep.ResolvedCommit,
			DeployedFiles: slices.Clone(dep.DeployedFiles),
		}
		if dep.Name != "" {
			row.Name = dep.Name
		}
		if len(row.Targets) == 0 {
			row.Targets = dep.TargetSubset
		}
	}

	// The repo/path join runs to completion first: the name join may only claim lock entries no repo/path dep owns.
	type nameJoin struct {
		row      int
		name     string
		eligible func(apmLockDep) bool
	}
	var pending []nameJoin
	for _, dep := range manifest.Dependencies.APM {
		if dep.Git == "" {
			continue
		}
		row := AgentsPackageRow{
			Name:           apmPackageName(dep),
			Source:         apmPackageSource(dep.Git, dep.Path),
			Ref:            dep.Ref,
			Targets:        dep.Targets,
			Status:         AgentsPackageMissing,
			SyncActionable: true,
		}
		lockDep, ok := claim(byRepoPath[apmPackageKey(dep.Git, dep.Path)], nil)
		if !ok && dep.Path == "" {
			lockDep, ok = claim(byLocalPath[filepath.Clean(strings.TrimSpace(dep.Git))], isLocalLockDep)
		}
		if ok {
			install(&row, lockDep)
		} else {
			// A dep carrying a repo identity must never name-join across repos; only APM's synthetic _local entries stay joinable by name.
			pending = append(pending, nameJoin{row: len(rows), name: strings.ToLower(row.Name), eligible: isLocalLockDep})
		}
		rows = append(rows, row)
	}

	for _, dep := range manifest.Dependencies.APM {
		if dep.Git != "" {
			continue
		}
		if dep.Marketplace == "" {
			name := dep.Name
			if name == "" {
				name = "(unnamed)"
			}
			rows = append(rows, AgentsPackageRow{
				Name:           name,
				Source:         agentsUnrecognizedSource,
				Targets:        dep.Targets,
				Status:         AgentsPackageMissing,
				SyncActionable: true,
			})
			continue
		}
		pending = append(pending, nameJoin{row: len(rows), name: strings.ToLower(dep.Name)})
		rows = append(rows, AgentsPackageRow{
			Name:           dep.Name,
			Source:         dep.Name + "@" + dep.Marketplace,
			Ref:            dep.Ref,
			Marketplace:    dep.Marketplace,
			Targets:        dep.Targets,
			Status:         AgentsPackageMissing,
			SyncActionable: true,
		})
	}

	for _, join := range pending {
		if lockDep, ok := claim(byName[join.name], join.eligible); ok {
			install(&rows[join.row], lockDep)
		}
	}

	for i, dep := range lock.Dependencies {
		if claimed[i] {
			continue
		}
		row := AgentsPackageRow{
			Name:          dep.Name,
			Source:        apmPackageSource(dep.RepoURL, dep.VirtualPath),
			ModuleSource:  apmPackageSource(dep.RepoURL, dep.VirtualPath),
			Version:       dep.Version,
			Commit:        dep.ResolvedCommit,
			License:       dep.DeclaredLicense,
			Marketplace:   dep.DiscoveredVia,
			LocalPath:     dep.LocalPath,
			Targets:       dep.TargetSubset,
			DeployedFiles: len(dep.DeployedFiles),
			Status:        AgentsPackageOrphaned,
		}
		if row.Name == "" {
			row.Name = path.Base(row.Source)
		}
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := agentsPackageStatusRank(rows[i].Status), agentsPackageStatusRank(rows[j].Status); ri != rj {
			return ri < rj
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Source < rows[j].Source
	})
	return rows
}

func agentsPackageStatusRank(s AgentsPackageStatus) int {
	if i := slices.Index(AgentsStatusOrder, s); i >= 0 {
		return i
	}
	return len(AgentsStatusOrder)
}
