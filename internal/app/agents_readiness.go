package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

type AgentsReadinessState string

const (
	AgentsReadinessReady          AgentsReadinessState = "ready"
	AgentsReadinessEmpty          AgentsReadinessState = "empty"
	AgentsReadinessTemplateOnly   AgentsReadinessState = "template-only"
	AgentsReadinessLiveIncomplete AgentsReadinessState = "live-incomplete"
	AgentsReadinessLockOnly       AgentsReadinessState = "lock-only"
	AgentsReadinessInvalid        AgentsReadinessState = "invalid"
)

type AgentsReadinessCTA string

const (
	AgentsCTANone    AgentsReadinessCTA = ""
	AgentsCTAMigrate AgentsReadinessCTA = "migrate"
	AgentsCTASync    AgentsReadinessCTA = "sync"
	AgentsCTARetry   AgentsReadinessCTA = "retry"
)

type AgentsReadiness struct {
	State        AgentsReadinessState
	CTA          AgentsReadinessCTA
	Details      []string
	TemplatePath string
	ManifestPath string
	LockPath     string
}

type AgentsOnboardingResult struct {
	Readiness AgentsReadiness
}

// EnsureAgentsReady repairs APM and completes any safe, unambiguous onboarding work.
func (a *App) EnsureAgentsReady(ctx context.Context, host string) (AgentsOnboardingResult, error) {
	if _, err := a.FixMissingAPM(ctx, false); err != nil {
		return AgentsOnboardingResult{}, err
	}
	a.seedPinnedAPM(apmVersionPin, nil)
	return a.CompleteAgentsOnboarding(ctx, host)
}

// CompleteAgentsOnboarding advances the current APM workspace to Ready without
// mutating ambiguous or invalid state.
func (a *App) CompleteAgentsOnboarding(ctx context.Context, host string) (AgentsOnboardingResult, error) {
	readiness, err := a.AgentsReadiness(ctx)
	if err != nil {
		return AgentsOnboardingResult{}, err
	}
	result := AgentsOnboardingResult{Readiness: readiness}
	var legacySnapshot string
	switch readiness.State {
	case AgentsReadinessReady:
		return result, nil
	case AgentsReadinessLockOnly, AgentsReadinessInvalid:
		return result, nil
	case AgentsReadinessEmpty:
		result, legacySnapshot, err = a.prepareAgentsOnboarding(ctx, host)
		if err != nil || result.Readiness.State == AgentsReadinessEmpty {
			return result, err
		}
	}

	if result.Readiness.State == AgentsReadinessTemplateOnly || result.Readiness.State == AgentsReadinessLiveIncomplete {
		if _, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{ForceTemplate: true}); err != nil {
			return result, err
		}
	}
	result.Readiness, err = a.AgentsReadiness(ctx)
	if err != nil {
		return result, err
	}
	if result.Readiness.State != AgentsReadinessReady {
		result.Readiness.Details = append(result.Readiness.Details, "APM onboarding did not produce a complete manifest and lockfile")
		return result, nil
	}
	if legacySnapshot != "" {
		if err := config.CleanupLegacyAgentsConfigFromSnapshot(a.ConfigPath, legacySnapshot); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (a *App) prepareAgentsOnboarding(ctx context.Context, host string) (AgentsOnboardingResult, string, error) {
	hasLegacy, err := config.HasRemovedAgentConfig(a.ConfigPath)
	if err != nil {
		return AgentsOnboardingResult{}, "", err
	}
	if hasLegacy {
		snapshot, err := config.CaptureLegacyAgentsSnapshot(a.ConfigPath)
		if err != nil {
			return AgentsOnboardingResult{}, "", err
		}
		result, err := a.agentsPrepareOnboarding(ctx, host, snapshot)
		return result, snapshot, err
	}
	result, err := a.stageEmptyAgentsOnboarding(ctx)
	return result, "", err
}

func (a *App) stageEmptyAgentsOnboarding(ctx context.Context) (AgentsOnboardingResult, error) {
	manifest, _, err := renderAPMTemplatePlan(agentBundlePlan{})
	if err != nil {
		return AgentsOnboardingResult{}, err
	}
	template, err := AgentsTemplatePath()
	if err != nil {
		return AgentsOnboardingResult{}, err
	}
	lock, err := config.AcquireWriteLock(template)
	if err != nil {
		return AgentsOnboardingResult{}, fmt.Errorf("lock agents onboarding: %w", err)
	}
	defer func() { _ = lock.Close() }()

	var readiness AgentsReadiness
	err = apm.WithGlobalWorkspaceLock(ctx, func(lockCtx context.Context) error {
		readiness, err = inspectAgentsReadiness()
		if err != nil || readiness.State != AgentsReadinessEmpty {
			return err
		}
		readiness, err = commitAgentsOnboardingLocked(lockCtx, a.StateDir, agentBundlePlan{}, nil, agentsMigrationMarker+"\n"+manifest)
		return err
	})
	return AgentsOnboardingResult{Readiness: readiness}, err
}

// AgentsReadiness probes the pinned APM contract before inspecting its workspace.
func (a *App) AgentsReadiness(ctx context.Context) (AgentsReadiness, error) {
	if !a.APMAvailable() {
		return AgentsReadiness{}, errAPMNotInstalled()
	}
	if err := a.requirePinnedAPM(ctx); err != nil {
		return AgentsReadiness{}, err
	}
	return inspectAgentsReadiness()
}

func inspectAgentsReadiness() (AgentsReadiness, error) {
	template, err := AgentsTemplatePath()
	if err != nil {
		return AgentsReadiness{}, err
	}
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return AgentsReadiness{}, err
	}
	r := AgentsReadiness{
		TemplatePath: template,
		ManifestPath: filepath.Join(dir, "apm.yml"),
		LockPath:     filepath.Join(dir, "apm.lock.yaml"),
	}
	if err := validateAPMWorkspaceDir(dir); err != nil {
		r.State, r.CTA, r.Details = AgentsReadinessInvalid, AgentsCTARetry, []string{err.Error()}
		return r, nil
	}
	templateExists, templateErr := readableRegularYAML(r.TemplatePath, &apmManifest{})
	manifestExists, manifestErr := readableRegularYAML(r.ManifestPath, &apmManifest{})
	lockExists, lockErr := readableRegularYAML(r.LockPath, &apmLockfile{})
	if first := firstNonNil(templateErr, manifestErr, lockErr); first != nil {
		r.State, r.CTA, r.Details = AgentsReadinessInvalid, AgentsCTARetry, []string{first.Error()}
		return r, nil
	}
	switch {
	case manifestExists && lockExists:
		r.State = AgentsReadinessReady
	case !templateExists && !manifestExists && !lockExists:
		r.State, r.CTA = AgentsReadinessEmpty, AgentsCTAMigrate
	case templateExists && !manifestExists && !lockExists:
		r.State, r.CTA, r.Details = AgentsReadinessTemplateOnly, AgentsCTASync, []string{"APM template is staged; sync to create the live manifest and lockfile"}
	case lockExists && !manifestExists:
		r.State, r.CTA, r.Details = AgentsReadinessLockOnly, AgentsCTARetry, []string{"APM lockfile exists without a live manifest"}
	default:
		r.State, r.CTA, r.Details = AgentsReadinessLiveIncomplete, AgentsCTASync, []string{"APM live manifest exists without a readable lockfile"}
	}
	return r, nil
}

func validateAPMWorkspaceDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect APM workspace %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("APM workspace %s is not a real directory", dir)
	}
	return nil
}

func readableRegularYAML(path string, dst any) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return true, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0o444 == 0 {
		return true, fmt.Errorf("%s is not readable", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return true, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) == 0 || yaml.Unmarshal(raw, dst) != nil {
		return true, fmt.Errorf("%s is invalid YAML", path)
	}
	return true, nil
}

func firstNonNil(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// AgentsPrepareOnboarding stages a unique safe legacy snapshot only while all APM state is empty.
// It never invokes APM, installs packages, creates a lockfile, or overwrites existing state.
func (a *App) AgentsPrepareOnboarding(ctx context.Context, host string) (AgentsOnboardingResult, error) {
	return a.agentsPrepareOnboarding(ctx, host, "")
}

func (a *App) agentsPrepareOnboarding(ctx context.Context, host, snapshotDir string) (AgentsOnboardingResult, error) {
	readiness, err := a.AgentsReadiness(ctx)
	if err != nil || readiness.State != AgentsReadinessEmpty {
		return AgentsOnboardingResult{Readiness: readiness}, err
	}
	lock, err := config.AcquireWriteLock(readiness.TemplatePath)
	if err != nil {
		return AgentsOnboardingResult{}, fmt.Errorf("lock agents onboarding: %w", err)
	}
	defer func() { _ = lock.Close() }()

	var result AgentsOnboardingResult
	err = apm.WithGlobalWorkspaceLock(ctx, func(lockCtx context.Context) error {
		if err := lockCtx.Err(); err != nil {
			return err
		}
		current, err := inspectAgentsReadiness()
		if err != nil {
			return err
		}
		result.Readiness = current
		if current.State != AgentsReadinessEmpty {
			return nil
		}
		if err := lockCtx.Err(); err != nil {
			return err
		}
		snapshot := snapshotDir
		if snapshot == "" {
			snapshot, err = a.defaultSnapshotDir()
			if err != nil {
				result.Readiness.Details = []string{err.Error()}
				return nil
			}
		}
		if err := lockCtx.Err(); err != nil {
			return err
		}
		plan, rendered, err := a.planAgentsMigration(host, snapshot)
		if err != nil {
			result.Readiness.Details = []string{err.Error()}
			return nil
		}
		if len(plan.Suppressed) != 0 {
			result.Readiness.Details = []string{"legacy snapshot contains suppressed package-owned declarations; review migration manually"}
			return nil
		}
		prepared, err := prepareAgentBundleWrappers(plan)
		if err != nil {
			return fmt.Errorf("prepare migration wrappers: %w", err)
		}
		defer discardPreparedAgentBundleWrappers(prepared)
		result.Readiness, err = commitAgentsOnboardingLocked(lockCtx, a.StateDir, plan, prepared, rendered)
		if err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func commitAgentsOnboardingLocked(ctx context.Context, stateDir string, plan agentBundlePlan, prepared []preparedAgentBundleWrapper, rendered string) (AgentsReadiness, error) {
	if err := ctx.Err(); err != nil {
		return AgentsReadiness{}, err
	}
	// This is the final publish boundary. Recheck after wrapper preparation because neither lock
	// protects the workspace from direct external writers.
	current, err := inspectAgentsReadiness()
	if err != nil {
		return current, err
	}
	if current.State != AgentsReadinessEmpty {
		return current, fmt.Errorf("APM state changed during onboarding; retry")
	}
	identity, err := inspectAgentsMigrationTemplate(current.TemplatePath)
	if err != nil {
		return current, err
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if _, err := commitAgentMigrationLocked(current.TemplatePath, stateDir, plan, prepared, identity, rendered, writeAgentsMigrationTemplate); err != nil {
		return current, err
	}
	return inspectAgentsReadiness()
}
