package apk

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type Provider struct {
	exec executor.Executor
}

func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "apk" }
func (p *Provider) Description() string { return "apk — Alpine Linux package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, _, err := p.exec.Run(ctx, "apk", "--version")
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	rawCmd, rawArgs, _ := p.PrivilegeCommand(provider.PrivilegeActionInstall, tool)
	cmd, args := provider.PrivilegedCommand(rawCmd, rawArgs...)
	return provider.RunCmd(ctx, p.exec, "apk add "+pkg, cmd, args...)
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	rawCmd, rawArgs, _ := p.PrivilegeCommand(provider.PrivilegeActionUninstall, tool)
	cmd, args := provider.PrivilegedCommand(rawCmd, rawArgs...)
	return provider.RunCmd(ctx, p.exec, "apk del "+pkg, cmd, args...)
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	pkg := tool.EffectivePackage()
	rawCmd, rawArgs, _ := p.PrivilegeCommand(provider.PrivilegeActionUpgrade, tool)
	cmd, args := provider.PrivilegedCommand(rawCmd, rawArgs...)
	return provider.RunCmd(ctx, p.exec, "apk upgrade "+pkg, cmd, args...)
}

func (p *Provider) PrivilegePlan(_ context.Context, action provider.PrivilegeAction, tool provider.Tool) (provider.PrivilegePlan, error) {
	return provider.SystemPrivilegePlan(p.Name(), action, tool), nil
}

func (p *Provider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	pkg := tool.EffectivePackage()
	switch action {
	case provider.PrivilegeActionInstall:
		return "apk", []string{"add", pkg}, true
	case provider.PrivilegeActionUpgrade:
		return "apk", []string{"upgrade", pkg}, true
	default:
		return "apk", []string{"del", pkg}, true
	}
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	stdout, _, err := p.exec.Run(ctx, "apk", "info", "-e", tool.EffectivePackage())
	if err != nil {
		return false, "", nil // non-zero exit → not installed
	}
	if strings.TrimSpace(stdout) == "" {
		return false, "", nil
	}
	// A failed version fetch still means installed, just with an unknown version.
	vout, _, err := p.exec.Run(ctx, "apk", "info", "-v")
	if err != nil {
		return true, "", nil
	}
	return true, parseAPKVersion(vout, tool.EffectivePackage()), nil
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	world := apkWorldPackages()
	stdout, stderr, err := p.exec.Run(ctx, "apk", "info", "-v")
	if err != nil {
		return nil, executor.WrapError(err, "apk info -v", stdout, stderr)
	}
	var tools []provider.InstalledTool
	for _, line := range strings.Split(stdout, "\n") {
		name, version := parseAPKInfoLine(line)
		if name == "" {
			continue
		}
		if world != nil && !world[strings.ToLower(name)] {
			continue
		}
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: "apk"},
			Version: version,
		})
	}
	return tools, nil
}

// InstalledMap returns explicitly requested apk packages as lowercase-name→version.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	world := apkWorldPackages()
	stdout, stderr, err := p.exec.Run(ctx, "apk", "info", "-v")
	if err != nil {
		return nil, executor.WrapError(err, "apk info -v", stdout, stderr)
	}
	m := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		name, version := parseAPKInfoLine(line)
		if name == "" {
			continue
		}
		if world != nil && !world[strings.ToLower(name)] {
			continue
		}
		m[strings.ToLower(name)] = version
	}
	return m, nil
}

func apkWorldPackages() map[string]bool {
	path := os.Getenv("OMNI_APK_WORLD")
	if path == "" {
		path = "/etc/apk/world"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		name := parseAPKWorldConstraint(line)
		if name != "" {
			m[strings.ToLower(name)] = true
		}
	}
	return m
}

func parseAPKWorldConstraint(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	line = strings.TrimPrefix(line, "!")
	if i := strings.Index(line, "@"); i >= 0 {
		line = line[:i]
	}
	if i := strings.IndexAny(line, "<>=~"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// BulkDescribe fetches descriptions for multiple tools via a single `apk info -d` call.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	args := make([]string, 0, len(tools)+2)
	args = append(args, "info", "-d")
	pkgSet := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		pkg := t.EffectivePackage()
		args = append(args, pkg)
		pkgSet[pkg] = struct{}{}
	}
	stdout, stderr, err := p.exec.Run(ctx, "apk", args...)
	if err != nil {
		return nil, executor.WrapError(err, "apk info -d", stdout, stderr)
	}
	return parseAPKBulkDescriptions(stdout, pkgSet), nil
}

// Each package block looks like: "<name>-<version> description:\n<text>"
func parseAPKBulkDescriptions(output string, pkgSet map[string]struct{}) map[string]string {
	m := make(map[string]string)
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasSuffix(trimmed, "description:") {
			continue
		}
		prefix := strings.TrimSuffix(trimmed, " description:")
		// Longest match wins so "python3" cannot claim "python3-dev-1.2.3-r0".
		var pkgName string
		for pkg := range pkgSet {
			if strings.HasPrefix(prefix, pkg) && len(pkg) > len(pkgName) {
				rest := prefix[len(pkg):]
				if rest == "" || rest[0] == '-' {
					pkgName = pkg
				}
			}
		}
		if pkgName == "" {
			continue
		}
		for _, next := range lines[i+1:] {
			if s := strings.TrimSpace(next); s != "" {
				m[pkgName] = s
				break
			}
		}
	}
	return m
}

// Describe fetches a one-line description via `apk info -d`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	stdout, stderr, err := p.exec.Run(ctx, "apk", "info", "-d", tool.EffectivePackage())
	if err != nil {
		return "", executor.WrapError(err, fmt.Sprintf("apk info -d %s", tool.EffectivePackage()), stdout, stderr)
	}
	return parseAPKDescription(stdout), nil
}

// Format: "<name>-<ver> description:\n<text>"
func parseAPKDescription(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), "description:") {
			for _, next := range lines[i+1:] {
				if s := strings.TrimSpace(next); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// Format: "ripgrep-14.1.1-r0" → ("ripgrep", "14.1.1-r0")
func parseAPKInfoLine(line string) (name, version string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	// Walk back to the '-' preceding a digit: "py3-requests-2.31.0-r1" splits after "py3-requests".
	for i := len(line) - 1; i > 0; i-- {
		if line[i] == '-' && i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
			return line[:i], line[i+1:]
		}
	}
	return line, ""
}

// parseAPKVersion extracts one package version from `apk info -v` output.
func parseAPKVersion(output, pkg string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, version := parseAPKInfoLine(line)
		if name == pkg {
			return version
		}
	}
	return ""
}
