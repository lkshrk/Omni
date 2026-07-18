package app

import (
	"fmt"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// dotsHost is the narrow set of App infrastructure the dots orchestration
// depends on: the config store, the dots-enablement/test guards, the repo-path
// resolver, and the executor factory. It is deliberately small — the dots
// service reaches only these, never the tools/agents/hosts clusters — so the
// carve is an honest seam rather than a *App back-pointer. *App satisfies it.
type dotsHost interface {
	loadConfig() (*config.RootConfig, error)
	requireDotsEnabled(*config.RootConfig) error
	dotsRepoPath() string
	requireSafeTestDotsMutation(repoPath string, entries []config.DotEntry) error
	newExecutor() executor.Executor
}

// dotsService owns the App-layer orchestration that sits on top of the carved
// internal/dots.Engine: loading config, resolving the active entry set, and
// building an engine. It exists to collapse the setup dance that every dots
// mutation repeated inline (load config, check enabled, resolve repo path,
// guard the test filesystem, build the content dir, resolve entries, construct
// the engine).
type dotsService struct {
	host dotsHost
}

func newDotsService(host dotsHost) *dotsService {
	return &dotsService{host: host}
}

// dotService returns App's dots orchestration service, lazily building it so
// tests that construct App as a struct literal (bypassing New) still work. The
// service is stateless beyond its host, so a benign race here only ever rebuilds
// an equivalent value.
func (a *App) dotService() *dotsService {
	if a.dotSvc == nil {
		a.dotSvc = newDotsService(a)
	}
	return a.dotSvc
}

// dotsPreflight is the result of the shared front half of a dots operation: the
// loaded config and the resolved repo path, computed before the caller installs
// its history/refresh defer (which needs repoPath) and before the engine is
// built (so a later engine failure still records history).
type dotsPreflight struct {
	rootCfg  *config.RootConfig
	repoPath string
}

// preflight loads config and resolves the repo path, running the dots-enabled
// and test-mutation guards. Callers invoke it first, then install their
// operation-specific history defer using the returned repoPath, then call
// engineFor to finish resolving.
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

// engineFor resolves the active entry set for the current host and builds a
// dots.Engine over it. When dryRun is false the content directory is created;
// on dry runs it is only referenced. filterActive drops inactive host variants
// (the set that mutations act on). The test-mutation guard is re-run against
// the resolved entries before the engine is constructed.
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

// hasEntry reports whether the engine resolved an entry with the given name.
func engineHasEntry(engine *dots.Engine, name string) bool {
	for _, entry := range engine.Entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}
