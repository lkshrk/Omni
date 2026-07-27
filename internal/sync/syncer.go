// Package sync diffs desired config against actual state and drives installs.
package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type OpKind int

const (
	OpInstall OpKind = iota //nolint:revive // tool is missing → install
	OpAlreadyInstalled
	OpUninstall
	OpProviderUnavailable
	OpIgnored
	OpFailed
)

type SyncOp struct { //nolint:revive // name is intentional for clarity in external call sites
	Tool    provider.Tool
	Kind    OpKind
	Version string
	Err     error
}

type SyncOptions struct { //nolint:revive // name is intentional for clarity in external call sites
	DryRun   bool
	Prune    bool
	Group    string
	Provider string
	// Called from the goroutine running the operation; must be safe for concurrent use.
	Progress     func(string)
	ToolProgress func(ProgressEvent)
	IgnoreList   []string
	// Limits the run to tools that failed previously; no effect in dry-run.
	RetryFailed bool
	// Zero uses the default of 5 minutes.
	InstallTimeout time.Duration
	// Privileged actions are recorded as failures rather than executed.
	SkipPrivileged bool
	// Accepts the best weak provider-discovery match when no high-confidence one exists.
	AllowWeak bool
}

type ProgressEvent struct {
	Tool          provider.Tool
	Message       string
	TargetVersion string
	Err           error
	Done          bool
}

func (o SyncOptions) progress(msg string) {
	if o.Progress != nil {
		o.Progress(msg)
	}
}

func (o SyncOptions) toolProgress(event ProgressEvent) {
	if o.ToolProgress != nil {
		o.ToolProgress(event)
	}
}

func (o SyncOptions) installTimeout() time.Duration {
	if o.InstallTimeout > 0 {
		return o.InstallTimeout
	}
	return 5 * time.Minute
}

func isContextCancel(err error) bool {
	return errors.Is(err, context.Canceled)
}

func (s *Syncer) recordCancelledInstall(ctx context.Context, result *SyncResult, opts SyncOptions, entry config.ToolEntry, tool provider.Tool, op SyncOp) {
	op.Kind = OpFailed
	if err := s.db.ClearFailure(context.WithoutCancel(ctx), entry.Name, entry.Provider, entry.EffectivePackage()); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("failed to clear cancelled install state for %s: %v", entry.Name, err))
	}
	opts.toolProgress(ProgressEvent{Tool: tool, Message: "Cancelled installing " + entry.Name, Err: op.Err, Done: true})
	result.Ops = append(result.Ops, op)
}

type SyncResult struct { //nolint:revive // name is intentional for clarity in external call sites
	Ops             []SyncOp
	Warnings        []string
	SatisfiedGroups []string // non-active groups whose tools are all installed
}

func (r *SyncResult) Installed() []SyncOp {
	return r.filter(func(op SyncOp) bool { return op.Kind == OpInstall && op.Err == nil })
}

func (r *SyncResult) Skipped() []SyncOp {
	return r.filter(func(op SyncOp) bool { return op.Kind == OpAlreadyInstalled })
}

func (r *SyncResult) Errors() []SyncOp {
	return r.filter(func(op SyncOp) bool { return op.Err != nil })
}

func (r *SyncResult) Ignored() []SyncOp {
	return r.filter(func(op SyncOp) bool { return op.Kind == OpIgnored })
}

func (r *SyncResult) Failed() []SyncOp {
	return r.filter(func(op SyncOp) bool { return op.Kind == OpFailed })
}

func (r *SyncResult) filter(fn func(SyncOp) bool) []SyncOp {
	var out []SyncOp
	for _, op := range r.Ops {
		if fn(op) {
			out = append(out, op)
		}
	}
	return out
}

type provState struct {
	available          bool
	installed          map[string]string                  // lowercase name → version
	installedByManager map[string]provider.InstalledEntry // lowercase name → version + concrete manager
}

type Syncer struct {
	registry *provider.Registry
	db       *database.DB
}

func toolEntryLookupKeys(t config.ToolEntry) []string {
	return provider.PackageLookupKeys(t.Name, t.EffectivePackage())
}

func New(registry *provider.Registry, db *database.DB) *Syncer {
	return &Syncer{registry: registry, db: db}
}

func (s *Syncer) Sync(ctx context.Context, cfg *config.Config, opts SyncOptions) (*SyncResult, error) {
	result := &SyncResult{}

	tools := cfg.Tools
	if opts.Provider != "" {
		var filtered []config.ToolEntry
		for _, t := range tools {
			if s.providerMatchesFilter(ctx, t, opts.Provider) {
				filtered = append(filtered, t)
			}
		}
		tools = filtered
	}

	if len(opts.IgnoreList) > 0 || hasIgnoredTools(tools) {
		ignoreSet := make(map[string]struct{}, len(opts.IgnoreList))
		for _, name := range opts.IgnoreList {
			ignoreSet[name] = struct{}{}
		}
		var active []config.ToolEntry
		for _, t := range tools {
			_, listed := ignoreSet[t.Name]
			if t.Ignore || listed {
				result.Ops = append(result.Ops, SyncOp{
					Tool: provider.Tool{
						Name:     t.Name,
						Provider: t.Provider,
						Package:  t.EffectivePackage(),
						Options:  t.Options,
					},
					Kind: OpIgnored,
				})
			} else {
				active = append(active, t)
			}
		}
		tools = active
	}

	if opts.RetryFailed {
		failed, err := s.db.ListFailed(ctx)
		if err != nil {
			return nil, fmt.Errorf("loading failed tools for retry: %w", err)
		}
		failedSet := make(map[string]struct{}, len(failed))
		for _, f := range failed {
			failedSet[f.Name+"\x00"+f.Provider] = struct{}{}
		}
		var retryTools []config.ToolEntry
		for _, t := range tools {
			if _, ok := failedSet[t.Name+"\x00"+t.Provider]; ok {
				retryTools = append(retryTools, t)
			}
		}
		tools = retryTools
	}

	opts.progress("checking providers…")
	uniqueProviders := s.uniqueProviderNames(tools)
	states := make(map[string]*provState, len(uniqueProviders))
	var mu sync.Mutex

	g1a, gCtx1a := errgroup.WithContext(ctx)
	for _, name := range uniqueProviders {
		name := name
		g1a.Go(func() error {
			prov, ok := s.registry.Get(name)
			st := &provState{}
			if ok {
				avail, err := prov.Available(gCtx1a)
				if err != nil {
					return fmt.Errorf("checking provider %s: %w", name, err)
				}
				st.available = avail
			}
			mu.Lock()
			states[name] = st
			mu.Unlock()
			return nil
		})
	}
	if err := g1a.Wait(); err != nil {
		return nil, fmt.Errorf("provider availability check: %w", err)
	}

	opts.progress("reading installed packages…")
	g1b, gCtx1b := errgroup.WithContext(ctx)
	for name, st := range states {
		if !st.available {
			continue
		}
		name, st := name, st
		g1b.Go(func() error {
			prov, _ := s.registry.Get(name)

			if mbc, ok := prov.(provider.MultiManagerBulkChecker); ok {
				m, err := mbc.InstalledByManager(gCtx1b)
				if err != nil {
					return fmt.Errorf("bulk check %s (by-manager): %w", name, err)
				}
				st.installedByManager = m
			}
			if st.installedByManager == nil {
				if bc, ok := prov.(provider.BulkChecker); ok {
					m, err := bc.InstalledMap(gCtx1b)
					if err != nil {
						return fmt.Errorf("bulk check %s: %w", name, err)
					}
					st.installed = m
				}
			}
			return nil
		})
	}
	if err := g1b.Wait(); err != nil {
		return nil, fmt.Errorf("bulk status check: %w", err)
	}

	type toolDecision struct {
		entry         config.ToolEntry
		opProvider    string
		installedWith string
		installed     bool
		version       string
		noProvider    bool
	}

	decisions := make([]toolDecision, len(tools))
	for i, entry := range tools {
		opProvider := s.operationProviderName(entry)
		st, ok := states[opProvider]
		if !ok || !st.available {
			decisions[i] = toolDecision{entry: entry, opProvider: opProvider, installedWith: entry.InstallWith, noProvider: true}
			continue
		}
		prov, ok := s.registry.Get(opProvider)
		if !ok {
			decisions[i] = toolDecision{entry: entry, opProvider: opProvider, installedWith: entry.InstallWith, noProvider: true}
			continue
		}
		installedWith := installedWithForOperation(ctx, prov, opProvider, entry.InstallWith)

		keys := toolEntryLookupKeys(entry)

		var (
			installed bool
			ver       string
		)
		if st.installedByManager != nil && entry.InstallWith == "" {
			entry := provider.LookupInstalledEntry(st.installedByManager, keys)
			if entry.ConcreteManager != "" {
				installed = true
				ver = entry.Version
				installedWith = entry.ConcreteManager
			}
		} else if st.installed != nil && !(entry.InstallWith != "" && opProvider == entry.Provider) {
			ver, installed = provider.LookupString(st.installed, keys)
		} else {
			var err error
			installed, ver, err = s.isInstalled(ctx, prov, opProvider, entry)
			if err != nil {
				return nil, fmt.Errorf("checking installed status for %s/%s: %w", entry.Provider, entry.Name, err)
			}
		}

		decisions[i] = toolDecision{
			entry:         entry,
			opProvider:    opProvider,
			installedWith: installedWith,
			installed:     installed,
			version:       ver,
		}
	}

	// Batched because a bare cache update is fsync-bound; post-install upserts are not.
	var alreadyInstalledUpserts []*database.ToolCache
	for _, d := range decisions {
		tool := provider.Tool{
			Name:     d.entry.Name,
			Provider: d.entry.Provider,
			Package:  d.entry.EffectivePackage(),
			Options:  d.entry.Options,
		}

		if d.noProvider {
			result.Ops = append(result.Ops, SyncOp{Tool: tool, Kind: OpProviderUnavailable})
			continue
		}

		if d.installed {
			result.Ops = append(result.Ops, SyncOp{
				Tool:    tool,
				Kind:    OpAlreadyInstalled,
				Version: d.version,
			})
			if !opts.DryRun {
				alreadyInstalledUpserts = append(alreadyInstalledUpserts, &database.ToolCache{
					Name:          d.entry.Name,
					Provider:      d.entry.Provider,
					Package:       d.entry.EffectivePackage(),
					Installed:     true,
					InstalledWith: d.installedWith,
					Version:       toNullString(d.version),
					LastChecked:   time.Now(),
				})
			}
		} else {
			op := SyncOp{Tool: tool, Kind: OpInstall}
			if !opts.DryRun {
				opts.progress("installing " + d.entry.Name + "…")
				prov, ok := s.registry.Get(d.opProvider)
				if !ok {
					result.Ops = append(result.Ops, SyncOp{Tool: tool, Kind: OpProviderUnavailable})
					continue
				}
				if opts.SkipPrivileged {
					if plan := s.privilegePlan(ctx, prov, provider.PrivilegeActionInstall, s.operationTool(d.entry, d.opProvider)); plan.RequiresPrivilege() {
						op.Kind = OpFailed
						op.Err = fmt.Errorf("requires sudo: %s", privilegeReason(plan))
						result.Ops = append(result.Ops, op)
						if err := s.db.MarkPrivilegeRequired(ctx, d.entry.Name, d.entry.Provider, d.entry.EffectivePackage(), string(plan.Requirement), plan.Reason); err != nil {
							result.Warnings = append(result.Warnings,
								fmt.Sprintf("failed to record privilege requirement for %s: %v", d.entry.Name, err))
						}
						opts.toolProgress(ProgressEvent{Tool: tool, Message: "Admin approval needed for " + d.entry.Name, Err: op.Err, Done: true})
						continue
					}
				}
				opts.toolProgress(ProgressEvent{Tool: tool, Message: "Installing " + d.entry.Name + "…"})

				installCtx, cancel := context.WithTimeout(traceReason(ctx, "installing", d.entry.Name, d.opProvider, d.entry.InstallWith), opts.installTimeout())
				op.Err = s.install(installCtx, prov, d.opProvider, d.entry)
				cancel()

				if op.Err != nil {
					if isContextCancel(op.Err) {
						s.recordCancelledInstall(ctx, result, opts, d.entry, tool, op)
						continue
					}
					op.Kind = OpFailed
					if plan, ok := provider.ClassifyPrivilegeError(op.Err); ok && plan.RequiresPrivilege() {
						if err := s.db.MarkPrivilegeRequired(ctx, d.entry.Name, d.entry.Provider, d.entry.EffectivePackage(), string(plan.Requirement), plan.Reason); err != nil {
							result.Warnings = append(result.Warnings,
								fmt.Sprintf("failed to record privilege requirement for %s: %v", d.entry.Name, err))
						}
					}
					if err := s.db.MarkFailed(ctx, d.entry.Name, d.entry.Provider, d.entry.EffectivePackage(), op.Err.Error()); err != nil {
						result.Warnings = append(result.Warnings,
							fmt.Sprintf("failed to record install failure for %s: %v", d.entry.Name, err))
					}
					opts.toolProgress(ProgressEvent{Tool: tool, Message: "Failed installing " + d.entry.Name, Err: op.Err, Done: true})
				} else {
					installed, ver, err := s.isInstalled(ctx, prov, d.opProvider, d.entry)
					if err != nil {
						op.Err = fmt.Errorf("checking installed status after install for %s/%s: %w", d.opProvider, d.entry.Name, err)
					} else if !installed {
						op.Err = fmt.Errorf("install verification failed for %s/%s: not installed after install", d.opProvider, d.entry.Name)
					}
					if op.Err != nil {
						if isContextCancel(op.Err) {
							s.recordCancelledInstall(ctx, result, opts, d.entry, tool, op)
							continue
						}
						op.Kind = OpFailed
						if plan, ok := provider.ClassifyPrivilegeError(op.Err); ok && plan.RequiresPrivilege() {
							if err := s.db.MarkPrivilegeRequired(ctx, d.entry.Name, d.entry.Provider, d.entry.EffectivePackage(), string(plan.Requirement), plan.Reason); err != nil {
								result.Warnings = append(result.Warnings,
									fmt.Sprintf("failed to record privilege requirement for %s: %v", d.entry.Name, err))
							}
						}
						if err := s.db.MarkFailed(ctx, d.entry.Name, d.entry.Provider, d.entry.EffectivePackage(), op.Err.Error()); err != nil {
							result.Warnings = append(result.Warnings,
								fmt.Sprintf("failed to record install failure for %s: %v", d.entry.Name, err))
						}
						opts.toolProgress(ProgressEvent{Tool: tool, Message: "Failed installing " + d.entry.Name, Err: op.Err, Done: true})
						result.Ops = append(result.Ops, op)
						continue
					}
					op.Version = ver
					if err := s.db.Upsert(ctx, &database.ToolCache{
						Name:          d.entry.Name,
						Provider:      d.entry.Provider,
						Package:       d.entry.EffectivePackage(),
						Installed:     true,
						InstalledWith: d.installedWith,
						Version:       toNullString(ver),
						LastChecked:   time.Now(),
					}); err != nil {
						result.Warnings = append(result.Warnings,
							fmt.Sprintf("cache write failed for %s: %v", d.entry.Name, err))
					}
					opts.toolProgress(ProgressEvent{Tool: tool, Message: "Installed " + d.entry.Name, Done: true})
				}
			}
			result.Ops = append(result.Ops, op)
		}
	}

	// Non-fatal: the cache catches up on the next refresh.
	if err := s.db.UpsertBatch(ctx, alreadyInstalledUpserts); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("cache update batch failed: %v", err))
	}

	if opts.Prune && !opts.DryRun {
		desiredSet := make(map[string]struct{}, len(cfg.Tools))
		for _, t := range cfg.Tools {
			desiredSet[t.Name+"\x00"+t.Provider+"\x00"+t.EffectivePackage()] = struct{}{}
		}
		cached, err := s.db.List(ctx)
		if err != nil {
			return result, fmt.Errorf("listing cached tools for prune: %w", err)
		}
		for _, c := range cached {
			if _, ok := desiredSet[c.Name+"\x00"+c.Provider+"\x00"+c.Package]; !ok {
				t := provider.Tool{Name: c.Name, Provider: c.Provider, Package: c.Package}
				if !c.Installed {
					if err := s.db.Delete(ctx, c.Name, c.Provider, c.Package); err != nil {
						result.Warnings = append(result.Warnings,
							fmt.Sprintf("cache delete failed for %s: %v", c.Name, err))
					}
					result.Ops = append(result.Ops, SyncOp{
						Tool: t,
						Kind: OpUninstall,
					})
					continue
				}
				prov, opProvider, manager, ok := s.lifecycleProvider(c.Provider, c.InstalledWith)
				if !ok {
					continue
				}
				t.Provider = opProvider
				opts.progress("removing " + c.Name + "…")
				if opts.SkipPrivileged {
					if plan := s.privilegePlan(ctx, prov, provider.PrivilegeActionUninstall, t); plan.RequiresPrivilege() {
						uninstallErr := fmt.Errorf("requires sudo: %s", privilegeReason(plan))
						if err := s.db.MarkPrivilegeRequired(ctx, c.Name, c.Provider, c.Package, string(plan.Requirement), plan.Reason); err != nil {
							result.Warnings = append(result.Warnings,
								fmt.Sprintf("failed to record privilege requirement for %s: %v", c.Name, err))
						}
						result.Ops = append(result.Ops, SyncOp{
							Tool: t,
							Kind: OpUninstall,
							Err:  uninstallErr,
						})
						continue
					}
				}
				uninstallErr := uninstall(traceReason(ctx, "removing", c.Name, opProvider, manager), prov, t, manager)
				if plan, ok := provider.ClassifyPrivilegeError(uninstallErr); ok && plan.RequiresPrivilege() {
					if err := s.db.MarkPrivilegeRequired(ctx, c.Name, c.Provider, c.Package, string(plan.Requirement), plan.Reason); err != nil {
						result.Warnings = append(result.Warnings,
							fmt.Sprintf("failed to record privilege requirement for %s: %v", c.Name, err))
					}
				}
				if uninstallErr == nil {
					if err := s.db.Delete(ctx, c.Name, c.Provider, c.Package); err != nil {
						result.Warnings = append(result.Warnings,
							fmt.Sprintf("cache delete failed for %s: %v", c.Name, err))
					}
				}
				result.Ops = append(result.Ops, SyncOp{
					Tool: t,
					Kind: OpUninstall,
					Err:  uninstallErr,
				})
			}
		}
	}

	return result, nil
}

func (s *Syncer) lifecycleProvider(providerName, installedWith string) (provider.Provider, string, string, bool) {
	if installedWith != "" {
		if owner, ok := s.registry.Get(installedWith); ok {
			return owner, installedWith, "", true
		}
	}
	prov, ok := s.registry.Get(providerName)
	return prov, providerName, installedWith, ok
}

func uninstall(ctx context.Context, prov provider.Provider, tool provider.Tool, manager string) error {
	if manager != "" && manager != tool.Provider {
		if uninstaller, ok := prov.(provider.ManagerUninstaller); ok {
			return uninstaller.UninstallFrom(ctx, tool, manager)
		}
	}
	return prov.Uninstall(ctx, tool)
}

func (s *Syncer) privilegePlan(ctx context.Context, prov provider.Provider, action provider.PrivilegeAction, tool provider.Tool) provider.PrivilegePlan {
	if planner, ok := prov.(provider.PrivilegePlanner); ok {
		plan, err := planner.PrivilegePlan(ctx, action, tool)
		if err == nil {
			return plan
		}
	}
	return provider.PrivilegePlan{}
}

func privilegeReason(plan provider.PrivilegePlan) string {
	if plan.Reason != "" {
		return plan.Reason
	}
	return "package manager needs privileged access"
}

func hasIgnoredTools(tools []config.ToolEntry) bool {
	for _, t := range tools {
		if t.Ignore {
			return true
		}
	}
	return false
}

func (s *Syncer) operationProviderName(t config.ToolEntry) string {
	if t.InstallWith != "" {
		if _, ok := s.registry.Get(t.InstallWith); ok {
			return t.InstallWith
		}
	}
	return t.Provider
}

func (s *Syncer) providerMatchesFilter(ctx context.Context, t config.ToolEntry, providerName string) bool {
	if providerName == "" {
		return true
	}
	for _, name := range []string{t.Provider, t.InstallWith, s.operationProviderName(t)} {
		if s.providerNameMatchesFilter(name, providerName) {
			return true
		}
	}
	opProvider := s.operationProviderName(t)
	prov, ok := s.registry.Get(opProvider)
	if !ok {
		return false
	}
	cr, ok := prov.(provider.ConcreteResolver)
	if !ok {
		return false
	}
	concrete, err := cr.ResolvedName(ctx)
	return err == nil && s.providerNameMatchesFilter(concrete, providerName)
}

func (s *Syncer) providerNameMatchesFilter(providerName, filter string) bool {
	if providerName == "" || filter == "" {
		return false
	}
	if providerName == filter {
		return true
	}
	if meta, ok := s.registry.Metadata(providerName); ok && meta.Ecosystem == filter {
		return true
	}
	ecosystem, ok := provider.BuiltinEcosystemFor(providerName)
	return ok && ecosystem == filter
}

func (s *Syncer) operationTool(t config.ToolEntry, opProvider string) provider.Tool {
	return provider.Tool{
		Name:     t.Name,
		Provider: opProvider,
		Package:  t.EffectivePackage(),
		Options:  t.Options,
	}
}

func (s *Syncer) isInstalled(ctx context.Context, prov provider.Provider, opProvider string, entry config.ToolEntry) (bool, string, error) {
	tool := s.operationTool(entry, opProvider)
	if entry.InstallWith != "" && opProvider == entry.Provider {
		if checker, ok := prov.(provider.ManagerInstalledChecker); ok {
			return checker.IsInstalledWithManager(ctx, tool, entry.InstallWith)
		}
	}
	return prov.IsInstalled(ctx, tool)
}

func installedWithForOperation(ctx context.Context, prov provider.Provider, opProvider, installWith string) string {
	if installWith != "" {
		return installWith
	}
	if cr, ok := prov.(provider.ConcreteResolver); ok {
		concrete, err := cr.ResolvedName(ctx)
		if err == nil {
			return concrete
		}
		return ""
	}
	return opProvider
}

func (s *Syncer) install(ctx context.Context, prov provider.Provider, opProvider string, entry config.ToolEntry) error {
	tool := s.operationTool(entry, opProvider)
	if entry.InstallWith != "" && opProvider == entry.Provider {
		if installer, ok := prov.(provider.ManagerInstaller); ok {
			return installer.InstallWithManager(ctx, tool, entry.InstallWith)
		}
	}
	return prov.Install(ctx, tool)
}

func traceReason(ctx context.Context, action, name, providerName, detail string) context.Context {
	providerName = fmt.Sprintf("%s/%s", providerName, detail)
	providerName = strings.Trim(providerName, "/")
	if providerName == "" {
		return executor.WithTraceReason(ctx, fmt.Sprintf("%s %s", action, name))
	}
	return executor.WithTraceReason(ctx, fmt.Sprintf("%s %s (%s)", action, name, providerName))
}

func (s *Syncer) uniqueProviderNames(tools []config.ToolEntry) []string {
	seen := make(map[string]struct{}, len(tools))
	var out []string
	for _, t := range tools {
		providerName := s.operationProviderName(t)
		if _, ok := seen[providerName]; !ok {
			seen[providerName] = struct{}{}
			out = append(out, providerName)
		}
	}
	return out
}

func toNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
