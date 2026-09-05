package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

const (
	adoptShapeBare             = "bare"
	adoptShapeDotfilesUnlinked = "dotfiles-unlinked"
	adoptShapeDotfilesLinked   = "dotfiles-linked"
	adoptShapeForeign          = "foreign"

	adoptActionWriteTemplate  = "would write the template"
	adoptActionLinkRepo       = "would link the repo template"
	adoptActionEmitManifest   = "would emit a manifest to commit"
	adoptActionUpdateTemplate = "would update the template"
	adoptActionRefuse         = "would refuse: the template is not migration-owned"

	adoptCoverageTitle     = "Coverage incomplete (adoption would be under-reported):"
	adoptAppendedTitle     = "Manifest gains:"
	adoptUnionedTitle      = "Manifest target unions:"
	adoptRejectedTitle     = "Manifest conflicts (not adopted):"
	adoptDecisionsTitle    = "Decisions left to the operator:"
	adoptIgnoredTitle      = "Ignored via agents.ignored:"
	adoptLegacyDarwinTitle = "Legacy Darwin template (adoption would adopt it; this preview does not):"

	adoptFooter = "Preview only: omni agents adopt never writes."
)

// agentsAdoptTemplate is the host template's shape as read, never as changed.
type agentsAdoptTemplate struct {
	Path       string
	Shape      string
	Action     string
	RepoPath   string
	LinkTarget string
	Existing   []byte
}

// AgentsAdoptPreview reports what adopting a host onto APM would do; building it changes nothing.
type AgentsAdoptPreview struct {
	Host         string
	Template     agentsAdoptTemplate
	LegacyDarwin string
	Merge        manifestMergeReport
	Replaced     []agentDisposition
	Retained     []agentDisposition
	Managed      []agentDisposition
	Ignored      []agentDisposition
	Incomplete   []nativeClientCoverage
}

// AgentsAdopt previews adopting this host onto APM. It has no write path and no apply mode.
func (a *App) AgentsAdopt(ctx context.Context, host string) (string, error) {
	preview, err := a.BuildAgentsAdoptPreview(ctx, host)
	if err != nil {
		return "", err
	}
	return preview.Render(), nil
}

// BuildAgentsAdoptPreview reads the host template, native inventory and APM lockfile without mutating any of them.
func (a *App) BuildAgentsAdoptPreview(ctx context.Context, host string) (AgentsAdoptPreview, error) {
	if strings.TrimSpace(host) == "" {
		return AgentsAdoptPreview{}, fmt.Errorf("host is required")
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return AgentsAdoptPreview{}, err
	}
	path, err := agentsAdoptTemplatePath()
	if err != nil {
		return AgentsAdoptPreview{}, err
	}
	template, err := a.inspectAgentsAdoptTemplate(cfg, host, path)
	if err != nil {
		return AgentsAdoptPreview{}, err
	}
	managed, err := loadAPMManagedIndex()
	if err != nil {
		return AgentsAdoptPreview{}, err
	}
	inventory := a.gatherNativeAgents(ctx)
	preview := AgentsAdoptPreview{Host: host, Template: template, Incomplete: inventory.Incomplete()}
	if legacy, found := legacyDarwinAgentsTemplate(); found {
		preview.LegacyDarwin = legacy
	}

	entries := adoptAgentIgnoreEntries(cfg, host)
	var adoptable []agentDisposition
	for _, disposition := range subtractAPMManaged(resolveAgentDispositions(inventory.Observations), managed) {
		switch disposition.Action {
		case agentActionImport, agentActionSuppress:
			if slices.ContainsFunc(entries, func(entry config.AgentIgnoreEntry) bool {
				return agentIgnoreMatches(entry, disposition.Observation)
			}) {
				preview.Ignored = append(preview.Ignored, disposition)
				continue
			}
			preview.Replaced = append(preview.Replaced, disposition)
			adoptable = append(adoptable, disposition)
		case agentActionRetain:
			preview.Retained = append(preview.Retained, disposition)
		case agentActionManaged:
			preview.Managed = append(preview.Managed, disposition)
		}
	}

	candidates, err := agentsAdoptCandidates(adoptable)
	if err != nil {
		return AgentsAdoptPreview{}, err
	}
	_, report, err := mergeAgentsManifest(template.Existing, candidates)
	if err != nil {
		return AgentsAdoptPreview{}, err
	}
	preview.Merge = report
	return preview, nil
}

// agentsAdoptTemplatePath resolves the host template purely; AgentsTemplatePath migrates the Darwin legacy copy as a side effect.
func agentsAdoptTemplatePath() (string, error) {
	base, err := config.DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "apm.yml"), nil
}

func legacyDarwinAgentsTemplate() (string, bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	path := filepath.Join(home, "Library", "Application Support", "omni", "apm.yml")
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func (a *App) inspectAgentsAdoptTemplate(cfg *config.RootConfig, host, path string) (agentsAdoptTemplate, error) {
	out := agentsAdoptTemplate{Path: path}
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return a.absentAgentsAdoptTemplate(cfg, host, out)
	case err != nil:
		return out, fmt.Errorf("inspect agents template: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		out.Shape, out.Action = adoptShapeDotfilesLinked, adoptActionEmitManifest
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return out, fmt.Errorf("resolve agents template link: %w", err)
		}
		out.LinkTarget = target
		if out.Existing, err = os.ReadFile(path); err != nil {
			return out, fmt.Errorf("read agents template: %w", err)
		}
		return out, nil
	case !info.Mode().IsRegular():
		out.Shape, out.Action = adoptShapeForeign, adoptActionRefuse
		return out, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read agents template: %w", err)
	}
	if !migrationOwnedTemplate(raw) {
		out.Shape, out.Action = adoptShapeForeign, adoptActionRefuse
		return out, nil
	}
	out.Shape, out.Action, out.Existing = adoptShapeBare, adoptActionUpdateTemplate, raw
	return out, nil
}

func (a *App) absentAgentsAdoptTemplate(cfg *config.RootConfig, host string, out agentsAdoptTemplate) (agentsAdoptTemplate, error) {
	repo, found, err := a.agentsRepoTemplatePath(cfg, host, out.Path)
	if err != nil {
		return out, err
	}
	if !found {
		out.Shape, out.Action = adoptShapeBare, adoptActionWriteTemplate
		return out, nil
	}
	out.Shape, out.Action, out.RepoPath = adoptShapeDotfilesUnlinked, adoptActionLinkRepo, repo
	if out.Existing, err = os.ReadFile(repo); err != nil {
		return out, fmt.Errorf("read repo agents template: %w", err)
	}
	return out, nil
}

func migrationOwnedTemplate(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return line == agentsMigrationMarker
	}
	return false
}

// agentsRepoTemplatePath finds the dots-repo source the template would be linked to for the named host.
func (a *App) agentsRepoTemplatePath(cfg *config.RootConfig, host, templatePath string) (string, bool, error) {
	raw := strings.TrimSpace(a.effectiveSettings(cfg).DotsRepo)
	if raw == "" {
		return "", false, nil
	}
	repoPath, err := resolveRepoPath(raw)
	if err != nil {
		return "", false, err
	}
	group := machineGroupName(host)
	groups := cfg.Groups
	if effective, _, ok := effectiveHostGroups(cfg, groups, host); ok {
		groups = effective
	}
	stowPath := dotsContentPath(repoPath)
	for _, entry := range collectDots(cfg, groups) {
		covers, err := dotEntryCoversPath(entry, templatePath)
		if err != nil {
			return "", false, err
		}
		if !covers {
			continue
		}
		source, err := stowPackagePath(stowPath, entry.PackageForHost(group), templatePath)
		if err != nil {
			return "", false, err
		}
		if info, err := os.Lstat(source); err == nil && info.Mode().IsRegular() {
			return source, true, nil
		}
	}
	return "", false, nil
}

func dotEntryCoversPath(entry config.DotEntry, path string) (bool, error) {
	expanded, err := dots.ExpandPath(entry.Path)
	if err != nil {
		return false, fmt.Errorf("dots entry %q: expand path: %w", entry.Name, err)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return false, fmt.Errorf("dots entry %q: %w", entry.Name, err)
	}
	abs = filepath.Clean(abs)
	return path == abs || strings.HasPrefix(path, abs+string(filepath.Separator)), nil
}

func adoptAgentIgnoreEntries(cfg *config.RootConfig, host string) []config.AgentIgnoreEntry {
	if cfg == nil || cfg.Agents == nil {
		return nil
	}
	short := shortHostname(host)
	var out []config.AgentIgnoreEntry
	for _, entry := range cfg.Agents.Ignored {
		declared := strings.TrimSpace(entry.Host)
		if strings.EqualFold(declared, host) || strings.EqualFold(declared, short) {
			out = append(out, entry)
		}
	}
	return out
}

// agentsAdoptCandidates renders the adoptable dispositions as manifest candidates with absolute package paths,
// because the merger keys `path:` entries with filepath.Clean alone and would not collapse a relative spelling.
func agentsAdoptCandidates(dispositions []agentDisposition) (manifestCandidates, error) {
	render, err := buildAPMRender(nativeAgentDecls(dispositions), nil)
	if err != nil {
		return manifestCandidates{}, err
	}
	var out manifestCandidates
	for _, dep := range render.Manifest.Dependencies.APM {
		if dep.Path != "" {
			abs, err := filepath.Abs(dep.Path)
			if err != nil {
				return manifestCandidates{}, fmt.Errorf("resolve package path %q: %w", dep.Path, err)
			}
			dep.Path = abs
		}
		out.Packages = append(out.Packages, dep)
	}
	for _, dep := range render.Manifest.Dependencies.MCP {
		out.MCP = append(out.MCP, manifestMCPCandidate{Dep: dep, Reach: render.MCPReach[dep.Name]})
	}
	out.LSP = render.Manifest.Dependencies.LSP
	for _, command := range render.Commands {
		if decl, ok := parseMarketplaceDecl("# " + command); ok {
			out.Marketplaces = append(out.Marketplaces, decl)
		}
	}
	return out, nil
}

func (p AgentsAdoptPreview) Render() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Host: %s\n", p.Host)
	fmt.Fprintf(&out, "Template: %s (%s)\n", p.Template.Path, p.Template.Shape)
	if p.Template.RepoPath != "" {
		fmt.Fprintf(&out, "Repo template: %s\n", p.Template.RepoPath)
	}
	if p.Template.LinkTarget != "" {
		fmt.Fprintf(&out, "Link target: %s\n", p.Template.LinkTarget)
	}
	fmt.Fprintf(&out, "Action: %s\n", p.Template.Action)
	if p.LegacyDarwin != "" {
		out.WriteString(adoptLegacyDarwinTitle + "\n  " + p.LegacyDarwin + "\n")
	}
	coverage := make([]string, 0, len(p.Incomplete))
	for _, client := range p.Incomplete {
		coverage = append(coverage, client.Client+": "+driftCoverageReason(client))
	}
	writeAdoptRows(&out, adoptCoverageTitle, coverage)

	writeAdoptRows(&out, adoptAppendedTitle, adoptMergeRows(p.Merge.Appended))
	writeAdoptRows(&out, adoptUnionedTitle, adoptMergeRows(p.Merge.Unioned))
	writeAdoptRows(&out, adoptRejectedTitle, adoptRejectionRows(p.Merge.Rejected))
	writeAdoptRows(&out, adoptDecisionsTitle, slices.Clone(p.Merge.Decisions))

	writeMigrationSection(&out, replacedSectionTitle, p.Replaced, func(d agentDisposition) string {
		return migrationRow(d, replacedEvidence(d))
	})
	writeMigrationSection(&out, retainedSectionTitle, p.Retained, func(d agentDisposition) string {
		return migrationRow(d, d.Reason)
	})
	writeMigrationSection(&out, managedSectionTitle, p.Managed, func(d agentDisposition) string {
		return migrationRow(d, "")
	})
	writeMigrationSection(&out, adoptIgnoredTitle, p.Ignored, func(d agentDisposition) string {
		return migrationRow(d, "")
	})
	out.WriteString(adoptFooter + "\n")
	return out.String()
}

func adoptMergeRows(entries []manifestMergeEntry) []string {
	rows := make([]string, 0, len(entries))
	for _, entry := range entries {
		row := entry.Kind + "  " + entry.Identity
		if len(entry.Targets) > 0 {
			row += "  targets: " + strings.Join(entry.Targets, ", ")
		}
		rows = append(rows, row)
	}
	return rows
}

func adoptRejectionRows(rejections []manifestMergeRejection) []string {
	rows := make([]string, 0, len(rejections))
	for _, rejection := range rejections {
		row := rejection.Kind
		if rejection.Identity != "" {
			row += "  " + rejection.Identity
		}
		rows = append(rows, row+"  "+rejection.Reason)
	}
	return rows
}

func writeAdoptRows(out *strings.Builder, title string, rows []string) {
	if len(rows) == 0 {
		return
	}
	sort.Strings(rows)
	out.WriteString(title + "\n")
	for _, row := range slices.Compact(rows) {
		out.WriteString("  " + row + "\n")
	}
}
