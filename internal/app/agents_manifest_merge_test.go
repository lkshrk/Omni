package app

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const mergeBaseManifest = `# omni:agents-migration:v1
name: omni-migrated
version: 1.0.0
dependencies:
  apm:
    - git: https://github.com/acme/alpha
      ref: v1
      targets:
        - claude
  mcp:
    - name: alpha
      registry: false
      transport: stdio
      command: alpha-server
      experimental: true
      passthrough:
        depth: 3
targets:
  - claude
# apm marketplace add https://github.com/acme/plugins --name acme
`

func mergeOrFatal(t *testing.T, existing string, candidates manifestCandidates) (string, manifestMergeReport) {
	t.Helper()
	out, report, err := mergeAgentsManifest([]byte(existing), candidates)
	if err != nil {
		t.Fatalf("mergeAgentsManifest: %v", err)
	}
	return string(out), report
}

func decodeMergedManifest(t *testing.T, body string) apmManifest {
	t.Helper()
	var manifest apmManifest
	if err := yaml.Unmarshal([]byte(body), &manifest); err != nil {
		t.Fatalf("decode merged manifest: %v\n%s", err, body)
	}
	return manifest
}

func assertOriginalLinesPreserved(t *testing.T, before, after string) {
	t.Helper()
	want := strings.Split(before, "\n")
	got := strings.Split(after, "\n")
	at := 0
	for _, line := range want {
		found := false
		for at < len(got) {
			if got[at] == line {
				at++
				found = true
				break
			}
			at++
		}
		if !found {
			t.Fatalf("original line %q missing or reordered in merged manifest:\n%s", line, after)
		}
	}
}

func TestMergeAgentsManifestPreservesExistingDependency(t *testing.T) {
	body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
		Packages: []apmPackageDep{{Git: "https://github.com/acme/beta", Ref: "v2", Targets: []string{"claude"}}},
	})
	manifest := decodeMergedManifest(t, body)
	if len(manifest.Dependencies.APM) != 2 {
		t.Fatalf("want 2 apm dependencies, got %d:\n%s", len(manifest.Dependencies.APM), body)
	}
	if manifest.Dependencies.APM[0].Git != "https://github.com/acme/alpha" || manifest.Dependencies.APM[0].Ref != "v1" {
		t.Fatalf("existing dependency lost: %+v", manifest.Dependencies.APM[0])
	}
	if manifest.Dependencies.APM[1].Git != "https://github.com/acme/beta" {
		t.Fatalf("new dependency missing: %+v", manifest.Dependencies.APM[1])
	}
	if len(report.Appended) != 1 || report.Appended[0].Identity != "git:https://github.com/acme/beta" {
		t.Fatalf("unexpected report: %+v", report)
	}
	assertOriginalLinesPreserved(t, mergeBaseManifest, body)
}

func TestMergeAgentsManifestKeepsMarketplacesAndPassthroughBytes(t *testing.T) {
	body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
		MCP: []manifestMCPCandidate{{
			Dep:   apmMCPDep{Name: "beta", Transport: "stdio", Command: "beta-server"},
			Reach: []string{"claude"},
		}},
		Marketplaces: []marketplaceDecl{{name: "other", source: "https://github.com/acme/other"}},
	})
	assertOriginalLinesPreserved(t, mergeBaseManifest, body)
	if !strings.Contains(body, "# apm marketplace add https://github.com/acme/plugins --name acme") {
		t.Fatalf("existing marketplace comment lost:\n%s", body)
	}
	if !strings.Contains(body, "# apm marketplace add https://github.com/acme/other --name other") {
		t.Fatalf("new marketplace comment missing:\n%s", body)
	}
	manifest := decodeMergedManifest(t, body)
	if len(manifest.Dependencies.MCP) != 2 {
		t.Fatalf("want 2 mcp dependencies, got %d:\n%s", len(manifest.Dependencies.MCP), body)
	}
	raw := manifest.Dependencies.MCP[0].Raw
	if raw["experimental"] != true {
		t.Fatalf("passthrough key experimental lost: %+v", raw)
	}
	if nested, ok := raw["passthrough"].(map[string]any); !ok || nested["depth"] != 3 {
		t.Fatalf("nested passthrough lost: %+v", raw)
	}
	if len(report.Appended) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestMergeAgentsManifestIdentityIsPerKind(t *testing.T) {
	t.Run("ref difference is a conflict", func(t *testing.T) {
		body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
			Packages: []apmPackageDep{{Git: "https://github.com/acme/alpha", Ref: "v2", Targets: []string{"claude"}}},
		})
		if body != mergeBaseManifest {
			t.Fatalf("manifest changed on conflict:\n%s", body)
		}
		if len(report.Rejected) != 1 || report.Rejected[0].Identity != "git:https://github.com/acme/alpha" {
			t.Fatalf("unexpected report: %+v", report)
		}
		if !strings.Contains(report.Rejected[0].Reason, "ref") {
			t.Fatalf("rejection does not name the field: %q", report.Rejected[0].Reason)
		}
	})

	t.Run("path and marketplace packages sharing a name are distinct", func(t *testing.T) {
		body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
			Packages: []apmPackageDep{
				{Path: "/srv/pkgs/shared", Name: "shared", Targets: []string{"claude"}},
				{Marketplace: "acme", Name: "shared", Targets: []string{"claude"}},
			},
		})
		if len(report.Appended) != 2 {
			t.Fatalf("want both packages appended, got %+v", report)
		}
		got := []string{report.Appended[0].Identity, report.Appended[1].Identity}
		want := []string{"path:/srv/pkgs/shared", "mkt:acme/shared"}
		if !slices.Equal(got, want) {
			t.Fatalf("identities %v, want %v", got, want)
		}
		if len(decodeMergedManifest(t, body).Dependencies.APM) != 3 {
			t.Fatalf("want 3 apm dependencies:\n%s", body)
		}
	})

	t.Run("an unnamed package is identified by its source", func(t *testing.T) {
		body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
			Packages: []apmPackageDep{{Git: "https://github.com/acme/alpha", Ref: "v1", Targets: []string{"claude"}}},
		})
		if body != mergeBaseManifest {
			t.Fatalf("manifest changed on a no-op:\n%s", body)
		}
		if len(report.Appended)+len(report.Rejected)+len(report.Unioned) != 0 {
			t.Fatalf("want a no-op, got %+v", report)
		}
	})
}

func TestMergeAgentsManifestUnionsTargetsWithoutWideningManifestTargets(t *testing.T) {
	body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
		Packages: []apmPackageDep{{Git: "https://github.com/acme/alpha", Ref: "v1", Targets: []string{"codex"}}},
	})
	manifest := decodeMergedManifest(t, body)
	if len(manifest.Dependencies.APM) != 1 {
		t.Fatalf("want the dependency unioned, not duplicated:\n%s", body)
	}
	if !slices.Equal(manifest.Dependencies.APM[0].Targets, []string{"claude", "codex"}) {
		t.Fatalf("targets %v, want [claude codex]", manifest.Dependencies.APM[0].Targets)
	}
	if !slices.Equal(manifest.Targets, []string{"claude"}) {
		t.Fatalf("manifest targets widened to %v", manifest.Targets)
	}
	if len(report.Unioned) != 1 || len(report.Rejected) != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if !strings.Contains(body, "experimental: true") {
		t.Fatalf("union edit disturbed unrelated declarations:\n%s", body)
	}
}

func TestMergeAgentsManifestRejectsDifferingServiceDefinition(t *testing.T) {
	body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
		MCP: []manifestMCPCandidate{{
			Dep:   apmMCPDep{Name: "alpha", Transport: "stdio", Command: "other-server"},
			Reach: []string{"claude"},
		}},
	})
	if body != mergeBaseManifest {
		t.Fatalf("manifest changed on conflict:\n%s", body)
	}
	if len(report.Rejected) != 1 || report.Rejected[0].Identity != "alpha" || report.Rejected[0].Kind != manifestKindMCP {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestMergeAgentsManifestReportsUncoveredReachInsteadOfWidening(t *testing.T) {
	body, report := mergeOrFatal(t, mergeBaseManifest, manifestCandidates{
		MCP: []manifestMCPCandidate{{
			Dep:   apmMCPDep{Name: "beta", Transport: "stdio", Command: "beta-server"},
			Reach: []string{"claude", "codex"},
		}},
	})
	if !slices.Equal(decodeMergedManifest(t, body).Targets, []string{"claude"}) {
		t.Fatalf("manifest targets widened:\n%s", body)
	}
	if len(report.Decisions) != 1 || !strings.Contains(report.Decisions[0], "codex") {
		t.Fatalf("unexpected decisions: %+v", report.Decisions)
	}
}

func TestMergeAgentsManifestGeneratesWholeManifestWhenEmpty(t *testing.T) {
	body, report := mergeOrFatal(t, "", manifestCandidates{
		Packages:     []apmPackageDep{{Git: "https://github.com/acme/alpha", Ref: "v1", Targets: []string{"claude"}}},
		MCP:          []manifestMCPCandidate{{Dep: apmMCPDep{Name: "alpha", Transport: "stdio", Command: "alpha-server"}, Reach: []string{"claude"}}},
		Marketplaces: []marketplaceDecl{{name: "acme", source: "https://github.com/acme/plugins"}},
	})
	if !strings.HasPrefix(body, agentsMigrationMarker+"\n") {
		t.Fatalf("generated manifest lacks the migration marker:\n%s", body)
	}
	manifest := decodeMergedManifest(t, body)
	if len(manifest.Dependencies.APM) != 1 || len(manifest.Dependencies.MCP) != 1 {
		t.Fatalf("generated manifest is incomplete:\n%s", body)
	}
	if !slices.Equal(manifest.Targets, []string{"claude"}) {
		t.Fatalf("generated targets %v", manifest.Targets)
	}
	if !strings.Contains(body, "# apm marketplace add https://github.com/acme/plugins --name acme") {
		t.Fatalf("generated marketplace comment missing:\n%s", body)
	}
	if len(report.Appended) != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestMergeAgentsManifestCreatesMissingDependencyBlocks(t *testing.T) {
	existing := "name: omni-migrated\nversion: 1.0.0\n"
	body, _ := mergeOrFatal(t, existing, manifestCandidates{
		Packages: []apmPackageDep{{Git: "https://github.com/acme/alpha", Targets: []string{"claude"}}},
		LSP:      []apmLSPDep{{Name: "gopls", Command: "gopls"}},
	})
	assertOriginalLinesPreserved(t, existing, body)
	manifest := decodeMergedManifest(t, body)
	if len(manifest.Dependencies.APM) != 1 || len(manifest.Dependencies.LSP) != 1 {
		t.Fatalf("dependency blocks not created:\n%s", body)
	}
}

func TestMergeAgentsManifestHandlesEmptyFlowDependencies(t *testing.T) {
	existing := "# omni:agents-migration:v1\nname: omni-migrated\nversion: 1.0.0\ndependencies: {}\n"
	merged, report, err := mergeAgentsManifest([]byte(existing), manifestCandidates{
		Packages: []apmPackageDep{{Git: "https://github.com/acme/beta", Targets: []string{"codex"}}},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(report.Appended) != 1 {
		t.Fatalf("appended = %v, want 1 entry", report.Appended)
	}
	var parsed struct {
		Dependencies struct {
			APM []apmPackageDep `yaml:"apm"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(merged, &parsed); err != nil {
		t.Fatalf("merged output does not parse: %v\n%s", err, merged)
	}
	if len(parsed.Dependencies.APM) != 1 || parsed.Dependencies.APM[0].Git != "https://github.com/acme/beta" {
		t.Fatalf("dependencies.apm = %+v\n%s", parsed.Dependencies.APM, merged)
	}
	if !strings.Contains(string(merged), "# omni:agents-migration:v1") {
		t.Fatalf("marker lost:\n%s", merged)
	}
}

func TestMergeAgentsManifestRefusesNonEmptyFlowDependencies(t *testing.T) {
	existing := "name: omni-migrated\nversion: 1.0.0\ndependencies: {apm: [{git: https://github.com/acme/alpha}]}\n"
	if _, _, err := mergeAgentsManifest([]byte(existing), manifestCandidates{
		Packages: []apmPackageDep{{Git: "https://github.com/acme/beta"}},
	}); err == nil {
		t.Fatal("merge accepted a flow-style dependencies mapping; want refusal")
	}
}
