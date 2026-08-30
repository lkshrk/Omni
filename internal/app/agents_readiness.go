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
	Readiness  AgentsReadiness
	AutoStaged bool
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
	err = apm.WithGlobalWorkspaceLock(ctx, func(context.Context) error {
		current, err := inspectAgentsReadiness()
		if err != nil {
			return err
		}
		result.Readiness = current
		if current.State != AgentsReadinessEmpty {
			return nil
		}
		snapshot, err := a.defaultSnapshotDir()
		if err != nil {
			result.Readiness.Details = []string{err.Error()}
			return nil
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
		// Recheck immediately before publishing; neither lock protects direct external writers.
		current, err = inspectAgentsReadiness()
		if err != nil || current.State != AgentsReadinessEmpty {
			result.Readiness = current
			return err
		}
		prepared, err := prepareAgentBundleWrappers(plan)
		if err != nil {
			return fmt.Errorf("prepare migration wrappers: %w", err)
		}
		defer discardPreparedAgentBundleWrappers(prepared)
		identity, err := inspectAgentsMigrationTemplate(current.TemplatePath)
		if err != nil {
			return err
		}
		if _, err := commitAgentMigrationLocked(current.TemplatePath, a.StateDir, plan, prepared, identity, rendered, writeAgentsMigrationTemplate); err != nil {
			return err
		}
		result.Readiness, err = inspectAgentsReadiness()
		result.AutoStaged = err == nil && result.Readiness.State == AgentsReadinessTemplateOnly
		return err
	})
	return result, err
}
