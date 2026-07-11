package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

var wellKnownDotPaths = map[string]string{
	"zshrc":             "~/.zshrc",
	"zprofile":          "~/.zprofile",
	"zshenv":            "~/.zshenv",
	"zlogin":            "~/.zlogin",
	"bashrc":            "~/.bashrc",
	"bash_profile":      "~/.bash_profile",
	"bash_logout":       "~/.bash_logout",
	"profile":           "~/.profile",
	"inputrc":           "~/.inputrc",
	"gitconfig":         "~/.gitconfig",
	"gitignore":         "~/.gitignore",
	"gitignore_global":  "~/.gitignore_global",
	"gitattributes":     "~/.gitattributes",
	"vimrc":             "~/.vimrc",
	"vim":               "~/.vim",
	"gvimrc":            "~/.gvimrc",
	"emacs":             "~/.emacs",
	"emacs.d":           "~/.emacs.d",
	"tmux.conf":         "~/.tmux.conf",
	"tmux":              "~/.tmux",
	"npmrc":             "~/.npmrc",
	"yarnrc":            "~/.yarnrc",
	"curlrc":            "~/.curlrc",
	"wgetrc":            "~/.wgetrc",
	"gemrc":             "~/.gemrc",
	"bundle":            "~/.bundle",
	"cargo":             "~/.cargo",
	"pyenv":             "~/.pyenv",
	"rbenv":             "~/.rbenv",
	"rustup":            "~/.rustup",
	"aws":               "~/.aws",
	"agents-skill-lock": "~/.agents/.skill-lock.json",
	"hushlogin":         "~/.hushlogin",
	"editorconfig":      "~/.editorconfig",
	"claude":            "~/.claude",
	"codex":             "~/.codex",
	"grok":              "~/.grok",
	"ssh":               "~/.ssh",
	"gnupg":             "~/.gnupg",
}

var ignoredDotCandidateNames = buildIgnoredDotCandidateNames()

func buildIgnoredDotCandidateNames() map[string]struct{} {
	names := map[string]struct{}{
		"cache":        {},
		"caches":       {},
		"local":        {},
		"logs":         {},
		"node_modules": {},
		"temp":         {},
		"tmp":          {},
		"trash":        {},
	}
	for _, name := range agentConfigDotCandidateNames() {
		names[name] = struct{}{}
	}
	return names
}

// agentConfigDotCandidateNames derives the ~/.config/<name> leaf names that
// dotfiles discovery must never surface as candidates, because they are
// machine-managed by the agents feature/CLIs (internal/app/agents_catalog.go
// supportedAgents), not user dotfiles.
//
// Only supportedAgents configDir values that live directly under ".config/"
// are included, using the exact leaf directory name — this is the only
// namespace discoverLocalConfigDotsEntries scans generically (via
// os.ReadDir(~/.config)). Home-level agent dirs (".claude", ".codex", ".grok",
// ".agents", ".gemini", etc.) are never swept up by that generic scan, so they
// are intentionally excluded here.
//
// A configDir that is itself a parent of other catalog entries (e.g.
// ".gemini" parenting ".gemini/antigravity") is excluded by construction:
// only entries with a literal "config/" prefix and no further nesting below
// the leaf are derived, and only the leaf segment is used — never a partial
// or ancestor path — so a legitimate dotfile dir sharing a name prefix can
// never be blanket-ignored.
func agentConfigDotCandidateNames() []string {
	seen := make(map[string]struct{})
	var names []string
	for _, agent := range supportedAgents {
		leaf, ok := configDotSubdirLeaf(agent.configDir)
		if !ok {
			continue
		}
		if _, dup := seen[leaf]; dup {
			continue
		}
		seen[leaf] = struct{}{}
		names = append(names, leaf)
	}
	return names
}

// configDotSubdirLeaf reports whether configDir is a direct child of
// ".config" (exactly one path segment below it, e.g. ".config/crush") and, if
// so, returns that leaf segment. Multi-level entries like ".config/foo/bar"
// are excluded: deriving only the top segment ("foo") would ignore siblings
// under ".config/foo" that the catalog does not actually own.
func configDotSubdirLeaf(configDir string) (string, bool) {
	const prefix = ".config/"
	if !strings.HasPrefix(configDir, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(configDir, prefix)
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

var claudeDotIgnorePatterns = dotAllowlistIgnorePatterns(
	"settings.json",
	"CLAUDE.md",
	"mcp.json",
	"plugins",
	"plugins/installed_plugins.json",
	"agents/",
	"hooks/",
	"scripts/",
	".omc-config.json",
	"keybindings.json",
	"statusline-command.sh",
)

var codexDotIgnorePatterns = dotAllowlistIgnorePatterns(
	"config.toml",
	"mcp.json",
	"AGENTS.md",
	"RTK.md",
	"rules/",
)

var grokDotIgnorePatterns = dotAllowlistIgnorePatterns(
	"config.toml",
	"skills/",
	"commands/",
	"hooks/",
)

var omniDotIgnorePatterns = dotAllowlistIgnorePatterns(
	"settings.json",
	"settings.d/",
)

// DiscoverDotsEntries returns initial dots candidates from the managed repo
// subtree, ~/.config, and well-known home-level dotfile paths.
func DiscoverDotsEntries(repoPath string) ([]config.DotEntry, error) {
	return discoverDotsEntries(repoPath, false)
}

func discoverDotsEntriesIncludingIgnored(repoPath string) ([]config.DotEntry, error) {
	return discoverDotsEntries(repoPath, true)
}

func discoverDotsEntries(repoPath string, includeIgnored bool) ([]config.DotEntry, error) {
	info, err := os.Lstat(repoPath)
	if err != nil {
		return nil, fmt.Errorf("repo path %q: %w", repoPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%q is a symlink; dots repo must be a real directory", repoPath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", repoPath)
	}
	seenNames := make(map[string]struct{})
	seenPaths := make(map[string]struct{})
	var out []config.DotEntry
	add := func(entry config.DotEntry) {
		if entry.Name == "" || entry.Path == "" {
			return
		}
		if ignoredDotCandidate(entry.Name) {
			if !includeIgnored {
				return
			}
			entry.Ignored = true
		}
		entry = dotEntryWithDefaults(entry)
		pathKey := strings.ToLower(filepath.ToSlash(entry.Path))
		nameKey := strings.ToLower(entry.Name)
		if _, ok := seenNames[nameKey]; ok {
			return
		}
		if _, ok := seenPaths[pathKey]; ok {
			return
		}
		seenNames[nameKey] = struct{}{}
		seenPaths[pathKey] = struct{}{}
		out = append(out, entry)
	}

	if entries, err := discoverRepoDotsEntries(repoPath, includeIgnored); err != nil {
		return nil, err
	} else {
		for _, entry := range entries {
			add(entry)
		}
	}
	if entries, err := discoverLocalConfigDotsEntries(includeIgnored); err != nil {
		return nil, err
	} else {
		for _, entry := range entries {
			add(entry)
		}
	}
	if entries, err := discoverLocalWellKnownDotsEntries(); err != nil {
		return nil, err
	} else {
		for _, entry := range entries {
			add(entry)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func dotEntryWithDefaults(entry config.DotEntry) config.DotEntry {
	switch {
	case isNamedHomeDotEntry(entry, "claude", "~/.claude"):
		entry.Ignore = appendMissingStrings(entry.Ignore, claudeDotIgnorePatterns...)
	case isNamedHomeDotEntry(entry, "codex", "~/.codex"):
		entry.Ignore = appendMissingStrings(entry.Ignore, codexDotIgnorePatterns...)
	case isNamedHomeDotEntry(entry, "grok", "~/.grok"):
		entry.Ignore = appendMissingStrings(entry.Ignore, grokDotIgnorePatterns...)
	case entry.Name == "omni" || entry.Path == "~/.config/omni":
		entry.Ignore = appendMissingStrings(entry.Ignore, omniDotIgnorePatterns...)
	}
	return entry
}

func isNamedHomeDotEntry(entry config.DotEntry, wantName, wantPath string) bool {
	name := strings.ToLower(strings.Trim(strings.TrimPrefix(entry.Name, "."), " "))
	path := strings.ToLower(filepath.ToSlash(strings.TrimSpace(entry.Path)))
	return name == wantName || path == wantPath
}

func dotAllowlistIgnorePatterns(paths ...string) []string {
	out := make([]string, 0, len(paths)+1)
	out = append(out, "*")
	for _, path := range paths {
		path = strings.TrimPrefix(filepath.ToSlash(path), "/")
		out = append(out, "!/"+path)
	}
	return out
}

func appendMissingStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}

// BootstrapDotsEntries adds discovered dots candidates to this host's machine
// group without syncing or mutating local files.
func (a *App) BootstrapDotsEntries() ([]config.DotEntry, error) {
	var added []config.DotEntry
	err := a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		rawRepo := a.effectiveSettings(rootCfg).DotsRepo
		repoPath, err := resolveRepoPath(rawRepo)
		if err != nil {
			return err
		}
		if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
			return err
		}
		candidates, err := DiscoverDotsEntries(repoPath)
		if err != nil {
			return fmt.Errorf("dots bootstrap: discover entries: %w", err)
		}
		if len(candidates) == 0 {
			return errSkipSave
		}
		group, err := ensureDestinationGroupInConfig(rootCfg, "")
		if err != nil {
			return err
		}
		for _, candidate := range untrackedDotCandidates(rootCfg, candidates) {
			group.Dots = append(group.Dots, candidate)
			added = append(added, candidate)
		}
		if len(added) == 0 {
			return errSkipSave
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// DotsAddDiscoveredEntry persists one currently discovered dotfile candidate to
// a config group without adopting, linking, or otherwise mutating local files.
func (a *App) DotsAddDiscoveredEntry(nameOrPath, groupName string) (config.DotEntry, error) {
	return a.DotsAddDiscoveredEntryContext(context.Background(), nameOrPath, groupName)
}

func (a *App) DotsAddDiscoveredEntryContext(ctx context.Context, nameOrPath, groupName string) (added config.DotEntry, err error) {
	defer func() {
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	err = a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		rawRepo := a.effectiveSettings(rootCfg).DotsRepo
		repoPath, err := resolveRepoPath(rawRepo)
		if err != nil {
			return err
		}
		if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
			return err
		}
		candidates, err := DiscoverDotsEntries(repoPath)
		if err != nil {
			return fmt.Errorf("dots add discovered: discover entries: %w", err)
		}
		candidate, ok := findDiscoveredDotCandidate(untrackedDotCandidates(rootCfg, candidates), nameOrPath)
		if !ok {
			return fmt.Errorf("dots add discovered: candidate %q not found", nameOrPath)
		}
		if groupName == "" {
			groupName = shortHostname(currentHostname())
		}
		group, err := ensureDestinationGroupInConfig(rootCfg, groupName)
		if err != nil {
			return err
		}
		group.Dots = append(group.Dots, candidate)
		added = candidate
		return nil
	})
	if err != nil {
		return config.DotEntry{}, err
	}
	return added, nil
}

func findDiscoveredDotCandidate(candidates []config.DotEntry, nameOrPath string) (config.DotEntry, bool) {
	selector := strings.ToLower(strings.TrimSpace(nameOrPath))
	selectorPath := strings.ToLower(filepath.ToSlash(normalisePath(nameOrPath)))
	for _, candidate := range candidates {
		if strings.ToLower(candidate.Name) == selector {
			return candidate, true
		}
		path := strings.ToLower(filepath.ToSlash(candidate.Path))
		if path == selector || path == selectorPath {
			return candidate, true
		}
	}
	return config.DotEntry{}, false
}

// DiscoverUntrackedDotsEntries returns repo/local/outlier dots candidates that
// are not already tracked by any configured group. It does not mutate config or
// local files; callers can present the candidates for explicit user choice.
func (a *App) DiscoverUntrackedDotsEntries() ([]config.DotEntry, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots discover: load config: %w", err)
	}
	rawRepo := a.effectiveSettings(rootCfg).DotsRepo
	repoPath, err := resolveRepoPath(rawRepo)
	if err != nil {
		return nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return nil, err
	}
	candidates, err := DiscoverDotsEntries(repoPath)
	if err != nil {
		return nil, fmt.Errorf("dots discover: discover entries: %w", err)
	}
	return untrackedDotCandidates(rootCfg, candidates), nil
}

func untrackedDotCandidates(rootCfg *config.RootConfig, candidates []config.DotEntry) []config.DotEntry {
	existingNames := make(map[string]struct{})
	existingPackages := make(map[string]struct{})
	existingPaths := make(map[string]struct{})
	for _, group := range rootCfg.Groups {
		for _, entry := range group.Dots {
			existingNames[strings.ToLower(entry.Name)] = struct{}{}
			for _, pkg := range dotEntryPackages(entry) {
				existingPackages[strings.ToLower(pkg)] = struct{}{}
			}
			existingPaths[strings.ToLower(filepath.ToSlash(entry.Path))] = struct{}{}
		}
	}
	out := make([]config.DotEntry, 0, len(candidates))
	for _, candidate := range candidates {
		nameKey := strings.ToLower(candidate.Name)
		pathKey := strings.ToLower(filepath.ToSlash(candidate.Path))
		if _, ok := existingNames[nameKey]; ok {
			continue
		}
		if _, ok := existingPackages[nameKey]; ok {
			continue
		}
		if _, ok := existingPaths[pathKey]; ok {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func discoverRepoDotsEntries(repoPath string, includeIgnored bool) ([]config.DotEntry, error) {
	dotfilesPath := filepath.Join(repoPath, dotsContentDirName)
	info, err := os.Lstat(dotfilesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading repo dotfiles dir %q: %w", dotfilesPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%q is a symlink; dots content dir must be a real directory", dotfilesPath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", dotfilesPath)
	}

	entries, err := os.ReadDir(dotfilesPath)
	if err != nil {
		return nil, fmt.Errorf("reading repo dotfiles dir %q: %w", dotfilesPath, err)
	}
	var out []config.DotEntry
	for _, entry := range entries {
		if candidate, ok := dotEntryForRepoPackage(dotfilesPath, entry.Name(), entry.IsDir(), includeIgnored); ok {
			out = append(out, candidate)
		}
	}
	return out, nil
}

func discoverLocalConfigDotsEntries(includeIgnored bool) ([]config.DotEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	configDir := filepath.Join(home, ".config")
	entries, err := os.ReadDir(configDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", configDir, err)
	}
	var out []config.DotEntry
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		ignored := ignoredDotCandidate(name)
		if ignored && !includeIgnored {
			continue
		}
		out = append(out, config.DotEntry{Name: name, Path: "~/.config/" + name, Ignored: ignored})
	}
	return out, nil
}

func discoverLocalWellKnownDotsEntries() ([]config.DotEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	names := make([]string, 0, len(wellKnownDotPaths))
	for name := range wellKnownDotPaths {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []config.DotEntry
	for _, name := range names {
		path := wellKnownDotPaths[name]
		abs := filepath.Join(home, strings.TrimPrefix(path, "~/"))
		if _, err := os.Lstat(abs); err == nil {
			out = append(out, config.DotEntry{Name: name, Path: path})
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %q: %w", abs, err)
		}
	}
	return out, nil
}

func dotEntryForRepoPackage(stowPath, name string, isDir, includeIgnored bool) (config.DotEntry, bool) {
	canon := strings.TrimPrefix(name, ".")
	if strings.Contains(canon, "@") {
		return config.DotEntry{}, false
	}
	if ignoredDotCandidate(canon) {
		if !includeIgnored || !isDir {
			return config.DotEntry{}, false
		}
		return config.DotEntry{Name: canon, Path: "~/.config/" + canon, Ignored: true}, true
	}
	if path, ok := wellKnownDotPaths[canon]; ok {
		return config.DotEntry{Name: canon, Path: path}, true
	}
	if isDir && !strings.HasPrefix(name, ".") {
		if inferred, ok := inferDotPathFromRepoPackage(stowPath, name); ok {
			entryName := canon
			if knownName, ok := wellKnownDotNameForPath(inferred); ok {
				entryName = knownName
			}
			return config.DotEntry{Name: entryName, Path: inferred}, true
		}
		return config.DotEntry{Name: name, Path: "~/.config/" + name}, true
	}
	return config.DotEntry{}, false
}

// inferDotPathFromRepoPackage derives a home target from a stow package tree when
// the package directory name does not match the mirrored home path (for example a
// legacy "skill-lock.json" package that actually contains ".agents/.skill-lock.json").
func inferDotPathFromRepoPackage(stowPath, packageName string) (string, bool) {
	pkgDir := filepath.Join(stowPath, packageName)
	info, err := os.Lstat(pkgDir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	if pathExists(filepath.Join(pkgDir, ".agents", ".skill-lock.json")) {
		return "~/.agents/.skill-lock.json", true
	}
	return "", false
}

func wellKnownDotNameForPath(path string) (string, bool) {
	norm := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	for name, candidate := range wellKnownDotPaths {
		if strings.ToLower(filepath.ToSlash(candidate)) == norm {
			return name, true
		}
	}
	return "", false
}

func ignoredDotCandidate(name string) bool {
	canon := strings.Trim(strings.ToLower(strings.TrimPrefix(name, ".")), " ")
	_, ok := ignoredDotCandidateNames[canon]
	return ok
}
