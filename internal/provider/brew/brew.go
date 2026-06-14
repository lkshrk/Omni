// Package brew implements the Homebrew provider.
package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

type Provider struct {
	exec executor.Executor
	mu   sync.RWMutex
}

const (
	brewKindOption  = "brew_kind"
	brewKindFormula = "formula"
	brewKindCask    = "cask"
)

func New(exec executor.Executor) *Provider {
	return &Provider{exec: exec}
}

func (p *Provider) Name() string        { return "brew" }
func (p *Provider) Description() string { return "Homebrew — macOS/Linux package manager" }

func (p *Provider) Available(ctx context.Context) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if checker, ok := p.exec.(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable("brew"), nil
	}
	_, _, err := p.exec.Run(ctx, "brew", "--version")
	if err != nil {
		return false, nil // binary not found; not an error from our perspective
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	args := installArgs(tool)
	_, stderr, err := p.exec.Run(ctx, "brew", args...)
	if err == nil {
		return nil
	}
	// The package is already installed from a tap; brew refuses to install the
	// same name from a different tap. Treat as installed rather than a failure —
	// the scan missed it only because its tap was untrusted (now self-healing).
	if isBrewAlreadyInstalledFromTap(stderr) {
		return nil
	}
	// brew refused because the formula's tap is untrusted. Trust it and retry
	// once; a tool omni is asked to install is one the user opted into.
	if tap, ok := parseBrewUntrustedTap(stderr); ok {
		if _, _, terr := p.exec.Run(ctx, "brew", "trust", tap); terr == nil {
			if _, rstderr, rerr := p.exec.Run(ctx, "brew", args...); rerr == nil {
				return nil
			} else {
				err, stderr = rerr, rstderr
			}
		}
	}
	// When the kind is unspecified and brew refuses because the name is both a
	// formula and a cask, retry explicitly — formula first (the common CLI case),
	// then cask. Tools added via search/import already carry brew_kind and skip
	// this path.
	if tool.Options[brewKindOption] == "" && isBrewAmbiguousFormulaCask(stderr) {
		for _, kind := range []string{"--formula", "--cask"} {
			retry := []string{"install", kind, tool.EffectivePackage()}
			if _, rstderr, rerr := p.exec.Run(ctx, "brew", retry...); rerr == nil {
				return nil
			} else {
				args, err, stderr = retry, rerr, rstderr
			}
		}
	}
	return fmt.Errorf("brew %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr))
}

// parseBrewUntrustedTap extracts the tap name from a brew tap-trust refusal,
// e.g. "Refusing to load formula getsentry/xcodebuildmcp/xcodebuildmcp from
// untrusted tap getsentry/xcodebuildmcp." → "getsentry/xcodebuildmcp".
func parseBrewUntrustedTap(stderr string) (string, bool) {
	_, rest, found := strings.Cut(stderr, "from untrusted tap ")
	if !found {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	field := strings.FieldsFunc(rest, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '.' || r == ',' || r == '"' || r == '\''
	})
	if len(field) == 0 || field[0] == "" {
		return "", false
	}
	return field[0], true
}

// isBrewAlreadyInstalledFromTap reports whether brew refused an install because
// the same-named formula is already installed from a different tap, e.g.
// "flux was installed from the fluxcd/tap tap but you are trying to install it
// from the homebrew/core tap."
func isBrewAlreadyInstalledFromTap(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "was installed from the") &&
		strings.Contains(s, "tap")
}

// isBrewAmbiguousFormulaCask reports whether brew refused an unqualified install
// because the name resolves to both a formula and a cask.
func isBrewAmbiguousFormulaCask(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "both a formula and a cask") ||
		strings.Contains(s, "please specify") && strings.Contains(s, "cask") ||
		strings.Contains(s, "provided by multiple")
}

func installArgs(tool provider.Tool) []string {
	pkg := tool.EffectivePackage()
	switch tool.Options[brewKindOption] {
	case brewKindFormula:
		return []string{"install", "--formula", pkg}
	case brewKindCask:
		return []string{"install", "--cask", pkg}
	default:
		return []string{"install", pkg}
	}
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "uninstall", tool.EffectivePackage())
	if err != nil {
		return fmt.Errorf("brew uninstall %s: %w (stderr: %s)", tool.EffectivePackage(), err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	args := p.upgradeArgsWithKind(ctx, tool.EffectivePackage(), tool.Options[brewKindOption])
	_, stderr, err := p.exec.Run(ctx, "brew", args...)
	if err == nil {
		return nil
	}
	// Same tap-trust self-heal as Install: trust the refused tap and retry once.
	if tap, ok := parseBrewUntrustedTap(stderr); ok {
		if _, _, terr := p.exec.Run(ctx, "brew", "trust", tap); terr == nil {
			if _, rstderr, rerr := p.exec.Run(ctx, "brew", args...); rerr == nil {
				return nil
			} else {
				err, stderr = rerr, rstderr
			}
		}
	}
	return fmt.Errorf("brew %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr))
}

func (p *Provider) upgradeArgsWithKind(ctx context.Context, pkg, kind string) []string {
	return upgradeArgsForKind(ctx, p, pkg, kind)
}

func upgradeArgsForKind(ctx context.Context, p *Provider, pkg, kind string) []string {
	name := formulaName(pkg)
	switch kind {
	case brewKindFormula:
		return []string{"upgrade", "--formula", pkg}
	case brewKindCask:
		return []string{"upgrade", "--cask", name}
	}
	// Fall back to live probing when no persisted kind is available.
	if p.installedFormula(ctx, name) {
		return []string{"upgrade", "--formula", pkg}
	}
	if p.installedCask(ctx, name) {
		return []string{"upgrade", "--cask", name}
	}
	return []string{"upgrade", pkg}
}

func (p *Provider) installedFormula(ctx context.Context, name string) bool {
	stdout, _, err := p.exec.Run(ctx, "brew", "list", "--versions", name)
	return err == nil && strings.TrimSpace(stdout) != ""
}

func (p *Provider) installedCask(ctx context.Context, name string) bool {
	stdout, _, err := p.exec.Run(ctx, "brew", "list", "--versions", "--cask", name)
	return err == nil && strings.TrimSpace(stdout) != ""
}

// IsInstalled checks whether a formula or cask is installed.
// Tap packages like "hashicorp/tap/terraform" must be stripped to "terraform" —
// brew list --versions does not accept the tap-qualified form.
// Modern brew auto-detects formula vs cask for install/uninstall/upgrade, but
// `brew list --versions` only searches formulae by default, so we try the cask
// flag as a fallback.
func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	name := formulaName(tool.EffectivePackage())
	for _, args := range [][]string{
		{"list", "--versions", name},
		{"list", "--versions", "--cask", name},
	} {
		stdout, _, err := p.exec.Run(ctx, "brew", args...)
		if err == nil {
			if out := strings.TrimSpace(stdout); out != "" {
				return true, parseBrewVersion(out), nil
			}
		}
	}
	return false, "", nil
}

// brewInfoOutput is the shape of `brew info --json=v2`.
type brewInfoOutput struct {
	Formulae []brewFormulaInfo `json:"formulae"`
	Casks    []brewCaskInfo    `json:"casks"`
}

type brewFormulaInfo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Desc     string `json:"desc"`
	Homepage string `json:"homepage"`
	URLs     struct {
		Stable struct {
			URL string `json:"url"`
		} `json:"stable"`
	} `json:"urls"`
	Installed []struct {
		Version            string `json:"version"`
		InstalledOnRequest bool   `json:"installed_on_request"`
	} `json:"installed"`
}

type brewCaskInfo struct {
	Token     string                       `json:"token"`
	Desc      string                       `json:"desc"`
	Homepage  string                       `json:"homepage"`
	URL       string                       `json:"url"`
	Installed string                       `json:"installed"` // installed version string
	Artifacts []map[string]json.RawMessage `json:"artifacts"`
}

// ListInstalled returns Homebrew-managed formulae and casks for import/discovery.
// Casks come from `brew list --cask`, so ordinary macOS apps that merely exist
// on disk are not claimed as tools.
func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	formulae, err := p.installedFormulae(ctx)
	if err != nil {
		return nil, err
	}
	casks, err := p.installedCasks(ctx)
	if err != nil {
		return nil, err
	}
	var tools []provider.InstalledTool
	tools = append(tools, formulae...)
	tools = append(tools, casks...)
	return tools, nil
}

// InstalledMap returns explicitly installed formulae and casks as lowercase-name→version map.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	metadata, err := p.InstalledMetadataMap(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(metadata))
	for name, entry := range metadata {
		m[name] = entry.Version
	}
	return m, nil
}

func (p *Provider) installedFormulae(ctx context.Context) ([]provider.InstalledTool, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "leaves", "--installed-on-request")
	if err != nil {
		return nil, fmt.Errorf("brew leaves --installed-on-request: %w", err)
	}
	packages := strings.Fields(stdout)
	if len(packages) == 0 {
		return nil, nil
	}
	args := append([]string{"list", "--versions"}, packages...)
	stdout, _, err = p.exec.Run(ctx, "brew", args...)
	if err != nil {
		return nil, fmt.Errorf("brew list --versions: %w", err)
	}
	versions := parseBrewListVersions(stdout)
	tools := make([]provider.InstalledTool, 0, len(packages))
	for _, pkg := range packages {
		name := formulaName(pkg)
		tools = append(tools, provider.InstalledTool{
			Tool: provider.Tool{
				Name:     name,
				Provider: "brew",
				Package:  pkg,
				Options:  map[string]string{brewKindOption: brewKindFormula},
			},
			Version: lookupBrewListVersion(versions, pkg),
		})
	}
	return tools, nil
}

func (p *Provider) installedCasks(ctx context.Context) ([]provider.InstalledTool, error) {
	tokens, err := p.installedCaskTokens(ctx)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	args := append([]string{"list", "--versions", "--cask"}, tokens...)
	stdout, _, err := p.exec.Run(ctx, "brew", args...)
	if err != nil {
		return nil, fmt.Errorf("brew list --versions --cask: %w", err)
	}
	versions := parseBrewListVersions(stdout)
	tools := make([]provider.InstalledTool, 0, len(tokens))
	for _, token := range tokens {
		tools = append(tools, provider.InstalledTool{
			Tool: provider.Tool{
				Name:     token,
				Provider: "brew",
				Package:  token,
				Options:  map[string]string{brewKindOption: brewKindCask},
			},
			Version: lookupBrewListVersion(versions, token),
		})
	}
	return tools, nil
}

func (p *Provider) installedCaskTokens(ctx context.Context) ([]string, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", "list", "--cask")
	if err != nil {
		return nil, fmt.Errorf("brew list --cask: %w", err)
	}
	return strings.Fields(stdout), nil
}

func (p *Provider) installedCaskTokenSet(ctx context.Context) (map[string]struct{}, error) {
	tokens, err := p.installedCaskTokens(ctx)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[strings.ToLower(token)] = struct{}{}
	}
	return set, nil
}

func parseBrewListVersions(output string) map[string]string {
	versions := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		version := ""
		if len(fields) > 1 {
			version = strings.Join(fields[1:], " ")
		}
		pkg := strings.ToLower(fields[0])
		versions[pkg] = version
		versions[strings.ToLower(formulaName(fields[0]))] = version
	}
	return versions
}

func lookupBrewListVersion(versions map[string]string, pkg string) string {
	if version, ok := versions[strings.ToLower(pkg)]; ok {
		return version
	}
	return versions[strings.ToLower(formulaName(pkg))]
}

// InstalledMetadataMap returns explicitly installed formulae and casks with
// provider metadata learned from Homebrew's JSON. Some casks install via a pkg
// artifact and uninstall through pkgutil, which may require sudo even though
// normal brew formula operations do not.
func (p *Provider) InstalledMetadataMap(ctx context.Context) (map[string]provider.InstalledMetadata, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out, err := p.info(ctx, "info", "--json=v2", "--installed")
	if err != nil {
		return nil, err
	}
	caskTokens, err := p.installedCaskTokenSet(ctx)
	if err != nil {
		return nil, err
	}

	metadata := make(map[string]provider.InstalledMetadata, len(out.Formulae)+len(caskTokens))
	for _, f := range out.Formulae {
		if len(f.Installed) == 0 || !f.Installed[0].InstalledOnRequest {
			continue
		}
		name := f.Name
		if name == "" {
			name = formulaName(f.FullName)
		}
		if name == "" {
			continue
		}
		metadata[strings.ToLower(name)] = provider.InstalledMetadata{
			Version:      f.Installed[0].Version,
			Source:       brewSourceHint(f.Homepage, f.URLs.Stable.URL),
			ArtifactKind: brewKindFormula,
		}
	}

	seenCasks := make(map[string]struct{}, len(caskTokens))
	for _, c := range out.Casks {
		key := strings.ToLower(c.Token)
		if key == "" {
			continue
		}
		if _, ok := caskTokens[key]; !ok {
			continue
		}
		entry := provider.InstalledMetadata{
			Version:      c.Installed,
			Source:       brewSourceHint(c.Homepage, c.URL),
			ArtifactKind: brewKindCask,
		}
		if plan := c.privilegePlan(provider.PrivilegeActionUninstall); plan.RequiresPrivilege() {
			entry.Privilege = plan
		}
		metadata[key] = entry
		seenCasks[key] = struct{}{}
	}
	for token := range caskTokens {
		if _, ok := seenCasks[token]; !ok {
			metadata[token] = provider.InstalledMetadata{ArtifactKind: brewKindCask}
		}
	}
	return metadata, nil
}

func (p *Provider) info(ctx context.Context, args ...string) (brewInfoOutput, error) {
	stdout, _, err := p.exec.Run(ctx, "brew", args...)
	if err != nil {
		return brewInfoOutput{}, fmt.Errorf("brew info: %w", err)
	}
	var out brewInfoOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return brewInfoOutput{}, fmt.Errorf("parsing brew info output: %w", err)
	}
	return out, nil
}

func (p *Provider) PrivilegePlan(ctx context.Context, action provider.PrivilegeAction, tool provider.Tool) (provider.PrivilegePlan, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out, err := p.info(ctx, "info", "--json=v2", "--cask", tool.EffectivePackage())
	if err != nil {
		return provider.PrivilegePlan{}, nil
	}
	for _, cask := range out.Casks {
		if !strings.EqualFold(cask.Token, tool.EffectivePackage()) {
			continue
		}
		return cask.privilegePlan(action), nil
	}
	return provider.PrivilegePlan{}, nil
}

func (p *Provider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	verb := "uninstall"
	noAsk := false
	switch action {
	case provider.PrivilegeActionInstall:
		verb = "install"
		noAsk = true
	case provider.PrivilegeActionUpgrade:
		verb = "upgrade"
		noAsk = true
	}
	args := []string{verb, "--cask"}
	if noAsk {
		args = append(args, "--no-ask")
	}
	args = append(args, tool.EffectivePackage())
	return "brew", args, true
}

func (c brewCaskInfo) privilegePlan(action provider.PrivilegeAction) provider.PrivilegePlan {
	if len(c.Artifacts) == 0 {
		return provider.PrivilegePlan{}
	}
	var reason string
	switch action {
	case provider.PrivilegeActionInstall:
		if c.hasArtifact("pkg") {
			reason = "brew cask " + c.Token + " uses a pkg installer"
		}
	case provider.PrivilegeActionUninstall:
		switch {
		case c.hasUninstallKey("pkgutil"):
			reason = "brew cask " + c.Token + " uses pkgutil uninstall"
		case c.hasArtifact("pkg"):
			reason = "brew cask " + c.Token + " uses a pkg installer"
		}
	case provider.PrivilegeActionUpgrade:
		switch {
		case c.hasArtifact("pkg"):
			reason = "brew cask " + c.Token + " uses a pkg installer"
		case c.hasUninstallKey("pkgutil"):
			reason = "brew cask " + c.Token + " uses pkgutil uninstall"
		}
	}
	if reason == "" {
		return provider.PrivilegePlan{}
	}
	return provider.PrivilegePlan{Requirement: provider.PrivilegeMaybe, Reason: reason}
}

func (c brewCaskInfo) hasArtifact(name string) bool {
	for _, artifact := range c.Artifacts {
		if _, ok := artifact[name]; ok {
			return true
		}
	}
	return false
}

func (c brewCaskInfo) hasUninstallKey(name string) bool {
	for _, artifact := range c.Artifacts {
		raw, ok := artifact["uninstall"]
		if !ok {
			continue
		}
		if rawObjectHasKey(raw, name) {
			return true
		}
	}
	return false
}

func rawObjectHasKey(raw json.RawMessage, key string) bool {
	var object any
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	return objectHasKey(object, key)
}

func objectHasKey(v any, key string) bool {
	switch typed := v.(type) {
	case map[string]any:
		for k, value := range typed {
			if k == key || objectHasKey(value, key) {
				return true
			}
		}
	case []any:
		for _, value := range typed {
			if objectHasKey(value, key) {
				return true
			}
		}
	}
	return false
}

// brewOutdatedOutput is the shape of `brew outdated --json=v2`.
type brewOutdatedOutput struct {
	Formulae []struct {
		Name           string `json:"name"`
		CurrentVersion string `json:"current_version"` // confusingly, this is the LATEST available version
	} `json:"formulae"`
	Casks []struct {
		Name           string `json:"name"`
		CurrentVersion string `json:"current_version"` // latest available version
	} `json:"casks"`
}

// OutdatedMap returns lowercase name → latest available version for outdated formulae and casks.
func (p *Provider) OutdatedMap(ctx context.Context) (map[string]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// --greedy includes casks that auto-update or pin to :latest; without it
	// `brew outdated` silently skips them, so those casks would never be flagged.
	stdout, _, err := p.exec.Run(ctx, "brew", "outdated", "--json=v2", "--greedy")
	if err != nil {
		return nil, fmt.Errorf("brew outdated: %w", err)
	}
	var out brewOutdatedOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		return nil, fmt.Errorf("parsing brew outdated: %w", err)
	}
	m := make(map[string]string, len(out.Formulae)+len(out.Casks))
	for _, f := range out.Formulae {
		m[strings.ToLower(formulaName(f.Name))] = f.CurrentVersion
	}
	for _, c := range out.Casks {
		m[strings.ToLower(formulaName(c.Name))] = c.CurrentVersion
	}
	return m, nil
}

// ResolveTap reports the tap-qualified full name and tap for a formula when it
// originates from a non-core tap. Used to backfill config entries that were
// stored with a bare package name (which loses the tap origin and breaks
// tap-trust). Returns ok=false for core formulae, casks, or unknown names.
func (p *Provider) ResolveTap(ctx context.Context, name string) (fullName string, tap string, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out, err := p.info(ctx, "info", "--json=v2", name)
	if err != nil {
		return "", "", false
	}
	for _, f := range out.Formulae {
		full := strings.TrimSpace(f.FullName)
		if full == "" || !strings.Contains(full, "/") {
			continue
		}
		parts := strings.Split(full, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
			continue
		}
		return full, parts[0] + "/" + parts[1], true
	}
	return "", "", false
}

// Describe fetches a one-line description via `brew info --json=v2`.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out, err := p.info(ctx, "info", "--json=v2", tool.EffectivePackage())
	if err != nil {
		return "", fmt.Errorf("brew info %s: %w", tool.EffectivePackage(), err)
	}
	if len(out.Formulae) > 0 {
		return out.Formulae[0].Desc, nil
	}
	if len(out.Casks) > 0 {
		return out.Casks[0].Desc, nil
	}
	return "", nil
}

// BulkDescribe fetches descriptions for multiple tools via a single `brew info --json=v2` call.
// Implements provider.BulkDescriber.
func (p *Provider) BulkDescribe(ctx context.Context, tools []provider.Tool) (map[string]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	args := make([]string, 0, len(tools)+2)
	args = append(args, "info", "--json=v2")
	for _, t := range tools {
		args = append(args, t.EffectivePackage())
	}
	out, err := p.info(ctx, args...)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.Formulae)+len(out.Casks))
	for _, f := range out.Formulae {
		if f.Desc != "" {
			m[strings.ToLower(f.Name)] = f.Desc
		}
	}
	for _, c := range out.Casks {
		if c.Desc != "" {
			m[strings.ToLower(c.Token)] = c.Desc
		}
	}
	return m, nil
}

// RefreshMetadata runs `brew update` to pull the latest formula and tap index
// so OutdatedMap can detect newly published versions, including those in
// personal taps that brew does not auto-refresh on `brew outdated`.
func (p *Provider) RefreshMetadata(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "update", "--quiet")
	if err != nil {
		return fmt.Errorf("brew update: %w\n%s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) Tap(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "tap", name)
	if err != nil {
		return fmt.Errorf("brew tap %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

// Trust marks a tap as trusted (Homebrew 5.2+/6.0 tap-trust). A tap declared in
// config is one the user opted into, so omni trusts it on their behalf — without
// this, short-name installs/outdated checks from the tap are refused once
// tap-trust is enforced. On older Homebrew without the `brew trust` subcommand
// this is a no-op (the trust model does not exist yet).
func (p *Provider) Trust(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "trust", name)
	if err != nil {
		if brewSubcommandUnsupported(stderr) {
			return nil // older Homebrew: no tap-trust to apply
		}
		return fmt.Errorf("brew trust %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

// brewSubcommandUnsupported reports whether stderr indicates the brew subcommand
// does not exist on this Homebrew version.
func brewSubcommandUnsupported(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "unknown command") || strings.Contains(s, "unknown subcommand")
}

func (p *Provider) Untap(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, stderr, err := p.exec.Run(ctx, "brew", "untap", name)
	if err != nil {
		return fmt.Errorf("brew untap %s: %w\n%s", name, err, strings.TrimSpace(stderr))
	}
	return nil
}

func (p *Provider) ListTaps(ctx context.Context) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stdout, _, err := p.exec.Run(ctx, "brew", "tap")
	if err != nil {
		return nil, fmt.Errorf("brew tap: %w", err)
	}
	var taps []string
	for _, line := range strings.Split(stdout, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			taps = append(taps, t)
		}
	}
	return taps, nil
}

func (p *Provider) IsTapped(ctx context.Context, name string) (bool, error) {
	taps, err := p.ListTaps(ctx)
	if err != nil {
		return false, err
	}
	for _, t := range taps {
		if t == name {
			return true, nil
		}
	}
	return false, nil
}

func (p *Provider) Search(ctx context.Context, query string) ([]provider.SearchResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stdout, stderr, err := p.exec.Run(ctx, "brew", "search", query)
	if err != nil {
		if isSearchNotFound(stdout) || isSearchNotFound(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("brew search: %w", err)
	}
	var results []provider.SearchResult
	kind := ""
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line {
		case "==> Formulae":
			kind = brewKindFormula
			continue
		case "==> Casks":
			kind = brewKindCask
			continue
		}
		if strings.HasPrefix(line, "==>") {
			kind = ""
			continue
		}
		// Each output line may contain multiple space-separated formula names.
		for _, name := range strings.Fields(line) {
			result := provider.SearchResult{Name: name, Provider: "brew"}
			if kind != "" {
				result.Options = map[string]string{brewKindOption: kind}
			}
			results = append(results, result)
		}
	}
	p.enrichSearchResults(ctx, results)
	return results, nil
}

func isSearchNotFound(output string) bool {
	msg := strings.ToLower(output)
	return strings.Contains(msg, "no formulae or casks found") ||
		strings.Contains(msg, "no formulae found") ||
		strings.Contains(msg, "no casks found")
}

func (p *Provider) enrichSearchResults(ctx context.Context, results []provider.SearchResult) {
	if len(results) == 0 {
		return
	}
	args := make([]string, 0, len(results)+2)
	args = append(args, "info", "--json=v2")
	for _, result := range results {
		args = append(args, result.Name)
	}
	out, err := p.info(ctx, args...)
	if err != nil {
		return
	}
	descriptions := make(map[string]string, len(out.Formulae)+len(out.Casks))
	sources := make(map[string]provider.SourceMetadata, len(out.Formulae)+len(out.Casks))
	privileges := make(map[string]provider.PrivilegePlan, len(out.Casks))
	for _, f := range out.Formulae {
		name := f.Name
		if name == "" {
			name = formulaName(f.FullName)
		}
		if name != "" {
			key := strings.ToLower(name)
			if f.Desc != "" {
				descriptions[key] = f.Desc
			}
			if source := brewSourceHint(f.Homepage, f.URLs.Stable.URL); source.Type != "" {
				sources[key] = source
			}
		}
	}
	for _, c := range out.Casks {
		if c.Token == "" {
			continue
		}
		key := strings.ToLower(c.Token)
		if c.Desc != "" {
			descriptions[key] = c.Desc
		}
		if source := brewSourceHint(c.Homepage, c.URL); source.Type != "" {
			sources[key] = source
		}
		if plan := c.privilegePlan(provider.PrivilegeActionInstall); plan.RequiresPrivilege() {
			privileges[key] = plan
		}
	}
	for i := range results {
		key := strings.ToLower(results[i].Name)
		if desc := descriptions[key]; desc != "" {
			results[i].Description = desc
		}
		if source := sources[key]; source.Type != "" {
			results[i].Source = source
		}
		if plan := privileges[key]; plan.RequiresPrivilege() {
			results[i].Privilege = plan
		}
	}
}

func brewSourceHint(values ...string) provider.SourceMetadata {
	for _, value := range values {
		source := githubSourceHint(value)
		if source.Type != "" {
			return source
		}
	}
	return provider.SourceMetadata{}
}

func githubSourceHint(raw string) provider.SourceMetadata {
	value := strings.TrimSpace(raw)
	if value == "" {
		return provider.SourceMetadata{}
	}
	value = strings.TrimPrefix(value, "git+")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "git@")
	value = strings.TrimPrefix(value, "www.")
	switch {
	case strings.HasPrefix(value, "github.com:"):
		value = strings.TrimPrefix(value, "github.com:")
	case strings.HasPrefix(value, "github.com/"):
		value = strings.TrimPrefix(value, "github.com/")
	default:
		return provider.SourceMetadata{}
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return provider.SourceMetadata{}
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if repo == "" {
		return provider.SourceMetadata{}
	}
	return provider.SourceMetadata{
		Type:  provider.SourceTypeGitHub,
		Owner: parts[0],
		Repo:  repo,
		URL:   "https://github.com/" + parts[0] + "/" + repo,
	}
}

// formulaName returns the bare formula name from a package path.
// "hashicorp/tap/terraform" → "terraform", "git" → "git"
func formulaName(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// parseBrewVersion extracts the installed version from `brew list --versions` output.
// Input: "ripgrep 14.1.1" → "14.1.1"
func parseBrewVersion(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return ""
}
