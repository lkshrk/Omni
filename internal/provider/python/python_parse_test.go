package python

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestParseUVToolList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []provider.InstalledTool
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "only indented/dash lines skipped",
			input: " /usr/bin/black\n- something\n\t/bin/ruff\n",
			want:  nil,
		},
		{
			name:  "v-prefixed version stripped",
			input: "black v24.3.0\n",
			want: []provider.InstalledTool{
				{Tool: provider.Tool{Name: "black", Provider: "python", Package: "black"}, Version: "24.3.0"},
			},
		},
		{
			name:  "version without v prefix",
			input: "ruff 0.4.1\n",
			want: []provider.InstalledTool{
				{Tool: provider.Tool{Name: "ruff", Provider: "python", Package: "ruff"}, Version: "0.4.1"},
			},
		},
		{
			name:  "no version field",
			input: "mytool\n",
			want: []provider.InstalledTool{
				{Tool: provider.Tool{Name: "mytool", Provider: "python", Package: "mytool"}, Version: ""},
			},
		},
		{
			name:  "mixed: entries and indented binary lines",
			input: "black v24.3.0\n - black\nruff 0.4.1\n - ruff\n",
			want: []provider.InstalledTool{
				{Tool: provider.Tool{Name: "black", Provider: "python", Package: "black"}, Version: "24.3.0"},
				{Tool: provider.Tool{Name: "ruff", Provider: "python", Package: "ruff"}, Version: "0.4.1"},
			},
		},
		{
			name:  "name lowercased",
			input: "Black v24.3.0\n",
			want: []provider.InstalledTool{
				{Tool: provider.Tool{Name: "black", Provider: "python", Package: "Black"}, Version: "24.3.0"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUVToolList(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tt.want), got)
			}
			for i, g := range got {
				w := tt.want[i]
				if g.Tool.Name != w.Tool.Name || g.Tool.Provider != w.Tool.Provider ||
					g.Tool.Package != w.Tool.Package || g.Version != w.Version {
					t.Errorf("[%d] got {Name:%q Provider:%q Package:%q Version:%q}, want {Name:%q Provider:%q Package:%q Version:%q}",
						i, g.Tool.Name, g.Tool.Provider, g.Tool.Package, g.Version,
						w.Tool.Name, w.Tool.Provider, w.Tool.Package, w.Version)
				}
			}
		})
	}
}

func TestParseUVOutdatedList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "single outdated tool",
			input: "black v24.3.0 (update available: v24.4.0)\n",
			want:  map[string]string{"black": "24.4.0"},
		},
		{
			name:  "v prefix stripped from latest version",
			input: "ruff v0.4.1 (update available: v0.5.0)\n",
			want:  map[string]string{"ruff": "0.5.0"},
		},
		{
			name:  "multiple outdated tools",
			input: "black v24.3.0 (update available: v24.4.0)\nruff v0.4.1 (update available: v0.5.0)\n",
			want:  map[string]string{"black": "24.4.0", "ruff": "0.5.0"},
		},
		{
			name:  "name lowercased",
			input: "Black v24.3.0 (update available: v24.4.0)\n",
			want:  map[string]string{"black": "24.4.0"},
		},
		{
			name:  "lines without update available annotation skipped",
			input: "black v24.3.0\nruff v0.4.1 (update available: v0.5.0)\n",
			want:  map[string]string{"ruff": "0.5.0"},
		},
		{
			name:  "indented binary lines skipped",
			input: "black v24.3.0 (update available: v24.4.0)\n - black\n",
			want:  map[string]string{"black": "24.4.0"},
		},
		{
			name:  "no outdated tools returns nil",
			input: "black v24.3.0\nruff v0.4.1\n",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUVOutdatedList(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d; got %v, want %v", len(got), len(tt.want), got, tt.want)
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("key %q missing from result", k)
				} else if gotV != wantV {
					t.Errorf("got[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestParsePipList(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		input := `[{"name":"black","version":"24.3.0"},{"name":"Ruff","version":"0.4.1"}]`
		got, err := parsePipList(input)
		if err != nil {
			t.Fatalf("parsePipList: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].Tool.Name != "black" {
			t.Errorf("got[0].Name = %q, want black", got[0].Tool.Name)
		}
		if got[1].Tool.Name != "ruff" {
			t.Errorf("got[1].Name = %q, want ruff", got[1].Tool.Name)
		}
		if got[0].Version != "24.3.0" {
			t.Errorf("got[0].Version = %q, want 24.3.0", got[0].Version)
		}
		if got[0].Tool.Provider != "python" {
			t.Errorf("provider = %q, want python", got[0].Tool.Provider)
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		if got, err := parsePipList("not json"); err == nil || got != nil {
			t.Errorf("parsePipList invalid = (%v, %v), want nil/error", got, err)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		got, err := parsePipList("[]")
		if err != nil {
			t.Fatalf("parsePipList: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})
}

func TestParsePipInstalledMap(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		input := `[{"name":"Black","version":"24.3.0"},{"name":"ruff","version":"0.4.1"}]`
		got, err := parsePipInstalledMap(input)
		if err != nil {
			t.Fatalf("parsePipInstalledMap: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got["black"] != "24.3.0" {
			t.Errorf("black version = %q, want 24.3.0", got["black"])
		}
		if got["ruff"] != "0.4.1" {
			t.Errorf("ruff version = %q, want 0.4.1", got["ruff"])
		}
	})

	t.Run("keys are lowercased", func(t *testing.T) {
		input := `[{"name":"MyPkg","version":"1.0"}]`
		got, err := parsePipInstalledMap(input)
		if err != nil {
			t.Fatalf("parsePipInstalledMap: %v", err)
		}
		if _, ok := got["mypkg"]; !ok {
			t.Error("expected lowercase key mypkg")
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		if got, err := parsePipInstalledMap("{bad}"); err == nil || got != nil {
			t.Errorf("parsePipInstalledMap invalid = (%v, %v), want nil/error", got, err)
		}
	})
}

func TestParsePipOutdatedMap(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		input := `[{"name":"black","latest_version":"24.4.0"},{"name":"Ruff","latest_version":"0.5.0"}]`
		got, err := parsePipOutdatedMap(input)
		if err != nil {
			t.Fatalf("parsePipOutdatedMap: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got["black"] != "24.4.0" {
			t.Errorf("black latest = %q, want 24.4.0", got["black"])
		}
		if got["ruff"] != "0.5.0" {
			t.Errorf("ruff latest = %q, want 0.5.0", got["ruff"])
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		if got, err := parsePipOutdatedMap("bad"); err == nil || got != nil {
			t.Errorf("parsePipOutdatedMap invalid = (%v, %v), want nil/error", got, err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got, err := parsePipOutdatedMap("[]")
		if err != nil {
			t.Fatalf("parsePipOutdatedMap: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
}

func TestParsePipShowVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "finds version line",
			input: "Name: black\nVersion: 24.3.0\nSummary: The uncompromising code formatter.\n",
			want:  "24.3.0",
		},
		{
			name:  "version with leading whitespace",
			input: "Name: black\nVersion:  24.3.0\n",
			want:  "24.3.0",
		},
		{
			name:  "missing version returns empty",
			input: "Name: black\nSummary: something\n",
			want:  "",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePipShowVersion(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
