package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
)

const AgentsRegistryEmptyNotice = "no marketplace cache — run 'apm marketplace update'"

type AgentsRegistryEntry struct {
	Name        string
	Marketplace string
	Description string
	Version     string
	Author      string
	Installed   bool
}

// Spec is the identifier apm install accepts; the installed row is later keyed by owner/repo instead.
func (e AgentsRegistryEntry) Spec() string { return e.Name + "@" + e.Marketplace }

type apmMarketplaceIndex struct {
	Marketplaces []apmMarketplaceRef `json:"marketplaces"`
}

type apmMarketplaceRef struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

func (r apmMarketplaceRef) repoKey() string {
	if r.Owner == "" || r.Repo == "" {
		return ""
	}
	return apmNormalizeRepo(r.Owner + "/" + r.Repo)
}

type apmMarketplaceCatalog struct {
	Name    string             `json:"name"`
	Owner   apmCatalogAuthor   `json:"owner"`
	Plugins []apmCatalogPlugin `json:"plugins"`
}

type apmCatalogPlugin struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Version     string           `json:"version"`
	Author      apmCatalogAuthor `json:"author"`
	Source      apmCatalogSource `json:"source"`
}

// A plugin's source is either a relative path inside the marketplace repo or an object naming its own repo.
type apmCatalogSource struct {
	Repo string
	Path string
}

func (s *apmCatalogSource) UnmarshalJSON(data []byte) error {
	var relative string
	if err := json.Unmarshal(data, &relative); err == nil {
		s.Path = relative
		return nil
	}
	var object struct {
		Repo string `json:"repo"`
		URL  string `json:"url"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return nil
	}
	s.Path = object.Path
	s.Repo = object.Repo
	if s.Repo == "" {
		s.Repo = object.URL
	}
	return nil
}

// A plugin that is a whole repo is identified by that repo; one carved out of a subdirectory shares
// its repo with siblings, so it still needs its name to match a locked entry.
func (s apmCatalogSource) ownRepo() string {
	if s.Repo == "" || s.Path != "" {
		return ""
	}
	return apmNormalizeRepo(s.Repo)
}

// Real catalogs spell an author either as a bare name or as an object; an unmodelled shape must
// degrade to no name rather than reject the whole marketplace.
type apmCatalogAuthor struct{ Name string }

func (a *apmCatalogAuthor) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		a.Name = name
		return nil
	}
	var object struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &object); err == nil {
		a.Name = object.Name
	}
	return nil
}

// AgentsRegistry lists every plugin the registered marketplaces offer, read from apm's own cache.
func (a *App) AgentsRegistry() ([]AgentsRegistryEntry, []string, error) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "marketplaces.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []string{AgentsRegistryEmptyNotice}, nil
		}
		return nil, nil, err
	}
	var index apmMarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, nil, err
	}

	_, lock, err := readAPMWorkspace()
	if err != nil {
		return nil, nil, err
	}
	installed, repos := installedMarketplacePlugins(lock)

	catalogs := readMarketplaceCatalogs(filepath.Join(dir, "cache", "marketplace"))
	var entries []AgentsRegistryEntry
	var notices []string
	for _, ref := range index.Marketplaces {
		parsed, ok := catalogs[strings.ToLower(ref.Name)]
		if !ok {
			notices = append(notices, AgentsRegistryEmptyNotice)
			continue
		}
		// The real fleet lock leaves discovered_via null, so a package installed straight from the
		// marketplace repo is matched by that repo plus its own locked name, never by repo alone.
		repoNames := repos[ref.repoKey()]
		for _, plugin := range parsed.Plugins {
			author := plugin.Author.Name
			if author == "" {
				author = parsed.Owner.Name
			}
			entries = append(entries, AgentsRegistryEntry{
				Name:        plugin.Name,
				Marketplace: ref.Name,
				Description: plugin.Description,
				Version:     plugin.Version,
				Author:      author,
				Installed: installed[strings.ToLower(plugin.Name)+"\x00"+strings.ToLower(ref.Name)] ||
					repoNames[strings.ToLower(plugin.Name)] ||
					len(repos[plugin.Source.ownRepo()]) > 0,
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Marketplace < entries[j].Marketplace
	})
	if len(entries) == 0 && len(notices) == 0 {
		notices = append(notices, AgentsRegistryEmptyNotice)
	}
	return entries, dedupeNotices(notices), nil
}

// APM files a catalog under the registered marketplace name, except a url marketplace, which it hashes;
// every catalog names itself, so an unmatched filename is resolved by reading that name back.
func readMarketplaceCatalogs(dir string) map[string]apmMarketplaceCatalog {
	out := map[string]apmMarketplaceCatalog{}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return out
	}
	byName := map[string]apmMarketplaceCatalog{}
	for _, file := range files {
		base := filepath.Base(file)
		if strings.HasSuffix(base, ".meta.json") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var parsed apmMarketplaceCatalog
		if err := json.Unmarshal(data, &parsed); err != nil || len(parsed.Plugins) == 0 {
			continue
		}
		if parsed.Name != "" {
			byName[strings.ToLower(parsed.Name)] = parsed
		}
		out[strings.ToLower(strings.TrimSuffix(base, ".json"))] = parsed
	}
	// An exact filename match outranks a self-declared name, so a fork cannot shadow the catalog it forked.
	for name, parsed := range byName {
		if _, ok := out[name]; !ok {
			out[name] = parsed
		}
	}
	return out
}

func installedMarketplacePlugins(lock apmLockfile) (map[string]bool, map[string]map[string]bool) {
	byPlugin := map[string]bool{}
	byRepo := map[string]map[string]bool{}
	for _, dep := range lock.Dependencies {
		if dep.MarketplacePluginName != "" && dep.DiscoveredVia != "" {
			byPlugin[strings.ToLower(dep.MarketplacePluginName)+"\x00"+strings.ToLower(dep.DiscoveredVia)] = true
		}
		if dep.RepoURL == "" || dep.Name == "" {
			continue
		}
		repo := apmNormalizeRepo(dep.RepoURL)
		if byRepo[repo] == nil {
			byRepo[repo] = map[string]bool{}
		}
		byRepo[repo][strings.ToLower(dep.Name)] = true
	}
	return byPlugin, byRepo
}

func dedupeNotices(notices []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(notices))
	for _, notice := range notices {
		if !seen[notice] {
			seen[notice] = true
			out = append(out, notice)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
