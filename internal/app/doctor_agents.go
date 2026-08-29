package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

func (a *App) doctorAgents(_ context.Context, result *DoctorResult, _ *config.RootConfig) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, err.Error())
		return
	}
	if !a.APMAvailable() {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "apm executable not found on PATH; run 'omni doctor --fix' to install it")
		return
	}
	files := []struct {
		label string
		path  string
	}{
		{label: "manifest", path: filepath.Join(dir, "apm.yml")},
		{label: "lockfile", path: filepath.Join(dir, "apm.lock.yaml")},
	}
	for _, file := range files {
		if _, err := os.ReadFile(file.path); err != nil {
			if os.IsNotExist(err) {
				result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "global APM "+file.label+" not found: "+file.path)
				return
			}
			result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "global APM "+file.label+" is not readable: "+err.Error())
			return
		}
	}
	result.addCheck("agents", "Agent packages (APM)", DoctorStatusOK, "global manifest and lockfile are readable", files[0].path, files[1].path)
	a.doctorAgentsOwnedChildren(result, dir)
}

func (a *App) doctorAgentsOwnedChildren(result *DoctorResult, workspaceDir string) {
	templatePath, err := AgentsTemplatePath()
	if err != nil {
		result.addCheck("agents-owned-children", "Agent ownership", DoctorStatusWarn, "package-owned services could not be checked", err.Error())
		return
	}
	manifest, lock, err := readAgentsOwnedWorkspace(workspaceDir)
	if err != nil {
		result.addCheck("agents-owned-children", "Agent ownership", DoctorStatusWarn, "package-owned services could not be checked", templatePath, err.Error())
		return
	}
	if raw, readErr := os.ReadFile(templatePath); readErr == nil {
		var template apmManifest
		if err := yaml.Unmarshal(raw, &template); err != nil {
			result.addCheck("agents-owned-children", "Agent ownership", DoctorStatusFail, "canonical agents template is invalid", agentsInvalidYAMLError("agents template", templatePath, err).Error())
			return
		}
		manifest = template
	} else if !os.IsNotExist(readErr) {
		result.addCheck("agents-owned-children", "Agent ownership", DoctorStatusWarn, "canonical agents template is not readable", templatePath, readErr.Error())
		return
	}

	evidence := readAPMModuleManifests(workspaceDir, joinAPMPackages(manifest, lock))
	if err := checkAgentsOwnedChildOwners(evidence.Children); err != nil {
		result.addCheck("agents-owned-children", "Agent ownership", DoctorStatusFail, "package ownership is ambiguous", templatePath, err.Error())
		return
	}
	collisions := classifyAgentsOwnedChildren(manifest, evidence.Children)
	details := []string{templatePath}
	status := DoctorStatusOK
	message := "no redundant package-owned MCP or LSP declarations"
	for _, collision := range collisions {
		if collision.Exact {
			if status == DoctorStatusOK {
				status = DoctorStatusWarn
			}
			message = "standalone services duplicate package-owned services"
			details = append(details, fmt.Sprintf("%s %s is provided identically by %s", strings.ToUpper(string(collision.Child.Kind)), collision.Child.Name, collision.Child.Owner))
			continue
		}
		status = DoctorStatusFail
		message = "standalone services conflict with package-owned services"
		fields := agentsOwnedCollisionDiffFields(manifest, collision)
		details = append(details, fmt.Sprintf("%s %s conflicts with package %s (%s differ)", strings.ToUpper(string(collision.Child.Kind)), collision.Child.Name, collision.Child.Owner, strings.Join(fields, ", ")))
	}
	if len(evidence.Unavailable) > 0 && status != DoctorStatusFail {
		status, message = DoctorStatusWarn, "some installed package manifests could not be evaluated"
		details = append(details, "unavailable package manifests: "+strings.Join(evidence.Unavailable, ", "))
	}
	sort.Strings(details[1:])
	result.addCheck("agents-owned-children", "Agent ownership", status, message, details...)
}

func readAgentsOwnedWorkspace(dir string) (apmManifest, apmLockfile, error) {
	var manifest apmManifest
	manifestPath := filepath.Join(dir, "apm.yml")
	_, raw, err := readAgentsFileIdentity(manifestPath)
	if err != nil {
		return manifest, apmLockfile{}, err
	}
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &manifest); err != nil {
			return manifest, apmLockfile{}, agentsInvalidYAMLError("APM manifest", manifestPath, err)
		}
	}
	var lock apmLockfile
	lockPath := filepath.Join(dir, "apm.lock.yaml")
	_, raw, err = readAgentsFileIdentity(lockPath)
	if err != nil {
		return manifest, lock, err
	}
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &lock); err != nil {
			return manifest, lock, agentsInvalidYAMLError("APM lockfile", lockPath, err)
		}
	}
	return manifest, lock, nil
}

func agentsOwnedChildOwners(children []agentsOwnedChild) map[string][]string {
	owners := make(map[string][]string)
	for _, child := range children {
		key := agentsChildKey(child.Kind, child.Name)
		owners[key] = append(owners[key], child.Owner)
	}
	for key := range owners {
		sort.Strings(owners[key])
		owners[key] = slices.Compact(owners[key])
	}
	return owners
}

func agentsOwnedCollisionDiffFields(manifest apmManifest, collision agentsChildCollision) []string {
	if collision.Child.MCP != nil {
		for _, dep := range manifest.Dependencies.MCP {
			if strings.EqualFold(dep.Name, collision.Child.Name) {
				return agentsMCPDiffFields(*collision.Child.MCP, dep, collision.Child.OwnerRoot)
			}
		}
	}
	if collision.Child.LSP != nil {
		for _, dep := range manifest.Dependencies.LSP {
			if strings.EqualFold(dep.Name, collision.Child.Name) {
				return agentsLSPDiffFields(*collision.Child.LSP, dep, collision.Child.OwnerRoot)
			}
		}
	}
	return nil
}
