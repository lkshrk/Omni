package agent

import (
	"context"
	"strings"
	"testing"
)

func stubExec(stdout string) func(context.Context, string, ...string) (string, string, error) {
	return func(context.Context, string, ...string) (string, string, error) {
		return stdout, "", nil
	}
}

// Output shape varies by CLI version: the documented envelope or the bare installed array.
func TestClaudePluginAdapter_ListPlugins_AcceptsBothListShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		stdout        string
		wantNames     []string
		wantLatestFor string
	}{
		{
			name:          "object envelope",
			stdout:        `{"installed":[{"id":"useful-skills@lkshrk","version":"1.0.0"}],"available":[{"name":"useful-skills","marketplaceName":"lkshrk","latestVersion":"2.0.0"}]}`,
			wantNames:     []string{"useful-skills"},
			wantLatestFor: "2.0.0",
		},
		{
			name:      "bare installed array",
			stdout:    `[{"id":"useful-skills@lkshrk","version":"1.0.0"}]`,
			wantNames: []string{"useful-skills"},
		},
		{
			name:      "bare empty array",
			stdout:    `[]`,
			wantNames: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewClaudeCodePluginAdapter(stubExec(tt.stdout), func(string) (string, bool) { return "", false })
			got, err := a.ListPlugins(context.Background())
			if err != nil {
				t.Fatalf("ListPlugins: %v", err)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("got %d plugins, want %d: %#v", len(got), len(tt.wantNames), got)
			}
			for i, want := range tt.wantNames {
				if got[i].Name != want {
					t.Errorf("plugin %d name = %q, want %q", i, got[i].Name, want)
				}
			}
			if tt.wantLatestFor != "" && got[0].LatestVersion != tt.wantLatestFor {
				t.Errorf("LatestVersion = %q, want %q", got[0].LatestVersion, tt.wantLatestFor)
			}
		})
	}
}

func TestClaudePluginAdapter_ListPlugins_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	a := NewClaudeCodePluginAdapter(stubExec("not json at all"), func(string) (string, bool) { return "", false })
	if _, err := a.ListPlugins(context.Background()); err == nil {
		t.Fatal("garbage output should still be an error")
	}
}

func TestCodexPluginAdapter_ListPlugins_AcceptsBothListShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		stdout    string
		wantNames []string
	}{
		{
			name:      "object envelope",
			stdout:    `{"installed":[{"name":"useful-skills","marketplaceName":"lkshrk","version":"1.0.0"}],"available":[]}`,
			wantNames: []string{"useful-skills"},
		},
		{
			name:      "bare installed array",
			stdout:    `[{"name":"useful-skills","marketplaceName":"lkshrk","version":"1.0.0"}]`,
			wantNames: []string{"useful-skills"},
		},
		{
			name:      "bare empty array",
			stdout:    `  []  `,
			wantNames: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewCodexPluginAdapter(stubExec(tt.stdout), func(string) (string, bool) { return "", false })
			got, err := a.ListPlugins(context.Background())
			if err != nil {
				t.Fatalf("ListPlugins: %v", err)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("got %d plugins, want %d: %#v", len(got), len(tt.wantNames), got)
			}
			for i, want := range tt.wantNames {
				if got[i].Name != want {
					t.Errorf("plugin %d name = %q, want %q", i, got[i].Name, want)
				}
			}
		})
	}
}

func TestCodexPluginAdapter_ListPlugins_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	a := NewCodexPluginAdapter(stubExec(`{"installed":`), func(string) (string, bool) { return "", false })
	_, err := a.ListPlugins(context.Background())
	if err == nil {
		t.Fatal("truncated output should still be an error")
	}
	if !strings.Contains(err.Error(), "parse json") {
		t.Fatalf("error = %v, want a parse-json error", err)
	}
}

func TestCodexPluginAdapter_ListMarketplaces_AcceptsBothListShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		stdout     string
		wantNames  []string
		wantSource string
	}{
		{
			name:       "object envelope",
			stdout:     `{"marketplaces":[{"name":"lkshrk","root":"/tmp/x","marketplaceSource":{"source":"https://github.com/lkshrk/skills"}}]}`,
			wantNames:  []string{"lkshrk"},
			wantSource: "https://github.com/lkshrk/skills",
		},
		{
			name:       "bare array",
			stdout:     `[{"name":"lkshrk","root":"/tmp/x","marketplaceSource":{"source":"https://github.com/lkshrk/skills"}}]`,
			wantNames:  []string{"lkshrk"},
			wantSource: "https://github.com/lkshrk/skills",
		},
		{
			name:      "bare empty array",
			stdout:    `[]`,
			wantNames: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := NewCodexPluginAdapter(stubExec(tt.stdout), func(string) (string, bool) { return "", false })
			got, err := a.ListMarketplaces(context.Background())
			if err != nil {
				t.Fatalf("ListMarketplaces: %v", err)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("got %d marketplaces, want %d: %#v", len(got), len(tt.wantNames), got)
			}
			for i, want := range tt.wantNames {
				if got[i].Name != want {
					t.Errorf("marketplace %d name = %q, want %q", i, got[i].Name, want)
				}
				if got[i].Source != tt.wantSource {
					t.Errorf("marketplace %d source = %q, want %q", i, got[i].Source, tt.wantSource)
				}
			}
		})
	}
}

func TestCodexPluginAdapter_ListMarketplaces_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	a := NewCodexPluginAdapter(stubExec("<html>error</html>"), func(string) (string, bool) { return "", false })
	if _, err := a.ListMarketplaces(context.Background()); err == nil {
		t.Fatal("garbage output should still be an error")
	}
}
