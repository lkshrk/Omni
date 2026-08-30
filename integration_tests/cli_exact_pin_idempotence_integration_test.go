//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryExactVersionPackageSpecsAreIdempotent(t *testing.T) {
	tests := []struct {
		name            string
		provider        string
		pinnedName      string
		pinnedSpec      string
		pinnedVersion   string
		unpinnedName    string
		unpinnedPackage string
		installLog      string
		queryLog        string
		writeStubs      func(*testing.T, string, []string) ([]string, string)
	}{
		{
			name: "apt", provider: "apt", pinnedName: "rbw", pinnedSpec: "rbw=1.13.2-7", pinnedVersion: "1.13.2-7",
			unpinnedName: "jq", unpinnedPackage: "jq", installLog: "apt-get install -y rbw=1.13.2-7", queryLog: "dpkg-query --showformat=${Version} --show rbw",
			writeStubs: writeExactPinAPTStubs,
		},
		{
			name: "scoped npm", provider: "npm", pinnedName: "scoped-cli", pinnedSpec: "@scope/name@1.2.3", pinnedVersion: "1.2.3",
			unpinnedName: "plain-cli", unpinnedPackage: "plain-cli", installLog: "npm install -g @scope/name@1.2.3", queryLog: "npm list -g --depth=0 @scope/name",
			writeStubs: writeExactPinNPMStubs,
		},
		{
			name: "python", provider: "pip", pinnedName: "example-cli", pinnedSpec: "example==1.2.3", pinnedVersion: "1.2.3",
			unpinnedName: "plain-example", unpinnedPackage: "plain-example", installLog: "pip3 install example==1.2.3", queryLog: "pip3 show example",
			writeStubs: writeExactPinPythonStubs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _, cache, env := newCLIBinarySandbox(t)
			env, pinnedState := tt.writeStubs(t, root, env)
			configPath := filepath.Join(root, "settings.json")
			if err := config.Save(configPath, exactPinConfig(tt.provider, tt.pinnedName, tt.pinnedSpec, tt.unpinnedName, tt.unpinnedPackage)); err != nil {
				t.Fatal(err)
			}

			bin := buildOmniBinary(t)
			runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "install", tt.pinnedName, "--provider", tt.provider)
			runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "install", tt.unpinnedName, "--provider", tt.provider)

			assertExactPinLog(t, filepath.Join(root, "provider.log"), tt.installLog, tt.queryLog, tt.pinnedSpec)
			assertExactPinToolState(t, bin, root, env, configPath, cache, tt.pinnedName, "installed", tt.pinnedVersion)
			assertExactPinToolState(t, bin, root, env, configPath, cache, tt.unpinnedName, "installed", "9.9.9")

			writeIntegrationFile(t, pinnedState, "0.0.1\n")
			runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "refresh")
			assertExactPinToolState(t, bin, root, env, configPath, cache, tt.pinnedName, "missing", "0.0.1")
			assertExactPinToolState(t, bin, root, env, configPath, cache, tt.unpinnedName, "installed", "9.9.9")
		})
	}
}

func exactPinConfig(providerName, pinnedName, pinnedSpec, unpinnedName, unpinnedPackage string) *config.RootConfig {
	settings := config.Settings{DisabledProviders: []string{"apk", "dnf", "pacman", "zypper", "brew", "bun", "pnpm", "npm", "uv", "pip", "cargo", "go", "gem", "script", "apt_repo"}}
	switch providerName {
	case "apt":
		settings.DisabledProviders = removeExactPinDisabled(settings.DisabledProviders, "apt")
	case "npm":
		settings.DisabledProviders = removeExactPinDisabled(settings.DisabledProviders, "npm")
	case "pip":
		settings.DisabledProviders = removeExactPinDisabled(settings.DisabledProviders, "pip")
	}
	return &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: settings,
		Hosts:    map[string][]string{"testhost": {}},
		Tools: map[string]config.ToolSpec{
			pinnedName:   {Providers: []config.ToolInstallSpec{{Provider: providerName, Package: pinnedSpec}}},
			unpinnedName: {Providers: []config.ToolInstallSpec{{Provider: providerName, Package: unpinnedPackage}}},
		},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: pinnedName}, {Name: unpinnedName}}}},
	}
}

func removeExactPinDisabled(disabled []string, name string) []string {
	out := disabled[:0]
	for _, candidate := range disabled {
		if candidate != name {
			out = append(out, candidate)
		}
	}
	return out
}

func assertExactPinLog(t *testing.T, path, wantInstall, wantQuery, fullSpec string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if !containsExactPinLine(lines, wantInstall) || !containsExactPinLine(lines, wantQuery) {
		t.Fatalf("provider calls missing install %q or query %q:\n%s", wantInstall, wantQuery, raw)
	}
	for _, line := range lines {
		if strings.Contains(line, "show "+fullSpec) || strings.Contains(line, "--depth=0 "+fullSpec) {
			t.Fatalf("installed query used full package spec %q: %s", fullSpec, line)
		}
	}
}

func containsExactPinLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func assertExactPinToolState(t *testing.T, bin, root string, env []string, configPath, cache, name, wantState, wantVersion string) {
	t.Helper()
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list", name, "--format", "json")
	var rows []struct {
		Name      string `json:"name"`
		State     string `json:"state"`
		Version   string `json:"version"`
		Installed bool   `json:"installed"`
		Tracked   bool   `json:"tracked"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode tools list: %v\n%s", err, out)
	}
	for _, row := range rows {
		if row.Name == name && row.Tracked {
			if row.State != wantState || row.Installed != (wantState == "installed") || row.Version != wantVersion {
				t.Fatalf("%s state = %+v, want state=%q version=%q", name, row, wantState, wantVersion)
			}
			return
		}
	}
	t.Fatalf("tracked row %q missing from %+v", name, rows)
}

func exactPinStubEnv(root string, env []string) ([]string, string, string) {
	binDir := filepath.Join(root, "bin")
	stateDir := filepath.Join(root, "provider-state")
	logPath := filepath.Join(root, "provider.log")
	_ = os.MkdirAll(stateDir, 0o755)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "OMNI_TEST_PROVIDER_STATE="+stateDir, "OMNI_TEST_PROVIDER_LOG="+logPath)
	return env, binDir, stateDir
}

func writeExactPinAPTStubs(t *testing.T, root string, env []string) ([]string, string) {
	t.Helper()
	env, binDir, state := exactPinStubEnv(root, env)
	writeExecutable(t, filepath.Join(binDir, "sudo"), "#!/bin/sh\n[ \"${1:-}\" = -n ] && shift\nexec \"$@\"\n")
	writeExecutable(t, filepath.Join(binDir, "apt-get"), `#!/bin/sh
set -eu
printf 'apt-get %s\n' "$*" >> "$OMNI_TEST_PROVIDER_LOG"
[ "${1:-}" = "--version" ] && { echo 'apt 2.0'; exit 0; }
spec="${3:-}"
case "$spec" in rbw=*) printf '%s\n' "${spec#*=}" > "$OMNI_TEST_PROVIDER_STATE/rbw" ;; jq) echo 9.9.9 > "$OMNI_TEST_PROVIDER_STATE/jq" ;; *) exit 64 ;; esac
`)
	writeExecutable(t, filepath.Join(binDir, "dpkg-query"), `#!/bin/sh
set -eu
printf 'dpkg-query %s\n' "$*" >> "$OMNI_TEST_PROVIDER_LOG"
if [ "${1:-}" = "-W" ]; then
  [ -f "$OMNI_TEST_PROVIDER_STATE/rbw" ] && printf 'rbw\t%s\tii \n' "$(cat "$OMNI_TEST_PROVIDER_STATE/rbw")"
  [ -f "$OMNI_TEST_PROVIDER_STATE/jq" ] && printf 'jq\t%s\tii \n' "$(cat "$OMNI_TEST_PROVIDER_STATE/jq")"
  exit 0
fi
name="${3:-}"
[ -f "$OMNI_TEST_PROVIDER_STATE/$name" ] || exit 1
cat "$OMNI_TEST_PROVIDER_STATE/$name"
`)
	writeExecutable(t, filepath.Join(binDir, "apt-mark"), `#!/bin/sh
[ -f "$OMNI_TEST_PROVIDER_STATE/rbw" ] && echo rbw
[ -f "$OMNI_TEST_PROVIDER_STATE/jq" ] && echo jq
`)
	writeExecutable(t, filepath.Join(binDir, "apt-cache"), "#!/bin/sh\nexit 0\n")
	return env, filepath.Join(state, "rbw")
}

func writeExactPinNPMStubs(t *testing.T, root string, env []string) ([]string, string) {
	t.Helper()
	env, binDir, state := exactPinStubEnv(root, env)
	writeExecutable(t, filepath.Join(binDir, "npm"), `#!/bin/sh
set -eu
printf 'npm %s\n' "$*" >> "$OMNI_TEST_PROVIDER_LOG"
[ "${1:-}" = "--version" ] && { echo 11.0.0; exit 0; }
if [ "${1:-}" = "install" ]; then
  spec="${3:-}"
  case "$spec" in @scope/name@*) printf '%s\n' "${spec##*@}" > "$OMNI_TEST_PROVIDER_STATE/scoped" ;; plain-cli) echo 9.9.9 > "$OMNI_TEST_PROVIDER_STATE/plain" ;; *) exit 64 ;; esac
  exit 0
fi
if [ "${1:-}" = "list" ]; then
  filter="${4:-}"
  echo '/test/lib:'
  { [ -z "$filter" ] || [ "$filter" = '@scope/name' ]; } && [ -f "$OMNI_TEST_PROVIDER_STATE/scoped" ] && printf '├── @scope/name@%s\n' "$(cat "$OMNI_TEST_PROVIDER_STATE/scoped")"
  { [ -z "$filter" ] || [ "$filter" = 'plain-cli' ]; } && [ -f "$OMNI_TEST_PROVIDER_STATE/plain" ] && printf '└── plain-cli@%s\n' "$(cat "$OMNI_TEST_PROVIDER_STATE/plain")"
  exit 0
fi
[ "${1:-}" = "outdated" ] && { echo '{}'; exit 0; }
exit 64
`)
	return env, filepath.Join(state, "scoped")
}

func writeExactPinPythonStubs(t *testing.T, root string, env []string) ([]string, string) {
	t.Helper()
	env, binDir, state := exactPinStubEnv(root, env)
	writeExecutable(t, filepath.Join(binDir, "pip3"), `#!/bin/sh
set -eu
printf 'pip3 %s\n' "$*" >> "$OMNI_TEST_PROVIDER_LOG"
[ "${1:-}" = "--version" ] && { echo 'pip 25.0'; exit 0; }
if [ "${1:-}" = "install" ]; then
  spec="${2:-}"
  case "$spec" in example==*) printf '%s\n' "${spec#*==}" > "$OMNI_TEST_PROVIDER_STATE/example" ;; plain-example) echo 9.9.9 > "$OMNI_TEST_PROVIDER_STATE/plain-example" ;; *) exit 64 ;; esac
  exit 0
fi
if [ "${1:-}" = "show" ]; then
  name="${2:-}"
  [ -f "$OMNI_TEST_PROVIDER_STATE/$name" ] || exit 1
  printf 'Name: %s\nVersion: %s\n' "$name" "$(cat "$OMNI_TEST_PROVIDER_STATE/$name")"
  exit 0
fi
if [ "${1:-}" = "list" ]; then
  [ "${2:-}" = "--outdated" ] && { echo '[]'; exit 0; }
  printf '['
  sep=''
  for name in example plain-example; do
    [ -f "$OMNI_TEST_PROVIDER_STATE/$name" ] || continue
    printf '%s{"name":"%s","version":"%s"}' "$sep" "$name" "$(cat "$OMNI_TEST_PROVIDER_STATE/$name")"
    sep=','
  done
  echo ']'
  exit 0
fi
exit 64
`)
	writeExecutable(t, filepath.Join(binDir, "python3"), `#!/bin/sh
set -eu
printf '{'
sep=''
for name in example plain-example; do
  [ -f "$OMNI_TEST_PROVIDER_STATE/$name" ] || continue
  printf '%s"%s":1' "$sep" "$name"
  sep=','
done
echo '}'
`)
	return env, filepath.Join(state, "example")
}
