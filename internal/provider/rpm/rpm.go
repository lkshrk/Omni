// Package rpm provides shared helpers for RPM-backed providers.
package rpm

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

const listQueryFormat = "%{NAME}\\t%{VERSION}-%{RELEASE}\\n"

func IsInstalled(ctx context.Context, exec executor.Executor, pkg string) (bool, string, error) {
	stdout, _, err := exec.Run(ctx, "rpm", "-q", "--queryformat", "%{VERSION}-%{RELEASE}", pkg)
	if err != nil {
		return false, "", nil
	}
	version := strings.TrimSpace(stdout)
	if version == "" {
		return false, "", nil
	}
	return true, version, nil
}

func ListInstalled(ctx context.Context, exec executor.Executor, providerName string) ([]provider.InstalledTool, error) {
	stdout, err := listInstalledOutput(ctx, exec)
	if err != nil {
		return nil, err
	}
	var tools []provider.InstalledTool
	for _, line := range strings.Split(stdout, "\n") {
		name, version := ParseListLine(line)
		if name == "" {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: providerName},
			Version: version,
		})
	}
	return tools, nil
}

func InstalledMap(ctx context.Context, exec executor.Executor) (map[string]string, error) {
	stdout, err := listInstalledOutput(ctx, exec)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		name, version := ParseListLine(line)
		if name == "" {
			continue
		}
		m[strings.ToLower(name)] = version
	}
	return m, nil
}

func ParseListLine(line string) (name, version string) {
	fields := strings.SplitN(line, "\t", 2)
	if len(fields) < 2 {
		return "", ""
	}
	return strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
}

func ParseInfoSummaries(output string) map[string]string {
	m := make(map[string]string)
	var currentName string
	for _, line := range strings.Split(output, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch key {
		case "Name":
			currentName = val
		case "Summary":
			if currentName != "" {
				if _, exists := m[currentName]; !exists {
					m[currentName] = val
				}
			}
		}
	}
	return m
}

func ParseInfoSummary(output string) string {
	for _, line := range strings.Split(output, "\n") {
		key, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.TrimSpace(key) == "Summary" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func listInstalledOutput(ctx context.Context, exec executor.Executor) (string, error) {
	stdout, _, err := exec.Run(ctx, "rpm", "-qa", "--queryformat", listQueryFormat)
	if err != nil {
		return "", fmt.Errorf("rpm list: %w", err)
	}
	return stdout, nil
}
