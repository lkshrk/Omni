//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryInitCreatesAnIsolatedHostConfig(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	configPath := filepath.Join(home, ".config", "omni", "settings.json")

	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "init", "--no-import")
	if !strings.Contains(out, "Created host \"testhost\"") {
		t.Fatalf("init output missing host creation: %s", out)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load initialized config: %v", err)
	}
	if _, ok := cfg.Hosts["testhost"]; !ok {
		t.Fatalf("initialized hosts = %#v", cfg.Hosts)
	}
}

func TestCLIBinaryBootstrapDiscoversAndPersistsDefaultConfig(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "bin")
	for name, output := range map[string]string{"pnpm": "9.0.0", "pip3": "pip 24.0"} {
		writeExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\necho '"+output+"'\n")
	}
	env = replaceIntegrationEnv(env, "PATH", binDir)
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	env = replaceIntegrationEnv(env, "OMNI_CONFIG", configPath)

	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "bootstrap", "--no-import")
	if !strings.Contains(out, "Created host \"testhost\"") {
		t.Fatalf("bootstrap output missing host creation: %s", out)
	}

	runOmniCommand(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "settings", "set", "update_quarantine", "24h")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load discovered config: %v", err)
	}
	if _, ok := cfg.Hosts["testhost"]; !ok || cfg.Settings.UpdateQuarantine != "24h" {
		t.Fatalf("persisted config = hosts %#v, quarantine %q", cfg.Hosts, cfg.Settings.UpdateQuarantine)
	}
}

func TestCLIBinaryToolLifecycleUsesConfiguredProviderCommands(t *testing.T) {
	root, _, cache, env := newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "bin")
	state := filepath.Join(root, "provider-state")
	logPath := filepath.Join(root, "provider.log")
	writeExecutable(t, filepath.Join(binDir, "fake-provider"), `#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$FAKE_PROVIDER_LOG"
case "$1" in
  install) printf '1.0.0\n' > "$FAKE_PROVIDER_STATE" ;;
  check) test -f "$FAKE_PROVIDER_STATE" ;;
  version) cat "$FAKE_PROVIDER_STATE" ;;
  latest) printf '1.1.0\n' ;;
  upgrade) printf '1.1.0\n' > "$FAKE_PROVIDER_STATE" ;;
  *) exit 64 ;;
esac
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "FAKE_PROVIDER_STATE="+state, "FAKE_PROVIDER_LOG="+logPath)
	configPath := filepath.Join(root, "settings.json")
	writeIntegrationFile(t, configPath, `{
  "settings":{"disabled_providers":["apt","apk","dnf","pacman","zypper","brew","node","bun","pnpm","npm","python","uv","pip"]},
  "tools":{"fixture":{"provider":"script","options":{"install":"fake-provider install","check":"fake-provider check","version":"fake-provider version","latest":"fake-provider latest","upgrade":"fake-provider upgrade"}}},
  "hosts":{"testhost":[]},
  "groups":[{"name":"testhost","special":"host","tools":["fixture"]}]
}`)

	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "sync")
	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "refresh")
	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "upgrade", "fixture", "--force")

	if got, err := os.ReadFile(state); err != nil || strings.TrimSpace(string(got)) != "1.1.0" {
		t.Fatalf("provider state = %q, %v", got, err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, command := range []string{"install\n", "latest\n", "upgrade\n"} {
		if !strings.Contains(log, command) {
			t.Fatalf("provider log missing %q: %s", command, log)
		}
	}
	var listed []struct {
		State   string `json:"state"`
		Version string `json:"version"`
	}
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "tools", "list", "fixture", "--format", "json")
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode tool state: %v\n%s", err, out)
	}
	if len(listed) != 1 || listed[0].State != "installed" || listed[0].Version != "1.1.0" {
		t.Fatalf("tool state = %+v", listed)
	}
}

func TestCLIBinaryDotsSyncResolvesConflictInsideSandbox(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	repo := filepath.Join(root, "dots")
	configPath := filepath.Join(root, "settings.json")
	target := filepath.Join(home, ".config", "fixture", "settings.toml")
	source := filepath.Join(repo, "dotfiles", "fixture", ".config", "fixture", "settings.toml")
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, source, "managed = true\n")
	writeIntegrationFile(t, target, "managed = false\n")
	conflictTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, path := range []string{source, target} {
		if err := os.Chtimes(path, conflictTime, conflictTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name: "testhost", Special: "host",
			Dots: []config.DotEntry{{Name: "fixture", Path: filepath.Join(home, ".config", "fixture")}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	runOmniCommand(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "dots", "sync", "--use-repo")
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target is not a symlink: %v, %v", info, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "managed = true\n" {
		t.Fatalf("resolved target = %q, %v", got, err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "managed = true\n" {
		t.Fatalf("repo source changed = %q, %v", got, err)
	}
}

func TestCLIBinaryDotsStatusReportsManagedFileDrift(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	repo := filepath.Join(root, "dots")
	configPath := filepath.Join(root, "settings.json")
	target := filepath.Join(home, ".config", "fixture", "settings.toml")
	source := filepath.Join(repo, "dotfiles", "fixture", ".config", "fixture", "settings.toml")
	initDotsRepo(t, repo, env)
	writeIntegrationFile(t, source, "managed = true\n")
	writeIntegrationFile(t, target, "managed = false\n")
	localTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(target, localTime, localTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, localTime.Add(time.Hour), localTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name: "testhost", Special: "host",
			Dots: []config.DotEntry{{Name: "fixture", Path: filepath.Dir(target)}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	var result struct {
		Entries []struct {
			Name   string `json:"name"`
			Health string `json:"health"`
		} `json:"entries"`
	}
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "dots", "status", "fixture", "--format", "json")
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode dots status: %v\n%s", err, out)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "fixture" || result.Entries[0].Health != "conflict" {
		t.Fatalf("dots status did not report drift: %+v", result.Entries)
	}
}

func TestCLIBinaryAgentsAddDelegatesPackageAndSkillsToAPM(t *testing.T) {
	root, _, cache, env, logPath := agentsCommandBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "agents", "add", "owner/pkg", "--skill", "alpha", "--skill", "beta")
	assertFileContains(t, logPath, "install -g owner/pkg --skill alpha --skill beta")
	if !strings.Contains(out, "declare it in the host template") || !strings.Contains(out, "git: owner/pkg") {
		t.Fatalf("agents add omitted persistence hint: %s", out)
	}
}

func TestCLIBinaryAgentsUpdateAllDelegatesGlobalConfirmationToAPM(t *testing.T) {
	root, _, cache, env, logPath := agentsCommandBinaryFixture(t)
	runOmniCommand(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "agents", "update")
	assertFileContains(t, logPath, "update -g --yes")
}

func TestCLIBinaryAgentsRemoveDelegatesEveryPackageToAPM(t *testing.T) {
	root, _, cache, env, logPath := agentsCommandBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--cache-dir", cache, "agents", "remove", "owner/one", "owner/two")
	assertFileContains(t, logPath, "uninstall -g owner/one owner/two")
	if !strings.Contains(out, "also remove it from the host template") {
		t.Fatalf("agents remove omitted persistence hint: %s", out)
	}
}

func TestCLIBinaryDoctorDryRunPreservesConfig(t *testing.T) {
	root, _, cache, env, configPath, original := doctorBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "doctor", "--fix", "--dry-run")
	if !strings.Contains(out, "would remove") || !strings.Contains(out, "dry run: no files were changed") {
		t.Fatalf("doctor dry-run output: %s", out)
	}
	if got, err := os.ReadFile(configPath); err != nil || string(got) != original {
		t.Fatalf("doctor dry-run changed config: %v\n%s", err, got)
	}
}

func TestCLIBinaryDoctorReportsPinnedAPM(t *testing.T) {
	root, _, cache, env, configPath, _ := doctorBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "doctor")
	if !strings.Contains(out, "APM version") || !strings.Contains(out, "apm 0.29.0") {
		t.Fatalf("doctor omitted pinned APM health: %s", out)
	}
}

func TestCLIBinaryDoctorFixRemovesOnlyDuplicateDefinition(t *testing.T) {
	root, _, cache, env, configPath, _ := doctorBinaryFixture(t)
	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "doctor", "--fix")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var dev *config.GroupConfig
	for _, group := range cfg.Groups {
		if group.Name == "dev" {
			dev = group
			break
		}
	}
	if dev == nil || len(dev.Dots) != 1 || dev.Dots[0].Name != "git" {
		t.Fatalf("fixed config lost included dot definition: %+v", cfg.Groups)
	}
	var main map[string]any
	raw, err := os.ReadFile(configPath)
	if err != nil || json.Unmarshal(raw, &main) != nil {
		t.Fatalf("read fixed main config: %v", err)
	}
	groups, _ := main["groups"].([]any)
	includes, _ := main["$include"].([]any)
	if len(includes) != 1 || includes[0] != "settings.d/dots.json" {
		t.Fatalf("main include = %#v", includes)
	}
	for _, rawGroup := range groups {
		group, _ := rawGroup.(map[string]any)
		if group["name"] != "dev" {
			continue
		}
		if _, exists := group["dots"]; exists {
			t.Fatalf("duplicate dots remained in main config: %#v", group)
		}
	}
}

func TestCLIBinaryAgentsMigratePreviewDoesNotWriteState(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	configPath, snapshot := migrationBinaryFixture(t, root)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "agents", "migrate", "--host", "testhost", "--snapshot", snapshot, "--dry-run")
	if !strings.Contains(out, "name: omni-migrated") || !strings.Contains(out, "agents-migration/bundles/") {
		t.Fatalf("migration preview missing wrapper dependency: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "omni", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote template: %v", err)
	}
	if wrappers, _ := filepath.Glob(filepath.Join(home, ".local", "state", "omni", "agents-migration", "bundles", "*")); len(wrappers) != 0 {
		t.Fatalf("preview materialized wrappers: %v", wrappers)
	}
}

func TestCLIBinaryAgentsMigrateWritePublishesTemplateAndWrapper(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	configPath, snapshot := migrationBinaryFixture(t, root)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "agents", "migrate", "--host", "testhost", "--snapshot", snapshot, "--write")
	if !strings.Contains(out, "Next: omni agents sync") {
		t.Fatalf("migration write output: %s", out)
	}
	template := filepath.Join(home, ".config", "omni", "apm.yml")
	raw, err := os.ReadFile(template)
	if err != nil || !strings.Contains(string(raw), "name: omni-migrated") {
		t.Fatalf("migration template: %v\n%s", err, raw)
	}
	wrappers, err := filepath.Glob(filepath.Join(home, ".local", "state", "omni", "agents-migration", "bundles", "*", "apm.yml"))
	if err != nil || len(wrappers) != 1 {
		t.Fatalf("published wrappers = %v, %v", wrappers, err)
	}
	wrapper, err := os.ReadFile(wrappers[0])
	if err != nil || !strings.Contains(string(wrapper), "fixture-owner") {
		t.Fatalf("published wrapper manifest: %v\n%s", err, wrapper)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("migration write changed live APM state: %v", err)
	}
}

func newCLIBinarySandbox(t *testing.T) (root, home, cache string, env []string) {
	t.Helper()
	root = t.TempDir()
	home = filepath.Join(root, "home")
	cache = filepath.Join(root, "cache")
	for _, dir := range []string{home, cache, filepath.Join(home, ".config"), filepath.Join(home, ".local", "state")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env = isolatedTUIEnv(t, home, cache)
	return root, home, cache, env
}

func agentsCommandBinaryFixture(t *testing.T) (root, home, cache string, env []string, logPath string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "bin")
	logPath = filepath.Join(root, "apm.log")
	writeExecutable(t, filepath.Join(binDir, "apm"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo 'Agent Package Manager (APM) CLI version 0.29.0'
  exit 0
fi
printf '%s\n' "$*" >> "$OMNI_TEST_APM_LOG"
printf 'ok\n'
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "OMNI_TEST_APM_LOG="+logPath)
	return root, home, cache, env, logPath
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(raw), want) {
		t.Fatalf("%s missing %q:\n%s", path, want, raw)
	}
}

func replaceIntegrationEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}

func integrationEnvValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	writeIntegrationFile(t, path, content)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func doctorBinaryFixture(t *testing.T) (root, home, cache string, env []string, configPath, original string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(binDir, "apm"), "#!/bin/sh\n[ \"${1:-}\" = \"--version\" ] || exit 64\necho 'Agent Package Manager (APM) CLI version 0.29.0'\n")
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	configPath = filepath.Join(root, "settings.json")
	original = `{
  "$include":["settings.d/dots.json"],
  "settings":{"disabled_providers":["apt","apk","dnf","pacman","zypper","brew","node","bun","pnpm","npm","python","uv","pip"]},
  "hosts":{"testhost":["dev"]},
  "groups":[{"name":"testhost","special":"host"},{"name":"dev","dots":[{"name":"git","path":"~/.gitconfig"}]}]
}`
	writeIntegrationFile(t, configPath, original)
	writeIntegrationFile(t, filepath.Join(root, "settings.d", "dots.json"), `{"groups":[{"name":"dev","dots":[{"name":"git","path":"~/.gitconfig"}]}]}`)
	return root, home, cache, env, configPath, original
}

func migrationBinaryFixture(t *testing.T, root string) (configPath, snapshot string) {
	t.Helper()
	configPath = filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Hosts:   map[string][]string{"testhost": {}},
		Groups:  []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot = filepath.Join(root, "snapshot")
	original := filepath.Join(root, "legacy", "fixture-owner")
	writeIntegrationFile(t, filepath.Join(snapshot, "fixture-owner", ".codex-plugin", "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"fixture-owner","version":"1.0.0"}`)
	writeIntegrationFile(t, filepath.Join(snapshot, "fixture-owner", "mcp.json"), `{"mcpServers":{"fixture":{"type":"stdio","command":"sh","args":["bin/server.sh"],"cwd":"."}}}`)
	writeExecutable(t, filepath.Join(snapshot, "fixture-owner", "bin", "server.sh"), "#!/bin/sh\nprintf 'fixture\\n'\n")
	writeIntegrationFile(t, filepath.Join(snapshot, "omni-config-000.json"), `{
  "agents":{"plugins":[{"name":"fixture-owner","path":"`+original+`"}]},
  "groups":[{"name":"legacy","plugins":["fixture-owner"]}],
  "hosts":{"testhost":["legacy"]}
}`)
	paths, err := json.Marshal(map[string]string{"omni-config-000.json": configPath, "fixture-owner": original})
	if err != nil {
		t.Fatal(err)
	}
	writeIntegrationFile(t, filepath.Join(snapshot, "paths.json"), string(paths))
	return configPath, snapshot
}
