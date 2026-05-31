package config_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestProviderEntry_JSONRoundTrip(t *testing.T) {
	in := config.ProviderEntry{
		Name:        "uv",
		Provider:    "brew",
		Package:     "astral-sh/uv",
		InstallWith: "",
		Options:     map[string]string{"flag": "v"},
		Variants:    []config.ToolInstallSpec{{Provider: "pip", InstallWith: "pip3"}},
		Hosts:       map[string]config.ToolInstallSpec{"laptop": {Provider: "brew"}},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out config.ProviderEntry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestProviderEntry_ToToolSpec(t *testing.T) {
	p := config.ProviderEntry{
		Name:        "uv",
		Provider:    "brew",
		Package:     "astral-sh/uv",
		InstallWith: "pip3",
		Options:     map[string]string{"k": "v"},
		Variants:    []config.ToolInstallSpec{{Provider: "pip"}},
		Hosts:       map[string]config.ToolInstallSpec{"h": {Provider: "apt"}},
	}
	want := config.ToolSpec{
		Provider:    "brew",
		Package:     "astral-sh/uv",
		InstallWith: "pip3",
		Options:     map[string]string{"k": "v"},
		Variants:    []config.ToolInstallSpec{{Provider: "pip"}},
		Hosts:       map[string]config.ToolInstallSpec{"h": {Provider: "apt"}},
	}
	if got := p.ToToolSpec(); !reflect.DeepEqual(got, want) {
		t.Errorf("ToToolSpec() = %+v, want %+v", got, want)
	}
}

func TestSettings_ProvidersField(t *testing.T) {
	data := []byte(`{"providers":[{"name":"uv","provider":"brew"}]}`)
	var s config.Settings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(s.Providers) != 1 || s.Providers[0].Name != "uv" || s.Providers[0].Provider != "brew" {
		t.Errorf("Providers = %+v, want one uv/brew entry", s.Providers)
	}
}
