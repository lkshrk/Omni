package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MaterializeInstallSpec expands a recipe-backed install candidate into concrete
// provider options. When options.install is already set the spec is returned
// unchanged.
func MaterializeInstallSpec(logicalName string, spec ToolInstallSpec, fallbackBinDir string) (ToolInstallSpec, error) {
	if spec.Options != nil && strings.TrimSpace(spec.Options["install"]) != "" {
		return spec, nil
	}
	if spec.Recipe == nil || strings.TrimSpace(spec.Recipe.Type) == "" {
		return spec, nil
	}
	switch spec.Recipe.Type {
	case FallbackRecipeCurlInstallScript:
		return materializeCurlInstallScript(logicalName, spec)
	case FallbackRecipeGitHubReleaseAsset:
		return materializeGitHubReleaseAsset(logicalName, spec, fallbackBinDir)
	case FallbackRecipeAptRepo:
		return materializeAptRepo(logicalName, spec)
	default:
		return spec, nil
	}
}

func materializeCurlInstallScript(logicalName string, spec ToolInstallSpec) (ToolInstallSpec, error) {
	url := optionValue(spec.Options, "url")
	if url == "" && spec.Source != nil {
		url = strings.TrimSpace(spec.Source.URL)
	}
	if url == "" {
		return spec, fmt.Errorf("curl_install_script for %q requires options.url or source.url", logicalName)
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
	// bash, not sh: upstream installers (bun, nvm) use bashisms like pipefail
	// that dash rejects when piped, since the shebang never applies.
	install := envPrefix + `curl -fsSL ` + shellSingleQuote(url) + ` | bash`
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
	binary := strings.TrimSpace(spec.Bin)
	if binary == "" {
		binary = logicalName
	}
	binDir := strings.TrimSpace(spec.BinDir)
	if binDir == "" {
		binDir = strings.TrimSpace(fallbackBinDir)
	}
	if binDir == "" {
		binDir = "~/.local/bin"
	}
	binDir = expandTilde(binDir)
	cacheDir := filepath.Join(filepath.Dir(binDir), "cache")
	tag := githubReleaseTag(&recipe, spec.Options)
	binaryPath := strings.TrimSpace(recipe.BinaryPath)
	extractDir := optionValue(spec.Options, "extract_dir")
	stripComponents := optionValue(spec.Options, "strip_components")

	var install string
	if strings.Contains(pattern, "{arch}") || strings.Contains(pattern, "{os}") {
		install = githubReleaseAssetArchAwareInstall(
			owner, repo, tag, pattern, binary, binDir, cacheDir, binaryPath,
			extractDir, stripComponents, optionValue(spec.Options, "arch_map"),
		)
	} else {
		filename := expandRecipePlaceholders(pattern, binary)
		downloadURL := githubReleaseAssetURL(owner, repo, tag, filename)
		install = githubReleaseAssetInstallCommand(downloadURL, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents)
	}

	check := optionValue(spec.Options, "check")
	if check == "" {
		check = fmt.Sprintf(`test -x %s/%s`, shellSingleQuote(binDir), shellSingleQuote(binary))
	}
	uninstall := strings.TrimSpace(spec.Options["uninstall"])
	if uninstall == "" {
		uninstall = fmt.Sprintf(`rm -f %s/%s`, shellSingleQuote(binDir), shellSingleQuote(binary))
	}

	out := spec
	out.Provider = "script"
	if out.Options == nil {
		out.Options = make(map[string]string)
	}
	out.Options["install"] = install
	out.Options["check"] = check
	out.Options["uninstall"] = uninstall
	out.Options["upgrade"] = install
	return out, nil
}

func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func githubReleaseTag(recipe *FallbackRecipe, opts map[string]string) string {
	if recipe != nil {
		if tag := strings.TrimSpace(recipe.TagName); tag != "" {
			return tag
		}
	}
	if tag := optionValue(opts, "release_tag"); tag != "" {
		return tag
	}
	return ""
}

func githubReleaseAssetURL(owner, repo, tag, asset string) string {
	base := githubReleaseDownloadBase(owner, repo, tag)
	return base + "/" + asset
}

func githubReleaseDownloadBase(owner, repo, tag string) string {
	if tag == "" {
		return fmt.Sprintf("https://github.com/%s/%s/releases/latest/download", owner, repo)
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s", owner, repo, tag)
}

func githubReleaseAssetArchAwareInstall(owner, repo, tag, pattern, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents, archMapRaw string) string {
	assetExpr := githubAssetPatternExprShell(pattern)
	archCase := buildArchCaseStatement(parseArchMap(archMapRaw))
	releaseBase := githubReleaseDownloadBase(owner, repo, tag)
	prefix := fmt.Sprintf(`mkdir -p %s %s`, shellSingleQuote(binDir), shellSingleQuote(cacheDir))
	if strings.Contains(pattern, "{os}") {
		prefix += ` && os=$(uname -s | tr '[:upper:]' '[:lower:]')`
	}
	prefix += ` && ` + archCase + ` && asset=` + assetExpr

	if isArchiveAssetPattern(pattern) {
		tarTarget := extractDir
		if tarTarget == "" {
			tarTarget = `"$tmp"`
		}
		stripFlag := ""
		if stripComponents != "" && stripComponents != "0" {
			stripFlag = ` --strip-components=` + stripComponents
		}
		extract := fmt.Sprintf(
			`tmp="$(mktemp -d)" && curl -fsSL %s/"$asset" -o %s/"$asset" && case "$asset" in *.zip) unzip -q %s/"$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf %s/"$asset" -C %s%s ;; *.tar.xz) tar -xJf %s/"$asset" -C %s%s ;; *) cp %s/"$asset" "$tmp/" ;; esac`,
			shellSingleQuote(releaseBase), shellSingleQuote(cacheDir), shellSingleQuote(cacheDir),
			shellSingleQuote(cacheDir), tarTarget, stripFlag,
			shellSingleQuote(cacheDir), tarTarget, stripFlag,
			shellSingleQuote(cacheDir),
		)
		if extractDir != "" {
			return prefix + ` && ` + extract + ` && test -x ` + shellSingleQuote(binDir) + `/` + shellSingleQuote(binary)
		}
		findClause := `found="$(find "$tmp" -type f -perm -111`
		if binaryPath != "" {
			findClause += ` -path "*/` + binaryPath + `"`
		} else {
			findClause += ` -name ` + shellSingleQuote(binary)
		}
		findClause += ` | head -n 1)" && test -n "$found" && cp "$found" ` + shellSingleQuote(binDir) + `/` + shellSingleQuote(binary) + ` && chmod +x ` + shellSingleQuote(binDir) + `/` + shellSingleQuote(binary)
		return prefix + ` && ` + extract + ` && ` + findClause
	}
	return prefix + fmt.Sprintf(
		` && curl -fsSL %s/"$asset" -o %s/%s && chmod +x %s/%s`,
		shellSingleQuote(releaseBase), shellSingleQuote(binDir), shellSingleQuote(binary),
		shellSingleQuote(binDir), shellSingleQuote(binary),
	)
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
	setup := strings.TrimSpace(spec.Options["setup"])
	if setup == "" {
		arch := runtime.GOARCH
		suite := optionValue(spec.Options, "suite")
		format := strings.ReplaceAll(sourcesFormat, "{arch}", arch)
		format = strings.ReplaceAll(format, "{signed_by}", signedBy)
		if suite != "" {
			sourcesLine := strings.ReplaceAll(format, "{suite}", suite)
			setup = fmt.Sprintf(
				`install -m 0755 -d /etc/apt/keyrings && curl -fsSL %s -o %s && chmod a+r %s && printf '%%s\n' %s > /etc/apt/sources.list.d/omni-%s.list && apt-get update`,
				shellSingleQuote(keyURL), shellSingleQuote(signedBy), shellSingleQuote(signedBy),
				shellSingleQuote(sourcesLine), shellSingleQuote(logicalName),
			)
		} else {
			setup = fmt.Sprintf(
				`install -m 0755 -d /etc/apt/keyrings && curl -fsSL %s -o %s && chmod a+r %s && suite=$({ . /etc/os-release; echo "${VERSION_CODENAME:-${UBUNTU_CODENAME:-stable}}"; }) && printf '%%s\n' %s | sed "s/{suite}/$suite/g" > /etc/apt/sources.list.d/omni-%s.list && apt-get update`,
				shellSingleQuote(keyURL), shellSingleQuote(signedBy), shellSingleQuote(signedBy),
				shellSingleQuote(format), shellSingleQuote(logicalName),
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

func githubAssetPatternExprShell(pattern string) string {
	base := strings.ReplaceAll(pattern, "{arch}", "${a}")
	base = strings.ReplaceAll(base, "{os}", "${os}")
	base = strings.ReplaceAll(base, "{version}", "latest")
	return `"` + base + `"`
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

func buildArchCaseStatement(archMap map[string]string) string {
	groups := make(map[string][]string)
	for uname, alias := range archMap {
		groups[alias] = append(groups[alias], uname)
	}
	var cases []string
	for alias, unames := range groups {
		cases = append(cases, strings.Join(unames, "|")+`) a=`+alias)
	}
	cases = append(cases, `*) a="$arch"`)
	return `arch=$(uname -m) && case "$arch" in ` + strings.Join(cases, " ;; ") + ` ;; esac`
}

func isArchiveAssetPattern(pattern string) bool {
	lower := strings.ToLower(pattern)
	return strings.Contains(lower, ".tar.") || strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tgz")
}

func expandRecipePlaceholders(pattern, binary string) string {
	out := strings.ReplaceAll(pattern, "{arch}", runtime.GOARCH)
	out = strings.ReplaceAll(out, "{os}", runtime.GOOS)
	out = strings.ReplaceAll(out, "{version}", "latest")
	out = strings.ReplaceAll(out, "{binary}", binary)
	return out
}

func githubReleaseAssetInstallCommand(downloadURL, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents string) string {
	assetName := filepath.Base(downloadURL)
	if strings.TrimSpace(assetName) == "" || assetName == "." || assetName == "/" {
		return ""
	}
	if !isArchiveAssetPattern(assetName) {
		return fmt.Sprintf(
			`mkdir -p %s && curl -fsSL %s -o %s/%s && chmod +x %s/%s`,
			shellSingleQuote(binDir), shellSingleQuote(downloadURL),
			shellSingleQuote(binDir), shellSingleQuote(binary),
			shellSingleQuote(binDir), shellSingleQuote(binary),
		)
	}
	stripFlag := ""
	if stripComponents != "" && stripComponents != "0" {
		stripFlag = ` --strip-components=` + stripComponents
	}
	extract := `found="$(find "$tmp" -type f -perm -111`
	if binaryPath != "" {
		extract += ` -path "*/` + binaryPath + `"`
	} else {
		extract += ` -name ` + shellSingleQuote(binary)
	}
	extract += ` | head -n 1)" && test -n "$found" && cp "$found" ` + shellSingleQuote(binDir) + `/` + shellSingleQuote(binary) + ` && chmod +x ` + shellSingleQuote(binDir) + `/` + shellSingleQuote(binary)
	if extractDir != "" {
		return fmt.Sprintf(
			`mkdir -p %s %s && asset=%s/%s && curl -fsSL %s -o "$asset" && case "$asset" in *.zip) unzip -q "$asset" -d %s ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C %s%s ;; *.tar.xz) tar -xJf "$asset" -C %s%s ;; esac && test -x %s/%s`,
			shellSingleQuote(binDir), shellSingleQuote(cacheDir),
			shellSingleQuote(cacheDir), shellSingleQuote(assetName),
			shellSingleQuote(downloadURL), shellSingleQuote(extractDir),
			shellSingleQuote(extractDir), stripFlag,
			shellSingleQuote(extractDir), stripFlag,
			shellSingleQuote(binDir), shellSingleQuote(binary),
		)
	}
	return fmt.Sprintf(
		`mkdir -p %s %s && asset=%s/%s && curl -fsSL %s -o "$asset" && tmp="$(mktemp -d)" && case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *.tar.xz) tar -xJf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/" ;; esac && %s`,
		shellSingleQuote(binDir), shellSingleQuote(cacheDir),
		shellSingleQuote(cacheDir), shellSingleQuote(assetName),
		shellSingleQuote(downloadURL), extract,
	)
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
