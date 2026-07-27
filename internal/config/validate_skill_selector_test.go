package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func skillPackageConfig(skills ...string) *config.RootConfig {
	return &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{{Source: "vercel-labs/agent-skills", Skills: skills}},
		},
	}
}

func TestValidateRoot_BlankSkillSelectorEntryIsRejected(t *testing.T) {
	errs := config.ValidateRoot(skillPackageConfig("review", "  "), config.ProviderValidation{})
	text := fmt.Sprintf("%v", errs)
	if !strings.Contains(text, "$.agents.packages[0].skills[1]") {
		t.Fatalf("errors = %v, want a blank-selector error", errs)
	}
	for _, e := range errs {
		if strings.Contains(e.Path, "skills[1]") && e.Warn {
			t.Fatalf("blank selector entry must be an error, not a warning: %v", e)
		}
	}
}

func TestValidateRoot_DuplicateSkillSelectorEntryWarns(t *testing.T) {
	errs := config.ValidateRoot(skillPackageConfig("review", "review"), config.ProviderValidation{})
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Path, "skills[1]") {
			found = true
			if !e.Warn {
				t.Fatalf("duplicate selector entry should be warn-level: %v", e)
			}
		}
	}
	if !found {
		t.Fatalf("errors = %v, want a duplicate-selector diagnostic", errs)
	}
}

func TestValidateRoot_ValidSkillSelectorPasses(t *testing.T) {
	if errs := config.ValidateRoot(skillPackageConfig("review", "test"), config.ProviderValidation{}); len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if errs := config.ValidateRoot(skillPackageConfig(), config.ProviderValidation{}); len(errs) != 0 {
		t.Fatalf("errors = %v, want none for an absent selector", errs)
	}
}
