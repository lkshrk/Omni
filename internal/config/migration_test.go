package config

import (
	"encoding/json"
	"testing"
)

func TestConfigMigrationsCoverCurrentVersion(t *testing.T) {
	seen := make(map[int]struct{}, len(configMigrations))
	version := 0
	for version < CurrentVersion {
		step, ok := configMigrationFrom(version)
		if !ok {
			t.Fatalf("missing migration from version %d to %d", version, version+1)
		}
		if _, dup := seen[step.from]; dup {
			t.Fatalf("duplicate migration from version %d", step.from)
		}
		seen[step.from] = struct{}{}
		if step.to <= step.from {
			t.Fatalf("migration from version %d to %d does not advance", step.from, step.to)
		}
		if step.apply == nil {
			t.Fatalf("migration from version %d to %d missing typed migration", step.from, step.to)
		}
		if step.applyRaw == nil {
			t.Fatalf("migration from version %d to %d missing raw migration", step.from, step.to)
		}
		version = step.to
	}
	if version != CurrentVersion {
		t.Fatalf("migration chain ends at version %d, want %d", version, CurrentVersion)
	}
}

func TestMigrate_UsesRegisteredMigrationChain(t *testing.T) {
	cfg := &RootConfig{}
	migrated, err := Migrate(cfg)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !migrated {
		t.Fatal("Migrate migrated = false, want true for legacy config")
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
}

func TestMigrate_V11ConfigReachesCurrentVersion(t *testing.T) {
	cfg := &RootConfig{Version: 11}
	migrated, err := Migrate(cfg)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !migrated {
		t.Fatal("Migrate migrated = false, want true for v11 config")
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
}

func TestMigrateRawVersion_UsesRegisteredMigrationChain(t *testing.T) {
	raw := map[string]json.RawMessage{
		"settings": json.RawMessage(`{}`),
	}
	if err := migrateRawVersion(raw); err != nil {
		t.Fatalf("migrateRawVersion: %v", err)
	}
	version, err := rawConfigVersion(raw)
	if err != nil {
		t.Fatalf("rawConfigVersion: %v", err)
	}
	if version != CurrentVersion {
		t.Fatalf("raw version = %d, want %d", version, CurrentVersion)
	}
}

func TestMigrateV8ToV9_ExpandsFamilyDisabledProviders(t *testing.T) {
	cfg := &RootConfig{
		Version:  8,
		Settings: Settings{DisabledProviders: []string{"node", "brew"}},
		HostSettings: map[string]Settings{
			"laptop": {DisabledProviders: []string{"python"}},
		},
	}
	if _, err := Migrate(cfg); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
	wantGlobal := []string{"bun", "pnpm", "npm", "brew"}
	if got := cfg.Settings.DisabledProviders; !equalStrings(got, wantGlobal) {
		t.Errorf("global disabled = %v, want %v", got, wantGlobal)
	}
	wantHost := []string{"uv", "pip"}
	if got := cfg.HostSettings["laptop"].DisabledProviders; !equalStrings(got, wantHost) {
		t.Errorf("host disabled = %v, want %v", got, wantHost)
	}
}

func TestMigrateV8ToV9_Idempotent(t *testing.T) {
	in := []string{"bun", "pnpm", "npm", "brew"}
	if got := expandFamilyDisabledProviders(in); !equalStrings(got, in) {
		t.Errorf("expand(concrete) = %v, want unchanged %v", got, in)
	}
}

func TestMigrateRawV8ToV9_ExpandsFamilyDisabledProviders(t *testing.T) {
	raw := map[string]json.RawMessage{
		"version":       json.RawMessage(`8`),
		"settings":      json.RawMessage(`{"disabled_providers":["node"],"auto_import":true}`),
		"host_settings": json.RawMessage(`{"laptop":{"disabled_providers":["system"]}}`),
	}
	if err := migrateRawConfigV8ToV9(raw); err != nil {
		t.Fatalf("migrateRawConfigV8ToV9: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw["settings"], &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	// Unknown/sibling keys preserved.
	if string(settings["auto_import"]) != "true" {
		t.Errorf("auto_import = %s, want true (sibling key preserved)", settings["auto_import"])
	}
	var dp []string
	if err := json.Unmarshal(settings["disabled_providers"], &dp); err != nil {
		t.Fatalf("parse disabled_providers: %v", err)
	}
	if want := []string{"bun", "pnpm", "npm"}; !equalStrings(dp, want) {
		t.Errorf("settings disabled = %v, want %v", dp, want)
	}
	var hosts map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw["host_settings"], &hosts); err != nil {
		t.Fatalf("parse host_settings: %v", err)
	}
	var hostDP []string
	if err := json.Unmarshal(hosts["laptop"]["disabled_providers"], &hostDP); err != nil {
		t.Fatalf("parse host disabled: %v", err)
	}
	if want := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper"}; !equalStrings(hostDP, want) {
		t.Errorf("host disabled = %v, want %v", hostDP, want)
	}
}

func TestMigrateV13ToV14_DropsAgentConfigDots(t *testing.T) {
	cfg := &RootConfig{
		Version: 13,
		Groups: []*GroupConfig{{
			Name: "base",
			Dots: []DotEntry{
				{Name: "claude", Path: "~/.claude"},
				{Name: "agents-root", Path: "~/.agents"},
				{Name: "agents-skills", Path: "~/.agents/skills"},
				{Name: "agents-skills-child", Path: "~/.agents/skills/demo"},
				{Name: "agents-lock", Path: "~/.agents/.skill-lock.json"},
				{Name: "ampcfg", Path: "~/.config/agents"},
				{Name: "claude-settings", Path: "~/.claude/settings.json"},
				{Name: "claude-agents-skill", Path: "~/.claude/agents/foo.md"},
				{Name: "zshrc", Path: "~/.zshrc"},
				{Name: "nvim", Path: "~/.config/nvim"},
			},
		}},
	}
	if _, err := Migrate(cfg); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
	got := make(map[string]struct{}, len(cfg.Groups[0].Dots))
	for _, d := range cfg.Groups[0].Dots {
		got[d.Name] = struct{}{}
	}
	for _, dropped := range []string{"claude", "agents-skills", "agents-skills-child", "ampcfg", "claude-settings", "claude-agents-skill"} {
		if _, ok := got[dropped]; ok {
			t.Errorf("dot %q still present after migration, want dropped", dropped)
		}
	}
	for _, kept := range []string{"agents-root", "agents-lock", "zshrc", "nvim"} {
		if _, ok := got[kept]; !ok {
			t.Errorf("dot %q missing after migration, want kept", kept)
		}
	}
}

func TestMigrateRawV13ToV14_DropsAgentConfigDots(t *testing.T) {
	raw := map[string]json.RawMessage{
		"version": json.RawMessage(`13`),
		"groups": json.RawMessage(`[
			{
				"name": "base",
				"dots": [
					{"name": "claude", "path": "~/.claude"},
					{"name": "claude-settings", "path": "~/.claude/settings.json"},
					{"name": "zshrc", "path": "~/.zshrc"},
					{"name": "nvim", "path": "~/.config/nvim"}
				]
			}
		]`),
	}
	if err := migrateRawConfigV13ToV14(raw); err != nil {
		t.Fatalf("migrateRawConfigV13ToV14: %v", err)
	}
	var groups []struct {
		Name string     `json:"name"`
		Dots []DotEntry `json:"dots"`
	}
	if err := json.Unmarshal(raw["groups"], &groups); err != nil {
		t.Fatalf("parse groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	got := make(map[string]struct{}, len(groups[0].Dots))
	for _, d := range groups[0].Dots {
		got[d.Name] = struct{}{}
	}
	if _, ok := got["claude"]; ok {
		t.Error("dot \"claude\" still present after raw migration, want dropped")
	}
	if _, ok := got["claude-settings"]; ok {
		t.Error("dot \"claude-settings\" still present after raw migration, want dropped")
	}
	if _, ok := got["zshrc"]; !ok {
		t.Error("dot \"zshrc\" missing after raw migration, want kept")
	}
	if _, ok := got["nvim"]; !ok {
		t.Error("dot \"nvim\" missing after raw migration, want kept")
	}
}

func TestIsAgentConfigDotPath_AgentsDirTruthTable(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "agents root dir itself is trackable", path: "~/.agents", want: false},
		{name: "agents skills subtree is machine-managed", path: "~/.agents/skills", want: true},
		{name: "agents skills subtree child is machine-managed", path: "~/.agents/skills/demo/SKILL.md", want: true},
		{name: "agents skill-lock file is trackable", path: "~/.agents/.skill-lock.json", want: false},
		{name: "other file directly under agents is trackable", path: "~/.agents/README.md", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAgentConfigDotPath(tc.path); got != tc.want {
				t.Errorf("isAgentConfigDotPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMigrateV5ToV6_ConvertsToolsToProviderLists(t *testing.T) {
	cfg := &RootConfig{
		Version: 5,
		Settings: Settings{
			Ecosystems: map[string]EcosystemSettings{
				"node": {Manager: "pnpm"},
			},
		},
		Tools: map[string]ToolSpec{
			"docker": {
				Provider: "brew",
				Package:  "docker-desktop",
				Options:  map[string]string{"brew_kind": "cask"},
			},
			"eslint": {
				Provider:    "node",
				Package:     "eslint",
				InstallWith: "pnpm",
				Variants: []ToolInstallSpec{
					{Provider: "npm", Package: "eslint"},
				},
			},
			"unresolved": {Provider: "system"},
		},
		Groups: []*GroupConfig{{
			Name:  "base",
			Tools: []ToolEntry{{Name: "docker"}, {Name: "eslint"}, {Name: "unresolved"}},
		}},
	}

	migrated, err := Migrate(cfg)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !migrated {
		t.Fatal("Migrate migrated = false, want true")
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
	if cfg.Settings.Ecosystems != nil {
		t.Fatalf("settings ecosystems = %#v, want nil", cfg.Settings.Ecosystems)
	}

	docker := cfg.Tools["docker"]
	if docker.Provider != "" || docker.Package != "" || docker.InstallWith != "" || docker.Options != nil || docker.Variants != nil || docker.Hosts != nil {
		t.Fatalf("deprecated docker fields still populated: %+v", docker)
	}
	if len(docker.Providers) != 1 {
		t.Fatalf("docker providers = %+v, want one", docker.Providers)
	}
	if got := docker.Providers[0]; got.Provider != "brew" || got.Package != "docker-desktop" || got.Bin != "" || got.Options["brew_kind"] != "cask" {
		t.Fatalf("docker provider = %+v, want brew/docker-desktop cask", got)
	}

	eslint := cfg.Tools["eslint"]
	if len(eslint.Providers) != 2 {
		t.Fatalf("eslint providers = %+v, want pnpm+npm", eslint.Providers)
	}
	if got := eslint.Providers[0]; got.Provider != "pnpm" || got.Package != "eslint" || got.InstallWith != "" {
		t.Fatalf("eslint primary provider = %+v, want pnpm/eslint without install_with", got)
	}
	if got := eslint.Providers[1]; got.Provider != "npm" || got.Package != "eslint" {
		t.Fatalf("eslint variant provider = %+v, want npm/eslint", got)
	}

	if providers := cfg.Tools["unresolved"].Providers; len(providers) != 0 {
		t.Fatalf("unresolved providers = %+v, want empty", providers)
	}
}

func TestMigrateRawV5ToV6_RemovesDeprecatedToolFields(t *testing.T) {
	raw := map[string]json.RawMessage{
		"version": json.RawMessage(`5`),
		"settings": json.RawMessage(`{
			"ecosystems": {"node": {"manager": "pnpm"}},
			"disabled_providers": ["pip"]
		}`),
		"tools": json.RawMessage(`{
			"docker": {
				"provider": "brew",
				"package": "docker-desktop",
				"options": {"brew_kind": "cask"}
			},
			"eslint": {
				"provider": "node",
				"package": "eslint",
				"install_with": "pnpm",
				"variants": [{"provider": "npm", "package": "eslint"}]
			}
		}`),
	}
	if err := migrateRawVersion(raw); err != nil {
		t.Fatalf("migrateRawVersion: %v", err)
	}
	var out struct {
		Version  int `json:"version"`
		Settings struct {
			Ecosystems        map[string]EcosystemSettings `json:"ecosystems,omitempty"`
			DisabledProviders []string                     `json:"disabled_providers,omitempty"`
		} `json:"settings"`
		Tools map[string]ToolSpec `json:"tools"`
	}
	merged, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("unmarshal migrated raw: %v\n%s", err, merged)
	}
	if out.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", out.Version, CurrentVersion)
	}
	if out.Settings.Ecosystems != nil {
		t.Fatalf("settings ecosystems = %#v, want nil", out.Settings.Ecosystems)
	}
	if len(out.Settings.DisabledProviders) != 1 || out.Settings.DisabledProviders[0] != "pip" {
		t.Fatalf("disabled providers = %#v, want [pip]", out.Settings.DisabledProviders)
	}
	docker := out.Tools["docker"]
	if docker.Provider != "" || docker.Package != "" || docker.Options != nil || len(docker.Providers) != 1 {
		t.Fatalf("docker migrated shape = %+v, want only providers[]", docker)
	}
	eslint := out.Tools["eslint"]
	if eslint.Provider != "" || eslint.InstallWith != "" || eslint.Variants != nil || len(eslint.Providers) != 2 {
		t.Fatalf("eslint migrated shape = %+v, want only providers[]", eslint)
	}
}
