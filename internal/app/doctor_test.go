package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

type doctorErrorProvider struct{}

func (p *doctorErrorProvider) Name() string        { return "broken" }
func (p *doctorErrorProvider) Description() string { return "broken provider" }
func (p *doctorErrorProvider) Available(context.Context) (bool, error) {
	return false, errors.New("availability failed")
}
func (p *doctorErrorProvider) Install(context.Context, provider.Tool) error   { return nil }
func (p *doctorErrorProvider) Uninstall(context.Context, provider.Tool) error { return nil }
func (p *doctorErrorProvider) Upgrade(context.Context, provider.Tool) error   { return nil }
func (p *doctorErrorProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *doctorErrorProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func TestDoctor_ReportsHostProviderAndCacheState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t, &stubProvider{name: "brew", available: true})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev"},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	for _, id := range []string{"config", "cache", "host", "providers", "dots", "services"} {
		if got := doctorCheck(result, id); got == nil {
			t.Fatalf("missing doctor check %q in %+v", id, result.Checks)
		}
	}
	if got := doctorCheck(result, "config"); got.Status != app.DoctorStatusOK {
		t.Fatalf("config status = %q, want ok: %+v", got.Status, got)
	}
	if got := doctorCheck(result, "cache"); got.Status != app.DoctorStatusOK {
		t.Fatalf("cache status = %q, want ok: %+v", got.Status, got)
	}
	if got := doctorCheck(result, "host"); got.Status != app.DoctorStatusOK {
		t.Fatalf("host status = %q, want ok: %+v", got.Status, got)
	}
	if got := doctorCheck(result, "providers"); got.Status != app.DoctorStatusOK {
		t.Fatalf("providers status = %q, want ok: %+v", got.Status, got)
	}
	if got := doctorCheck(result, "dots"); got.Status != app.DoctorStatusWarn {
		t.Fatalf("dots status = %q, want warn for unconfigured dots: %+v", got.Status, got)
	}
	if result.HasFailures() {
		t.Fatalf("HasFailures = true, want false: %+v", result.Summary)
	}
}

func TestDoctor_InvalidConfigFailsAfterReadOnlyInit(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = t.TempDir()
	if err := a.InitReadOnly(context.Background()); err != nil {
		t.Fatalf("InitReadOnly: %v", err)
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	check := doctorCheck(result, "config")
	if check == nil || check.Status != app.DoctorStatusFail {
		t.Fatalf("config check = %+v, want fail", check)
	}
	if !result.HasFailures() {
		t.Fatalf("HasFailures = false, want true: %+v", result.Summary)
	}
}

func TestDoctor_ProviderAvailabilityErrorsAreWarnings(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t, &doctorErrorProvider{})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	check := doctorCheck(result, "providers")
	if check == nil || check.Status != app.DoctorStatusWarn {
		t.Fatalf("providers check = %+v, want warn", check)
	}
	if len(check.Details) == 0 {
		t.Fatalf("providers check missing error detail: %+v", check)
	}
}

func doctorCheck(result *app.DoctorResult, id string) *app.DoctorCheck {
	for i := range result.Checks {
		if result.Checks[i].ID == id {
			return &result.Checks[i]
		}
	}
	return nil
}
