package app

import (
	"fmt"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// Deliberately narrow: the dots service reaches only these, never the tools, agents or hosts clusters.
type dotsHost interface {
	loadConfig() (*config.RootConfig, error)
	requireDotsEnabled(*config.RootConfig) error
	dotsRepoPath() string
	requireSafeTestDotsMutation(repoPath string, entries []config.DotEntry) error
	newExecutor() executor.Executor
}

// Collapses the setup dance every dots mutation repeated inline.
type dotsService struct {
	host dotsHost
}

func newDotsService(host dotsHost) *dotsService {
	return &dotsService{host: host}
}

// Lazily built so tests constructing App as a struct literal still work; a benign race only rebuilds an equivalent value.
func (a *App) dotService() *dotsService {
	if a.dotSvc == nil {
		a.dotSvc = newDotsService(a)
	}
	return a.dotSvc
}

// Computed before the caller installs its history defer and before the engine is built.
type dotsPreflight struct {
	rootCfg  *config.RootConfig
	repoPath string
}

// Callers run it first, install their history defer with repoPath, then call engineFor.
func (s *dotsService) preflight() (dotsPreflight, error) {
	rootCfg, err := s.host.loadConfig()
	if err != nil {
		return dotsPreflight{}, fmt.Errorf("dots: load config: %w", err)
	}
	if err := s.host.requireDotsEnabled(rootCfg); err != nil {
		return dotsPreflight{}, err
	}
	repoPath, err := resolveRepoPath(s.host.dotsRepoPath())
	if err != nil {
		return dotsPreflight{}, err
	}
	if err := s.host.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return dotsPreflight{}, err
	}
	return dotsPreflight{rootCfg: rootCfg, repoPath: repoPath}, nil
}

// filterActive drops inactive host variants; the test-mutation guard re-runs against the resolved entries.
func (s *dotsService) engineFor(pf dotsPreflight, dryRun, filterActive bool) (*dots.Engine, error) {
	stowPath := dotsContentPath(pf.repoPath)
	if !dryRun {
		var err error
		stowPath, err = ensureDotsContentPath(pf.repoPath)
		if err != nil {
			return nil, fmt.Errorf("dots: content dir: %w", err)
		}
	}
	groups := pf.rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(pf.rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(pf.rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	if filterActive {
		entries = filterActiveDotEntries(entries)
	}
	if err := s.host.requireSafeTestDotsMutation(pf.repoPath, entries); err != nil {
		return nil, err
	}
	engine, err := dots.NewEngine(stowPath, entries, dots.WithExecutor(s.host.newExecutor()))
	if err != nil {
		return nil, fmt.Errorf("dots: resolve entries: %w", err)
	}
	return engine, nil
}

func engineHasEntry(engine *dots.Engine, name string) bool {
	for _, entry := range engine.Entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}
