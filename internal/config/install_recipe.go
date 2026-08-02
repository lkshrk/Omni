package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CurlFetch blocks plaintext redirects because fetched bytes may be executed or trusted.
const CurlFetch = `curl -fsSL --proto-redir =https`

// MaterializeInstallSpec — A spec whose options.install is already set is returned unchanged.
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
	checksumPattern := strings.TrimSpace(recipe.ChecksumAssetPattern)
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
	version := strings.TrimPrefix(tag, "v")
	resolveLatest := tag == "" && strings.Contains(pattern+checksumPattern, "{version}")
	if version == "" && !resolveLatest {
		version = "latest"
	}
	binaryPath := strings.TrimSpace(recipe.BinaryPath)
	extractDir := optionValue(spec.Options, "extract_dir")
	stripComponents := optionValue(spec.Options, "strip_components")
	if checksumPattern != "" && extractDir != "" {
		return spec, fmt.Errorf("github_release_asset for %q cannot combine recipe.checksum_asset_pattern with options.extract_dir", logicalName)
	}

	var install string
	downloadURL := strings.TrimSpace(recipe.AssetDownloadURL)
	if downloadURL != "" && !IsHTTPSURL(downloadURL) {
		return spec, fmt.Errorf("github_release_asset for %q: %s", logicalName, errAssetDownloadURLScheme)
	}
	dynamicPattern := strings.Contains(pattern+checksumPattern, "{arch}") || strings.Contains(pattern+checksumPattern, "{os}") || resolveLatest
	if downloadURL != "" {
		if checksumPattern != "" && dynamicPattern {
			install = githubReleaseAssetResolvedVerifiedInstall(
				owner, repo, tag, version, downloadURL, checksumPattern, binary, binDir, binaryPath,
				optionValue(spec.Options, "arch_map"),
			)
		} else {
			checksumURL := ""
			if checksumPattern != "" {
				checksumAsset := expandRecipePlaceholders(checksumPattern, binary, version)
				checksumURL = githubReleaseAssetURL(owner, repo, tag, checksumAsset)
			}
			install = githubReleaseAssetInstallCommand(downloadURL, checksumURL, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents)
		}
	} else if dynamicPattern {
		install = githubReleaseAssetArchAwareInstall(
			owner, repo, tag, version, pattern, checksumPattern, binary, binDir, cacheDir, binaryPath,
			extractDir, stripComponents, optionValue(spec.Options, "arch_map"),
		)
	} else {
		filename := expandRecipePlaceholders(pattern, binary, version)
		downloadURL := githubReleaseAssetURL(owner, repo, tag, filename)
		checksumURL := ""
		if checksumPattern != "" {
			checksumAsset := expandRecipePlaceholders(checksumPattern, binary, version)
			checksumURL = githubReleaseAssetURL(owner, repo, tag, checksumAsset)
		}
		install = githubReleaseAssetInstallCommand(downloadURL, checksumURL, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents)
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
	out.Options[OptionBin] = binary
	if recorded := recipeRecordedVersion(recipe.InstalledVersion, tag); recorded != "" {
		out.Options[OptionRecordedVersion] = recorded
	}
	return out, nil
}

// OptionRecordedVersion names the literal installed version a recipe already knows, so providers need not probe the binary.
const OptionRecordedVersion = "recorded_version"

// OptionBin names the installed executable, which a recipe knows but the logical tool name may not match.
const OptionBin = "bin"

func recipeRecordedVersion(installedVersion, tag string) string {
	if installed := strings.TrimSpace(installedVersion); installed != "" {
		return strings.TrimPrefix(installed, "v")
	}
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

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

func githubReleaseAssetArchAwareInstall(owner, repo, tag, version, pattern, checksumPattern, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents, archMapRaw string) string {
	assetExpr := githubAssetPatternExprShell(pattern, version, binary)
	releaseBase := githubReleaseDownloadBase(owner, repo, tag)
	prefix := fmt.Sprintf(`mkdir -p %s %s`, shellSingleQuote(binDir), shellSingleQuote(cacheDir))
	releaseBaseExpr := shellSingleQuote(releaseBase)
	if version == "" {
		prefix += ` && ` + githubLatestReleaseResolve(owner, repo)
		releaseBaseExpr = `"$release_base"`
	}
	if strings.Contains(pattern+checksumPattern, "{os}") {
		prefix += ` && os=$(uname -s | tr '[:upper:]' '[:lower:]')`
	}
	if strings.Contains(pattern+checksumPattern, "{arch}") || version != "" {
		prefix += ` && ` + buildArchCaseStatement(parseArchMap(archMapRaw))
	}
	prefix += ` && asset=` + assetExpr
	if checksumPattern != "" {
		checksumExpr := githubAssetPatternExprShell(checksumPattern, version, binary)
		prefix += ` && checksum_asset=` + checksumExpr + ` && asset_url=` + releaseBaseExpr + `/"$asset" && checksum_url=` + releaseBaseExpr + `/"$checksum_asset"`
		return githubReleaseAssetVerifiedInstallCommand(prefix, binary, binDir, binaryPath, isArchiveAssetPattern(pattern))
	}

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
			`tmp="$(mktemp -d)" && `+CurlFetch+` %s/"$asset" -o %s/"$asset" && case "$asset" in *.zip) unzip -q %s/"$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf %s/"$asset" -C %s%s ;; *.tar.xz) tar -xJf %s/"$asset" -C %s%s ;; *) cp %s/"$asset" "$tmp/" ;; esac`,
			releaseBaseExpr, shellSingleQuote(cacheDir), shellSingleQuote(cacheDir),
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
		` && `+CurlFetch+` %s/"$asset" -o %s/%s && chmod +x %s/%s`,
		releaseBaseExpr, shellSingleQuote(binDir), shellSingleQuote(binary),
		shellSingleQuote(binDir), shellSingleQuote(binary),
	)
}

func githubReleaseAssetResolvedVerifiedInstall(owner, repo, tag, version, downloadURL, checksumPattern, binary, binDir, binaryPath, archMapRaw string) string {
	assetName := filepath.Base(downloadURL)
	prefix := `mkdir -p ` + shellSingleQuote(binDir)
	releaseBaseExpr := shellSingleQuote(githubReleaseDownloadBase(owner, repo, tag))
	if version == "" {
		prefix += ` && ` + githubLatestReleaseResolve(owner, repo)
		releaseBaseExpr = `"$release_base"`
	}
	if strings.Contains(checksumPattern, "{os}") {
		prefix += ` && os=$(uname -s | tr '[:upper:]' '[:lower:]')`
	}
	if strings.Contains(checksumPattern, "{arch}") {
		prefix += ` && ` + buildArchCaseStatement(parseArchMap(archMapRaw))
	}
	checksumExpr := githubAssetPatternExprShell(checksumPattern, version, binary)
	prefix += ` && asset=` + shellSingleQuote(assetName) + ` && asset_url=` + shellSingleQuote(downloadURL) + ` && checksum_asset=` + checksumExpr + ` && checksum_url=` + releaseBaseExpr + `/"$checksum_asset"`
	return githubReleaseAssetVerifiedInstallCommand(prefix, binary, binDir, binaryPath, isArchiveAssetPattern(assetName))
}

func githubLatestReleaseResolve(owner, repo string) string {
	latestURL := fmt.Sprintf("https://github.com/%s/%s/releases/latest", owner, repo)
	tagBase := fmt.Sprintf("https://github.com/%s/%s/releases/tag/", owner, repo)
	downloadBase := fmt.Sprintf("https://github.com/%s/%s/releases/download", owner, repo)
	return `release_url="$(` + CurlFetch + ` -o /dev/null -w '%{url_effective}' ` + shellSingleQuote(latestURL) + `)" && ` +
		`tag="${release_url##*/}" && case "$release_url" in ` + shellSingleQuote(tagBase) + `"$tag") ;; *) echo "invalid latest release redirect" >&2; exit 1 ;; esac && ` +
		`case "$tag" in ""|*[!A-Za-z0-9._+@%~-]*) echo "invalid latest release tag" >&2; exit 1 ;; esac && ` +
		`version="${tag#v}" && release_base=` + shellSingleQuote(downloadBase) + `/"$tag"`
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
		if suite != "" {
			sourcesLine := strings.ReplaceAll(format, "{suite}", suite)
			setup = fmt.Sprintf(
				`install -m 0755 -d /etc/apt/keyrings && `+CurlFetch+` %s -o %s && chmod a+r %s && printf '%%s\n' %s > /etc/apt/sources.list.d/omni-%s.list && apt-get update`,
				shellSingleQuote(keyURL), shellSingleQuote(signedBy), shellSingleQuote(signedBy),
				shellSingleQuote(sourcesLine), shellSingleQuote(logicalName),
			)
		} else {
			setup = fmt.Sprintf(
				`install -m 0755 -d /etc/apt/keyrings && `+CurlFetch+` %s -o %s && chmod a+r %s && suite=$({ . /etc/os-release; echo "${VERSION_CODENAME:-${UBUNTU_CODENAME:-stable}}"; }) && printf '%%s\n' %s | sed "s/{suite}/$suite/g" > /etc/apt/sources.list.d/omni-%s.list && apt-get update`,
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

func githubAssetPatternExprShell(pattern, version, binary string) string {
	versionExpr := shellSingleQuote(version)
	if version == "" {
		versionExpr = `"$version"`
	}
	values := map[string]string{
		"{arch}":    `"$a"`,
		"{os}":      `"$os"`,
		"{version}": versionExpr,
		"{binary}":  shellSingleQuote(binary),
	}
	var expression strings.Builder
	for pattern != "" {
		nextIndex := len(pattern)
		nextToken := ""
		for token := range values {
			if index := strings.Index(pattern, token); index >= 0 && index < nextIndex {
				nextIndex = index
				nextToken = token
			}
		}
		if nextIndex > 0 {
			expression.WriteString(shellSingleQuote(pattern[:nextIndex]))
		}
		if nextToken == "" {
			break
		}
		expression.WriteString(values[nextToken])
		pattern = pattern[nextIndex+len(nextToken):]
	}
	if expression.Len() == 0 {
		return "''"
	}
	return expression.String()
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

func expandRecipePlaceholders(pattern, binary, version string) string {
	out := strings.ReplaceAll(pattern, "{arch}", runtime.GOARCH)
	out = strings.ReplaceAll(out, "{os}", runtime.GOOS)
	out = strings.ReplaceAll(out, "{version}", version)
	out = strings.ReplaceAll(out, "{binary}", binary)
	return out
}

func githubReleaseAssetVerifiedInstallCommand(prefix, binary, binDir, binaryPath string, archive bool) string {
	selectBinary := `found="$tmp/$asset_name"`
	if archive {
		match := binary
		if binaryPath != "" {
			match = binaryPath
		}
		selectBinary = `extract="$tmp/extract" && mkdir "$extract" && case "$asset_name" in *.zip) unzip -q "$tmp/$asset_name" -d "$extract" ;; *.tar.gz|*.tgz) tar -xzf "$tmp/$asset_name" -C "$extract" ;; *.tar.xz) tar -xJf "$tmp/$asset_name" -C "$extract" ;; esac && found="$(find "$extract" -type f -perm -111 -path ` + shellSingleQuote("*/"+match) + ` | head -n 1)" && test -n "$found"`
	}
	destination := filepath.Join(binDir, binary)
	stagePattern := filepath.Join(binDir, "."+binary+".XXXXXX")
	return prefix + ` && tmp="$(mktemp -d)" && staged= && trap 'rm -rf "$tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi' 0 && asset_name="${asset##*/}" && ` +
		CurlFetch + ` "$asset_url" -o "$tmp/$asset_name" && ` +
		CurlFetch + ` "$checksum_url" -o "$tmp/checksums" && ` +
		`digests="$(awk -v target="$asset_name" '{ line=$0; sub(/\r$/, "", line); separator=index(line, " "); if (!separator) next; marker=substr(line, separator, 2); if (marker != "  " && marker != " *") next; if (substr(line, separator + 2) == target) print substr(line, 1, separator - 1) }' "$tmp/checksums")" && count="$(printf '%s\n' "$digests" | awk 'NF { count++ } END { print count + 0 }')" && ` +
		`if [ "$count" -eq 0 ]; then echo "checksum manifest has no entry for $asset_name" >&2; exit 1; fi && if [ "$count" -ne 1 ]; then echo "checksum manifest has duplicate entries for $asset_name" >&2; exit 1; fi && expected="$digests" && if [ "${#expected}" -ne 64 ]; then echo "checksum manifest has malformed digest for $asset_name" >&2; exit 1; fi && case "$expected" in *[!0123456789abcdefABCDEF]*) echo "checksum manifest has malformed digest for $asset_name" >&2; exit 1 ;; esac && ` +
		`if command -v sha256sum >/dev/null 2>&1; then actual="$(sha256sum "$tmp/$asset_name" | awk '{print $1}')"; elif command -v shasum >/dev/null 2>&1; then actual="$(shasum -a 256 "$tmp/$asset_name" | awk '{print $1}')"; else echo "SHA-256 verification requires sha256sum or shasum" >&2; exit 1; fi && expected="$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')" && if [ "$actual" != "$expected" ]; then echo "checksum mismatch for $asset_name" >&2; exit 1; fi && ` +
		selectBinary + ` && staged="$(mktemp ` + shellSingleQuote(stagePattern) + `)" && install -m 0755 "$found" "$staged" && mv -f "$staged" ` + shellSingleQuote(destination) + ` && staged=`
}

func githubReleaseAssetInstallCommand(downloadURL, checksumURL, binary, binDir, cacheDir, binaryPath, extractDir, stripComponents string) string {
	assetName := filepath.Base(downloadURL)
	if strings.TrimSpace(assetName) == "" || assetName == "." || assetName == "/" {
		return ""
	}
	if checksumURL != "" {
		prefix := fmt.Sprintf(
			`mkdir -p %s && asset=%s && asset_url=%s && checksum_url=%s`,
			shellSingleQuote(binDir), shellSingleQuote(assetName), shellSingleQuote(downloadURL), shellSingleQuote(checksumURL),
		)
		return githubReleaseAssetVerifiedInstallCommand(prefix, binary, binDir, binaryPath, isArchiveAssetPattern(assetName))
	}
	if !isArchiveAssetPattern(assetName) {
		return fmt.Sprintf(
			`mkdir -p %s && `+CurlFetch+` %s -o %s/%s && chmod +x %s/%s`,
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
			`mkdir -p %s %s && asset=%s/%s && `+CurlFetch+` %s -o "$asset" && case "$asset" in *.zip) unzip -q "$asset" -d %s ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C %s%s ;; *.tar.xz) tar -xJf "$asset" -C %s%s ;; esac && test -x %s/%s`,
			shellSingleQuote(binDir), shellSingleQuote(cacheDir),
			shellSingleQuote(cacheDir), shellSingleQuote(assetName),
			shellSingleQuote(downloadURL), shellSingleQuote(extractDir),
			shellSingleQuote(extractDir), stripFlag,
			shellSingleQuote(extractDir), stripFlag,
			shellSingleQuote(binDir), shellSingleQuote(binary),
		)
	}
	return fmt.Sprintf(
		`mkdir -p %s %s && asset=%s/%s && `+CurlFetch+` %s -o "$asset" && tmp="$(mktemp -d)" && case "$asset" in *.zip) unzip -q "$asset" -d "$tmp" ;; *.tar.gz|*.tgz) tar -xzf "$asset" -C "$tmp" ;; *.tar.xz) tar -xJf "$asset" -C "$tmp" ;; *) cp "$asset" "$tmp/" ;; esac && %s`,
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
