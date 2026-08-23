// Package rpm provides shared helpers for RPM-backed providers.
package rpm

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

const summaryQueryFormat = "%{NAME}\\t%{SUMMARY}\\n"

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

func Summaries(ctx context.Context, exec executor.Executor, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	args := make([]string, 0, len(tools)+3)
	args = append(args, "-q", "--queryformat", summaryQueryFormat)
	for _, tool := range tools {
		args = append(args, tool.EffectivePackage())
	}
	stdout, stderr, err := exec.Run(ctx, "rpm", args...)
	summaries := ParseSummaryLines(stdout)
	if err != nil && len(summaries) == 0 && !isRPMMissOutput(stdout) {
		return nil, executor.WrapError(err, "rpm summaries", stdout, stderr)
	}
	return summaries, nil
}

func Summary(ctx context.Context, exec executor.Executor, pkg string) (string, error) {
	stdout, stderr, err := exec.Run(ctx, "rpm", "-q", "--queryformat", "%{SUMMARY}", pkg)
	desc := strings.TrimSpace(stdout)
	if err != nil {
		if isRPMMissOutput(desc) {
			return "", nil
		}
		return "", executor.WrapError(err, fmt.Sprintf("rpm summary %s", pkg), stdout, stderr)
	}
	if isRPMMissOutput(desc) {
		return "", nil
	}
	return desc, nil
}

func ParseListLine(line string) (name, version string) {
	fields := strings.SplitN(line, "\t", 2)
	if len(fields) < 2 {
		return "", ""
	}
	return strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
}

func ParseSummaryLines(output string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		name, summary := ParseListLine(line)
		if name == "" || summary == "" {
			continue
		}
		m[name] = summary
	}
	return m
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

func isRPMMissOutput(output string) bool {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return strings.TrimSpace(output) != ""
}
