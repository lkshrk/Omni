package agent

import "testing"

func TestExtractMcpPinnedVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"pinned npx", "npx -y @example/mcp@1.2.3", "1.2.3"},
		{"pinned bunx", "bunx some-pkg@2.0.0", "2.0.0"},
		{"scoped package not confused with scope prefix", "npx -y @scope/pkg@2.0.0", "2.0.0"},
		{"latest tag", "npx -y @example/mcp@latest", ""},
		{"next tag", "npx -y @example/mcp@next", ""},
		{"bare path no npx/bunx", "/usr/local/bin/my-mcp-server", ""},
		{"empty command", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractMcpPinnedVersion(tc.command); got != tc.want {
				t.Errorf("ExtractMcpPinnedVersion(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func mcpSliceEq(a, b []string) bool {
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

func mcpContainsPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func mcpHasSeparatorThenCmd(args, cmdParts []string) bool {
	for i, a := range args {
		if a == "--" {
			return mcpSliceEq(args[i+1:], cmdParts)
		}
	}
	return false
}
