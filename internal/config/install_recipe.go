package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"runtime"
	"strings"
)

const (
	ProviderGitHubReleaseAsset   = "github_release_asset"
	OptionGitHubReleaseAssetSpec = "_omni_github_release_asset_spec"
)

// CurlFetch blocks plaintext redirects because fetched bytes may be executed or trusted.
const CurlFetch = `curl -fsSL --proto-redir =https`

// MaterializeInstallSpec — Explicit shell installs win except for native GitHub recipes.
func MaterializeInstallSpec(logicalName string, spec ToolInstallSpec, fallbackBinDir string) (ToolInstallSpec, error) {
	if spec.Recipe != nil && spec.Recipe.Type == FallbackRecipeGitHubReleaseAsset {
		return materializeGitHubReleaseAsset(logicalName, spec, fallbackBinDir)
	}
	if spec.Options != nil && strings.TrimSpace(spec.Options["install"]) != "" {
		return spec, nil
	}
	if spec.Recipe == nil || strings.TrimSpace(spec.Recipe.Type) == "" {
		return spec, nil
	}
	switch spec.Recipe.Type {
	case FallbackRecipeCurlInstallScript:
		return materializeCurlInstallScript(logicalName, spec)
	case FallbackRecipeAptRepo:
		return materializeAptRepo(logicalName, spec)
	default:
		return spec, nil
	}
}

func curlInstallScriptURL(spec ToolInstallSpec) string {
	if url := optionValue(spec.Options, "url"); url != "" {
		return url
	}
	if spec.Source != nil {
		return strings.TrimSpace(spec.Source.URL)
	}
	return ""
}

func materializeCurlInstallScript(logicalName string, spec ToolInstallSpec) (ToolInstallSpec, error) {
	url := curlInstallScriptURL(spec)
	if url == "" {
		return spec, fmt.Errorf("curl_install_script for %q requires options.url or source.url", logicalName)
	}
	// Re-checked at the sink: materialization is reachable without ValidateRoot having run.
	if !IsHTTPSURL(url) {
		return spec, fmt.Errorf("curl_install_script for %q: %s", logicalName, errCurlInstallScriptURLScheme)
	}
	checkPath := optionValue(spec.Options, "check_path")
	if checkPath == "" {
		bin := strings.TrimSpace(spec.Bin)
		if bin == "" {
			bin = logicalName
		}
		checkPath = bin
	}
	envPrefix := curlInstallEnvPrefix(spec.Options)
	// bash, not sh: piping skips the shebang, and upstream installers use bashisms dash rejects.
	install := envPrefix + CurlFetch + ` ` + shellSingleQuote(url) + ` | bash`
	check := envPrefix + `command -v ` + shellSingleQuote(checkPath) + ` >/dev/null 2>&1`
	if path := optionValue(spec.Options, "check_path"); path != "" && strings.Contains(path, "/") {
		check = envPrefix + `test -x ` + shellSingleQuote(path)
	}
	uninstall := strings.TrimSpace(spec.Options["uninstall"])
	out := spec
	out.Provider = "script"
	if out.Options == nil {
		out.Options = make(map[string]string)
	}
	out.Options["install"] = install
	out.Options["check"] = check
	out.Options[OptionBin] = checkPath
	if uninstall != "" {
		out.Options["uninstall"] = uninstall
	}
	return out, nil
}

func materializeGitHubReleaseAsset(logicalName string, spec ToolInstallSpec, fallbackBinDir string) (ToolInstallSpec, error) {
	if spec.Source == nil || spec.Source.Type != FallbackSourceGitHub {
		return spec, fmt.Errorf("github_release_asset for %q requires source.type github", logicalName)
	}
	owner := strings.TrimSpace(spec.Source.Owner)
	repo := strings.TrimSpace(spec.Source.Repo)
	if owner == "" || repo == "" {
		return spec, fmt.Errorf("github_release_asset for %q requires source.owner and source.repo", logicalName)
	}
	recipe := *spec.Recipe
	pattern := strings.TrimSpace(recipe.AssetPattern)
	if pattern == "" {
		return spec, fmt.Errorf("github_release_asset for %q requires recipe.asset_pattern", logicalName)
	}
	if downloadURL := strings.TrimSpace(recipe.AssetDownloadURL); downloadURL != "" && !IsHTTPSURL(downloadURL) {
		return spec, fmt.Errorf("github_release_asset for %q: %s", logicalName, errAssetDownloadURLScheme)
	}
	if strings.TrimSpace(recipe.ChecksumAssetPattern) != "" && optionValue(spec.Options, "extract_dir") != "" {
		return spec, fmt.Errorf("github_release_asset for %q cannot combine recipe.checksum_asset_pattern with options.extract_dir", logicalName)
	}
	// The native extractor selects the configured binary at any archive depth, which subsumes
	// legacy extract_dir/strip_components without extracting unrelated archive contents.
	if strings.TrimSpace(spec.BinDir) == "" {
		spec.BinDir = strings.TrimSpace(fallbackBinDir)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return spec, fmt.Errorf("github_release_asset for %q: encode native recipe: %w", logicalName, err)
	}
	spec.Provider = "script"
	spec.InstallWith = ProviderGitHubReleaseAsset
	spec.Options = maps.Clone(spec.Options)
	if spec.Options == nil {
		spec.Options = make(map[string]string)
	}
	spec.Options[OptionGitHubReleaseAssetSpec] = string(encoded)
	return spec, nil
}

// OptionRecordedVersion names the literal installed version a recipe already knows, so providers need not probe the binary.
const OptionRecordedVersion = "recorded_version"

// OptionBin names the installed executable, which a recipe knows but the logical tool name may not match.
const OptionBin = "bin"

// GitHubReleaseAssetName — Uses the same architecture mapping as command materialization so callers match the configured asset exactly.
func GitHubReleaseAssetName(logicalName string, spec ToolInstallSpec, tag string) (string, error) {
	if spec.Recipe == nil || spec.Recipe.Type != FallbackRecipeGitHubReleaseAsset {
		return "", fmt.Errorf("github release asset recipe is required for %q", logicalName)
	}
	pattern := strings.TrimSpace(spec.Recipe.AssetPattern)
	if pattern == "" {
		return "", fmt.Errorf("github_release_asset for %q requires recipe.asset_pattern", logicalName)
	}
	binary := strings.TrimSpace(spec.Bin)
	if binary == "" {
		binary = logicalName
	}
	version := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if version == "" {
		version = "latest"
	}
	arch := currentMappedArch(parseArchMap(optionValue(spec.Options, "arch_map")))
	name := strings.ReplaceAll(pattern, "{arch}", arch)
	name = strings.ReplaceAll(name, "{os}", runtime.GOOS)
	name = strings.ReplaceAll(name, "{version}", version)
	name = strings.ReplaceAll(name, "{binary}", binary)
	return name, nil
}

func currentMappedArch(archMap map[string]string) string {
	aliases := []string{runtime.GOARCH}
	switch runtime.GOARCH {
	case "amd64":
		aliases = []string{"x86_64", "amd64"}
	case "arm64":
		if runtime.GOOS == "darwin" {
			aliases = []string{"arm64", "aarch64"}
		} else {
			aliases = []string{"aarch64", "arm64"}
		}
	case "arm":
		aliases = []string{"armv7l", "armv6l", "arm"}
	}
	for _, alias := range aliases {
		if mapped := strings.TrimSpace(archMap[alias]); mapped != "" {
			return mapped
		}
	}
	return runtime.GOARCH
}

func materializeAptRepo(logicalName string, spec ToolInstallSpec) (ToolInstallSpec, error) {
	keyURL := optionValue(spec.Options, "key_url")
	signedBy := optionValue(spec.Options, "signed_by")
	sourcesFormat := optionValue(spec.Options, "sources_format")
	packages := optionValue(spec.Options, "packages")
	if packages == "" {
		packages = spec.EffectivePackage(logicalName)
	}
	if keyURL == "" || signedBy == "" || sourcesFormat == "" || packages == "" {
		return spec, fmt.Errorf("apt_repo for %q requires options key_url, signed_by, sources_format, and packages", logicalName)
	}
	// Re-checked at the sink: materialization is reachable without ValidateRoot having run.
	if !IsHTTPSURL(keyURL) {
		return spec, fmt.Errorf("apt_repo for %q: %s", logicalName, errAptRepoKeyURLScheme)
	}
	setup := strings.TrimSpace(spec.Options["setup"])
	if setup == "" {
		arch := runtime.GOARCH
		suite := optionValue(spec.Options, "suite")
		format := strings.ReplaceAll(sourcesFormat, "{arch}", arch)
		format = strings.ReplaceAll(format, "{signed_by}", signedBy)
		// apt picks the parser by extension: one-line entries need .list, deb822 stanzas need .sources.
		ext := "list"
		if isDeb822Sources(sourcesFormat) {
			ext = "sources"
		}
		if suite != "" {
			sourcesLine := strings.ReplaceAll(format, "{suite}", suite)
			setup = fmt.Sprintf(
				`install -m 0755 -d /etc/apt/keyrings && `+CurlFetch+` %s -o %s && chmod a+r %s && printf '%%s\n' %s > /etc/apt/sources.list.d/omni-%s.%s && apt-get update`,
				shellSingleQuote(keyURL), shellSingleQuote(signedBy), shellSingleQuote(signedBy),
				shellSingleQuote(sourcesLine), shellSingleQuote(logicalName), ext,
			)
		} else {
			setup = fmt.Sprintf(
				`install -m 0755 -d /etc/apt/keyrings && `+CurlFetch+` %s -o %s && chmod a+r %s && suite=$({ . /etc/os-release; echo "${VERSION_CODENAME:-${UBUNTU_CODENAME:-stable}}"; }) && printf '%%s\n' %s | sed "s/{suite}/$suite/g" > /etc/apt/sources.list.d/omni-%s.%s && apt-get update`,
				shellSingleQuote(keyURL), shellSingleQuote(signedBy), shellSingleQuote(signedBy),
				shellSingleQuote(format), shellSingleQuote(logicalName), ext,
			)
		}
	}
	out := spec
	out.Provider = "apt_repo"
	if out.Options == nil {
		out.Options = make(map[string]string)
	}
	out.Options["setup"] = setup
	out.Options["packages"] = packages
	if check := strings.TrimSpace(spec.Options["check"]); check != "" {
		out.Options["check"] = check
	}
	if upgrade := strings.TrimSpace(spec.Options["upgrade"]); upgrade != "" {
		out.Options["upgrade"] = upgrade
	}
	return out, nil
}

func isDeb822Sources(format string) bool {
	fields := strings.Fields(format)
	// deb822 stanzas open with a "Key:" field; one-line entries open with deb/deb-src.
	return len(fields) > 0 && strings.HasSuffix(fields[0], ":")
}

func parseArchMap(raw string) map[string]string {
	defaults := map[string]string{
		"aarch64": "aarch64",
		"arm64":   "aarch64",
		"x86_64":  "x86_64",
		"amd64":   "x86_64",
		"armv7l":  "armv7",
		"armv6l":  "armv7",
	}
	if strings.TrimSpace(raw) == "" {
		return defaults
	}
	out := make(map[string]string, len(defaults))
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return defaults
	}
	return out
}

func curlInstallEnvPrefix(opts map[string]string) string {
	if opts == nil {
		return ""
	}
	var exports []string
	if raw := strings.TrimSpace(opts["env"]); raw != "" {
		for _, pair := range strings.Fields(raw) {
			if strings.Contains(pair, "=") {
				exports = append(exports, "export "+pair+"; ")
			}
		}
	}
	for key, value := range opts {
		if strings.HasPrefix(key, "env.") {
			name := strings.TrimPrefix(key, "env.")
			if name != "" {
				exports = append(exports, fmt.Sprintf("export %s=%s; ", name, shellSingleQuote(value)))
			}
		}
	}
	return strings.Join(exports, "")
}

func optionValue(opts map[string]string, key string) string {
	if opts == nil {
		return ""
	}
	return strings.TrimSpace(opts[key])
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
