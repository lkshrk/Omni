package app

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
)

const apmOrphanDetailLimit = 20

// apmDeployLock is the deployed-file view of apm.lock.yaml: POSIX paths relative to the deploy root, recording a bundle either as its directory or as the files under it.
type apmDeployLock struct {
	Dependencies       []apmLockDependency `yaml:"dependencies"`
	Deployments        []apmLockDeployment `yaml:"deployments"`
	LocalDeployedFiles []string            `yaml:"local_deployed_files"`
}

type apmLockDependency struct {
	DeployedFiles []string `yaml:"deployed_files"`
}

type apmLockDeployment struct {
	Value string `yaml:"value"`
}

type apmOrphanReport struct {
	Paths      []string
	McpServers []string
}

func readAPMDeployLock(manifestPath string) (apmDeployLock, error) {
	var lock apmDeployLock
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "apm.lock.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return lock, nil
		}
		return lock, fmt.Errorf("reading APM lockfile: %w", err)
	}
	if err := yaml.Unmarshal(raw, &lock); err != nil {
		return lock, fmt.Errorf("parsing APM lockfile: %w", err)
	}
	return lock, nil
}

func (l apmDeployLock) accountedPaths() []string {
	var paths []string
	add := func(raw string) {
		if trimmed := strings.Trim(strings.TrimSpace(filepath.ToSlash(raw)), "/"); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	for _, dep := range l.Dependencies {
		for _, file := range dep.DeployedFiles {
			add(file)
		}
	}
	for _, deployment := range l.Deployments {
		add(deployment.Value)
	}
	for _, file := range l.LocalDeployedFiles {
		add(file)
	}
	return paths
}

// accountedCovers — a lockfile entry accounts for an entry either way round: it may name the bundle directory omni is inspecting, or a file nested inside it.
func accountedCovers(accounted []string, relPath string) bool {
	for _, entry := range accounted {
		if entry == relPath || strings.HasPrefix(entry, relPath+"/") || strings.HasPrefix(relPath, entry+"/") {
			return true
		}
	}
	return false
}

// A root APM has deployed nothing into is hand-maintained, and reporting every file the user keeps there as an orphan would warn forever.
func accountedUnderRoot(accounted []string, root string) bool {
	for _, entry := range accounted {
		if entry == root || strings.HasPrefix(entry, root+"/") {
			return true
		}
	}
	return false
}

// apmOrphanReport lists deployed state APM has no record of, scoped to the deploy roots of the targets this host selects.
func (a *App) apmOrphanReport(cfg *config.RootConfig) (apmOrphanReport, error) {
	var report apmOrphanReport
	root, err := a.apmDeployRoot()
	if err != nil {
		return report, err
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return report, err
	}
	deployLock, err := readAPMDeployLock(manifestPath)
	if err != nil {
		return report, err
	}
	accounted := deployLock.accountedPaths()
	for _, rel := range apmUserRootsForTargets(a.apmSelectableTargets(cfg)) {
		if !accountedUnderRoot(accounted, filepath.ToSlash(rel)) {
			continue
		}
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return report, fmt.Errorf("inspecting APM deploy root %s: %w", dir, err)
		}
		for _, entry := range entries {
			if accountedCovers(accounted, path.Join(filepath.ToSlash(rel), entry.Name())) {
				continue
			}
			report.Paths = append(report.Paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(report.Paths)

	mcpLock, err := readAPMMcpLock(manifestPath)
	if err != nil {
		return report, err
	}
	locked := make(map[string]bool)
	for _, names := range mcpLock.McpTargetServers {
		for _, name := range names {
			locked[name] = true
		}
	}
	for name := range a.claudeStateForAPMAgents(a.apmMcpAgentIDs(cfg)) {
		if !locked[name] {
			report.McpServers = append(report.McpServers, name)
		}
	}
	sort.Strings(report.McpServers)
	return report, nil
}

// doctorAPMOrphans is report-only: APM offers no reclamation verb at global scope, and omni cannot tell a hand-installed primitive from one a failed install orphaned.
func (a *App) doctorAPMOrphans(result *DoctorResult, cfg *config.RootConfig) {
	if !a.AgentsEnabled(cfg) {
		return
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		result.addCheck("apm-orphans", "APM orphans", DoctorStatusWarn, "APM state could not be located", err.Error())
		return
	}
	if _, err := os.Stat(manifestPath); err != nil {
		return
	}
	report, err := a.apmOrphanReport(cfg)
	if err != nil {
		result.addCheck("apm-orphans", "APM orphans", DoctorStatusWarn, "deployed APM state could not be inspected", err.Error())
		return
	}
	if len(report.Paths) == 0 && len(report.McpServers) == 0 {
		result.addCheck("apm-orphans", "APM orphans", DoctorStatusOK, "every deployed primitive is recorded in apm.lock.yaml")
		return
	}
	details := make([]string, 0, apmOrphanDetailLimit+3)
	if len(report.Paths) > 0 {
		details = append(details, fmt.Sprintf("%d deployed path(s) missing from apm.lock.yaml:", len(report.Paths)))
		details = append(details, truncateAPMOrphans(report.Paths)...)
	}
	if len(report.McpServers) > 0 {
		details = append(details, fmt.Sprintf("%d mcp server(s) in claude's config missing from apm.lock.yaml: %s",
			len(report.McpServers), strings.Join(report.McpServers, ", ")))
	}
	details = append(details, "APM cannot see or remove these; delete them by hand once you confirm nothing else owns them")
	result.addCheck("apm-orphans", "APM orphans", DoctorStatusWarn, "this host carries deployed state APM does not track", details...)
}

func truncateAPMOrphans(paths []string) []string {
	if len(paths) <= apmOrphanDetailLimit {
		return paths
	}
	return append(paths[:apmOrphanDetailLimit:apmOrphanDetailLimit],
		fmt.Sprintf("and %d more", len(paths)-apmOrphanDetailLimit))
}
