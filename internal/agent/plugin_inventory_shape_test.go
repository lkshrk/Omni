package agent

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestPluginAdaptersRejectNullInventoryPayloads(t *testing.T) {
	t.Parallel()
	type listInventory func(context.Context, PluginAdapter) (int, error)
	listPlugins := func(ctx context.Context, adapter PluginAdapter) (int, error) {
		rows, err := adapter.ListPlugins(ctx)
		return len(rows), err
	}
	listMarketplaces := func(ctx context.Context, adapter PluginAdapter) (int, error) {
		rows, err := adapter.ListMarketplaces(ctx)
		return len(rows), err
	}
	tests := []struct {
		name          string
		newAdapter    func(func(context.Context, string, ...string) (string, string, error)) PluginAdapter
		list          listInventory
		emptyJSON     string
		invalidJSON   []string
		failAvailable bool
	}{
		{"claude plugins", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewClaudeCodePluginAdapter(exec, nil)
		}, listPlugins, `{"installed":[],"available":[]}`, []string{
			`{}`,
			`{"installed":null,"available":[]}`,
			`{"installed":[],"available":null}`,
		}, false},
		{"claude plugins fallback", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewClaudeCodePluginAdapter(exec, nil)
		}, listPlugins, `[]`, nil, true},
		{"claude marketplaces", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewClaudeCodePluginAdapter(exec, nil)
		}, listMarketplaces, `[]`, nil, false},
		{"codex plugins", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewCodexPluginAdapter(exec, nil)
		}, listPlugins, `{"installed":[],"available":[]}`, []string{
			`{}`,
			`{"installed":null,"available":[]}`,
			`{"installed":[],"available":null}`,
		}, false},
		{"codex marketplaces", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewCodexPluginAdapter(exec, nil)
		}, listMarketplaces, `{"marketplaces":[]}`, []string{
			`{}`,
			`{"marketplaces":null}`,
		}, false},
		{"grok plugins", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewGrokPluginAdapter(exec, nil)
		}, listPlugins, `[]`, nil, false},
		{"grok plugins fallback", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewGrokPluginAdapter(exec, nil)
		}, listPlugins, `[]`, nil, true},
		{"grok marketplaces", func(exec func(context.Context, string, ...string) (string, string, error)) PluginAdapter {
			return NewGrokPluginAdapter(exec, nil)
		}, listMarketplaces, `[]`, nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, fixture := range []struct {
				name    string
				payload string
				wantErr bool
			}{
				{name: "null", payload: "null", wantErr: true},
				{name: "empty", payload: tc.emptyJSON},
			} {
				t.Run(fixture.name, func(t *testing.T) {
					exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
						if tc.failAvailable && slices.Contains(args, "--available") {
							return "", "", errors.New("available listing unsupported")
						}
						return fixture.payload, "", nil
					}
					count, err := tc.list(t.Context(), tc.newAdapter(exec))
					if fixture.wantErr {
						if err == nil {
							t.Fatalf("null inventory returned count=%d with no error", count)
						}
						return
					}
					if err != nil || count != 0 {
						t.Fatalf("empty inventory = count %d, err %v; want zero rows and no error", count, err)
					}
				})
			}
			for i, payload := range tc.invalidJSON {
				t.Run("invalid wrapper "+string(rune('a'+i)), func(t *testing.T) {
					exec := func(_ context.Context, _ string, args ...string) (string, string, error) {
						if tc.failAvailable && slices.Contains(args, "--available") {
							return "", "", errors.New("available listing unsupported")
						}
						return payload, "", nil
					}
					count, err := tc.list(t.Context(), tc.newAdapter(exec))
					if err == nil {
						t.Fatalf("invalid inventory %s returned count=%d with no error", payload, count)
					}
				})
			}
		})
	}
}
