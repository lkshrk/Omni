package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestMaterializeInstallSpec_CurlInstallScript(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options: map[string]string{
			"url":        "https://example.com/install.sh",
			"check_path": "/usr/local/bin/tool",
			"env":        "FOO=bar",
		},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "script" {
		t.Fatalf("provider = %q, want script", got.Provider)
	}
	if !strings.Contains(got.Options["install"], "https://example.com/install.sh") {
		t.Fatalf("install = %q, want curl install script", got.Options["install"])
	}
	if !strings.Contains(got.Options["check"], "/usr/local/bin/tool") {
		t.Fatalf("check = %q, want check_path", got.Options["check"])
	}
	if !strings.HasPrefix(got.Options["install"], "export FOO=bar; ") {
		t.Fatalf("install = %q, want env export prefix", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetArchAware(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "eza-community",
			Repo:  "eza",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "eza_{arch}-unknown-linux-gnu.tar.gz",
		},
	}
	got, err := config.MaterializeInstallSpec("eza", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "uname -m") {
		t.Fatalf("install = %q, want arch detection", got.Options["install"])
	}
	if !strings.Contains(got.Options["install"], "eza-community/eza/releases/latest/download") {
		t.Fatalf("install = %q, want latest release URL", got.Options["install"])
	}
	if strings.Contains(got.Options["install"], "url_effective") {
		t.Fatalf("install = %q, want patterns without {version} unchanged", got.Options["install"])
	}
	if !strings.Contains(got.Options["install"], filepath.Join(home, ".local", "bin")) {
		t.Fatalf("install = %q, want expanded home directory", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_AptRepoRejectsInsecureKeyURL(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options: map[string]string{
			"key_url":        "http://example.com/key.asc",
			"signed_by":      "/etc/apt/keyrings/example.asc",
			"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
			"packages":       "example-cli",
		},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
	}
	got, err := config.MaterializeInstallSpec("example-cli", spec, "")
	if err == nil {
		t.Fatalf("MaterializeInstallSpec = %+v, want an error for a plain-http key_url", got)
	}
	if !strings.Contains(err.Error(), "key_url must use https") {
		t.Fatalf("err = %v, want an https key_url error", err)
	}
	if strings.Contains(got.Options["setup"], "http://example.com/key.asc") {
		t.Fatalf("setup = %q, want no materialized command for a rejected key_url", got.Options["setup"])
	}
}

func TestMaterializeInstallSpec_AptRepo(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options: map[string]string{
			"key_url":        "https://example.com/key.asc",
			"signed_by":      "/etc/apt/keyrings/example.asc",
			"sources_format": "deb [arch={arch} signed-by={signed_by}] https://example.com/debian {suite} stable",
			"packages":       "example-cli",
		},
		Recipe: &config.FallbackRecipe{Type: config.FallbackRecipeAptRepo},
	}
	got, err := config.MaterializeInstallSpec("example-cli", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Provider != "apt_repo" {
		t.Fatalf("provider = %q, want apt_repo", got.Provider)
	}
	if got.Options["setup"] == "" || got.Options["packages"] != "example-cli" {
		t.Fatalf("options = %+v, want generated setup and packages", got.Options)
	}
	if strings.Contains(got.Options["setup"], "${line//") {
		t.Fatalf("setup = %q, want POSIX shell syntax", got.Options["setup"])
	}
	if !strings.Contains(got.Options["setup"], `sed "s/{suite}/$suite/g"`) {
		t.Fatalf("setup = %q, want suite substitution through sed", got.Options["setup"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetPinnedTag(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "jesseduffield",
			Repo:  "lazygit",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "lazygit_0.62.2_linux_{arch}.tar.gz",
			TagName:      "v0.62.2",
		},
		Bin: "lazygit",
		Options: map[string]string{
			"arch_map": "aarch64:arm64,x86_64:x86_64,arm64:arm64,amd64:x86_64",
		},
	}
	got, err := config.MaterializeInstallSpec("lazygit", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "releases/download/v0.62.2") {
		t.Fatalf("install = %q, want pinned tag URL", got.Options["install"])
	}
	if strings.Contains(got.Options["install"], "url_effective") {
		t.Fatalf("install = %q, want pinned recipe unchanged", got.Options["install"])
	}
	if got.Options[config.OptionRecordedVersion] != "0.62.2" {
		t.Fatalf("recorded version = %q, want %q", got.Options[config.OptionRecordedVersion], "0.62.2")
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetRecordsInstalledVersionOverTag(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "rhysd", Repo: "actionlint"},
		Recipe: &config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern:     "actionlint_{version}_linux_amd64.tar.gz",
			TagName:          "v1.7.12",
			InstalledVersion: "v1.7.11",
		},
		Bin: "actionlint",
	}
	got, err := config.MaterializeInstallSpec("actionlint", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Options[config.OptionRecordedVersion] != "1.7.11" {
		t.Fatalf("recorded version = %q, want %q", got.Options[config.OptionRecordedVersion], "1.7.11")
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetWithoutPinRecordsNoVersion(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "rhysd", Repo: "actionlint"},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "actionlint_{version}_linux_amd64.tar.gz",
		},
		Bin: "actionlint",
	}
	got, err := config.MaterializeInstallSpec("actionlint", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if _, ok := got.Options[config.OptionRecordedVersion]; ok {
		t.Fatalf("recorded version = %q, want unset for an unpinned recipe", got.Options[config.OptionRecordedVersion])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetExpandsPinnedVersion(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "gh_{version}_linux_amd64.tar.gz",
			TagName:      "v2.93.0",
		},
		Bin: "gh",
	}
	got, err := config.MaterializeInstallSpec("gh", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "gh_2.93.0_linux_amd64.tar.gz") {
		t.Fatalf("install = %q, want asset version expanded from tag", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetResolvesLatestVersion(t *testing.T) {
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fakeBin, "uname"), "#!/bin/sh\necho x86_64\n")
	writeExecutable(t, filepath.Join(fakeBin, "curl"), `#!/bin/sh
out=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -w) printf '%s' "$FAKE_RELEASE_URL"; exit 0 ;;
    -o) out="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
printf '%s\n' "$url" > "$FAKE_CURL_LOG"
contents="$out.contents"
mkdir -p "$contents"
printf '#!/bin/sh\nexit 0\n' > "$contents/tool"
chmod +x "$contents/tool"
tar -czf "$out" -C "$contents" tool
`)
	logPath := filepath.Join(tmp, "curl.log")
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "tool_{version}_linux_{arch}.tar.gz",
		},
		Bin:    "tool",
		BinDir: filepath.Join(tmp, "install"),
		Options: map[string]string{
			"arch_map": "x86_64:amd64",
		},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if strings.Contains(got.Options["install"], "tool_latest_") {
		t.Fatalf("install = %q, want no latest substitution in asset filename", got.Options["install"])
	}
	cmd := exec.Command("sh", "-c", got.Options["install"])
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"FAKE_RELEASE_URL=https://github.com/owner/repo/releases/tag/v1.2.3",
		"FAKE_CURL_LOG="+logPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated install command failed: %v\n%s\ncommand: %s", err, output, got.Options["install"])
	}
	requested, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/owner/repo/releases/download/v1.2.3/tool_1.2.3_linux_amd64.tar.gz\n"
	if string(requested) != want {
		t.Fatalf("asset URL = %q, want %q", requested, want)
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetRejectsUnsafeRedirectTag(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "injected")
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fakeBin, "curl"), "#!/bin/sh\nprintf '%s' \"$FAKE_RELEASE_URL\"\n")
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "tool_{version}_linux_amd64",
		},
		Bin:    "tool",
		BinDir: filepath.Join(tmp, "install"),
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	cmd := exec.Command("sh", "-c", got.Options["install"])
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"FAKE_RELEASE_URL=https://github.com/owner/repo/releases/tag/v1$(touch "+marker+")",
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("generated install command succeeded with unsafe redirect tag\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("redirected release tag executed shell substitution; marker stat error = %v", err)
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetDoesNotExecuteTagSubstitution(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "injected")
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCurl := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    : > "$2"
    exit 0
  fi
  shift
done
exit 1
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "owner", Repo: "repo"},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "tool_{version}_{arch}",
			TagName:      "v1$(touch " + marker + ")",
		},
		Bin:    "tool",
		BinDir: filepath.Join(tmp, "install"),
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	cmd := exec.Command("sh", "-c", got.Options["install"])
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+":/usr/bin:/bin")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated install command failed: %v\n%s\ncommand: %s", err, output, got.Options["install"])
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("remote release tag executed shell substitution; marker stat error = %v", err)
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetRawBinaryArchMap(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "mikefarah",
			Repo:  "yq",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "yq_{arch}",
		},
		Bin: "yq",
		Options: map[string]string{
			"arch_map": "aarch64:linux_arm64,x86_64:linux_amd64,arm64:linux_arm64,amd64:linux_amd64",
		},
	}
	got, err := config.MaterializeInstallSpec("yq", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if strings.Contains(got.Options["install"], "mktemp -d") {
		t.Fatalf("install = %q, want raw binary install without archive extract", got.Options["install"])
	}
	if !strings.Contains(got.Options["install"], "linux_amd64") {
		t.Fatalf("install = %q, want arch alias in generated asset name", got.Options["install"])
	}
}

func TestMaterializeInstallSpec_GitHubReleaseAssetOSArch(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Source: &config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "lkshrk",
			Repo:  "omni",
		},
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "omni_{os}_{arch}.tar.gz",
		},
		Bin: "omni",
		Options: map[string]string{
			"arch_map": "aarch64:arm64,x86_64:x86_64,arm64:arm64,amd64:x86_64",
		},
	}
	got, err := config.MaterializeInstallSpec("omni", spec, "~/.local/bin")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if !strings.Contains(got.Options["install"], "uname -s") {
		t.Fatalf("install = %q, want runtime os detection", got.Options["install"])
	}
}

func TestGitHubReleaseAssetNameUsesUnameArchAlias(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("x86_64 uname alias regression applies to amd64 hosts")
	}
	spec := config.ToolInstallSpec{
		Provider: "script",
		Recipe: &config.FallbackRecipe{
			Type:         config.FallbackRecipeGitHubReleaseAsset,
			AssetPattern: "tool_{arch}.tar.gz",
		},
		Options: map[string]string{"arch_map": "x86_64:x64"},
	}
	got, err := config.GitHubReleaseAssetName("tool", spec, "v1.0.0")
	if err != nil {
		t.Fatalf("GitHubReleaseAssetName: %v", err)
	}
	if got != "tool_x64.tar.gz" {
		t.Fatalf("asset name = %q, want uname-mapped x86_64 asset", got)
	}
}

func TestMaterializeInstallSpec_PreservesExplicitInstall(t *testing.T) {
	spec := config.ToolInstallSpec{
		Provider: "script",
		Options:  map[string]string{"install": "true"},
		Recipe:   &config.FallbackRecipe{Type: config.FallbackRecipeCurlInstallScript},
	}
	got, err := config.MaterializeInstallSpec("tool", spec, "")
	if err != nil {
		t.Fatalf("MaterializeInstallSpec: %v", err)
	}
	if got.Options["install"] != "true" {
		t.Fatalf("install = %q, want unchanged", got.Options["install"])
	}
}
