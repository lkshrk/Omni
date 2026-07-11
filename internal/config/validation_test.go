package config_test

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestToolInstallSpec_EffectivePackage(t *testing.T) {
	tests := []struct {
		name        string
		spec        config.ToolInstallSpec
		logicalName string
		want        string
	}{
		{"explicit package wins", config.ToolInstallSpec{Package: "ripgrep"}, "rg", "ripgrep"},
		{"empty package falls back to logical name", config.ToolInstallSpec{}, "rg", "rg"},
		{"empty package + empty name", config.ToolInstallSpec{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.EffectivePackage(tt.logicalName); got != tt.want {
				t.Errorf("EffectivePackage(%q) = %q, want %q", tt.logicalName, got, tt.want)
			}
		})
	}
}

func TestToolSpec_DefaultInstallSpec(t *testing.T) {
	spec := config.ToolSpec{
		Providers: []config.ToolInstallSpec{{
			Provider: "brew",
			Package:  "ripgrep",
			Bin:      "rg",
			Options:  map[string]string{"k": "v"},
		}},
		Taps:   []string{"homebrew/core"},
		Ignore: true,
	}
	got := spec.DefaultInstallSpec()
	if got.Provider != "brew" || got.Package != "ripgrep" || got.Bin != "rg" {
		t.Errorf("DefaultInstallSpec did not copy provider/package/bin: %+v", got)
	}
	if got.Options["k"] != "v" {
		t.Errorf("DefaultInstallSpec did not copy options: %+v", got.Options)
	}
}

func TestToolSpec_ToToolEntry(t *testing.T) {
	spec := config.ToolSpec{Ignore: true}
	install := config.ToolInstallSpec{
		Provider:    "system",
		Package:     "rg",
		InstallWith: "brew",
		Options:     map[string]string{"k": "v"},
	}
	got := spec.ToToolEntry("ripgrep", install)
	if got.Name != "ripgrep" {
		t.Errorf("Name = %q, want ripgrep", got.Name)
	}
	if got.Provider != "system" || got.Package != "rg" || got.InstallWith != "brew" {
		t.Errorf("install fields not copied: %+v", got)
	}
	if !got.Ignore {
		t.Error("Ignore should propagate from spec to entry")
	}
	if got.Options["k"] != "v" {
		t.Errorf("Options not copied: %+v", got.Options)
	}
}

func TestToolSpec_ToToolEntry_PackageDefaultsFromLogicalName(t *testing.T) {
	spec := config.ToolSpec{}
	install := config.ToolInstallSpec{Provider: "brew"} // no Package
	got := spec.ToToolEntry("ripgrep", install)
	if got.Package != "ripgrep" {
		t.Errorf("Package = %q, want fallback to logical name 'ripgrep'", got.Package)
	}
}

func TestValidationError_FormatsWithAndWithoutPath(t *testing.T) {
	tests := []struct {
		name string
		err  config.ValidationError
		want string
	}{
		{"with path", config.ValidationError{Path: "$.tools.foo", Message: "broken"}, "$.tools.foo: broken"},
		{"without path", config.ValidationError{Message: "global problem"}, "global problem"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidationErrors_JoinsAllMessages(t *testing.T) {
	errs := config.ValidationErrors{
		{Path: "$.a", Message: "first"},
		{Message: "second"},
	}
	got := errs.Error()
	if !strings.Contains(got, "$.a: first") || !strings.Contains(got, "second") {
		t.Errorf("ValidationErrors.Error() = %q, want both messages", got)
	}
	if got != "$.a: first\nsecond" {
		t.Errorf("ValidationErrors.Error() = %q, want newline-joined", got)
	}
}

func TestValidationErrors_EmptyReturnsEmptyString(t *testing.T) {
	var errs config.ValidationErrors
	if got := errs.Error(); got != "" {
		t.Errorf("empty errs Error() = %q, want \"\"", got)
	}
}

func TestValidateRoot_NilCfgReturnsNil(t *testing.T) {
	if got := config.ValidateRoot(nil, config.ProviderValidation{}); got != nil {
		t.Errorf("ValidateRoot(nil) = %v, want nil", got)
	}
}

func TestValidateRoot_EmptyToolNameRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"   ": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "tool name is required") {
		t.Errorf("expected 'tool name is required', got %v", errs)
	}
}

func TestValidateRoot_MissingProviderRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {Providers: []config.ToolInstallSpec{{}}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "provider is required") {
		t.Errorf("expected 'provider is required', got %v", errs)
	}
}

func TestValidateRoot_UnknownProviderRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "fakemgr"}}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `unknown provider "fakemgr"`) {
		t.Errorf("expected unknown-provider error, got %v", errs)
	}
}

func TestValidateRoot_ProviderEntriesAcceptConcreteProvidersWhenEcosystemsListed(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{
		Known:      []string{"brew", "system"},
		Ecosystems: []string{"system"},
	})
	if len(errs) != 0 {
		t.Errorf("concrete provider entry produced errors: %v", errs)
	}
}

func TestValidateRoot_EcosystemProviderEntryRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "system"}}},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{
		Known:      []string{"system", "brew"},
		Ecosystems: []string{"system"},
	})
	if !containsErrorMessage(errs, "provider family") {
		t.Errorf("expected provider-family error, got %v", errs)
	}
}

func TestValidateRoot_InstallWithRejectedOnProviderEntry(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "brew", InstallWith: "apt"}}},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{
		Known: []string{"brew", "apt"},
	})
	if !containsErrorMessage(errs, "install_with is not supported") {
		t.Errorf("expected install_with rejection, got %v", errs)
	}
}

func TestValidateRoot_AcceptsHostProviderOverride(t *testing.T) {
	cfg := &config.RootConfig{Tools: map[string]config.ToolSpec{
		"pnpm": {
			Providers: []config.ToolInstallSpec{{Provider: "brew"}, {Provider: "bun"}},
			Hosts:     map[string]config.ToolInstallSpec{"topaz": {Provider: "bun"}},
		},
	}}
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew", "bun"}}); len(errs) != 0 {
		t.Fatalf("host provider override produced errors: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackAcceptsGitHubSystemTool(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
					Binary: "rg",
					Recipe: config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "ripgrep-{version}-{os}-{arch}.tar.gz"},
					Commands: config.FallbackCommands{
						Check: "command -v rg",
					},
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if len(errs) != 0 {
		t.Errorf("valid fallback produced errors: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackNativeRecipeNoCheckRequired(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"glow": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "charmbracelet", Repo: "glow"},
					Binary: "glow",
					Recipe: config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "glow_{version}_Linux_x86_64.tar.gz"},
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if len(errs) != 0 {
		t.Errorf("native github_release_asset fallback without a check command should be valid, got: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackRawCommandsStillRequiresCheck(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"tool": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "o", Repo: "r"},
					Binary: "tool",
					Recipe: config.FallbackRecipe{Type: config.FallbackRecipeRawCommands},
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "fallback check command is required") {
		t.Errorf("raw_commands fallback without a check should still be rejected, got: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackRawCommandsGitHubSourceNoOwnerRepo(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pi": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source:   config.FallbackSource{Type: config.FallbackSourceGitHub, URL: "https://pi.dev"},
					Binary:   "pi",
					Recipe:   config.FallbackRecipe{Type: config.FallbackRecipeRawCommands},
					Commands: config.FallbackCommands{Check: "command -v pi", Install: "curl -fsSL https://pi.dev/install.sh | sh"},
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if len(errs) != 0 {
		t.Errorf("raw_commands fallback with a github source but no owner/repo should be valid, got: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackReleaseAssetStillRequiresOwnerRepo(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"x": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub},
					Binary: "x",
					Recipe: config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "x-{version}.tar.gz"},
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "github fallback source requires owner and repo") {
		t.Errorf("release-asset fallback without owner/repo should be rejected, got: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackAcceptsAnyToolProvider(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"eslint": {
				Providers: []config.ToolInstallSpec{{Provider: "npm"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "eslint", Repo: "eslint"},
					Status: config.FallbackStatusUnresolved,
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"npm"}})
	if len(errs) != 0 {
		t.Errorf("fallback on concrete provider tool produced errors: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackRequiresGitHubOwnerAndRepo(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Repo: "ripgrep"},
					Status: config.FallbackStatusUnresolved,
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "github fallback source requires owner and repo") {
		t.Errorf("expected github owner/repo error, got %v", errs)
	}
}

func TestValidateRoot_ToolFallbackUnresolvedCanOmitCheck(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnresolved,
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if len(errs) != 0 {
		t.Errorf("unresolved fallback without check produced errors: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackUnsupportedCanOmitCheck(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnsupported,
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if len(errs) != 0 {
		t.Errorf("unsupported fallback without check produced errors: %v", errs)
	}
}

func TestValidateRoot_ToolFallbackRequiresCheckWhenUsable(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnverified,
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "fallback check command is required unless status is unresolved") {
		t.Errorf("expected fallback check error, got %v", errs)
	}
}

func TestValidateRoot_ToolFallbackRejectsUnknownStatus(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {
				Providers: []config.ToolInstallSpec{{Provider: "brew"}},
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: "desperate",
				},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `unknown fallback status "desperate"`) {
		t.Errorf("expected unknown status error, got %v", errs)
	}
}

func TestValidateRoot_NilGroupRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{nil},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "must not be null") {
		t.Errorf("expected nil-group error, got %v", errs)
	}
}

func TestValidateRoot_GroupToolEntryRejectsExtraFields(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "a", Provider: "brew"}},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "logical tool names only") {
		t.Errorf("expected 'logical tool names only', got %v", errs)
	}
}

func TestValidateRoot_GroupReferencesMissingTool(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "ghost"}},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `missing logical tool "ghost"`) {
		t.Errorf("expected missing-tool error, got %v", errs)
	}
}

func TestValidateRoot_GroupDuplicateTool(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{{
			Tools: []config.ToolEntry{{Name: "a"}, {Name: "a"}},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "duplicate tool membership") {
		t.Errorf("expected duplicate-tool error, got %v", errs)
	}
}

func TestValidateRoot_ToolSingleOwnerIncludesHostGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{
			{Name: "host", Special: "host", Tools: []config.ToolEntry{{Name: "a"}}},
			{Name: "work", Tools: []config.ToolEntry{{Name: "a"}}},
		},
		Hosts: map[string][]string{"host": {"work"}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `tool "a" already belongs to group "host"`) {
		t.Errorf("expected cross-group tool owner error, got %v", errs)
	}
}

func TestValidateRoot_DotSingleOwnerIncludesHostGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "host", Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
			{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
		},
		Hosts: map[string][]string{"host": {"work"}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `dotfile "nvim" already belongs to group "host"`) {
		t.Errorf("expected cross-group dot owner error, got %v", errs)
	}
}

func TestValidateRoot_DotPackageCollisions(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Name: "base",
			Dots: []config.DotEntry{
				{Name: "nvim", Path: "~/.config/nvim", Hosts: map[string]config.DotVariant{
					"work": {Package: "nvim-work"},
				}},
				{Name: "vim", Path: "~/.vim", Package: "nvim-work"},
			},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !containsErrorMessage(errs, `package "nvim-work" is already used by dotfile "nvim"`) {
		t.Errorf("expected duplicate dot package error, got %v", errs)
	}
}

func TestValidateRoot_DotRejectsPathLikePackage(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Name: "base",
			Dots: []config.DotEntry{{
				Name:    "nvim",
				Path:    "~/.config/nvim",
				Package: "../nvim",
			}},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !containsErrorMessage(errs, `invalid package name "../nvim"`) {
		t.Errorf("expected invalid dot package error, got %v", errs)
	}
}

func TestValidateRoot_DotRejectsInvalidOnConflict(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Name: "base",
			Dots: []config.DotEntry{{
				Name:       "codex",
				Path:       "~/.config/codex",
				OnConflict: "yolo",
			}},
		}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	if !containsErrorMessage(errs, `invalid on_conflict "yolo"`) {
		t.Errorf("expected invalid on_conflict error, got %v", errs)
	}
}

func TestValidateRoot_DotAcceptsValidOnConflict(t *testing.T) {
	for _, v := range []string{"", "use_repo", "use_local"} {
		cfg := &config.RootConfig{
			Groups: []*config.GroupConfig{{
				Name: "base",
				Dots: []config.DotEntry{{Name: "codex", Path: "~/.config/codex", OnConflict: v}},
			}},
		}
		errs := config.ValidateRoot(cfg, config.ProviderValidation{})
		if containsErrorMessage(errs, "on_conflict") {
			t.Errorf("on_conflict=%q rejected unexpectedly: %v", v, errs)
		}
	}
}

func TestValidateRoot_HostReferencesMissingGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{{Name: "home", Special: "host"}},
		Hosts:  map[string][]string{"home": {"ghost"}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `missing group "ghost"`) {
		t.Errorf("expected missing-group error, got %v", errs)
	}
}

func TestValidateRoot_HostRequiresSpecialHostGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"a": {Provider: "brew"}},
		Groups: []*config.GroupConfig{{Name: "laptop"}},
		Hosts:  map[string][]string{"laptop": {}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `must be marked as special host group`) {
		t.Errorf("expected special-host error, got %v", errs)
	}
}

func TestValidateRoot_CleanCfgReturnsEmpty(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {Provider: "brew"}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Tools:   []config.ToolEntry{{Name: "ripgrep"}},
		}},
		Hosts: map[string][]string{"testhost": {}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if len(errs) != 0 {
		t.Errorf("clean cfg produced errors: %v", errs)
	}
}

func containsErrorMessage(errs []config.ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return true
		}
	}
	return false
}
