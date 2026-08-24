//go:build integration

package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
	"gopkg.in/yaml.v3"
)

func TestAgentsOnboardLifecycleOnlyAPM(t *testing.T) {
	apmPath, err := exec.LookPath("apm")
	if err != nil {
		t.Fatalf("lifecycle-only APM is required on PATH: %v", err)
	}
	help, err := exec.Command(apmPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(help), "  import ") {
		t.Fatal("lifecycle-only APM unexpectedly exposes import")
	}
	versionOut, err := exec.Command(apmPath, "--version").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(versionOut), "0.28.0+omni.8") {
		t.Fatalf("unexpected lifecycle build: %s", versionOut)
	}
	originalCheck := onboardingPinnedAPMCheck
	onboardingPinnedAPMCheck = func(context.Context, *App) error { return nil }
	defer func() { onboardingPinnedAPMCheck = originalCheck }()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNI_HOSTNAME", "onboard-e2e")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("PATH", filepath.Dir(apmPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := filepath.Join(home, "dotfiles-repo")
	sourceRoot := filepath.Join(repo, "dotfiles", "claude", ".claude")
	targetRoot := filepath.Join(home, ".claude")
	files := map[string]string{
		"packages/pkg/apm.yml":                      "name: adopted-package\nversion: 1.0.0\nincludes: auto\n",
		"packages/pkg/.apm/agents/package.agent.md": "# Package agent\n",
		"plugins/plug/.claude-plugin/plugin.json":   `{"name":"adopted-plugin","version":"1.0.0"}`,
		"plugins/plug/commands/plugin-command.md":   "plugin command\n",
		"skills/demo/SKILL.md":                      "---\nname: demo\ndescription: demo\n---\n",
		"agents/standalone.md":                      "# Standalone agent\n",
		"commands/standalone.md":                    "standalone command\n",
		"hooks/onboard.json":                        `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo onboard"}]}]}}`,
	}
	for rel, content := range files {
		source := filepath.Join(sourceRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(targetRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{"version": 22, "settings": map[string]any{"dots_repo": repo}, "groups": []any{map[string]any{"name": "onboard-e2e", "dots": []any{map[string]any{"name": "claude", "path": "~/.claude"}}}}}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(configPath)
	a.StateDir = filepath.Join(home, ".local", "state", "omni")
	a.CacheDir = filepath.Join(home, ".cache", "omni")
	ctx := context.Background()
	planPath := filepath.Join(home, "plan.json")
	preview, err := a.AgentsOnboardPlan(ctx, AgentsOnboardOptions{PlanJSON: planPath})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Envelope.Plan == nil || len(preview.Envelope.Plan.Items) != 6 {
		t.Fatalf("items=%+v", preview.Envelope.Plan)
	}
	plan := *preview.Envelope.Plan
	for i := range plan.Items {
		plan.Items[i].Resolution.Decision = "move-to-apm"
		plan.Items[i].Resolution.ApprovedTargets = []string{"claude"}
	}
	if _, err := a.AgentsOnboardApplyReviewed(ctx, plan); err != nil {
		if recovery, ok := err.(*OnboardingRecoveryError); ok {
			t.Fatalf("%v cause=%v", err, recovery.Cause)
		}
		t.Fatal(err)
	}
	committed, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(committed), `"version": 24`) {
		t.Fatalf("v24 not committed: %s", committed)
	}
	expected := []string{filepath.Join(targetRoot, "agents", "package.md"), filepath.Join(targetRoot, "commands", "plugin-command.md"), filepath.Join(targetRoot, "skills", "demo", "SKILL.md"), filepath.Join(targetRoot, "agents", "standalone.md"), filepath.Join(targetRoot, "commands", "standalone.md"), filepath.Join(targetRoot, "settings.json")}
	for _, path := range expected {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("APM did not deploy %s: %v; tree=%v", path, err, snapshotOnboardTestTree(t, targetRoot))
		}
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	client := a.APMClient(apm.Global)
	if _, err := client.InstallOnly(ctx, apm.SurfacePackages, []string{"claude"}, apm.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range expected {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("APM did not restore tracked deployment %s: %v", path, err)
		}
	}
	second, err := a.AgentsOnboardPlan(ctx, AgentsOnboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Envelope.Plan == nil || len(second.Envelope.Plan.Items) != 0 {
		t.Fatalf("second plan not no-op: %+v", second.Envelope.Plan)
	}
}

func TestOnboardGeneratedMarketplacePluginInstallsWithPinnedAPM(t *testing.T) {
	apmPath, err := exec.LookPath("apm")
	if err != nil {
		t.Fatalf("pinned APM is required on PATH: %v", err)
	}
	versionOut, err := exec.Command(apmPath, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOut), "0.28.0+omni.8") {
		t.Fatalf("expected pinned APM 0.28.0+omni.8: err=%v output=%s", err, versionOut)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	market := filepath.Join(root, "claude-plugins-official")
	plugin := filepath.Join(market, "packages", "superpowers")
	for _, dir := range []string{filepath.Join(home, ".apm"), filepath.Join(plugin, "skills", "superpowers")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"superpowers","version":"1.0.0","description":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "skills", "superpowers", "SKILL.md"), []byte("---\nname: superpowers\ndescription: fixture\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marketManifest := "name: claude-plugins-official\nversion: 1.0.0\nlicense: MIT\nmarketplace:\n  owner: {name: anthropics, url: https://example.invalid/anthropics}\n  outputs: {claude: {}}\n  packages:\n    - name: superpowers\n      description: fixture\n      source: ./packages/superpowers\n      version: 1.0.0\n"
	if err := os.WriteFile(filepath.Join(market, "apm.yml"), []byte(marketManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := exec.Command(apmPath, "pack", "--marketplace=claude")
	pack.Dir = market
	pack.Env = os.Environ()
	if output, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("pack fixture marketplace: %v\n%s", err, output)
	}

	marketPayload, err := json.Marshal(map[string]string{"name": "claude-plugins-official", "source": market})
	if err != nil {
		t.Fatal(err)
	}
	items := []OnboardItem{
		{ID: strings.Repeat("1", 64), Kind: "marketplace", Payload: marketPayload, Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"claude"}}},
		{ID: strings.Repeat("2", 64), Kind: "plugin", Name: "superpowers", Payload: json.RawMessage(`{"name":"superpowers","marketplace":"claude-plugins-official"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"claude"}}},
	}
	manifest, markets, blockers, err := buildOnboardManifest(nil, items)
	if err != nil || len(blockers) != 0 {
		t.Fatalf("build onboarding manifest: err=%v blockers=%v", err, blockers)
	}
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	client := (&App{}).APMClient(apm.Global)
	if err := registerOnboardMarketplaces(t.Context(), client, markets); err != nil {
		t.Fatalf("register generated marketplace: %v", err)
	}
	preview, err := client.InstallOnly(t.Context(), apm.SurfacePackages, nil, apm.InstallOptions{DryRun: true})
	if err != nil {
		t.Fatalf("preview generated dependency: %v\nstdout:\n%s\nstderr:\n%s\nmanifest:\n%s", err, preview.Stdout, preview.Stderr, manifest)
	}
	result, err := client.InstallOnly(t.Context(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if err != nil {
		t.Fatalf("install generated dependency: %v\nstdout:\n%s\nstderr:\n%s\nmanifest:\n%s", err, result.Stdout, result.Stderr, manifest)
	}
	lock, err := os.ReadFile(filepath.Join(home, ".apm", "apm.lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Dependencies []struct {
			MarketplacePluginName string   `yaml:"marketplace_plugin_name"`
			TargetSubset          []string `yaml:"target_subset"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(lock, &decoded); err != nil {
		t.Fatal(err)
	}
	locked := false
	for _, dependency := range decoded.Dependencies {
		if dependency.MarketplacePluginName == "superpowers" && slices.Equal(dependency.TargetSubset, []string{"claude"}) {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatalf("marketplace dependency was not locked with target_subset=[claude]:\n%s", lock)
	}
}
