package app

import (
	"path/filepath"
	"strings"
)

const apmLockDeploymentKind = "project-relative"

// A nil set means the ledger is unknown, and every caller must then treat all paths as deployed.
func apmDeployedPaths() (map[string]bool, error) {
	lock, err := readAPMLockfile()
	if err != nil {
		return nil, err
	}
	deployed := make(map[string]bool, len(lock.Deployments))
	for _, entry := range lock.Deployments {
		if entry.Kind == apmLockDeploymentKind && entry.Value != "" {
			deployed[filepath.Clean(entry.Value)] = true
		}
	}
	return deployed, nil
}

// Details are either "prefix: path" or free text, so only a space-free tail with a separator counts.
func apmAuditDetailPath(detail, home string) string {
	path := apmAuditDriftPath(detail)
	if path == "" || strings.ContainsAny(path, " \t") || !strings.Contains(path, "/") {
		return ""
	}
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Clean(path)
}

func splitAPMAuditDetails(details []string, home string, deployed map[string]bool) (onDeployed, elsewhere []string) {
	for _, detail := range details {
		path := apmAuditDetailPath(detail, home)
		if deployed == nil || path == "" || deployed[path] {
			onDeployed = append(onDeployed, detail)
			continue
		}
		elsewhere = append(elsewhere, path)
	}
	return onDeployed, elsewhere
}
