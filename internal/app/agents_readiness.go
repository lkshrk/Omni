package app

import (
	"context"
	"errors"
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
	AgentsReadinessInvalid        AgentsReadinessState = "invalid"
)

type AgentsReadinessCTA string

const (
	AgentsCTANone    AgentsReadinessCTA = ""
	AgentsCTAMigrate AgentsReadinessCTA = "migrate"
	AgentsCTASync    AgentsReadinessCTA = "sync"
)

type AgentsReadiness struct {
	State        AgentsReadinessState
	CTA          AgentsReadinessCTA
	Details      []string
	TemplatePath string
	ManifestPath string
	LockPath     string
}

// AgentsReadiness inspects the pinned APM contract and its workspace without writing anything.
func (a *App) AgentsReadiness(ctx context.Context, host string) (AgentsReadiness, error) {
	if !a.APMAvailable() {
		return agentsAPMRepairReadiness(errAPMNotInstalled())
	}
	if err := a.requirePinnedAPM(ctx); err != nil {
		var repair *APMRepairError
		if !errors.As(err, &repair) {
			return AgentsReadiness{}, err
		}
		return agentsAPMRepairReadiness(err)
	}
	readiness, err := inspectAgentsReadiness()
	if err != nil {
		return readiness, err
	}
	hasLegacy, err := config.HasRemovedAgentConfig(a.ConfigPath)
	if err != nil {
		return readiness, err
	}
	if hasLegacy {
		if host == "" {
			host = "<host>"
		}
		readiness.CTA = AgentsCTAMigrate
		readiness.Details = append(readiness.Details, "run omni agents migrate --host "+host)
	}
	return readiness, nil
}

func agentsAPMRepairReadiness(err error) (AgentsReadiness, error) {
	r := AgentsReadiness{State: AgentsReadinessInvalid, Details: []string{err.Error(), "run omni doctor --fix"}}
	if template, pathErr := AgentsTemplatePath(); pathErr == nil {
		r.TemplatePath = template
	}
	if dir, dirErr := apm.GlobalWorkspaceDir(); dirErr == nil {
		r.ManifestPath, r.LockPath = filepath.Join(dir, "apm.yml"), filepath.Join(dir, "apm.lock.yaml")
	}
	return r, nil
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
		r.State, r.Details = AgentsReadinessInvalid, []string{err.Error()}
		return r, nil
	}
	templateExists, templateErr := readableRegularYAML(r.TemplatePath, &apmManifest{})
	var manifest apmManifest
	manifestExists, manifestErr := readableRegularYAML(r.ManifestPath, &manifest)
	lockExists, lockErr := readableRegularYAML(r.LockPath, &apmLockfile{})
	if first := firstNonNil(templateErr, manifestErr, lockErr); first != nil {
		r.State, r.Details = AgentsReadinessInvalid, []string{first.Error()}
		return r, nil
	}
	switch {
	case manifestExists && (lockExists || noAPMDependencies(manifest)):
		r.State = AgentsReadinessReady
	case !templateExists && !manifestExists && !lockExists:
		r.State, r.CTA = AgentsReadinessEmpty, AgentsCTAMigrate
	case templateExists && !manifestExists && !lockExists:
		r.State, r.CTA, r.Details = AgentsReadinessTemplateOnly, AgentsCTASync, []string{"APM template is staged; sync to create the live manifest and lockfile"}
	case lockExists && !manifestExists:
		r.State, r.Details = AgentsReadinessInvalid, []string{"APM lockfile exists without a live manifest"}
	default:
		r.State, r.CTA, r.Details = AgentsReadinessLiveIncomplete, AgentsCTASync, []string{"APM live manifest exists without a readable lockfile"}
	}
	return r, nil
}

func noAPMDependencies(manifest apmManifest) bool {
	return len(manifest.Dependencies.APM) == 0 && len(manifest.Dependencies.MCP) == 0 && len(manifest.Dependencies.LSP) == 0
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
