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
		Provider:    "brew",
		Package:     "ripgrep",
		InstallWith: "brew",
		Options:     map[string]string{"k": "v"},
		Taps:        []string{"homebrew/core"},
		Ignore:      true,
	}
	got := spec.DefaultInstallSpec()
	if got.Provider != "brew" || got.Package != "ripgrep" || got.InstallWith != "brew" {
		t.Errorf("DefaultInstallSpec did not copy provider/package/install_with: %+v", got)
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

func TestSettings_EcosystemPriority(t *testing.T) {
	s := config.Settings{
		Ecosystems: map[string]config.EcosystemSettings{
			"node": {Priority: []string{"pnpm", "npm"}},
		},
	}
	if got := s.EcosystemPriority("node"); len(got) != 2 || got[0] != "pnpm" {
		t.Errorf("EcosystemPriority(node) = %v, want [pnpm npm]", got)
	}
	if got := s.EcosystemPriority("missing"); got != nil {
		t.Errorf("EcosystemPriority(missing) = %v, want nil", got)
	}
	// Empty priority slice returns nil rather than empty slice.
	s.Ecosystems["python"] = config.EcosystemSettings{}
	if got := s.EcosystemPriority("python"); got != nil {
		t.Errorf("EcosystemPriority(empty) = %v, want nil", got)
	}
}

func TestSettings_EcosystemManager_FallbackEmpty(t *testing.T) {
	var s config.Settings
	if got := s.EcosystemManager("node"); got != "" {
		t.Errorf("nil-Ecosystems EcosystemManager = %q, want empty", got)
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
		Tools: map[string]config.ToolSpec{"   ": {Provider: "brew"}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "tool name is required") {
		t.Errorf("expected 'tool name is required', got %v", errs)
	}
}

func TestValidateRoot_MissingProviderRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {}}, // no provider
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, "provider is required") {
		t.Errorf("expected 'provider is required', got %v", errs)
	}
}

func TestValidateRoot_UnknownProviderRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {Provider: "fakemgr"}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{Known: []string{"brew"}})
	if !containsErrorMessage(errs, `unknown provider "fakemgr"`) {
		t.Errorf("expected unknown-provider error, got %v", errs)
	}
}

func TestValidateRoot_NonEcosystemProviderRejectedWhenEcosystemsListed(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{"ripgrep": {Provider: "brew"}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{
		Known:      []string{"brew", "system"},
		Ecosystems: []string{"system"}, // brew is not an ecosystem
	})
	if !containsErrorMessage(errs, "is not an ecosystem provider") {
		t.Errorf("expected ecosystem-only error, got %v", errs)
	}
}

func TestValidateRoot_InstallWithEcosystemRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "system"},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{
		Known:      []string{"system", "brew"},
		Ecosystems: []string{"system"},
	})
	if !containsErrorMessage(errs, "must be a concrete provider or manager") {
		t.Errorf("expected install_with-not-concrete error, got %v", errs)
	}
}

func TestValidateRoot_InstallWithWrongEcosystemRejected(t *testing.T) {
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "pip"},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{
		Known:              []string{"system", "brew", "pip"},
		Ecosystems:         []string{"system", "python"},
		ConcreteEcosystems: map[string]string{"pip": "python", "brew": "system"},
	})
	if !containsErrorMessage(errs, `belongs to ecosystem "python"`) {
		t.Errorf("expected wrong-ecosystem error, got %v", errs)
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
